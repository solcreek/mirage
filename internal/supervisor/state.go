// Package supervisor implements the per-VM helper process (__vmm) that owns a
// running vz.VirtualMachine and serves a per-VM Unix socket, plus the client
// side the CLI uses to reach it. This is the daemonless, helper-per-VM model:
// each running VM is one `mirage __vmm <name>` process.
package supervisor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/solcreek/mirage/internal/bundle"
	"github.com/solcreek/mirage/pkg/miragerr"
)

// Status values for a VM's lifecycle as seen by clients.
const (
	StatusBooting = "booting"
	StatusRunning = "running"
)

// Owner identifies which process model is running a VM. GUI-owned VMs run
// in-process in the SwiftUI app (no per-VM socket); supervisor-owned VMs are
// the headless __vmm helpers the CLI talks to. Both count toward the macOS-VM
// quota; the empty value means supervisor (backward compatible).
const (
	OwnerSupervisor = ""
	OwnerGUI        = "gui"
)

// State is the PID-stamped record a supervisor (or the GUI) writes so clients
// can find it and the quota can count it. Stored at <stateVMsDir>/<name>.json.
type State struct {
	Name      string `json:"name"`
	PID       int    `json:"pid"`
	OS        string `json:"os"`
	Socket    string `json:"socket"`
	Status    string `json:"status"`
	StartedAt string `json:"started_at"`
	Owner     string `json:"owner,omitempty"`
}

func dir() string { return bundle.StateVMsDir() }

func StatePath(name string) string  { return filepath.Join(dir(), name+".json") }
func SocketPath(name string) string { return filepath.Join(dir(), name+".sock") }
func LogPath(name string) string    { return filepath.Join(dir(), name+".log") }

// Save writes the state file atomically (0600).
func (s *State) Save() error {
	if err := os.MkdirAll(dir(), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := StatePath(s.Name) + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, StatePath(s.Name))
}

// Load reads a VM's state file.
func Load(name string) (*State, error) {
	raw, err := os.ReadFile(StatePath(name))
	if err != nil {
		return nil, err
	}
	var s State
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// List returns every state file present (alive or stale).
func List() ([]*State, error) {
	entries, err := os.ReadDir(dir())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []*State
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		s, err := Load(strings.TrimSuffix(e.Name(), ".json"))
		if err == nil {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// RemoveState deletes a VM's state file and socket.
func RemoveState(name string) {
	_ = os.Remove(StatePath(name))
	_ = os.Remove(SocketPath(name))
}

func errPath(name string) string { return filepath.Join(dir(), name+".err") }

// startErr is the serialized form of a typed startup failure, so the CLI can
// recover the real reason a detached supervisor exited before serving.
type startErr struct {
	Slug    string `json:"slug"`
	Message string `json:"message"`
	Hint    string `json:"hint"`
}

// ClearStartError removes any stale failure record (called before a start).
func ClearStartError(name string) { _ = os.Remove(errPath(name)) }

// WriteStartError records why a supervisor failed to start.
func WriteStartError(name string, err error) {
	se := startErr{Message: err.Error()}
	if me := miragerr.AsError(err); me != nil {
		se = startErr{Slug: string(me.Slug), Message: me.Message, Hint: me.Hint}
	}
	if raw, e := json.Marshal(se); e == nil {
		_ = os.MkdirAll(dir(), 0o700)
		_ = os.WriteFile(errPath(name), raw, 0o600)
	}
}

// ReadStartError returns the recorded startup failure as a typed error, or nil.
func ReadStartError(name string) error {
	raw, err := os.ReadFile(errPath(name))
	if err != nil {
		return nil
	}
	var se startErr
	if json.Unmarshal(raw, &se) != nil || se.Slug == "" {
		return nil
	}
	return miragerr.New(miragerr.Slug(se.Slug), se.Message).WithHint(se.Hint)
}

// alive reports whether a PID is a live process.
func alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}

// Running reports whether the supervisor for this VM is alive.
func (s *State) Running() bool { return alive(s.PID) }

// IsRunning reports whether a live supervisor exists for name.
func IsRunning(name string) bool {
	s, err := Load(name)
	return err == nil && s.Running()
}

// OwnedByGUI reports whether a live VM is owned by the GUI app (in-process, no
// per-VM socket — so the CLI can't drive it and should say so).
func OwnedByGUI(name string) bool {
	s, err := Load(name)
	return err == nil && s.Running() && s.Owner == OwnerGUI
}
