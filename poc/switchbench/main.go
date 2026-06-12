// switchbench measures the VM lifecycle operations that decide whether
// switching between macOS guests can feel instant: APFS clone, cold boot,
// suspend (save), and resume (restore).
//
// Subcommands:
//
//	install --bundle <dir> --ipsw <path> [--disk-gb 40]
//	clone   <src-bundle> <dst-bundle>
//	bench   --base <bundle> --workdir <dir> [--cycles 3] [--settle 60s]
//	gui     --bundle <dir> [--restore <file.vzsave>]
//
// The binary must be signed with the com.apple.security.virtualization
// entitlement (see Makefile).
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/Code-Hex/vz/v3"
)

type meta struct {
	SchemaVersion int    `json:"schema_version"`
	CPU           uint   `json:"cpu"`
	MemoryMB      uint64 `json:"memory_mb"`
	MAC           string `json:"mac"`
	DisplayW      int64  `json:"display_w"`
	DisplayH      int64  `json:"display_h"`
}

func main() {
	if len(os.Args) < 2 {
		fatal("usage: switchbench <install|clone|bench> ...")
	}
	var err error
	switch os.Args[1] {
	case "install":
		err = cmdInstall(os.Args[2:])
	case "clone":
		err = cmdClone(os.Args[2:])
	case "bench":
		err = cmdBench(os.Args[2:])
	case "gui":
		err = cmdGUI(os.Args[2:])
	default:
		err = fmt.Errorf("unknown subcommand %q", os.Args[1])
	}
	if err != nil {
		fatal(err.Error())
	}
}

func fatal(msg string) {
	fmt.Fprintln(os.Stderr, "switchbench:", msg)
	os.Exit(1)
}

func logf(format string, a ...any) {
	fmt.Printf("[%s] %s\n", time.Now().Format("15:04:05"), fmt.Sprintf(format, a...))
}

// ---------- bundle ----------

func bundlePaths(dir string) (disk, aux, hw, mid, metaPath string) {
	return filepath.Join(dir, "disk.img"), filepath.Join(dir, "aux.img"),
		filepath.Join(dir, "hw.bin"), filepath.Join(dir, "mid.bin"),
		filepath.Join(dir, "meta.json")
}

