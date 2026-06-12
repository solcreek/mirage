package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/solcreek/mirage/internal/supervisor"
	"github.com/solcreek/mirage/pkg/miragerr"
)

// runVmm is the hidden `mirage __vmm <name>` entry: it becomes the per-VM
// supervisor and blocks for the VM's lifetime. Not an envelope command.
func runVmm(args []string) {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: mirage __vmm <name>")
		os.Exit(2)
	}
	name := args[0]
	supervisor.ClearStartError(name)
	if err := supervisor.Run(name); err != nil {
		supervisor.WriteStartError(name, err) // let the spawning CLI recover the reason
		fmt.Fprintln(os.Stderr, "mirage __vmm:", err)
		os.Exit(1)
	}
}

// startHeadless spawns a detached supervisor for name and waits until it
// reports running (or dies / times out).
func startHeadless(name string) (any, error) {
	if supervisor.IsRunning(name) {
		return nil, miragerr.New(miragerr.SlugConflict, name+" is already running")
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(supervisor.LogPath(name)), 0o700); err != nil {
		return nil, err
	}
	logf, err := os.OpenFile(supervisor.LogPath(name), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	defer logf.Close()

	cmd := exec.Command(exe, "__vmm", name)
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // detach into its own session
	if err := cmd.Start(); err != nil {
		return nil, miragerr.New(miragerr.SlugHostEnv, "could not spawn supervisor").WithCause(err)
	}

	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()

	fmt.Fprintf(os.Stderr, "starting %s…\n", name)
	deadline := time.Now().Add(3 * time.Minute)
	for {
		select {
		case werr := <-waitCh:
			// Recover the typed reason the supervisor recorded (quota,
			// boot failure, agent timeout); fall back to generic.
			if reason := supervisor.ReadStartError(name); reason != nil {
				return nil, reason
			}
			return nil, miragerr.New(miragerr.SlugHostEnv,
				"supervisor exited before the VM was ready").
				WithHint("see `mirage logs "+name+"`").WithCause(werr)
		default:
		}
		if st, err := supervisor.Load(name); err == nil && st.Status == supervisor.StatusRunning {
			return map[string]any{"name": name, "status": st.Status, "pid": st.PID}, nil
		}
		if time.Now().After(deadline) {
			return nil, miragerr.New(miragerr.SlugAgentTimeout, "VM did not become ready in time").
				WithHint("see `mirage logs " + name + "`")
		}
		time.Sleep(400 * time.Millisecond)
	}
}

func cmdStop(args []string) (any, error) {
	if len(args) != 1 {
		return nil, miragerr.New(miragerr.SlugHostEnv, "usage: mirage stop <name>")
	}
	name := args[0]
	if !supervisor.IsRunning(name) {
		supervisor.RemoveState(name) // clean up any stale state
		return nil, miragerr.New(miragerr.SlugInvalidState, name+" is not running")
	}
	if err := supervisor.Stop(name); err != nil {
		return nil, miragerr.New(miragerr.SlugHostEnv, "stop failed").WithCause(err)
	}
	return map[string]any{"name": name, "stopped": true}, nil
}

func cmdLogs(args []string) (any, error) {
	if len(args) != 1 {
		return nil, miragerr.New(miragerr.SlugHostEnv, "usage: mirage logs <name>")
	}
	raw, err := os.ReadFile(supervisor.LogPath(args[0]))
	if err != nil {
		return nil, miragerr.New(miragerr.SlugNotFound, "no logs for "+args[0])
	}
	return map[string]any{"name": args[0], "log": string(raw)}, nil
}
