//go:build darwin

// Command mirage-agent runs inside a macOS guest and exposes a control channel
// to the host over vsock (AF_VSOCK). This is the v0 spike build: it listens on
// the agent port, and answers a line-oriented `ping` with guest facts and
// `exec <cmd>` by running a shell command. The production protocol
// (length-prefixed JSON, token auth) lands later; this proves the transport.
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
	// Bind on the guest's own CID, any local CID, well-known agent port.
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

	r := bufio.NewReader(conn)
	line, err := r.ReadString('\n')
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
	ver, _ := exec.Command("sw_vers", "-productVersion").Output()
	return map[string]string{
		"hostname":     host,
		"product_ver":  strings.TrimSpace(string(ver)),
	}
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