func readMeta(dir string) (*meta, error) {
	_, _, _, _, mp := bundlePaths(dir)
	b, err := os.ReadFile(mp)
	if err != nil {
		return nil, err
	}
	var m meta
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

func writeMeta(dir string, m *meta) error {
	_, _, _, _, mp := bundlePaths(dir)
	b, _ := json.MarshalIndent(m, "", "  ")
	return os.WriteFile(mp, b, 0o644)
}

// buildVM constructs a VirtualMachine from a bundle. The configuration must be
// byte-identical across boots of the same bundle or restore-from-save fails,
// so everything variable (MAC, display size) lives in meta.json.
func buildVM(dir string) (*vz.VirtualMachine, error) {
	disk, aux, hwPath, midPath, _ := bundlePaths(dir)
	m, err := readMeta(dir)
	if err != nil {
		return nil, fmt.Errorf("read meta: %w", err)
	}

	hw, err := vz.NewMacHardwareModelWithDataPath(hwPath)
	if err != nil {
		return nil, fmt.Errorf("hardware model: %w", err)
	}
	machineID, err := vz.NewMacMachineIdentifierWithDataPath(midPath)
	if err != nil {
		return nil, fmt.Errorf("machine id: %w", err)
	}
	auxStorage, err := vz.NewMacAuxiliaryStorage(aux)
	if err != nil {
		return nil, fmt.Errorf("aux storage: %w", err)
	}
	platform, err := vz.NewMacPlatformConfiguration(
		vz.WithMacHardwareModel(hw),
		vz.WithMacMachineIdentifier(machineID),
		vz.WithMacAuxiliaryStorage(auxStorage),
	)
	if err != nil {
		return nil, fmt.Errorf("platform: %w", err)
	}

	bootloader, err := vz.NewMacOSBootLoader()
	if err != nil {
		return nil, err
	}
	cfg, err := vz.NewVirtualMachineConfiguration(bootloader, m.CPU, m.MemoryMB<<20)
	if err != nil {
		return nil, err
	}
	cfg.SetPlatformVirtualMachineConfiguration(platform)

	// Display is always attached, even headless (mirage plan §1).
	gfx, err := vz.NewMacGraphicsDeviceConfiguration()
	if err != nil {
		return nil, err
	}
	display, err := vz.NewMacGraphicsDisplayConfiguration(m.DisplayW, m.DisplayH, 80)
	if err != nil {
		return nil, err
	}
	gfx.SetDisplays(display)
	cfg.SetGraphicsDevicesVirtualMachineConfiguration([]vz.GraphicsDeviceConfiguration{gfx})

	natAttach, err := vz.NewNATNetworkDeviceAttachment()
	if err != nil {
		return nil, err
	}
	netDev, err := vz.NewVirtioNetworkDeviceConfiguration(natAttach)
	if err != nil {
		return nil, err
	}
	hwAddr, err := parseMAC(m.MAC)
	if err != nil {
		return nil, err
	}
	netDev.SetMACAddress(hwAddr)
	cfg.SetNetworkDevicesVirtualMachineConfiguration([]*vz.VirtioNetworkDeviceConfiguration{netDev})

	diskAttach, err := vz.NewDiskImageStorageDeviceAttachment(disk, false)
	if err != nil {
		return nil, err
	}
	blk, err := vz.NewVirtioBlockDeviceConfiguration(diskAttach)
	if err != nil {
		return nil, err
	}
	cfg.SetStorageDevicesVirtualMachineConfiguration([]vz.StorageDeviceConfiguration{blk})

	kbd, err := vz.NewMacKeyboardConfiguration()
	if err != nil {
		return nil, err
	}
	cfg.SetKeyboardsVirtualMachineConfiguration([]vz.KeyboardConfiguration{kbd})
	pad, err := vz.NewMacTrackpadConfiguration()
	if err != nil {
		return nil, err
	}
	cfg.SetPointingDevicesVirtualMachineConfiguration([]vz.PointingDeviceConfiguration{pad})

	if ok, err := cfg.Validate(); !ok || err != nil {
		return nil, fmt.Errorf("config validate: ok=%v err=%w", ok, err)
	}
	if ok, err := cfg.ValidateSaveRestoreSupport(); !ok || err != nil {
		// Not fatal for install/boot, but the whole point of the bench.
		logf("WARNING: save/restore unsupported by this config: ok=%v err=%v", ok, err)
	}
	return vz.NewVirtualMachine(cfg)
}

func parseMAC(s string) (*vz.MACAddress, error) {
	hw, err := vz.NewRandomLocallyAdministeredMACAddress()
	if s == "" {
		return hw, err
	}
	addr, err := parseHardwareAddr(s)
	if err != nil {
		return nil, err
	}
	return vz.NewMACAddress(addr)
}

func waitState(vm *vz.VirtualMachine, want vz.VirtualMachineState, timeout time.Duration) error {
	if vm.State() == want {
		return nil
	}
	ch := vm.StateChangedNotify()
	deadline := time.After(timeout)
	for {
		select {
		case s := <-ch:
			if s == want {
				return nil
			}
			if s == vz.VirtualMachineStateError {
				return fmt.Errorf("vm entered error state while waiting for %v", want)
			}
		case <-deadline:
			return fmt.Errorf("timeout (%s) waiting for state %v, currently %v", timeout, want, vm.State())
		}
	}
}

// ---------- install ----------

func cmdInstall(args []string) error {
	fs := flag.NewFlagSet("install", flag.ExitOnError)
	bundle := fs.String("bundle", "", "bundle directory to create")
	ipsw := fs.String("ipsw", "", "path to macOS restore image (.ipsw)")
	diskGB := fs.Int64("disk-gb", 40, "disk image size in GB (sparse)")
	fs.Parse(args)
	if *bundle == "" || *ipsw == "" {
		return fmt.Errorf("install: --bundle and --ipsw are required")
	}

	if err := os.MkdirAll(*bundle, 0o755); err != nil {
		return err
	}
	disk, aux, hwPath, midPath, _ := bundlePaths(*bundle)

	logf("loading restore image %s", *ipsw)
	img, err := vz.LoadMacOSRestoreImageFromPath(*ipsw)
	if err != nil {
		return fmt.Errorf("load ipsw: %w", err)
	}
	osv := img.OperatingSystemVersion()
	logf("restore image: macOS %d.%d.%d (build %s)", osv.MajorVersion, osv.MinorVersion, osv.PatchVersion, img.BuildVersion())

	req := img.MostFeaturefulSupportedConfiguration()
	hw := req.HardwareModel()
	if !hw.Supported() {
		return fmt.Errorf("hardware model in this IPSW is not supported on this host")
	}
	if err := os.WriteFile(hwPath, hw.DataRepresentation(), 0o644); err != nil {
		return err
	}

	machineID, err := vz.NewMacMachineIdentifier()
	if err != nil {
		return err
	}
	if err := os.WriteFile(midPath, machineID.DataRepresentation(), 0o644); err != nil {
		return err
	}

	cpu := max(uint(4), uint(req.MinimumSupportedCPUCount()))
	memMB := max(uint64(4096), req.MinimumSupportedMemorySize()>>20)

	logf("creating %d GB sparse disk image", *diskGB)
	if err := vz.CreateDiskImage(disk, *diskGB<<30); err != nil {
		return fmt.Errorf("create disk: %w", err)
	}
	// Aux storage is created via the option that formats it for this hw model.
	if _, err := vz.NewMacAuxiliaryStorage(aux, vz.WithCreatingMacAuxiliaryStorage(hw)); err != nil {
		return fmt.Errorf("create aux storage: %w", err)
	}

	mac, err := vz.NewRandomLocallyAdministeredMACAddress()
	if err != nil {
		return err
	}
	if err := writeMeta(*bundle, &meta{
		SchemaVersion: 1, CPU: cpu, MemoryMB: memMB,
		MAC: mac.String(), DisplayW: 1920, DisplayH: 1080,
	}); err != nil {
		return err
	}

	vm, err := buildVM(*bundle)
	if err != nil {
		return err
	}
	installer, err := vz.NewMacOSInstaller(vm, *ipsw)
	if err != nil {
		return fmt.Errorf("installer: %w", err)
	}

	start := time.Now()
	logf("starting install (this takes ~15-25 min)")
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(20 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				logf("install progress: %.1f%%", installer.FractionCompleted()*100)
			}
		}
	}()
	err = installer.Install(context.Background())
	close(done)
	if err != nil {
		return fmt.Errorf("install failed after %s: %w", time.Since(start).Round(time.Second), err)
	}
	logf("install completed in %s", time.Since(start).Round(time.Second))

	_ = vm.Stop()
	_ = waitState(vm, vz.VirtualMachineStateStopped, 30*time.Second)
	return nil
}

