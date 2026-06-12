package supervisor

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"time"
)

// request opens the per-VM socket, sends one Request, and returns the Response.
func request(name string, req Request, timeout time.Duration) (*Response, error) {
	conn, err := net.DialTimeout("unix", SocketPath(name), 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("no running supervisor for %q: %w", name, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))

	b, _ := json.Marshal(req)
	if _, err := conn.Write(append(b, '\n')); err != nil {
		return nil, err
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return nil, err
	}
	var resp Response
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// Exec runs a command in a running VM via its supervisor.
func Exec(name, cmd string, timeout time.Duration) (exitCode int, output string, err error) {
	resp, err := request(name, Request{Op: OpExec, Cmd: cmd, TimeoutS: int(timeout.Seconds())}, timeout+10*time.Second)
	if err != nil {
		return 0, "", err
	}
	if !resp.OK {
		return 0, "", fmt.Errorf("%s", resp.Error)
	}
	return resp.ExitCode, resp.Output, nil
}

// Stop asks a supervisor to shut its VM down and exit, then waits until the
// supervisor process is actually gone so callers see a consistent state.
func Stop(name string) error {
	resp, err := request(name, Request{Op: OpStop}, 35*time.Second)
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("%s", resp.Error)
	}
	deadline := time.Now().Add(35 * time.Second)
	for time.Now().Before(deadline) {
		if !IsRunning(name) {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("supervisor did not exit in time")
}

// Screenshot asks a running VM's supervisor for a PNG of the guest display.
func Screenshot(name string) ([]byte, error) {
	resp, err := request(name, Request{Op: OpScreenshot}, 90*time.Second)
	if err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, fmt.Errorf("%s", resp.Error)
	}
	return base64.StdEncoding.DecodeString(resp.PNGBase64)
}

// Ping checks that a supervisor is responsive.
func Ping(name string) error {
	resp, err := request(name, Request{Op: OpPing}, 5*time.Second)
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("%s", resp.Error)
	}
	return nil
}
