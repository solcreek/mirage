// Package miragerr defines Mirage's typed error model, exit codes, and the
// JSON output envelope shared by every command. The slug table is append-only
// and is part of the v0.1 contract.
package miragerr

import "errors"

// Exit codes. 1 is generic, 2 is usage, 100+ are typed (one per slug).
const (
	ExitGeneric = 1
	ExitUsage   = 2

	ExitNotFound     = 100
	ExitConflict     = 101
	ExitInvalidState = 102
	ExitVMLimit      = 103
	ExitAgentTimeout = 104
	ExitHostEnv      = 105
)

// Slug is a stable, machine-readable error identifier.
type Slug string

const (
	SlugNotFound     Slug = "not_found"
	SlugConflict     Slug = "conflict"
	SlugInvalidState Slug = "invalid_state"
	SlugVMLimit      Slug = "macos_vm_limit"
	SlugAgentTimeout Slug = "agent_timeout"
	SlugHostEnv      Slug = "host_env"
)

var slugExit = map[Slug]int{
	SlugNotFound:     ExitNotFound,
	SlugConflict:     ExitConflict,
	SlugInvalidState: ExitInvalidState,
	SlugVMLimit:      ExitVMLimit,
	SlugAgentTimeout: ExitAgentTimeout,
	SlugHostEnv:      ExitHostEnv,
}

// Error is a typed Mirage error. Retryable defaults to false; the only slug
// that overrides it to true by default is the VM-limit error.
type Error struct {
	Slug      Slug
	Message   string
	Hint      string
	Retryable bool
	wrapped   error
}

func (e *Error) Error() string { return string(e.Slug) + ": " + e.Message }
func (e *Error) Unwrap() error { return e.wrapped }

// ExitCode returns the process exit code for this error.
func (e *Error) ExitCode() int {
	if c, ok := slugExit[e.Slug]; ok {
		return c
	}
	return ExitGeneric
}

// New builds a typed error. Retryability follows the slug default; use
// WithRetryable to override per-error (the envelope field is authoritative).
func New(slug Slug, msg string) *Error {
	return &Error{Slug: slug, Message: msg, Retryable: slug == SlugVMLimit}
}

func (e *Error) WithHint(h string) *Error      { e.Hint = h; return e }
func (e *Error) WithRetryable(r bool) *Error    { e.Retryable = r; return e }
func (e *Error) WithCause(err error) *Error     { e.wrapped = err; return e }

// AsError extracts a *Error from err, or nil if err is not one.
func AsError(err error) *Error {
	var me *Error
	if errors.As(err, &me) {
		return me
	}
	return nil
}
