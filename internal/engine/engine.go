// Package engine is the single home for all Virtualization.framework calls.
// Every other package treats a VM as an opaque handle; only this package
// imports Code-Hex/vz. The macOS-guest config it builds is validated against
// save/restore support so suspend/resume stays available (proven in
// docs/poc-findings.md).
package engine

import (
	"net"

	"github.com/Code-Hex/vz/v3"
	"github.com/solcreek/mirage/internal/bundle"
	"github.com/solcreek/mirage/pkg/miragerr"
)

// NewIdentity generates a fresh machine identifier and locally-administered
// MAC for a clone, so it can boot concurrently with its source.
func NewIdentity() (bundle.Identity, error) {
	mid, err := vz.NewMacMachineIdentifier()
	if err != nil {
		return bundle.Identity{}, err
	}
	mac, err := vz.NewRandomLocallyAdministeredMACAddress()
	if err != nil {
		return bundle.Identity{}, err
	}
	return bundle.Identity{MachineID: mid.DataRepresentation(), MAC: mac.String()}, nil
}

// Options carries optional devices attached at build time.
type Options struct {
	// Share, if set, is a host directory exposed to the guest over VirtioFS
	// under the mount tag "mirage".
	Share string
	// ToolsImage, if set, is a read-only disk image (the agent tools image)
	// attached as a second block device; the guest auto-mounts it under
	// /Volumes. Used to deliver and install the guest agent.
	ToolsImage string
	// ISO, if set, is a read-only installer image (Linux guests) attached as a
	// second block device so the VM can boot the distro installer.
	ISO string
}

// BuildVM constructs a runnable VirtualMachine from a macOS bundle. The
// configuration must be byte-identical across boots of the same bundle or
// restore-from-save fails, so everything variable lives in config.json.
func BuildVM(b bundle.Bundle, c *bundle.Config, opts Options) (*vz.VirtualMachine, error) {
	if c.OS == "linux" {
		return buildLinuxVM(b, c, opts)
	}
	if c.OS != "macos" {
		return nil, miragerr.New(miragerr.SlugInvalidState, "unsupported guest OS: "+c.OS)
	}
	hw, err := vz.NewMacHardwareModelWithData(c.HardwareModel)
	if err != nil {
		return nil, err
	}
	machineID, err := vz.NewMacMachineIdentifierWithData(c.MachineID)
	if err != nil {
		return nil, err
	}
	aux, err := vz.NewMacAuxiliaryStorage(b.AuxPath())
	if err != nil {
		return nil, err
	}
	platform, err := vz.NewMacPlatformConfiguration(
		vz.WithMacHardwareModel(hw),
		vz.WithMacMachineIdentifier(machineID),
		vz.WithMacAuxiliaryStorage(aux),
	)
	if err != nil {
		return nil, err
	}
	bootloader, err := vz.NewMacOSBootLoader()
	if err != nil {
		return nil, err
	}
	cfg, err := vz.NewVirtualMachineConfiguration(bootloader, c.CPU, c.MemoryMB<<20)
	if err != nil {
		return nil, err
	}
	cfg.SetPlatformVirtualMachineConfiguration(platform)

	// A graphics device is always attached, even headless: screencapture needs
	// a display, and save/restore must see an identical device set every boot.
	gfx, err := vz.NewMacGraphicsDeviceConfiguration()
	if err != nil {
		return nil, err
	}
	ppi := c.Display.PPI
	if ppi == 0 {
		ppi = 80 // legacy default (standard density)
	}
	display, err := vz.NewMacGraphicsDisplayConfiguration(c.Display.Width, c.Display.Height, ppi)
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

	diskAttach, err := vz.NewDiskImageStorageDeviceAttachment(b.DiskPath(), false)
	if err != nil {
		return nil, err
	}
	blk, err := vz.NewVirtioBlockDeviceConfiguration(diskAttach)
	if err != nil {
		return nil, err
	}
	storage := []vz.StorageDeviceConfiguration{blk}
	if opts.ToolsImage != "" {
		toolsAttach, err := vz.NewDiskImageStorageDeviceAttachment(opts.ToolsImage, true) // read-only
		if err != nil {
			return nil, err
		}
		toolsBlk, err := vz.NewVirtioBlockDeviceConfiguration(toolsAttach)
		if err != nil {
			return nil, err
		}
		storage = append(storage, toolsBlk)
	}
	cfg.SetStorageDevicesVirtualMachineConfiguration(storage)

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

	// Virtio socket device: the host↔guest control channel the agent uses.
	sockDev, err := vz.NewVirtioSocketDeviceConfiguration()
	if err != nil {
		return nil, err
	}
	cfg.SetSocketDevicesVirtualMachineConfiguration([]vz.SocketDeviceConfiguration{sockDev})

	// Optional VirtioFS share (mount tag "mirage") — used to stage the agent
	// binary into a guest during image prep before the agent itself exists.
	if opts.Share != "" {
		dir, err := vz.NewSharedDirectory(opts.Share, false)
		if err != nil {
			return nil, err
		}
		single, err := vz.NewSingleDirectoryShare(dir)
		if err != nil {
			return nil, err
		}
		fsDev, err := vz.NewVirtioFileSystemDeviceConfiguration("mirage")
		if err != nil {
			return nil, err
		}
		fsDev.SetDirectoryShare(single)
		cfg.SetDirectorySharingDevicesVirtualMachineConfiguration([]vz.DirectorySharingDeviceConfiguration{fsDev})
	}

	if ok, err := cfg.Validate(); !ok || err != nil {
		return nil, miragerr.New(miragerr.SlugHostEnv, "VM configuration is invalid").WithCause(err)
	}
	return vz.NewVirtualMachine(cfg)
}
