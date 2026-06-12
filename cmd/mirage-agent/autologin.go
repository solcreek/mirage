//go:build darwin

package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// kcKey is the fixed obfuscation key macOS uses for /etc/kcpassword.
var kcKey = []byte{0x7D, 0x89, 0x52, 0x23, 0xD2, 0xBC, 0xDD, 0xEA, 0xA3, 0xB9, 0x1F}

// encodeKCPassword XORs the password with the cycling key and pads to the next
// multiple of 12 (a full padding block is added even when already aligned,
// which macOS requires to read the file correctly).
func encodeKCPassword(pw []byte) []byte {
	pad := 12 - (len(pw) % 12)
	out := make([]byte, len(pw)+pad)
	copy(out, pw)
	for i := range out {
		out[i] ^= kcKey[i%len(kcKey)]
	}
	return out
}

// setupAutologin enables passwordless boot-to-desktop for the given user by
// writing /etc/kcpassword and setting autoLoginUser. The password is read from
// stdin so it never lands in argv or shell history. Must run as root.
func setupAutologin(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: mirage-agent setup-autologin <user> (password on stdin)")
	}
	user := args[0]
	if os.Geteuid() != 0 {
		return fmt.Errorf("must run as root: sudo mirage-agent setup-autologin %s", user)
	}

	fmt.Fprint(os.Stderr, "password for "+user+": ")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return fmt.Errorf("read password: %w", err)
	}
	pw := strings.TrimRight(line, "\r\n")

	if err := os.WriteFile("/etc/kcpassword", encodeKCPassword([]byte(pw)), 0o600); err != nil {
		return fmt.Errorf("write /etc/kcpassword: %w", err)
	}
	if err := os.Chmod("/etc/kcpassword", 0o600); err != nil {
		return err
	}
	cmd := exec.Command("defaults", "write",
		"/Library/Preferences/com.apple.loginwindow", "autoLoginUser", "-string", user)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("set autoLoginUser: %v: %s", err, out)
	}
	fmt.Fprintln(os.Stderr, "auto-login enabled for "+user+" — reboot to boot straight to the desktop")
	return nil
}
