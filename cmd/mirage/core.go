package main

import (
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/solcreek/mirage/internal/bundle"
	"github.com/solcreek/mirage/internal/engine"
	"github.com/solcreek/mirage/internal/supervisor"
	"github.com/solcreek/mirage/pkg/miragerr"
)

// Core operations shared by the CLI commands and the MCP tools so there is one
// implementation of each VM action.

// coreExec runs a command in a VM. If a supervisor is already running it reuses
// the live VM; otherwise it cold-boots one-shot (boot → exec → stop).
func coreExec(name, command string, timeout time.Duration) (exitCode int, output string, err error) {
	if supervisor.IsRunning(name) {
		return supervisor.Exec(name, command, timeout)
	}
	b, _, ok := bundle.Find(name)
	if !ok {
		return 0, "", miragerr.New(miragerr.SlugNotFound, "no bundle named "+name)
	}
	cfg, err := b.Load()
	if err != nil {
		return 0, "", err
	}
	vm, err := engine.StartFresh(b, cfg, engine.Options{}, 5)
	if err != nil {
		return 0, "", miragerr.New(miragerr.SlugHostEnv, "vm start failed").
			WithHint("another VM using this image may still be shutting down").WithCause(err)
	}
	defer func() { _ = vm.Stop() }()
	res, err := engine.AgentExec(vm, command, timeout)
	if err != nil {
		return 0, "", miragerr.New(miragerr.SlugAgentTimeout, "guest agent not reachable").
			WithHint("is mirage-agent installed in the image? run the tools-image install.sh once").WithCause(err)
	}
	return res.ExitCode, res.Output, nil
}

// coreRun clones an image to a fresh ephemeral VM, runs one command, and
// destroys the clone. Returns the ephemeral name and the command result.
func coreRun(image, command string, timeout time.Duration) (name string, exitCode int, output string, err error) {
	src, _, ok := bundle.Find(image)
	if !ok {
		return "", 0, "", miragerr.New(miragerr.SlugNotFound, "no image named "+image)
	}
	name = "run-" + randHex(5)
	dst := bundle.Resolve(bundle.VM, name)
	id, err := engine.NewIdentity()
	if err != nil {
		return name, 0, "", err
	}
	if err := bundle.Clone(src, dst, id); err != nil {
		return name, 0, "", err
	}
	defer func() { _ = bundle.Remove(dst) }()

	cfg, err := dst.Load()
	if err != nil {
		return name, 0, "", err
	}
	cfg.Ephemeral = true
	if err := dst.Save(cfg); err != nil {
		return name, 0, "", err
	}
	vm, err := engine.StartFresh(dst, cfg, engine.Options{}, 5)
	if err != nil {
		return name, 0, "", miragerr.New(miragerr.SlugHostEnv, "vm start failed").WithCause(err)
	}
	defer func() { _ = vm.Stop() }()
	res, err := engine.AgentExec(vm, command, timeout)
	if err != nil {
		return name, 0, "", miragerr.New(miragerr.SlugAgentTimeout, "guest agent not reachable").WithCause(err)
	}
	return name, res.ExitCode, res.Output, nil
}

// coreList returns every image and VM bundle with its current run status.
func coreList() ([]lsRow, error) {
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
			status := "stopped"
			if st, err := supervisor.Load(b.Name); err == nil && st.Running() {
				status = st.Status
			}
			rows = append(rows, lsRow{b.Name, k.label, cfg.OS, cfg.CPU, cfg.MemoryMB, status})
		}
	}
	return rows, nil
}

// coreClone makes an instant CoW clone of an image/VM with a fresh identity.
func coreClone(srcName, dstName string) (mac string, err error) {
	src, _, ok := bundle.Find(srcName)
	if !ok {
		return "", miragerr.New(miragerr.SlugNotFound, "no bundle named "+srcName)
	}
	id, err := engine.NewIdentity()
	if err != nil {
		return "", err
	}
	if err := bundle.Clone(src, bundle.Resolve(bundle.VM, dstName), id); err != nil {
		return "", err
	}
	return id.MAC, nil
}

// randHex returns n random hex characters for ephemeral VM names.
func randHex(n int) string {
	b := make([]byte, (n+1)/2)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)[:n]
}
