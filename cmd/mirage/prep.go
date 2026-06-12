package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/solcreek/mirage/pkg/miragerr"
)

// scriptsDir resolves the repo's scripts/ directory relative to the running
// binary (bin/mirage → ../scripts). Bundling these into the binary is a v0.2
// item; for now they ship alongside the repo.
func scriptsDir() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(filepath.Dir(exe), "..", "scripts")
	if _, err := os.Stat(filepath.Join(dir, "zt-apply.sh")); err != nil {
		return "", miragerr.New(miragerr.SlugHostEnv,
			"zero-touch scripts not found next to the binary").
			WithHint("run mirage from its repo (bin/mirage), or prep manually with scripts/zt-stage.sh + sudo scripts/zt-apply.sh")
	}
	return dir, nil
}

// runZeroTouchPrep stages artifacts (no sudo) then applies them offline (sudo).
// It inherits the terminal so sudo can prompt; not usable under --json.
func runZeroTouchPrep(name string) error {
	dir, err := scriptsDir()
	if err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "zero-touch prep: staging agent + user record…")
	if err := runInherit("sh", filepath.Join(dir, "zt-stage.sh")); err != nil {
		return miragerr.New(miragerr.SlugHostEnv, "staging failed").WithCause(err)
	}
	fmt.Fprintln(os.Stderr, "applying offline (sudo — you may be prompted for your password)…")
	if err := runInherit("sudo", "sh", filepath.Join(dir, "zt-apply.sh"), name); err != nil {
		return miragerr.New(miragerr.SlugHostEnv, "offline prep failed").WithCause(err)
	}
	fmt.Fprintf(os.Stderr, "%s is ready (admin / mirage) — boot with: mirage start %s\n", name, name)
	return nil
}

func runInherit(cmd string, args ...string) error {
	c := exec.Command(cmd, args...)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stderr, os.Stderr
	return c.Run()
}

// cmdPrep runs zero-touch prep on an already-installed image (e.g. created
// without --headless). Boots nothing; just offline-prepares the disk.
func cmdPrep(args []string) (any, error) {
	if len(args) != 1 {
		return nil, miragerr.New(miragerr.SlugHostEnv, "usage: mirage prep <name>")
	}
	if err := runZeroTouchPrep(args[0]); err != nil {
		return nil, err
	}
	return map[string]any{"name": args[0], "prepped": "headless"}, nil
}
