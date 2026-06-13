package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/solcreek/mirage/internal/bundle"
	"github.com/solcreek/mirage/internal/engine"
	"github.com/solcreek/mirage/internal/kcpassword"
	"github.com/solcreek/mirage/internal/supervisor"
	"github.com/solcreek/mirage/pkg/miragerr"
)

// Core operations shared by the CLI commands and the MCP tools so there is one
// implementation of each VM action.

// coreExec runs a command in a VM. If a supervisor is already running it reuses
// the live VM; otherwise it cold-boots one-shot (boot → exec → stop).
func coreExec(name, command string, timeout time.Duration) (exitCode int, output string, err error) {
	if supervisor.OwnedByGUI(name) {
		return 0, "", miragerr.New(miragerr.SlugInvalidState, name+" is open in the Mirage GUI").
			WithHint("control it from the GUI window, or close it first")
	}
	if supervisor.IsRunning(name) {
		return supervisor.Exec(name, command, timeout)
	}
	b, _, ok := bundle.Find(name)
	if !ok {
		return 0, "", miragerr.New(miragerr.SlugNotFound, "no bundle named "+name)
	}
	cfg, err := b.Load()
	if err != nil {
		return 0, "", err
	}
	vm, err := engine.StartFresh(b, cfg, engine.Options{}, 5)
	if err != nil {
		return 0, "", miragerr.New(miragerr.SlugHostEnv, "vm start failed").
			WithHint("another VM using this image may still be shutting down").WithCause(err)
	}
	defer func() { _ = vm.Stop() }()
	res, err := engine.AgentExec(vm, command, timeout)
	if err != nil {
		return 0, "", miragerr.New(miragerr.SlugAgentTimeout, "guest agent not reachable").
			WithHint("is mirage-agent installed in the image? run the tools-image install.sh once").WithCause(err)
	}
	// Flush the guest before the deferred force-stop, or writes this command
	// made to a persistent image would be lost on power-off.
	engine.SyncGuest(vm, 15*time.Second)
	return res.ExitCode, res.Output, nil
}

// coreRun clones an image to a fresh ephemeral VM, runs one command, and
// destroys the clone. Returns the ephemeral name and the command result.
func coreRun(image, command string, timeout time.Duration) (name string, exitCode int, output string, err error) {
	src, _, ok := bundle.Find(image)
	if !ok {
		return "", 0, "", miragerr.New(miragerr.SlugNotFound, "no image named "+image)
	}
	name = "run-" + randHex(5)
	dst := bundle.Resolve(bundle.VM, name)
	id, err := engine.NewIdentity()
	if err != nil {
		return name, 0, "", err
	}
	if err := bundle.Clone(src, dst, id); err != nil {
		return name, 0, "", err
	}
	defer func() { _ = bundle.Remove(dst) }()

	cfg, err := dst.Load()
	if err != nil {
		return name, 0, "", err
	}
	cfg.Ephemeral = true
	if err := dst.Save(cfg); err != nil {
		return name, 0, "", err
	}
	vm, err := engine.StartFresh(dst, cfg, engine.Options{}, 5)
	if err != nil {
		return name, 0, "", miragerr.New(miragerr.SlugHostEnv, "vm start failed").WithCause(err)
	}
	defer func() { _ = vm.Stop() }()
	res, err := engine.AgentExec(vm, command, timeout)
	if err != nil {
		return name, 0, "", miragerr.New(miragerr.SlugAgentTimeout, "guest agent not reachable").WithCause(err)
	}
	return name, res.ExitCode, res.Output, nil
}

// coreList returns every image and VM bundle with its current run status.
func coreList() ([]lsRow, error) {
	var rows []lsRow
	for _, k := range []struct {
		kind  bundle.Kind
		label string
	}{{bundle.Image, "image"}, {bundle.VM, "vm"}} {
		list, err := bundle.List(k.kind)
		if err != nil {
			return nil, err
		}
		for _, b := range list {
			cfg, err := b.Load()
			if err != nil {
				continue
			}
			status := "stopped"
			if st, err := supervisor.Load(b.Name); err == nil && st.Running() {
				status = st.Status
			}
			rows = append(rows, lsRow{b.Name, k.label, cfg.OS, cfg.CPU, cfg.MemoryMB, status})
		}
	}
	return rows, nil
}

