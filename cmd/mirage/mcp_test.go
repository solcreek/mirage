package main

import (
	"context"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestTimeoutOr(t *testing.T) {
	if d := timeoutOr(0); d != 2*time.Minute {
		t.Errorf("default = %v, want 2m", d)
	}
	if d := timeoutOr(-5); d != 2*time.Minute {
		t.Errorf("negative = %v, want 2m", d)
	}
	if d := timeoutOr(30); d != 30*time.Second {
		t.Errorf("explicit = %v, want 30s", d)
	}
}

// TestRegisterTools verifies the full tool set is registered and discoverable
// over an in-memory MCP session (no VMs are started).
func TestRegisterTools(t *testing.T) {
	s := mcp.NewServer(&mcp.Implementation{Name: "mirage", Version: "test"}, nil)
	registerTools(s)

	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	ss, err := s.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()

	client := mcp.NewClient(&mcp.Implementation{Name: "probe", Version: "test"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	res, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, tool := range res.Tools {
		got[tool.Name] = true
	}
	for _, want := range []string{"vm_list", "vm_clone", "vm_start", "vm_stop", "vm_delete", "vm_exec", "vm_run", "vm_screenshot"} {
		if !got[want] {
			t.Errorf("tool %q not registered", want)
		}
	}
}
