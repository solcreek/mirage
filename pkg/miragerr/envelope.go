package miragerr

import (
	"encoding/json"
	"fmt"
	"io"
)

// SchemaVersion is the envelope schema version, bumped on breaking changes.
const SchemaVersion = 1

// Envelope is the single JSON object every command emits under --json: exactly
// one envelope on stdout at exit. Human progress goes to stderr.
type Envelope struct {
	OK            bool       `json:"ok"`
	SchemaVersion int        `json:"schema_version"`
	Data          any        `json:"data,omitempty"`
	Error         *envErr    `json:"error,omitempty"`
}

type envErr struct {
	Code      Slug   `json:"code"`
	ExitCode  int    `json:"exit_code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
	Hint      string `json:"hint,omitempty"`
}

// WriteData writes a success envelope and returns exit code 0.
func WriteData(w io.Writer, data any) int {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(Envelope{OK: true, SchemaVersion: SchemaVersion, Data: data})
	return 0
}

// WriteError writes a failure envelope and returns the error's exit code.
// A non-typed error is rendered as a generic host_env-free failure (exit 1).
func WriteError(w io.Writer, err error) int {
	me := AsError(err)
	if me == nil {
		me = &Error{Slug: "", Message: err.Error()}
	}
	exit := me.ExitCode()
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(Envelope{
		OK:            false,
		SchemaVersion: SchemaVersion,
		Error: &envErr{
			Code: me.Slug, ExitCode: exit, Message: me.Message,
			Retryable: me.Retryable, Hint: me.Hint,
		},
	})
	return exit
}

// FprintErr writes a human-readable error line to stderr (non-JSON mode).
func FprintErr(w io.Writer, err error) int {
	me := AsError(err)
	if me == nil {
		fmt.Fprintln(w, "mirage:", err.Error())
		return ExitGeneric
	}
	fmt.Fprintf(w, "mirage: %s: %s\n", me.Slug, me.Message)
	if me.wrapped != nil {
		fmt.Fprintf(w, "  cause: %s\n", me.wrapped.Error())
	}
	if me.Hint != "" {
		fmt.Fprintf(w, "  hint: %s\n", me.Hint)
	}
	return me.ExitCode()
}
