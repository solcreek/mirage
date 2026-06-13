package engine

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Code-Hex/vz/v3"
)

// AgentPort is the guest vsock port the root agent binds: exec, ping, and
// screenshot (the agent runs screencapture via launchctl asuser).
const AgentPort = 4444

// ConnectGuest opens a host→guest vsock connection to the given port. The host
// has no AF_VSOCK API; the connection is routed through the VM's
// VZVirtioSocketDevice and surfaces as a net.Conn-compatible stream.
func ConnectGuest(vm *vz.VirtualMachine, port uint32) (*vz.VirtioSocketConnection, error) {
	devs := vm.SocketDevices()
	if len(devs) == 0 {
		return nil, fmt.Errorf("vm has no virtio socket device")
	}
	return devs[0].Connect(port)
}

// ExecResult is one command's outcome from the guest agent.
type ExecResult struct {
	ExitCode int    `json:"exit_code"`
	Output   string `json:"output"`
}

// AgentExec waits for the guest agent, runs one command, and returns its
// output and exit code. It dials with the given timeout (the agent isn't
// reachable until the guest has booted far enough to start it).
//
// After a restore, the first vsock connections often connect but break on the
// first write ("broken pipe") until the guest's virtio-vsock driver settles, so
// a write failure is retried with a fresh connection until the deadline. The
// retry is safe because a failed write means the command never reached the
// agent. A failure once the command has been written (read side) is not retried
// — the command may have run, and re-running it could double-execute.
func AgentExec(vm *vz.VirtualMachine, command string, timeout time.Duration) (ExecResult, error) {
	deadline := time.Now().Add(timeout)
	delay := 250 * time.Millisecond
	var lastErr error
	for {
		res, err, wrote := agentExecOnce(vm, command, timeout)
		if err == nil {
			return res, nil
		}
		lastErr = err
		if wrote { // command may have executed — do not retry
			return ExecResult{}, err
		}
		if time.Now().After(deadline) {
			return ExecResult{}, lastErr
		}
		time.Sleep(delay)
		if delay < 2*time.Second {
			delay *= 2
		}
	}
}

// agentExecOnce performs a single connect→write→read exchange. wrote reports
// whether the command was fully written (and so may have executed), which tells
// AgentExec whether retrying is safe.
func agentExecOnce(vm *vz.VirtualMachine, command string, timeout time.Duration) (res ExecResult, err error, wrote bool) {
	conn, err := ConnectGuest(vm, AgentPort)
	if err != nil {
		return ExecResult{}, fmt.Errorf("connect to agent: %w", err), false
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("exec " + command + "\n")); err != nil {
		return ExecResult{}, fmt.Errorf("write to agent: %w", err), false
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return ExecResult{}, fmt.Errorf("read from agent: %w", err), true
	}
	if err := json.Unmarshal([]byte(line), &res); err != nil {
		return ExecResult{}, fmt.Errorf("bad agent reply %q: %w", line, err), true
	}
	return res, nil, true
}

// SyncGuest flushes the guest filesystem so writes survive the force-stop that
// ends a VM (vz Stop is an unclean power-off; unsynced writes are otherwise
// lost — the file's metadata may persist while its contents do not). Best
// effort: an unreachable agent simply means there is nothing to flush.
func SyncGuest(vm *vz.VirtualMachine, timeout time.Duration) {
	_, _ = AgentExec(vm, "sync", timeout)
}

// AgentScreenshot asks the guest agent for a PNG of the main display and
// returns the decoded image bytes.
func AgentScreenshot(vm *vz.VirtualMachine, timeout time.Duration) ([]byte, error) {
	conn, err := DialGuest(vm, AgentPort, timeout)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("screenshot\n")); err != nil {
		return nil, fmt.Errorf("write to gui agent: %w", err)
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("read from gui agent: %w", err)
	}
	var res struct {
		OK     bool   `json:"ok"`
		PNG    string `json:"png_base64"`
		ErrMsg string `json:"error"`
	}
	if err := json.Unmarshal([]byte(line), &res); err != nil {
		return nil, fmt.Errorf("bad gui agent reply: %w", err)
	}
	if !res.OK {
		return nil, fmt.Errorf("guest screenshot failed: %s", res.ErrMsg)
	}
	return base64.StdEncoding.DecodeString(res.PNG)
}

// DialGuest retries ConnectGuest until the guest agent is listening or the
// timeout elapses (the connect fails until the in-guest listener binds).
func DialGuest(vm *vz.VirtualMachine, port uint32, timeout time.Duration) (*vz.VirtioSocketConnection, error) {
	deadline := time.Now().Add(timeout)
	delay := 250 * time.Millisecond
	for {
		conn, err := ConnectGuest(vm, port)
		if err == nil {
			return conn, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("guest agent not reachable on port %d within %s: %w", port, timeout, err)
		}
		time.Sleep(delay)
		if delay < 2*time.Second {
			delay *= 2
		}
	}
}
