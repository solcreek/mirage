package supervisor

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/Code-Hex/vz/v3"
	"github.com/solcreek/mirage/internal/bundle"
	"github.com/solcreek/mirage/internal/engine"
	"github.com/solcreek/mirage/pkg/miragerr"
)

// bootTimestamp is injected by the caller (time.Now formatted) to keep this
// package free of wall-clock reads where avoidable; the server stamps its own.

// Run is the entry point for `mirage __vmm <name>`: it enforces the macOS-guest
// quota, boots the VM headless, waits for the guest agent, then serves the
// per-VM Unix socket until told to stop or signalled. It blocks for the VM's
// lifetime. When restore is true and a snapshot exists, it restores the warm
// saved state instead of cold-booting.
func Run(name string, restore bool) error {
	b, _, ok := bundle.Find(name)
	if !ok {
		return miragerr.New(miragerr.SlugNotFound, "no bundle named "+name)
	}
	cfg, err := b.Load()
	if err != nil {
		return err
	}

	st := &State{
		Name: name, PID: os.Getpid(), OS: cfg.OS,
		Socket: SocketPath(name), Status: StatusBooting,
		StartedAt: time.Now().UTC().Format(time.RFC3339),
	}
	// Claim a quota slot for macOS guests: count + write our state file under
	// a lock so two concurrent starts can't both pass the count-of-2 check.
	// The lock is released immediately; the slot is then held by the presence
	// of our (PID-stamped) state file, not by the lock.
	if err := reserveSlot(st); err != nil {
		return err
	}
	defer RemoveState(name)

	// Listen before booting so clients can observe "booting" and connect early.
	_ = os.Remove(st.Socket)
	ln, err := net.Listen("unix", st.Socket)
	if err != nil {
		return fmt.Errorf("listen %s: %w", st.Socket, err)
	}
	defer ln.Close()
	_ = os.Chmod(st.Socket, 0o600)

	var vm *vz.VirtualMachine
	if restore && b.HasSnapshot() {
		// Restore the warm snapshot: reset the disk to the snapshot's paired
		// disk first so the restored RAM/device state matches it.
		if err := b.ResetDiskToSnapshot(); err != nil {
			return err
		}
		vm, err = engine.RestoreFresh(b, cfg, engine.Options{}, b.SnapshotStatePath(), 5)
		if err != nil {
			return miragerr.New(miragerr.SlugHostEnv, "restore from snapshot failed").
				WithHint("the snapshot may be incompatible (host/OS changed); `mirage snapshot " + name + " --discard` then start fresh").
				WithCause(err)
		}
	} else {
		vm, err = engine.StartFresh(b, cfg, engine.Options{}, 5)
		if err != nil {
			return miragerr.New(miragerr.SlugHostEnv, "vm start failed").WithCause(err)
		}
	}

	// Wait for the guest agent, then mark running.
	if _, err := engine.AgentExec(vm, "true", 3*time.Minute); err != nil {
		_ = vm.Stop()
		return miragerr.New(miragerr.SlugAgentTimeout, "guest agent never came up").WithCause(err)
	}
	st.Status = StatusRunning
	_ = st.Save()

	srv := &server{vm: vm, name: name, b: b}
	return srv.serve(ln)
}

type server struct {
	vm   *vz.VirtualMachine
	name string
	b    bundle.Bundle
	once sync.Once
	done chan struct{}
}

func (s *server) serve(ln net.Listener) error {
	s.done = make(chan struct{})

	// Stop cleanly on SIGTERM/SIGINT.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		select {
		case <-sig:
		case <-s.done:
		}
		_ = ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-s.done:
			default:
			}
			break // listener closed → shut down
		}
		s.handle(conn)
		select {
		case <-s.done:
			ln.Close()
			s.shutdown()
			return nil
		default:
		}
	}
	s.shutdown()
	return nil
}

func (s *server) shutdown() {
	s.once.Do(func() {
		_ = s.vm.Stop()
		_ = engine.WaitState(s.vm, vz.VirtualMachineStateStopped, 30*time.Second)
	})
}

