package miragerr

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestWriteDataEnvelope(t *testing.T) {
	var buf bytes.Buffer
	code := WriteData(&buf, map[string]string{"name": "vm1"})
	if code != 0 {
		t.Errorf("success exit code = %d, want 0", code)
	}
	var env Envelope
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if !env.OK || env.SchemaVersion != SchemaVersion || env.Error != nil {
		t.Errorf("unexpected envelope: %+v", env)
	}
}

func TestWriteErrorEnvelope(t *testing.T) {
	var buf bytes.Buffer
	code := WriteError(&buf, New(SlugVMLimit, "too many").WithHint("stop one"))
	if code != ExitVMLimit {
		t.Errorf("exit code = %d, want %d", code, ExitVMLimit)
	}
	var env Envelope
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.OK || env.Error == nil {
		t.Fatal("expected a failure envelope with an error")
	}
	if env.Error.Code != SlugVMLimit || env.Error.ExitCode != ExitVMLimit ||
		!env.Error.Retryable || env.Error.Hint != "stop one" {
		t.Errorf("unexpected error body: %+v", env.Error)
	}
}

func TestWriteErrorUntyped(t *testing.T) {
	var buf bytes.Buffer
	code := WriteError(&buf, errString("boom"))
	if code != ExitGeneric {
		t.Errorf("untyped error exit = %d, want %d", code, ExitGeneric)
	}
}
