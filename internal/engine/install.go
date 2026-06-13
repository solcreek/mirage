package engine

import (
	"context"
	"os"
	"time"

	"github.com/Code-Hex/vz/v3"
	"github.com/solcreek/mirage/internal/bundle"
	"github.com/solcreek/mirage/pkg/miragerr"
)

// ImageInfo describes a restore image without installing it.
type ImageInfo struct {
	Major, Minor, Patch int
	Build               string
}

// InspectIPSW reports the macOS version in a restore image and verifies its
// hardware model is supported on this host.
func InspectIPSW(path string) (ImageInfo, error) {
	img, err := vz.LoadMacOSRestoreImageFromPath(path)
	if err != nil {
		return ImageInfo{}, miragerr.New(miragerr.SlugHostEnv, "cannot read restore image").WithCause(err)
	}
	if !img.MostFeaturefulSupportedConfiguration().HardwareModel().Supported() {
		return ImageInfo{}, miragerr.New(miragerr.SlugHostEnv,
			"this restore image's hardware model is not supported on this host")
	}
	v := img.OperatingSystemVersion()
	return ImageInfo{int(v.MajorVersion), int(v.MinorVersion), int(v.PatchVersion), img.BuildVersion()}, nil
}

// ProgressFn receives the install fraction in [0,1] periodically.
type ProgressFn func(fraction float64)

// Install creates a fresh macOS golden image in b from the given IPSW: it sizes
// the disk, formats auxiliary storage, generates a unique identity, writes
// config.json, and runs the restore. It blocks until the install completes.
func Install(ctx context.Context, b bundle.Bundle, ipsw string, diskGB int64, prog ProgressFn) (*bundle.Config, error) {
	img, err := vz.LoadMacOSRestoreImageFromPath(ipsw)
	if err != nil {
		return nil, miragerr.New(miragerr.SlugHostEnv, "cannot read restore image").WithCause(err)
	}
	req := img.MostFeaturefulSupportedConfiguration()
	hw := req.HardwareModel()
	if !hw.Supported() {
		return nil, miragerr.New(miragerr.SlugHostEnv, "restore image hardware model unsupported on this host")
	}

	if err := os.MkdirAll(b.Dir, 0o755); err != nil {
		return nil, err
	}
	if err := vz.CreateDiskImage(b.DiskPath(), diskGB<<30); err != nil {
		return nil, miragerr.New(miragerr.SlugHostEnv, "create disk image (out of space?)").WithCause(err)
	}
	if _, err := vz.NewMacAuxiliaryStorage(b.AuxPath(), vz.WithCreatingMacAuxiliaryStorage(hw)); err != nil {
		return nil, miragerr.New(miragerr.SlugHostEnv, "create auxiliary storage").WithCause(err)
	}

	mid, err := vz.NewMacMachineIdentifier()
	if err != nil {
		return nil, err
	}
	mac, err := vz.NewRandomLocallyAdministeredMACAddress()
	if err != nil {
		return nil, err
	}
	cpu := uint(req.MinimumSupportedCPUCount())
	if cpu < 4 {
		cpu = 4
	}
	memMB := req.MinimumSupportedMemorySize() >> 20
	if memMB < 4096 {
		memMB = 4096
	}
	cfg := &bundle.Config{
		SchemaVersion: 1, OS: "macos", CPU: cpu, MemoryMB: memMB,
		MAC: mac.String(), HardwareModel: hw.DataRepresentation(),
		MachineID: mid.DataRepresentation(),
		// HiDPI by default: 2560x1600 @ 220ppi renders @2x ("looks like" 1280x800
		// Retina) — crisp on a modern Mac, vs soft standard-density at 80ppi.
		Display:   bundle.Display{Width: 2560, Height: 1600, PPI: 220},
	}
	if err := b.Save(cfg); err != nil {
		return nil, err
	}

	vm, err := BuildVM(b, cfg, Options{})
	if err != nil {
		return nil, err
	}
	installer, err := vz.NewMacOSInstaller(vm, ipsw)
	if err != nil {
		return nil, miragerr.New(miragerr.SlugHostEnv, "create installer").WithCause(err)
	}

	if prog != nil {
		done := make(chan struct{})
		defer close(done)
		go func() {
			t := time.NewTicker(15 * time.Second)
			defer t.Stop()
			for {
				select {
				case <-done:
					return
				case <-t.C:
					prog(installer.FractionCompleted())
				}
			}
		}()
	}
	if err := installer.Install(ctx); err != nil {
		return nil, miragerr.New(miragerr.SlugHostEnv, "macOS install failed").WithCause(err)
	}
	_ = vm.Stop()
	return cfg, nil
}
