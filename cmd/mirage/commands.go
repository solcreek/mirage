package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/solcreek/mirage/internal/bundle"
	"github.com/solcreek/mirage/internal/engine"
	"github.com/solcreek/mirage/pkg/miragerr"
)

const version = "0.1.0-dev"

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
	if err := fs.Parse(args); err != nil {
		return nil, miragerr.New(miragerr.SlugHostEnv, "bad flags")
	}
	if fs.NArg() != 1 {
		return nil, miragerr.New(miragerr.SlugHostEnv, "usage: mirage create <name> --ipsw <path>")
	}
	if *ipsw == "" {
		return nil, miragerr.New(miragerr.SlugHostEnv, "--ipsw is required")
	}
	name := fs.Arg(0)
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
	if err := fs.Parse(args); err != nil {
		return nil, miragerr.New(miragerr.SlugHostEnv, "bad flags")
	}
	if fs.NArg() != 1 {
		return nil, miragerr.New(miragerr.SlugHostEnv, "usage: mirage start <name> --gui [--share <dir>]")
	}
	name := fs.Arg(0)
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
	vm, err := engine.BuildVM(b, cfg, *share)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(os.Stderr, "booting %s with a window — close it to stop the VM\n", name)
	if err := engine.StartGUI(vm, "Mirage: "+name, float64(cfg.Display.Width)/2, float64(cfg.Display.Height)/2); err != nil {
		return nil, miragerr.New(miragerr.SlugHostEnv, "gui session failed").WithCause(err)
	}
	return map[string]any{"name": name, "stopped": true}, nil
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

