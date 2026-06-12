package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/solcreek/mirage/internal/bundle"
	"github.com/solcreek/mirage/internal/engine"
	"github.com/solcreek/mirage/pkg/miragerr"
)

const version = "0.1.0-dev"

// parseMixed parses flags that may appear before, after, or interspersed with
// positional arguments — Go's flag package stops at the first positional, so we
// resume parsing after each one. Returns the collected positionals.
func parseMixed(fs *flag.FlagSet, args []string) ([]string, error) {
	var positionals []string
	rest := args
	for len(rest) > 0 {
		if err := fs.Parse(rest); err != nil {
			return nil, err
		}
		rest = fs.Args()
		if len(rest) > 0 {
			positionals = append(positionals, rest[0])
			rest = rest[1:]
		}
	}
	return positionals, nil
}

// cmdContext carries global flags and renders the result envelope.
type cmdContext struct {
	json bool
}

// run renders (data, err) as either a JSON envelope or human output and
// returns the process exit code.
func (c *cmdContext) run(data any, err error) int {
	if err != nil {
		if c.json {
			return miragerr.WriteError(os.Stdout, err)
		}
		return miragerr.FprintErr(os.Stderr, err)
	}
	if c.json {
		return miragerr.WriteData(os.Stdout, data)
	}
	printHuman(data)
	return 0
}

