package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/solcreek/mirage/internal/bundle"
	"github.com/solcreek/mirage/internal/engine"
	"github.com/solcreek/mirage/internal/supervisor"
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
	headless := fs.Bool("headless", false, "after install, run zero-touch prep (offline user+agent+TCC; needs sudo)")
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
	out := map[string]any{
		"name": name, "os": cfg.OS, "cpu": cfg.CPU, "memory_mb": cfg.MemoryMB,
		"macos_build": info.Build, "path": b.Dir,
	}
	if *headless {
		// Zero-touch: offline-prep the just-installed image (admin user, agent,
		// auto-login, TCC) so it boots ready with no Setup Assistant.
		if err := runZeroTouchPrep(name); err != nil {
			return nil, err
		}
		out["prepped"] = "headless"
	}
	return out, nil
}

// lsRow is one line of `mirage ls` output (package-level so the human renderer
// can type-assert it).
type lsRow struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	OS     string `json:"os"`
	CPU    uint   `json:"cpu"`
	MemMB  uint64 `json:"memory_mb"`
	Status string `json:"status"`
}

func cmdLs(_ []string) (any, error) {
	rows, err := coreList()
	if err != nil {
		return nil, err
	}
	return map[string]any{"bundles": rows}, nil
}

func cmdClone(args []string) (any, error) {
	if len(args) != 2 {
		return nil, miragerr.New(miragerr.SlugHostEnv, "usage: mirage clone <src> <dst>")
	}
	mac, err := coreClone(args[0], args[1])
	if err != nil {
		return nil, err
	}
	dst := bundle.Resolve(bundle.VM, args[1])
	return map[string]any{"name": args[1], "from": args[0], "mac": mac, "path": dst.Dir}, nil
}

func cmdStart(args []string) (any, error) {
	fs := flag.NewFlagSet("start", flag.ContinueOnError)
	gui := fs.Bool("gui", false, "open an interactive window (foreground)")
	recovery := fs.Bool("recovery", false, "boot into recoveryOS (implies --gui; for toggling SIP)")
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
	if !*gui && !*recovery {
		// Headless: spawn a detached per-VM supervisor that keeps the VM
		// running and serves its socket for fast exec.
		status, pid, err := startHeadless(name)
		if err != nil {
			return nil, err
		}
		return map[string]any{"name": name, "status": status, "pid": pid}, nil
	}
	// A GUI/recovery boot needs exclusive access to the disk; a running
	// supervisor holds it (otherwise vz fails cryptically locking aux storage).
	if supervisor.IsRunning(name) {
		return nil, miragerr.New(miragerr.SlugConflict, name+" is already running headless").
			WithHint("stop it first: mirage stop " + name)
	}
	vm, err := engine.BuildVM(b, cfg, engine.Options{Share: *share, ToolsImage: *tools})
	if err != nil {
		return nil, err
	}
	mode := "with a window"
	if *recovery {
		mode = "into recoveryOS"
	}
	fmt.Fprintf(os.Stderr, "booting %s %s — close the window to stop the VM\n", name, mode)
	if err := engine.StartGUI(vm, "Mirage: "+name, float64(cfg.Display.Width)/2, float64(cfg.Display.Height)/2, *recovery); err != nil {
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
	if !supervisor.IsRunning(name) {
		fmt.Fprintf(os.Stderr, "waiting for guest agent on %s…\n", name)
	}
	exit, out, err := coreExec(name, strings.Join(cmd, " "), timeout)
	if err != nil {
		return nil, err
	}
	return map[string]any{"name": name, "exit_code": exit, "output": out}, nil
}

// cmdRun is the agent fan-out primitive: clone an image to a fresh ephemeral
// VM, boot it, run one command, then destroy the clone. The clone is marked
// ephemeral so a crash mid-run leaves a reapable bundle.
func cmdRun(args []string) (any, error) {
	var image string
	var cmd []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--" {
			cmd = args[i+1:]
			break
		}
		if image == "" {
			image = args[i]
		}
	}
	if image == "" || len(cmd) == 0 {
		return nil, miragerr.New(miragerr.SlugHostEnv, "usage: mirage run <image> -- <command...>")
	}
	fmt.Fprintf(os.Stderr, "ephemeral run of %s: cloning and booting…\n", image)
	name, exit, out, err := coreRun(image, strings.Join(cmd, " "), 3*time.Minute)
	if err != nil {
		return nil, err
	}
	return map[string]any{"ephemeral": name, "exit_code": exit, "output": out}, nil
}

func cmdScreenshot(args []string) (any, error) {
	fs := flag.NewFlagSet("screenshot", flag.ContinueOnError)
	out := fs.String("o", "", "output PNG path (default: <name>.png)")
	pos, err := parseMixed(fs, args)
	if err != nil || len(pos) != 1 {
		return nil, miragerr.New(miragerr.SlugHostEnv, "usage: mirage screenshot <name> [-o out.png]")
	}
	name := pos[0]
	png, err := coreScreenshot(name)
	if err != nil {
		return nil, err
	}
	path := *out
	if path == "" {
		path = name + ".png"
	}
	if err := os.WriteFile(path, png, 0o644); err != nil {
		return nil, err
	}
	return map[string]any{"name": name, "path": path, "bytes": len(png)}, nil
}

// cmdAutologin enables boot-to-desktop for a guest user. The password is read
// from stdin (echo disabled on a terminal) so it never lands in argv or shell
// history; it is then obfuscated into /etc/kcpassword inside the guest.
func cmdAutologin(args []string) (any, error) {
	if len(args) < 1 || len(args) > 2 {
		return nil, miragerr.New(miragerr.SlugHostEnv, "usage: mirage autologin <name> [user] (password on stdin)")
	}
	name := args[0]
	user := "admin"
	if len(args) == 2 {
		user = args[1]
	}
	if _, _, ok := bundle.Find(name); !ok {
		return nil, miragerr.New(miragerr.SlugNotFound, "no bundle named "+name)
	}
	fmt.Fprintf(os.Stderr, "password for %s in %s: ", user, name)
	pw, err := readSecret(os.Stdin)
	if err != nil {
		return nil, miragerr.New(miragerr.SlugHostEnv, "read password").WithCause(err)
	}
	if !supervisor.IsRunning(name) {
		fmt.Fprintf(os.Stderr, "booting %s to apply auto-login…\n", name)
	}
	if err := coreAutologin(name, user, pw, 3*time.Minute); err != nil {
		return nil, err
	}
	return map[string]any{"name": name, "user": user, "autologin": true,
		"note": "reboot (or Open in the GUI) to boot straight to the desktop"}, nil
}

// readSecret reads one line from r, disabling terminal echo when r is a TTY so
// a typed password is not shown. Falls back to a plain read for piped input.
func readSecret(f *os.File) (string, error) {
	if fi, _ := f.Stat(); fi != nil && fi.Mode()&os.ModeCharDevice != 0 {
		stty := func(arg string) { c := exec.Command("stty", arg); c.Stdin = f; _ = c.Run() }
		stty("-echo")
		defer func() { stty("echo"); fmt.Fprintln(os.Stderr) }()
	}
	line, err := bufio.NewReader(f).ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
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
	fmt.Printf("%-16s %-6s %-6s %4s %8s  %s\n", "NAME", "KIND", "OS", "CPU", "MEM(MB)", "STATUS")
	for _, r := range rows {
		fmt.Printf("%-16s %-6s %-6s %4d %8d  %s\n", r.Name, r.Kind, r.OS, r.CPU, r.MemMB, r.Status)
	}
}

