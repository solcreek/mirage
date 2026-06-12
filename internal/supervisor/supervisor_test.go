package supervisor

import (
	"os"
	"testing"

	"github.com/solcreek/mirage/pkg/miragerr"
)

// isolate points the state dir at a temp dir for the duration of a test.
func isolate(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
}

func TestStateRoundTrip(t *testing.T) {
	isolate(t)
	want := &State{Name: "vm1", PID: os.Getpid(), OS: "macos", Status: StatusRunning}
	if err := want.Save(); err != nil {
		t.Fatal(err)
	}
	got, err := Load("vm1")
	if err != nil {
		t.Fatal(err)
	}
	if got.PID != want.PID || got.OS != want.OS || got.Status != want.Status {
		t.Errorf("round-trip mismatch: %+v", got)
	}
	if !got.Running() {
		t.Error("state with this process's PID should be Running()")
	}
}

func TestListAndIsRunning(t *testing.T) {
	isolate(t)
	(&State{Name: "live", PID: os.Getpid(), OS: "macos"}).Save()
	(&State{Name: "dead", PID: 0x7fffffff, OS: "macos"}).Save() // PID won't exist

	list, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("List returned %d, want 2", len(list))
	}
	if !IsRunning("live") {
		t.Error("live should be running")
	}
	if IsRunning("dead") {
		t.Error("dead PID should not be running")
	}
}

func TestReserveSlotQuota(t *testing.T) {
	isolate(t)
	// Two live macOS VMs occupy the host limit.
	for _, n := range []string{"a", "b"} {
		if err := reserveSlot(&State{Name: n, PID: os.Getpid(), OS: "macos"}); err != nil {
			t.Fatalf("reserving %s should succeed: %v", n, err)
		}
	}
	// A third macOS VM must be refused with the typed quota error.
	err := reserveSlot(&State{Name: "c", PID: os.Getpid(), OS: "macos"})
	if me := miragerr.AsError(err); me == nil || me.Slug != miragerr.SlugVMLimit {
		t.Fatalf("third macOS VM: got %v, want macos_vm_limit", err)
	}
	if !me(err).Retryable {
		t.Error("macos_vm_limit should be retryable")
	}
}

func TestReserveSlotIgnoresDeadAndLinux(t *testing.T) {
	isolate(t)
	// A dead macOS slot must not count toward the limit.
	(&State{Name: "ghost", PID: 0x7fffffff, OS: "macos"}).Save()
	(&State{Name: "live", PID: os.Getpid(), OS: "macos"}).Save()
	// One live macOS VM + a dead one ⇒ a second live macOS VM is still allowed.
	if err := reserveSlot(&State{Name: "ok", PID: os.Getpid(), OS: "macos"}); err != nil {
		t.Fatalf("second live macOS VM should be allowed: %v", err)
	}
	// Linux guests are never quota-limited.
	for i := 0; i < 5; i++ {
		if err := reserveSlot(&State{Name: "lx", PID: os.Getpid(), OS: "linux"}); err != nil {
			t.Fatalf("linux guest should bypass quota: %v", err)
		}
	}
}

func TestStartErrorRoundTrip(t *testing.T) {
	isolate(t)
	orig := miragerr.New(miragerr.SlugVMLimit, "too many").WithHint("stop one")
	WriteStartError("vm", orig)

	got := ReadStartError("vm")
	me := miragerr.AsError(got)
	if me == nil || me.Slug != miragerr.SlugVMLimit || me.Hint != "stop one" {
		t.Fatalf("recovered error mismatch: %+v", got)
	}

	ClearStartError("vm")
	if ReadStartError("vm") != nil {
		t.Error("ClearStartError should remove the record")
	}
}

// me is a tiny helper so the quota test can read .Retryable inline.
func me(err error) *miragerr.Error { return miragerr.AsError(err) }
