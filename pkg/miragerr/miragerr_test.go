package miragerr

import "testing"

func TestExitCodes(t *testing.T) {
	cases := map[Slug]int{
		SlugNotFound:     ExitNotFound,
		SlugConflict:     ExitConflict,
		SlugInvalidState: ExitInvalidState,
		SlugVMLimit:      ExitVMLimit,
		SlugAgentTimeout: ExitAgentTimeout,
		SlugHostEnv:      ExitHostEnv,
	}
	for slug, want := range cases {
		if got := New(slug, "x").ExitCode(); got != want {
			t.Errorf("%s: exit code = %d, want %d", slug, got, want)
		}
	}
}

func TestVMLimitRetryableByDefault(t *testing.T) {
	if !New(SlugVMLimit, "x").Retryable {
		t.Error("macos_vm_limit should default to retryable")
	}
	if New(SlugConflict, "x").Retryable {
		t.Error("conflict should default to non-retryable")
	}
}

func TestAsErrorUnwrap(t *testing.T) {
	base := New(SlugNotFound, "missing")
	if AsError(base) == nil {
		t.Fatal("AsError should extract a *Error")
	}
	if AsError(errString("plain")) != nil {
		t.Error("AsError should return nil for a non-typed error")
	}
}

type errString string

func (e errString) Error() string { return string(e) }