func cmdCreate(args []string) (any, error) {
	fs := flag.NewFlagSet("create", flag.ContinueOnError)
	ipsw := fs.String("ipsw", "", "path to a macOS restore image (.ipsw)")
	diskGB := fs.Int64("disk-gb", 40, "disk size in GB (sparse)")
	pos, err := parseMixed(fs, args)
	if err != nil {
		return nil, miragerr.New(miragerr.SlugHostEnv, "bad flags")
	}
	if len(pos) != 1 {
		return nil, miragerr.New(miragerr.SlugHostEnv, "usage: mirage create <name> --ipsw <path>")
	}
	if *ipsw == "" {
		return nil, miragerr.New(miragerr.SlugHostEnv, "--ipsw is required")
	}
	name := pos[0]
	b := bundle.Resolve(bundle.Image, name)
	if _, err := os.Stat(b.ConfigPath()); err == nil {
		return nil, miragerr.New(miragerr.SlugConflict, "image "+name+" already exists")
	}

	info, err := engine.InspectIPSW(*ipsw)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(os.Stderr, "installing macOS %d.%d.%d (build %s) — this takes several minutes\n",
		info.Major, info.Minor, info.Patch, info.Build)

	cfg, err := engine.Install(context.Background(), b, *ipsw, *diskGB, func(f float64) {
		fmt.Fprintf(os.Stderr, "  install progress: %.0f%%\n", f*100)
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"name": name, "os": cfg.OS, "cpu": cfg.CPU, "memory_mb": cfg.MemoryMB,
		"macos_build": info.Build, "path": b.Dir,
	}, nil
}

// lsRow is one line of `mirage ls` output (package-level so the human renderer
// can type-assert it).
type lsRow struct {
	Name  string `json:"name"`
	Kind  string `json:"kind"`
	OS    string `json:"os"`
	CPU   uint   `json:"cpu"`
	MemMB uint64 `json:"memory_mb"`
}

func cmdLs(_ []string) (any, error) {
	var rows []lsRow
	for _, k := range []struct {
		kind  bundle.Kind
		label string
	}{{bundle.Image, "image"}, {bundle.VM, "vm"}} {
		list, err := bundle.List(k.kind)
		if err != nil {
			return nil, err
		}
		for _, b := range list {
			cfg, err := b.Load()
			if err != nil {
				continue
			}
			rows = append(rows, lsRow{b.Name, k.label, cfg.OS, cfg.CPU, cfg.MemoryMB})
		}
	}
	return map[string]any{"bundles": rows}, nil
}

func cmdClone(args []string) (any, error) {
	if len(args) != 2 {
		return nil, miragerr.New(miragerr.SlugHostEnv, "usage: mirage clone <src> <dst>")
	}
	srcName, dstName := args[0], args[1]
	src, _, ok := bundle.Find(srcName)
	if !ok {
		return nil, miragerr.New(miragerr.SlugNotFound, "no bundle named "+srcName)
	}
	dst := bundle.Resolve(bundle.VM, dstName)
	id, err := engine.NewIdentity()
	if err != nil {
		return nil, err
	}
	if err := bundle.Clone(src, dst, id); err != nil {
		return nil, err
	}
	return map[string]any{"name": dstName, "from": srcName, "mac": id.MAC, "path": dst.Dir}, nil
}

func cmdStart(args []string) (any, error) {
	fs := flag.NewFlagSet("start", flag.ContinueOnError)
	gui := fs.Bool("gui", false, "open an interactive window (foreground)")
	share := fs.String("share", "", "host directory to expose to the guest over VirtioFS (tag \"mirage\")")
	tools := fs.String("tools", "", "attach a read-only tools image (auto-mounts in the guest)")
	pos, err := parseMixed(fs, args)
	if err != nil {
		return nil, miragerr.New(miragerr.SlugHostEnv, "bad flags")
	}
	if len(pos) != 1 {
		return nil, miragerr.New(miragerr.SlugHostEnv, "usage: mirage start <name> --gui [--share <dir>] [--tools <img>]")
	}
	name := pos[0]
	b, _, ok := bundle.Find(name)
	if !ok {
		return nil, miragerr.New(miragerr.SlugNotFound, "no bundle named "+name)
	}
	cfg, err := b.Load()
	if err != nil {
		return nil, err
	}
	if !*gui {
		return nil, miragerr.New(miragerr.SlugInvalidState,
			"headless start needs the per-VM supervisor (not in this build); use --gui").
			WithHint("headless `mirage start` lands with the supervisor milestone")
	}
	vm, err := engine.BuildVM(b, cfg, engine.Options{Share: *share, ToolsImage: *tools})
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(os.Stderr, "booting %s with a window — close it to stop the VM\n", name)
	if err := engine.StartGUI(vm, "Mirage: "+name, float64(cfg.Display.Width)/2, float64(cfg.Display.Height)/2); err != nil {
		return nil, miragerr.New(miragerr.SlugHostEnv, "gui session failed").WithCause(err)
	}
	return map[string]any{"name": name, "stopped": true}, nil
}

// cmdExec boots a VM headlessly, waits for the guest agent on vsock, runs one
// command, prints its output, and stops the VM. Because vz ties VM lifetime to
// this process, exec is one-shot (boot→exec→stop) until the per-VM supervisor
// lands; persistent `start`/`exec` is the next milestone.
func cmdExec(args []string) (any, error) {
	// Split on "--": everything after is the guest command.
	var name string
	var cmd []string
	timeout := 3 * time.Minute
	rest := args
	for i := 0; i < len(rest); i++ {
		if rest[i] == "--" {
			cmd = rest[i+1:]
			break
		}
		if name == "" {
			name = rest[i]
		}
	}
	if name == "" || len(cmd) == 0 {
		return nil, miragerr.New(miragerr.SlugHostEnv, "usage: mirage exec <name> -- <command...>")
	}
	b, _, ok := bundle.Find(name)
	if !ok {
		return nil, miragerr.New(miragerr.SlugNotFound, "no bundle named "+name)
	}
	cfg, err := b.Load()
	if err != nil {
		return nil, err
	}
	// StartFresh retries briefly: a prior one-shot exec may still be releasing
	// the disk image (vz Stop is async), which transiently fails Start. The
	// per-VM supervisor will remove the boot-per-exec pattern entirely.
	vm, err := engine.StartFresh(b, cfg, engine.Options{}, 5)
	if err != nil {
		return nil, miragerr.New(miragerr.SlugHostEnv, "vm start failed").
			WithHint("another VM using this image may still be shutting down").WithCause(err)
	}
	defer func() { _ = vm.Stop() }()

	fmt.Fprintf(os.Stderr, "waiting for guest agent on %s…\n", name)
	res, err := engine.AgentExec(vm, strings.Join(cmd, " "), timeout)
	if err != nil {
		return nil, miragerr.New(miragerr.SlugAgentTimeout, "guest agent not reachable").
			WithHint("is mirage-agent installed in the image? run the tools-image install.sh once").WithCause(err)
	}
	return map[string]any{"name": name, "exit_code": res.ExitCode, "output": res.Output}, nil
}

// cmdRun is the agent fan-out primitive: clone an image to a fresh ephemeral
// VM, boot it, run one command, then destroy the clone. The clone is marked
// ephemeral so a crash mid-run leaves a reapable bundle.
func cmdRun(args []string) (any, error) {
	var image string
	var cmd []string
	keep := false
	for i := 0; i < len(args); i++ {
		if args[i] == "--" {
			cmd = args[i+1:]
			break
		}
		if args[i] == "--keep" {
			keep = true
			continue
		}
		if image == "" {
			image = args[i]
		}
	}
	if image == "" || len(cmd) == 0 {
		return nil, miragerr.New(miragerr.SlugHostEnv, "usage: mirage run <image> [--keep] -- <command...>")
	}
	src, _, ok := bundle.Find(image)
	if !ok {
		return nil, miragerr.New(miragerr.SlugNotFound, "no image named "+image)
	}

	name := "run-" + randHex(5)
	dst := bundle.Resolve(bundle.VM, name)
	id, err := engine.NewIdentity()
	if err != nil {
		return nil, err
	}
	if err := bundle.Clone(src, dst, id); err != nil {
		return nil, err
	}
	cfg, err := dst.Load()
	if err != nil {
		return nil, err
	}
	cfg.Ephemeral = true
	if err := dst.Save(cfg); err != nil {
		return nil, err
	}
	if !keep {
		defer func() { _ = bundle.Remove(dst) }()
	}

	vm, err := engine.StartFresh(dst, cfg, engine.Options{}, 5)
	if err != nil {
		return nil, miragerr.New(miragerr.SlugHostEnv, "vm start failed").WithCause(err)
	}
	defer func() { _ = vm.Stop() }()

	fmt.Fprintf(os.Stderr, "ephemeral %s: waiting for guest agent…\n", name)
	res, err := engine.AgentExec(vm, strings.Join(cmd, " "), 3*time.Minute)
	if err != nil {
		return nil, miragerr.New(miragerr.SlugAgentTimeout, "guest agent not reachable").WithCause(err)
	}
	out := map[string]any{"ephemeral": name, "exit_code": res.ExitCode, "output": res.Output}
	if keep {
		out["kept"] = true
	}
	return out, nil
}

// randHex returns n random hex characters for ephemeral VM names.
func randHex(n int) string {
	b := make([]byte, (n+1)/2)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)[:n]
}