// ---------- clone ----------

func cloneBundle(src, dst string) (time.Duration, error) {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return 0, err
	}
	sd, sa, shw, _, _ := bundlePaths(src)
	dd, da, dhw, dmid, _ := bundlePaths(dst)

	start := time.Now()
	// cp -c uses clonefile(2) on APFS: metadata-only copy-on-write.
	for _, p := range [][2]string{{sd, dd}, {sa, da}} {
		if out, err := exec.Command("cp", "-c", p[0], p[1]).CombinedOutput(); err != nil {
			return 0, fmt.Errorf("cp -c %s: %v: %s", p[0], err, out)
		}
	}
	cloneDur := time.Since(start)

	if out, err := exec.Command("cp", shw, dhw).CombinedOutput(); err != nil {
		return 0, fmt.Errorf("cp hw.bin: %v: %s", err, out)
	}
	// Fresh machine identity + MAC: mandatory for concurrent boot of clones.
	machineID, err := vz.NewMacMachineIdentifier()
	if err != nil {
		return 0, err
	}
	if err := os.WriteFile(dmid, machineID.DataRepresentation(), 0o644); err != nil {
		return 0, err
	}
	m, err := readMeta(src)
	if err != nil {
		return 0, err
	}
	mac, err := vz.NewRandomLocallyAdministeredMACAddress()
	if err != nil {
		return 0, err
	}
	m.MAC = mac.String()
	if err := writeMeta(dst, m); err != nil {
		return 0, err
	}
	return cloneDur, nil
}

func cmdClone(args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: switchbench clone <src-bundle> <dst-bundle>")
	}
	d, err := cloneBundle(args[0], args[1])
	if err != nil {
		return err
	}
	logf("clone (disk+aux clonefile): %s", d)
	return nil
}

// ---------- bench ----------

type sample struct {
	Op  string  `json:"op"`
	VM  string  `json:"vm"`
	Sec float64 `json:"sec"`
}

type benchReport struct {
	Host       string   `json:"host"`
	GuestImage string   `json:"guest_bundle"`
	SaveBytes  int64    `json:"save_file_bytes"`
	Samples    []sample `json:"samples"`
}