// coreScreenshot returns a PNG of a running VM's display. Screenshot needs the
// GUI session, so the VM must be started (a running supervisor).
func coreScreenshot(name string) ([]byte, error) {
	if supervisor.OwnedByGUI(name) {
		return nil, miragerr.New(miragerr.SlugInvalidState, name+" is open in the Mirage GUI").
			WithHint("the GUI shows it live; screenshot from there")
	}
	if !supervisor.IsRunning(name) {
		return nil, miragerr.New(miragerr.SlugInvalidState, name+" is not running").
			WithHint("start it first: mirage start " + name)
	}
	return supervisor.Screenshot(name)
}

// coreClone makes an instant CoW clone of an image/VM with a fresh identity.
func coreClone(srcName, dstName string) (mac string, err error) {
	src, _, ok := bundle.Find(srcName)
	if !ok {
		return "", miragerr.New(miragerr.SlugNotFound, "no bundle named "+srcName)
	}
	id, err := engine.NewIdentity()
	if err != nil {
		return "", err
	}
	if err := bundle.Clone(src, bundle.Resolve(bundle.VM, dstName), id); err != nil {
		return "", err
	}
	return id.MAC, nil
}

// coreAutologin enables boot-to-desktop for a guest user by writing
// /etc/kcpassword and setting autoLoginUser, both via the root guest agent. The
// obfuscated password is base64'd into a single guest command, so the plaintext
// never appears in argv on either host or guest; the host caller passes it in
// out of band (stdin). The VM must have a SecureToken user for the GUI session
// to actually start — a Setup-Assistant account qualifies, an offline one does not.
func coreAutologin(name, user, password string, timeout time.Duration) error {
	if user == "" {
		return miragerr.New(miragerr.SlugInvalidState, "user is required")
	}
	enc := base64.StdEncoding.EncodeToString(kcpassword.Encode([]byte(password)))
	// One shell command, run as root by the agent: decode kcpassword into place,
	// lock it down, then point the login window at the user. base64 -D is BSD
	// (the macOS guest's base64); printf %s avoids a trailing newline.
	// autoLoginUserScreenLocked must be cleared too: if it is set, macOS performs
	// the auto-login but then presents a locked screen (password prompt) instead
	// of the desktop. It is runtime state, though — macOS re-arms it to 1 whenever
	// the screen locks (idle screensaver, or wake/restore). So we also disable the
	// screen lock itself for the user (no screensaver password, no idle
	// screensaver), keeping the flag at 0 across reboots.
	//
	// `sync` is essential — coreExec cold-boots then force-stops the VM, so an
	// unsynced write to /etc/kcpassword is lost on power-off (the file's metadata
	// persists but its contents do not). Flush before returning.
	lw := "/Library/Preferences/com.apple.loginwindow"
	cmd := fmt.Sprintf(
		"printf %%s '%[1]s' | base64 -D > /etc/kcpassword && chmod 600 /etc/kcpassword && "+
			"defaults write %[2]s autoLoginUser -string '%[3]s' && "+
			"defaults write %[2]s autoLoginUserScreenLocked -bool false; "+
			"sudo -u '%[3]s' defaults write com.apple.screensaver askForPassword -int 0; "+
			"sudo -u '%[3]s' defaults write com.apple.screensaver askForPasswordDelay -int 0; "+
			"sudo -u '%[3]s' defaults -currentHost write com.apple.screensaver idleTime -int 0; "+
			"sync",
		enc, lw, user)
	code, out, err := coreExec(name, cmd, timeout)
	if err != nil {
		return err
	}
	if code != 0 {
		return miragerr.New(miragerr.SlugInvalidState,
			"enabling auto-login failed in "+name+": "+out).
			WithHint("the guest agent runs as root; check the user name exists in the guest")
	}
	return nil
}

// randHex returns n random hex characters for ephemeral VM names.
func randHex(n int) string {
	b := make([]byte, (n+1)/2)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)[:n]
}
