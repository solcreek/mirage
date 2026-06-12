//go:build darwin

// Command mirage-agent runs inside a macOS guest and exposes a control channel
// to the host over vsock (AF_VSOCK). This is the v0 spike build: it listens on
// the agent port, and answers a line-oriented `ping` with guest facts and
// `exec <cmd>` by running a shell command. The production protocol
// (length-prefixed JSON, token auth) lands later; this proves the transport.
package main

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"golang.org/x/sys/unix"
)

// captureScreen grabs the main display as PNG via the screencapture CLI. This
// only yields a non-black image when run in a logged-in GUI session with Screen
// Recording (TCC) permission — hence it must run from the user LaunchAgent.
func captureScreen() ([]byte, error) {
	out := "/tmp/.mirage-shot.png"
	if b, err := exec.Command("/usr/sbin/screencapture", "-x", "-t", "png", out).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("screencapture: %v: %s", err, b)
	}
	defer os.Remove(out)
	return os.ReadFile(out)
}

const (
	agentPort = 4444 // root LaunchDaemon: exec, ping
	guiPort   = 4445 // user LaunchAgent (GUI session): screenshot
)

func main() {
	port := uint32(agentPort)
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "setup-autologin":
			if err := setupAutologin(os.Args[2:]); err != nil {
				fmt.Fprintln(os.Stderr, "mirage-agent:", err)
				os.Exit(1)
			}
			return
		case "serve-gui":
			// Runs in the logged-in user's GUI session (LaunchAgent) so
			// screencapture has a display + the session's TCC grants.
			port = guiPort
		}
	}
	if err := serve(port); err != nil {
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
	case req == "screenshot":
		png, err := captureScreen()
		if err != nil {
			writeJSON(conn, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		writeJSON(conn, map[string]any{"ok": true, "png_base64": base64.StdEncoding.EncodeToString(png)})
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