func cmdBench(args []string) error {
	fs := flag.NewFlagSet("bench", flag.ExitOnError)
	base := fs.String("base", "", "installed base bundle")
	workdir := fs.String("workdir", "", "directory for clones + save files")
	cycles := fs.Int("cycles", 3, "number of A<->B switch cycles")
	settle := fs.Duration("settle", 60*time.Second, "settle time after first cold boot")
	skipSuspend := fs.Bool("skip-suspend", false, "measure clone + cold boot + concurrent/quota only (no save files, no disk cost)")
	fs.Parse(args)
	if *base == "" || *workdir == "" {
		return fmt.Errorf("bench: --base and --workdir are required")
	}
	if err := os.MkdirAll(*workdir, 0o755); err != nil {
		return err
	}

	rep := &benchReport{GuestImage: *base}
	rec := func(op, vm string, d time.Duration) {
		logf("%-22s %-3s %8.2fs", op, vm, d.Seconds())
		rep.Samples = append(rep.Samples, sample{Op: op, VM: vm, Sec: d.Seconds()})
	}

	vms := map[string]string{
		"A": filepath.Join(*workdir, "vmA"),
		"B": filepath.Join(*workdir, "vmB"),
	}
	saves := map[string]string{
		"A": filepath.Join(*workdir, "vmA.vzsave"),
		"B": filepath.Join(*workdir, "vmB.vzsave"),
	}

	// 1. Clone base -> A, B.
	for _, name := range []string{"A", "B"} {
		if _, err := os.Stat(vms[name]); err == nil {
			logf("clone %s exists, reusing", name)
			continue
		}
		d, err := cloneBundle(*base, vms[name])
		if err != nil {
			return fmt.Errorf("clone %s: %w", name, err)
		}
		rec("clone", name, d)
	}

	// skip-suspend: measure cold boot + concurrent boot + quota, no save files.
	if *skipSuspend {
		var running []*vz.VirtualMachine
		for _, name := range []string{"A", "B"} {
			vm, err := buildVM(vms[name])
			if err != nil {
				return fmt.Errorf("build %s: %w", name, err)
			}
			t0 := time.Now()
			if err := vm.Start(); err != nil {
				return fmt.Errorf("start %s: %w", name, err)
			}
			if err := waitState(vm, vz.VirtualMachineStateRunning, 2*time.Minute); err != nil {
				return fmt.Errorf("boot %s: %w", name, err)
			}
			rec("cold_boot_to_running", name, time.Since(t0))
			running = append(running, vm)
		}
		logf("both clones running concurrently: %d VMs", len(running))

		// Third macOS VM must hit the kernel quota.
		third := filepath.Join(*workdir, "vmC")
		if _, err := os.Stat(third); err != nil {
			if _, err := cloneBundle(*base, third); err != nil {
				return fmt.Errorf("clone C: %w", err)
			}
		}
		vmC, err := buildVM(third)
		if err != nil {
			return fmt.Errorf("build C: %w", err)
		}
		if err := vmC.Start(); err == nil {
			_ = waitState(vmC, vz.VirtualMachineStateRunning, 10*time.Second)
			logf("UNEXPECTED: third macOS VM started (state=%v) — quota not enforced?", vmC.State())
			_ = vmC.Stop()
		} else {
			logf("third macOS VM correctly refused: %v", err)
		}

		for i, vm := range running {
			_ = vm.Stop()
			_ = waitState(vm, vz.VirtualMachineStateStopped, 30*time.Second)
			logf("stopped VM %d", i)
		}
		fmt.Println()
		summarize(rep)
		return nil
	}

	// 2. Cold boot each, settle, suspend.
	for _, name := range []string{"A", "B"} {
		vm, err := buildVM(vms[name])
		if err != nil {
			return fmt.Errorf("build %s: %w", name, err)
		}
		t0 := time.Now()
		if err := vm.Start(); err != nil {
			return fmt.Errorf("start %s: %w", name, err)
		}
		if err := waitState(vm, vz.VirtualMachineStateRunning, 2*time.Minute); err != nil {
			return fmt.Errorf("boot %s: %w", name, err)
		}
		rec("cold_boot_to_running", name, time.Since(t0))

		logf("settling %s for %s (guest OS booting)", name, *settle)
		time.Sleep(*settle)

		if d, err := suspend(vm, saves[name]); err != nil {
			return fmt.Errorf("suspend %s: %w", name, err)
		} else {
			rec("suspend(save)", name, d)
		}
		if fi, err := os.Stat(saves[name]); err == nil {
			rep.SaveBytes = fi.Size()
			logf("save file %s: %.2f GB", name, float64(fi.Size())/1e9)
		}
	}

	// 3. Switch cycles: resume X, brief use, suspend X, resume Y...
	order := []string{"A", "B"}
	for i := 0; i < *cycles; i++ {
		for _, name := range order {
			vm, err := buildVM(vms[name])
			if err != nil {
				return fmt.Errorf("rebuild %s: %w", name, err)
			}
			t0 := time.Now()
			if err := vm.RestoreMachineStateFromURL(saves[name]); err != nil {
				return fmt.Errorf("restore %s (cycle %d): %w", name, i, err)
			}
			restoreDur := time.Since(t0)
			t1 := time.Now()
			if err := vm.Resume(); err != nil {
				return fmt.Errorf("resume %s: %w", name, err)
			}
			if err := waitState(vm, vz.VirtualMachineStateRunning, time.Minute); err != nil {
				return fmt.Errorf("resume-wait %s: %w", name, err)
			}
			rec("restore(load_state)", name, restoreDur)
			rec("resume_to_running", name, time.Since(t1))

			time.Sleep(5 * time.Second) // brief "user is working" window

			if d, err := suspend(vm, saves[name]); err != nil {
				return fmt.Errorf("re-suspend %s: %w", name, err)
			} else {
				rec("suspend(save)", name, d)
			}
		}
	}

	// Report.
	fmt.Println()
	summarize(rep)
	b, _ := json.MarshalIndent(rep, "", "  ")
	out := filepath.Join(*workdir, "report.json")
	if err := os.WriteFile(out, b, 0o644); err != nil {
		return err
	}
	logf("report written to %s", out)
	return nil
}

