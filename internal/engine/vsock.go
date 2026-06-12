package engine

import (
	"bufio"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Code-Hex/vz/v3"
)

// AgentPort is the guest vsock port the agent's root listener binds.
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
func AgentExec(vm *vz.VirtualMachine, command string, timeout time.Duration) (ExecResult, error) {
	conn, err := DialGuest(vm, AgentPort, timeout)
	if err != nil {
		return ExecResult{}, err
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("exec " + command + "\n")); err != nil {
		return ExecResult{}, fmt.Errorf("write to agent: %w", err)
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return ExecResult{}, fmt.Errorf("read from agent: %w", err)
	}
	var res ExecResult
	if err := json.Unmarshal([]byte(line), &res); err != nil {
		return ExecResult{}, fmt.Errorf("bad agent reply %q: %w", line, err)
	}
	return res, nil
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
