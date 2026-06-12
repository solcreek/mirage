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

// captureScreen grabs the main display as PNG. screencapture only sees the
// display when it runs inside the logged-in GUI (Aqua) session, so the root
// daemon launches it via `launchctl asuser <console-uid>`. TCC attributes the
// capture to the responsible process (mirage-agent), which must hold the
// ScreenCapture grant (seeded in the golden image).
func captureScreen() ([]byte, error) {
	uidBytes, err := exec.Command("stat", "-f", "%u", "/dev/console").Output()
	uid := strings.TrimSpace(string(uidBytes))
	if err != nil || uid == "" || uid == "0" {
		return nil, fmt.Errorf("no GUI login session (console uid=%q) — enable auto-login", uid)
	}
	// Unique path per capture: a fixed path leaves a stale file that /tmp's
	// sticky bit can make unwritable on the next capture.
	tmp, err := os.CreateTemp("", "mirage-shot-*.png")
	if err != nil {
		return nil, err
	}
	out := tmp.Name()
	tmp.Close()
	_ = os.Remove(out) // let screencapture create it fresh
	defer os.Remove(out)
	combined, err := exec.Command("launchctl", "asuser", uid,
		"/usr/sbin/screencapture", "-x", "-t", "png", out).CombinedOutput()
	msg := strings.TrimSpace(string(combined))
	if err != nil {
		return nil, fmt.Errorf("screencapture failed: %v (%s)", err, msg)
	}
	info, statErr := os.Stat(out)
	if statErr != nil || info.Size() == 0 {
		return nil, fmt.Errorf("screencapture produced no image — Screen Recording (TCC) not granted to mirage-agent; output=%q", msg)
	}
	defer os.Remove(out)
	return os.ReadFile(out)
}

const agentPort = 4444 // root LaunchDaemon: exec, ping, screenshot

func main() {
	if len(os.Args) > 1 && os.Args[1] == "setup-autologin" {
		if err := setupAutologin(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "mirage-agent:", err)
			os.Exit(1)
		}
		return
	}
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