// cmdGUI boots (or restores) a VM in the foreground with an interactive
// window — keyboard and trackpad events go to the guest. The VM dies with
// the window; this is the image-prep path, not the headless lifecycle.
func cmdGUI(args []string) error {
	fs := flag.NewFlagSet("gui", flag.ExitOnError)
	bundle := fs.String("bundle", "", "bundle directory")
	restore := fs.String("restore", "", "optional .vzsave file to resume from")
	fs.Parse(args)
	if *bundle == "" {
		return fmt.Errorf("gui: --bundle is required")
	}
	vm, err := buildVM(*bundle)
	if err != nil {
		return err
	}
	m, err := readMeta(*bundle)
	if err != nil {
		return err
	}
	if *restore != "" {
		t0 := time.Now()
		if err := vm.RestoreMachineStateFromURL(*restore); err != nil {
			return fmt.Errorf("restore: %w", err)
		}
		if err := vm.Resume(); err != nil {
			return fmt.Errorf("resume: %w", err)
		}
		logf("restored + resumed in %s", time.Since(t0).Round(time.Millisecond))
	} else {
		if err := vm.Start(); err != nil {
			return fmt.Errorf("start: %w", err)
		}
	}
	if err := waitState(vm, vz.VirtualMachineStateRunning, 2*time.Minute); err != nil {
		return err
	}
	logf("opening window — closing it kills the VM")
	return vm.StartGraphicApplication(float64(m.DisplayW)/2, float64(m.DisplayH)/2,
		vz.WithWindowTitle("switchbench: "+filepath.Base(*bundle)))
}

// suspend pauses the VM, saves its state to path, then stops it.
func suspend(vm *vz.VirtualMachine, path string) (time.Duration, error) {
	_ = os.Remove(path)
	if err := vm.Pause(); err != nil {
		return 0, fmt.Errorf("pause: %w", err)
	}
	if err := waitState(vm, vz.VirtualMachineStatePaused, 30*time.Second); err != nil {
		return 0, err
	}
	t0 := time.Now()
	if err := vm.SaveMachineStateToPath(path); err != nil {
		return 0, fmt.Errorf("save: %w", err)
	}
	d := time.Since(t0)
	if err := vm.Stop(); err != nil {
		return d, fmt.Errorf("stop after save: %w", err)
	}
	_ = waitState(vm, vz.VirtualMachineStateStopped, 30*time.Second)
	return d, nil
}

func summarize(rep *benchReport) {
	agg := map[string][]float64{}
	for _, s := range rep.Samples {
		agg[s.Op] = append(agg[s.Op], s.Sec)
	}
	fmt.Println("== summary (seconds) ==")
	fmt.Printf("%-22s %5s %8s %8s %8s\n", "op", "n", "min", "median", "max")
	for _, op := range []string{"clone", "cold_boot_to_running", "suspend(save)", "restore(load_state)", "resume_to_running"} {
		v := agg[op]
		if len(v) == 0 {
			continue
		}
		fmt.Printf("%-22s %5d %8.2f %8.2f %8.2f\n", op, len(v), minF(v), medianF(v), maxF(v))
	}
	// The user-perceived switch = suspend current + restore&resume next.
	if s, r, m := agg["suspend(save)"], agg["restore(load_state)"], agg["resume_to_running"]; len(s) > 0 && len(r) > 0 {
		fmt.Printf("\nperceived A->B switch (median suspend + restore + resume): %.2fs\n",
			medianF(s)+medianF(r)+medianF(m))
	}
}
