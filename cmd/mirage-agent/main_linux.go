//go:build linux

// Command mirage-agent (Linux build) runs inside a Linux guest and exposes the
// same vsock control channel as the macOS build: a line-oriented `ping` and
// `exec <cmd>`. The host's AgentExec speaks this identical protocol, so no
// host-side changes are needed. Installed as a systemd service (see
// guest/linux/install.sh) — unlike Apple's container init it runs alongside the
// distro's own init, not as PID 1, because we boot full distros.
//
// There is no `screenshot` op: Linux guests are shown via the live window, not
// captured in-guest. DNS overrides and other setup ride on `exec` (run as root).
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"golang.org/x/sys/unix"
)

const agentPort = 4444

func main() {
	if err := serve(agentPort); err != nil {
		fmt.Fprintln(os.Stderr, "mirage-agent:", err)
		os.Exit(1)
	}
}

func serve(port uint32) error {
	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM, 0)
	if err != nil {
		return fmt.Errorf("socket(AF_VSOCK): %w", err)
	}
	if err := unix.Bind(fd, &unix.SockaddrVM{CID: unix.VMADDR_CID_ANY, Port: port}); err != nil {
		return fmt.Errorf("bind vsock port %d: %w", port, err)
	}
	if err := unix.Listen(fd, 4); err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	fmt.Printf("mirage-agent listening on vsock port %d\n", port)
	for {
		nfd, _, err := unix.Accept(fd)
		if err != nil {
			return fmt.Errorf("accept: %w", err)
		}
		go handle(nfd)
	}
}

func handle(fd int) {
	defer unix.Close(fd)
	conn := os.NewFile(uintptr(fd), "vsock")
	defer conn.Close()

	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return
	}
	req := strings.TrimSpace(line)

	switch {
	case req == "ping":
		writeJSON(conn, map[string]any{"ok": true, "agent": "mirage-agent", "guest": guestInfo()})
	case strings.HasPrefix(req, "exec "):
		out, code := runShell(strings.TrimPrefix(req, "exec "))
		writeJSON(conn, map[string]any{"ok": code == 0, "exit_code": code, "output": out})
	default:
		writeJSON(conn, map[string]any{"ok": false, "error": "unknown request: " + req})
	}
}

func writeJSON(w *os.File, v any) {
	b, _ := json.Marshal(v)
	w.Write(append(b, '\n'))
}

func guestInfo() map[string]string {
	host, _ := os.Hostname()
	var uts unix.Utsname
	_ = unix.Uname(&uts)
	return map[string]string{
		"hostname":    host,
		"product_ver": osPrettyName(),
		"kernel":      charsToString(uts.Release[:]),
	}
}

// osPrettyName returns PRETTY_NAME from /etc/os-release (e.g. "Ubuntu 24.04.3 LTS").
func osPrettyName() string {
	raw, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return ""
	}
	for _, ln := range strings.Split(string(raw), "\n") {
		if v, ok := strings.CutPrefix(ln, "PRETTY_NAME="); ok {
			return strings.Trim(v, "\"")
		}
	}
	return ""
}

func charsToString(b []byte) string {
	if i := strings.IndexByte(string(b), 0); i >= 0 {
		return string(b[:i])
	}
	return string(b)
}

func runShell(cmd string) (string, int) {
	c := exec.Command("/bin/sh", "-c", cmd)
	out, err := c.CombinedOutput()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			code = -1
		}
	}
	return string(out), code
}
