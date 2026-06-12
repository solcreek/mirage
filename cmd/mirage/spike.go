package main

import (
	"bufio"
	"fmt"
	"os"
	"time"

	"github.com/Code-Hex/vz/v3"
	"github.com/solcreek/mirage/internal/bundle"
	"github.com/solcreek/mirage/internal/engine"
	"github.com/solcreek/mirage/pkg/miragerr"
)

// cmdVsockProbe is the S1 spike harness. It boots a VM with a window (so a human
// can complete Setup Assistant and launch mirage-agent inside the guest) and,
// concurrently, repeatedly attempts a host→guest vsock connection. When the
// in-guest agent starts listening, the probe connects, sends "ping", and prints
// the guest's reply — proving the AF_VSOCK transport end to end.
//
// The host connect must run in the same process that owns the VM, hence this is
// one command rather than a separate connect tool.
func cmdVsockProbe(args []string) (any, error) {
	if len(args) < 1 {
		return nil, miragerr.New(miragerr.SlugHostEnv, "usage: mirage __vsock-probe <name> [shareDir]")
	}
	name := args[0]
	share := ""
	if len(args) > 1 {
		share = args[1]
	}
	b, _, ok := bundle.Find(name)
	if !ok {
		return nil, miragerr.New(miragerr.SlugNotFound, "no bundle named "+name)
	}
	cfg, err := b.Load()
	if err != nil {
		return nil, err
	}
	vm, err := engine.BuildVM(b, cfg, engine.Options{Share: share})
	if err != nil {
		return nil, err
	}
	if err := vm.Start(); err != nil {
		return nil, miragerr.New(miragerr.SlugHostEnv, "vm start failed").WithCause(err)
	}
	if err := engine.WaitState(vm, vz.VirtualMachineStateRunning, 2*time.Minute); err != nil {
		return nil, err
	}

	fmt.Fprintln(os.Stderr, "── S1 vsock probe ──────────────────────────────────")
	fmt.Fprintln(os.Stderr, "VM is running. In the guest, once you have a session:")
	fmt.Fprintln(os.Stderr, "  1. mkdir -p /tmp/m && mount_virtiofs mirage /tmp/m   # if --share given")
	fmt.Fprintf(os.Stderr, "  2. /tmp/m/mirage-agent      # listen on vsock %d\n", engine.AgentPort)
	fmt.Fprintln(os.Stderr, "Host is polling for the agent…")

	// resultFile records PASS/FAIL so the run can be observed out-of-band.
	const resultFile = "/tmp/mirage-s1-result.txt"
	record := func(s string) {
		fmt.Fprintln(os.Stderr, s)
		_ = os.WriteFile(resultFile, []byte(s+"\n"), 0o644)
	}

	// Host-side connector runs while the GUI owns the main thread.
	go func() {
		conn, err := engine.DialGuest(vm, engine.AgentPort, 3*time.Hour)
		if err != nil {
			record("❌ S1 FAILED: host never connected: " + err.Error())
			return
		}
		defer conn.Close()
		fmt.Fprintln(os.Stderr, "✅ host→guest vsock connection established")
		if _, err := conn.Write([]byte("ping\n")); err != nil {
			record("❌ S1 FAILED: write: " + err.Error())
			return
		}
		reply, err := bufio.NewReader(conn).ReadString('\n')
		if err != nil {
			record("❌ S1 FAILED: read: " + err.Error())
			return
		}
		record("✅ S1 PASSED — guest replied over vsock: " + reply)
	}()

	// Block on the GUI run loop so the user can drive the guest.
	if err := vm.StartGraphicApplication(float64(cfg.Display.Width)/2, float64(cfg.Display.Height)/2,
		vz.WithWindowTitle("Mirage S1 probe: "+name)); err != nil {
		return nil, miragerr.New(miragerr.SlugHostEnv, "gui failed").WithCause(err)
	}
	return map[string]any{"name": name, "done": true}, nil
}