func cmdRm(args []string) (any, error) {
	if len(args) != 1 {
		return nil, miragerr.New(miragerr.SlugHostEnv, "usage: mirage rm <name>")
	}
	name := args[0]
	b, _, ok := bundle.Find(name)
	if !ok {
		return nil, miragerr.New(miragerr.SlugNotFound, "no bundle named "+name)
	}
	if err := bundle.Remove(b); err != nil {
		return nil, err
	}
	return map[string]any{"name": name, "deleted": true}, nil
}

// printHuman renders a result map as readable lines.
func printHuman(data any) {
	switch d := data.(type) {
	case map[string]any:
		if bundles, ok := d["bundles"]; ok {
			printBundles(bundles)
			return
		}
		for k, v := range d {
			fmt.Printf("%-12s %v\n", k+":", v)
		}
	case map[string]string:
		for k, v := range d {
			fmt.Printf("%-12s %v\n", k+":", v)
		}
	default:
		fmt.Printf("%v\n", d)
	}
}

func printBundles(bundles any) {
	rows, ok := bundles.([]lsRow)
	if !ok {
		fmt.Printf("%v\n", bundles)
		return
	}
	if len(rows) == 0 {
		fmt.Println("no bundles (create one with: mirage create <name> --ipsw <path>)")
		return
	}
	fmt.Printf("%-16s %-6s %-6s %4s %8s\n", "NAME", "KIND", "OS", "CPU", "MEM(MB)")
	for _, r := range rows {
		fmt.Printf("%-16s %-6s %-6s %4d %8d\n", r.Name, r.Kind, r.OS, r.CPU, r.MemMB)
	}
}

