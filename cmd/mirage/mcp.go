package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/solcreek/mirage/internal/bundle"
	"github.com/solcreek/mirage/internal/supervisor"
	"github.com/solcreek/mirage/pkg/miragerr"
)

const mcpInstructions = `Mirage controls ephemeral macOS virtual machines on this Mac.

Model guidance:
- VMs are disposable. The canonical loop is vm_clone → vm_exec → vm_delete, or
  vm_run for a one-shot (it clones, runs, and destroys in a single call).
- At most 2 macOS VMs can run at once (a host-wide kernel limit, counting VMs
  from other apps too). Starting a 3rd returns a macos_vm_limit error naming the
  running VMs — stop one before starting another.
- vm_exec/vm_run commands run as root inside the guest.
- vm_start keeps a VM running so repeated vm_exec calls are fast; without it,
  vm_exec cold-boots the VM for one command (slower).`

// runMCPServer starts the stdio MCP server and blocks until the client
// disconnects. Not an envelope command.
func runMCPServer() {
	s := mcp.NewServer(
		&mcp.Implementation{Name: "mirage", Title: "Mirage macOS VMs", Version: version},
		&mcp.ServerOptions{Instructions: mcpInstructions},
	)
	registerTools(s)
	if err := s.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		fmt.Fprintln(os.Stderr, "mirage mcp:", err)
		os.Exit(1)
	}
}

// --- tool I/O types (the SDK infers JSON schema from these) ---

type emptyIn struct{}

type vmInfo struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	OS     string `json:"os"`
	Status string `json:"status"`
}
type listOut struct {
	VMs []vmInfo `json:"vms"`
}

type cloneIn struct {
	Source string `json:"source" jsonschema:"image or VM to clone from"`
	Name   string `json:"name" jsonschema:"name for the new clone"`
}
type cloneOut struct {
	Name string `json:"name"`
	MAC  string `json:"mac"`
}

type nameIn struct {
	Name string `json:"name" jsonschema:"VM name"`
}
type startOut struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	PID    int    `json:"pid"`
}
type stoppedOut struct {
	Name    string `json:"name"`
	Stopped bool   `json:"stopped"`
}
type deletedOut struct {
	Name    string `json:"name"`
	Deleted bool   `json:"deleted"`
}

type execIn struct {
	Name     string `json:"name" jsonschema:"VM name"`
	Command  string `json:"command" jsonschema:"shell command to run in the guest (as root)"`
	TimeoutS int    `json:"timeout_s,omitempty" jsonschema:"timeout in seconds (default 120)"`
}
type runIn struct {
	Image    string `json:"image" jsonschema:"image to clone for this one-shot run"`
	Command  string `json:"command" jsonschema:"shell command to run in the guest (as root)"`
	TimeoutS int    `json:"timeout_s,omitempty" jsonschema:"timeout in seconds (default 120)"`
}
type execOut struct {
	ExitCode int    `json:"exit_code"`
	Output   string `json:"output"`
}
type runOut struct {
	Ephemeral string `json:"ephemeral"`
	ExitCode  int    `json:"exit_code"`
	Output    string `json:"output"`
}

func timeoutOr(s int) time.Duration {
	if s <= 0 {
		return 2 * time.Minute
	}
	return time.Duration(s) * time.Second
}

// ok is a tiny helper: an empty result so the SDK fills Content from the
// structured output.
func ok() *mcp.CallToolResult { return &mcp.CallToolResult{} }

func registerTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "vm_list",
		Description: "List all macOS images and VMs with their run status.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, _ emptyIn) (*mcp.CallToolResult, listOut, error) {
		rows, err := coreList()
		if err != nil {
			return nil, listOut{}, err
		}
		vms := make([]vmInfo, 0, len(rows))
		for _, r := range rows {
			vms = append(vms, vmInfo{r.Name, r.Kind, r.OS, r.Status})
		}
		return ok(), listOut{VMs: vms}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "vm_clone",
		Description: "Instantly clone an image or VM (copy-on-write) into a new VM with a fresh identity.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in cloneIn) (*mcp.CallToolResult, cloneOut, error) {
		mac, err := coreClone(in.Source, in.Name)
		if err != nil {
			return nil, cloneOut{}, err
		}
		return ok(), cloneOut{Name: in.Name, MAC: mac}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "vm_start",
		Description: "Boot a VM and keep it running so repeated vm_exec calls are fast. Returns macos_vm_limit if 2 macOS VMs already run.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in nameIn) (*mcp.CallToolResult, startOut, error) {
		status, pid, err := startHeadless(in.Name)
		if err != nil {
			return nil, startOut{}, err
		}
		return ok(), startOut{Name: in.Name, Status: status, PID: pid}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "vm_stop",
		Description: "Stop a running VM (frees a macOS-VM quota slot).",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in nameIn) (*mcp.CallToolResult, stoppedOut, error) {
		if !supervisor.IsRunning(in.Name) {
			supervisor.RemoveState(in.Name)
			return ok(), stoppedOut{Name: in.Name, Stopped: true}, nil
		}
		if err := supervisor.Stop(in.Name); err != nil {
			return nil, stoppedOut{}, err
		}
		return ok(), stoppedOut{Name: in.Name, Stopped: true}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "vm_delete",
		Description: "Delete a VM bundle from disk.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in nameIn) (*mcp.CallToolResult, deletedOut, error) {
		b, _, found := bundle.Find(in.Name)
		if !found {
			return nil, deletedOut{}, miragerr.New(miragerr.SlugNotFound, "no bundle named "+in.Name)
		}
		if supervisor.IsRunning(in.Name) {
			_ = supervisor.Stop(in.Name)
		}
		if err := bundle.Remove(b); err != nil {
			return nil, deletedOut{}, err
		}
		return ok(), deletedOut{Name: in.Name, Deleted: true}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "vm_exec",
		Description: "Run a shell command in a VM (as root) and return its stdout/stderr and exit code. Reuses a running VM if vm_start was called; otherwise cold-boots one-shot.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in execIn) (*mcp.CallToolResult, execOut, error) {
		exit, out, err := coreExec(in.Name, in.Command, timeoutOr(in.TimeoutS))
		if err != nil {
			return nil, execOut{}, err
		}
		return ok(), execOut{ExitCode: exit, Output: out}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "vm_run",
		Description: "One-shot: clone an image, run a command in the fresh VM, then destroy it. The agent fan-out primitive.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in runIn) (*mcp.CallToolResult, runOut, error) {
		name, exit, out, err := coreRun(in.Image, in.Command, timeoutOr(in.TimeoutS))
		if err != nil {
			return nil, runOut{}, err
		}
		return ok(), runOut{Ephemeral: name, ExitCode: exit, Output: out}, nil
	})
}
