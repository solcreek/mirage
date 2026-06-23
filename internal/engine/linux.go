package engine

import (
	"net"
	"os"

	"github.com/Code-Hex/vz/v3"
	"github.com/solcreek/mirage/internal/bundle"
	"github.com/solcreek/mirage/pkg/miragerr"
)

// Linux guests boot via EFI (GRUB/systemd-boot) on the generic platform, with
// virtio devices throughout — distinct from the macOS path (Mac platform,
// hardware model, Mac graphics). The EFI variable store is the Linux analogue of
// macOS auxiliary storage and lives at the bundle's aux path, so clone copies it.

// NewLinuxImage creates an empty Linux image bundle: a blank disk, a fresh EFI
// variable store, and config.json. The caller then boots it with an installer
// ISO attached (a windowed session) to install the distro onto the disk.
func NewLinuxImage(b bundle.Bundle, diskGB int64, cpu uint, memMB uint64, disp bundle.Display) (*bundle.Config, error) {
	if err := os.MkdirAll(b.Dir, 0o755); err != nil {
		return nil, err
	}
	if err := vz.CreateDiskImage(b.DiskPath(), diskGB<<30); err != nil {
		return nil, miragerr.New(miragerr.SlugHostEnv, "create disk image (out of space?)").WithCause(err)
	}
	if _, err := vz.NewEFIVariableStore(b.AuxPath(), vz.WithCreatingEFIVariableStore()); err != nil {
		return nil, miragerr.New(miragerr.SlugHostEnv, "create EFI variable store").WithCause(err)
	}
	mac, err := vz.NewRandomLocallyAdministeredMACAddress()
	if err != nil {
		return nil, err
	}
	cfg := &bundle.Config{
		SchemaVersion: 1, OS: "linux", CPU: cpu, MemoryMB: memMB,
		MAC: mac.String(), Display: disp,
	}
	if err := b.Save(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// buildLinuxVM constructs a runnable Linux VM from a bundle. With opts.ISO set,
// the installer image is attached read-only as a second disk so the VM boots it.
func buildLinuxVM(b bundle.Bundle, c *bundle.Config, opts Options) (*vz.VirtualMachine, error) {
	platform, err := vz.NewGenericPlatformConfiguration()
	if err != nil {
		return nil, err
	}
	vars, err := vz.NewEFIVariableStore(b.AuxPath())
	if err != nil {
		return nil, miragerr.New(miragerr.SlugInvalidState, "missing EFI variable store for "+b.Name).WithCause(err)
	}
	bootloader, err := vz.NewEFIBootLoader(vz.WithEFIVariableStore(vars))
	if err != nil {
		return nil, err
	}
	cfg, err := vz.NewVirtualMachineConfiguration(bootloader, c.CPU, c.MemoryMB<<20)
	if err != nil {
		return nil, err
	}
	cfg.SetPlatformVirtualMachineConfiguration(platform)

	// virtio-gpu graphics (the Mac graphics device is macOS-only).
	gfx, err := vz.NewVirtioGraphicsDeviceConfiguration()
	if err != nil {
		return nil, err
	}
	scanout, err := vz.NewVirtioGraphicsScanoutConfiguration(c.Display.Width, c.Display.Height)
	if err != nil {
		return nil, err
	}
	gfx.SetScanouts(scanout)
	cfg.SetGraphicsDevicesVirtualMachineConfiguration([]vz.GraphicsDeviceConfiguration{gfx})

	if !opts.NoNetwork {
		natAttach, err := vz.NewNATNetworkDeviceAttachment()
		if err != nil {
			return nil, err
		}
		netDev, err := vz.NewVirtioNetworkDeviceConfiguration(natAttach)
		if err != nil {
			return nil, err
		}
		hwAddr, err := net.ParseMAC(c.MAC)
		if err != nil {
			return nil, miragerr.New(miragerr.SlugInvalidState, "bad MAC in config: "+c.MAC).WithCause(err)
		}
		macAddr, err := vz.NewMACAddress(hwAddr)
		if err != nil {
			return nil, err
		}
		netDev.SetMACAddress(macAddr)
		cfg.SetNetworkDevicesVirtualMachineConfiguration([]*vz.VirtioNetworkDeviceConfiguration{netDev})
	}

	// Boot disk, plus the installer ISO (read-only) when creating an image.
	bootAttach, err := vz.NewDiskImageStorageDeviceAttachment(b.DiskPath(), false)
	if err != nil {
		return nil, err
	}
	bootBlk, err := vz.NewVirtioBlockDeviceConfiguration(bootAttach)
	if err != nil {
		return nil, err
	}
	storage := []vz.StorageDeviceConfiguration{bootBlk}
	// Read-only extra disks: the installer ISO (create), the cloud-init seed,
	// and/or the tools image. Each appears in the guest as the next /dev/vd*.
	for _, ro := range []string{opts.ISO, opts.Seed, opts.ToolsImage} {
		if ro == "" {
			continue
		}
		att, err := vz.NewDiskImageStorageDeviceAttachment(ro, true)
		if err != nil {
			return nil, err
		}
		blk, err := vz.NewVirtioBlockDeviceConfiguration(att)
		if err != nil {
			return nil, err
		}
		storage = append(storage, blk)
	}
	cfg.SetStorageDevicesVirtualMachineConfiguration(storage)

	kbd, err := vz.NewUSBKeyboardConfiguration()
	if err != nil {
		return nil, err
	}
	cfg.SetKeyboardsVirtualMachineConfiguration([]vz.KeyboardConfiguration{kbd})
	pad, err := vz.NewUSBScreenCoordinatePointingDeviceConfiguration()
	if err != nil {
		return nil, err
	}
	cfg.SetPointingDevicesVirtualMachineConfiguration([]vz.PointingDeviceConfiguration{pad})

	entropy, err := vz.NewVirtioEntropyDeviceConfiguration()
	if err != nil {
		return nil, err
	}
	cfg.SetEntropyDevicesVirtualMachineConfiguration([]*vz.VirtioEntropyDeviceConfiguration{entropy})

	// Virtio socket device: the host↔guest vsock channel the agent uses. Without
	// it the host cannot reach the guest agent at all.
	sockDev, err := vz.NewVirtioSocketDeviceConfiguration()
	if err != nil {
		return nil, err
	}
	cfg.SetSocketDevicesVirtualMachineConfiguration([]vz.SocketDeviceConfiguration{sockDev})

	if ok, err := cfg.Validate(); !ok || err != nil {
		return nil, miragerr.New(miragerr.SlugHostEnv, "Linux VM configuration is invalid").WithCause(err)
	}
	return vz.NewVirtualMachine(cfg)
}