// snapshot freezes the live VM into a restore point without ending the session:
// pause + save RAM/device state, clone the now-quiesced disk as the paired disk,
// then resume. The live disk may diverge afterwards — restore resets it to this
// clone, so the snapshot stays consistent.
func (s *server) snapshot() error {
	if err := engine.SaveState(s.vm, s.b.SnapshotStatePath()); err != nil {
		return err
	}
	if err := s.b.SnapshotDisk(); err != nil {
		_ = s.vm.Resume() // best effort: get the VM running again even on failure
		return err
	}
	return s.vm.Resume()
}

func (s *server) handle(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Minute))

	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return
	}
	var req Request
	if err := json.Unmarshal([]byte(line), &req); err != nil {
		writeResp(conn, Response{OK: false, Error: "bad request: " + err.Error()})
		return
	}

	switch req.Op {
	case OpPing:
		writeResp(conn, Response{OK: true})
	case OpInfo:
		st, _ := Load(s.name)
		writeResp(conn, Response{OK: true, State: st})
	case OpStop:
		writeResp(conn, Response{OK: true})
		close(s.done) // serve loop sees this after handle returns
	case OpExec:
		timeout := 2 * time.Minute
		if req.TimeoutS > 0 {
			timeout = time.Duration(req.TimeoutS) * time.Second
		}
		res, err := engine.AgentExec(s.vm, req.Cmd, timeout)
		if err != nil {
			writeResp(conn, Response{OK: false, Error: err.Error()})
			return
		}
		writeResp(conn, Response{OK: true, ExitCode: res.ExitCode, Output: res.Output})
	case OpScreenshot:
		png, err := engine.AgentScreenshot(s.vm, time.Minute)
		if err != nil {
			writeResp(conn, Response{OK: false, Error: err.Error()})
			return
		}
		writeResp(conn, Response{OK: true, PNGBase64: base64.StdEncoding.EncodeToString(png)})
	case OpSnapshot:
		if err := s.snapshot(); err != nil {
			writeResp(conn, Response{OK: false, Error: err.Error()})
			return
		}
		writeResp(conn, Response{OK: true})
	default:
		writeResp(conn, Response{OK: false, Error: "unknown op: " + req.Op})
	}
}

func writeResp(conn net.Conn, r Response) {
	b, _ := json.Marshal(r)
	_, _ = conn.Write(append(b, '\n'))
}

// reserveSlot enforces the host-global limit of 2 running macOS guests. It
// holds an exclusive lock only across counting live macOS supervisors and
// writing this VM's state file, then releases it — the slot is subsequently
// held by the presence of the PID-stamped state file, not by the lock.
func reserveSlot(st *State) error {
	if st.OS != "macos" {
		return st.Save() // only macOS guests are quota-limited
	}
	if err := os.MkdirAll(dir(), 0o700); err != nil {
		return err
	}
	lf, err := os.OpenFile(filepath.Join(dir(), ".quota.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lf.Close()
	if err := syscall.Flock(int(lf.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lf.Fd()), syscall.LOCK_UN)

	states, _ := List()
	running := 0
	for _, s := range states {
		if s.OS == "macos" && s.Name != st.Name && s.Running() {
			running++
		}
	}
	if running >= 2 {
		return miragerr.New(miragerr.SlugVMLimit,
			fmt.Sprintf("the host limit of 2 running macOS VMs is reached (running: %s)", runningMacNames(states, st.Name))).
			WithHint("stop one with `mirage stop <name>`; the kernel quota also counts macOS VMs from other apps")
	}
	return st.Save() // claim the slot while holding the lock
}

func runningMacNames(states []*State, except string) string {
	var ns []string
	for _, s := range states {
		if s.OS == "macos" && s.Name != except && s.Running() {
			ns = append(ns, s.Name)
		}
	}
	out := ""
	for i, n := range ns {
		if i > 0 {
			out += ", "
		}
		out += n
	}
	return out
}
