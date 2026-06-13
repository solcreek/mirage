// Package bundle owns the on-disk .mirage bundle format and the XDG directory
// layout. It performs no virtualization calls (all VZ work lives in
// internal/engine); identity generation for clones is injected by the caller.
package bundle

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/solcreek/mirage/pkg/miragerr"
)

// Config is the contents of config.json. Hardware model and machine identifier
// are stored inline (base64 via []byte JSON) so a bundle is self-describing.
type Config struct {
	SchemaVersion int     `json:"schema_version"`
	OS            string  `json:"os"` // "macos" | "linux"
	CPU          uint    `json:"cpu"`
	MemoryMB     uint64  `json:"memory_mb"`
	MAC          string  `json:"mac"`
	HardwareModel []byte `json:"hardware_model"` // VZMacHardwareModel data
	MachineID    []byte  `json:"machine_id"`     // VZMacMachineIdentifier data
	Display      Display `json:"display"`
	Ephemeral    bool    `json:"ephemeral"`
}

type Display struct {
	Width  int64 `json:"width"`
	Height int64 `json:"height"`
	// PPI is the display's pixels-per-inch. A high value (e.g. 220) makes the
	// guest treat it as a Retina/HiDPI display and render @2x — much crisper.
	// Zero means the legacy default (80 ppi, standard density).
	PPI int64 `json:"ppi,omitempty"`
}

// Bundle is a resolved on-disk bundle directory.
type Bundle struct {
	Name string
	Dir  string
}

func (b Bundle) ConfigPath() string { return filepath.Join(b.Dir, "config.json") }
func (b Bundle) DiskPath() string   { return filepath.Join(b.Dir, "disk.img") }
func (b Bundle) AuxPath() string    { return filepath.Join(b.Dir, "aux.img") }

// A snapshot is a paired freeze: saved RAM/device state plus a CoW clone of the
// disk taken at the same paused moment. Both are required for a consistent
// restore — restoring saved RAM onto a diverged disk would corrupt the guest.
func (b Bundle) SnapshotStatePath() string { return filepath.Join(b.Dir, "snapshot.vzstate") }
func (b Bundle) SnapshotDiskPath() string  { return filepath.Join(b.Dir, "snapshot-disk.img") }

// HasSnapshot reports whether both halves of a snapshot are present.
func (b Bundle) HasSnapshot() bool {
	_, e1 := os.Stat(b.SnapshotStatePath())
	_, e2 := os.Stat(b.SnapshotDiskPath())
	return e1 == nil && e2 == nil
}

// SnapshotDisk clones the current (paused, quiesced) disk into the snapshot's
// paired disk via clonefile — metadata-only on APFS regardless of image size.
func (b Bundle) SnapshotDisk() error { return cloneFile(b.DiskPath(), b.SnapshotDiskPath()) }

// ResetDiskToSnapshot replaces the live disk with a fresh clone of the
// snapshot's disk, so a restore lands on the exact frozen disk state.
func (b Bundle) ResetDiskToSnapshot() error { return cloneFile(b.SnapshotDiskPath(), b.DiskPath()) }

// DiscardSnapshot removes both halves of the snapshot.
func (b Bundle) DiscardSnapshot() error {
	_ = os.Remove(b.SnapshotStatePath())
	_ = os.Remove(b.SnapshotDiskPath())
	return nil
}

// cloneFile makes an APFS copy-on-write clone of src at dst (replacing dst),
// the same metadata-only primitive used for instant VM clones.
func cloneFile(src, dst string) error {
	_ = os.Remove(dst)
	if out, err := exec.Command("cp", "-c", src, dst).CombinedOutput(); err != nil {
		return miragerr.New(miragerr.SlugHostEnv,
			"clonefile failed for "+filepath.Base(src)+": "+string(out)).WithCause(err)
	}
	return nil
}

// Load reads and validates a bundle's config.json.
func (b Bundle) Load() (*Config, error) {
	raw, err := os.ReadFile(b.ConfigPath())
	if os.IsNotExist(err) {
		return nil, miragerr.New(miragerr.SlugNotFound, "no bundle named "+b.Name)
	}
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, miragerr.New(miragerr.SlugInvalidState, "corrupt config.json for "+b.Name).WithCause(err)
	}
	return &c, nil
}

// Save writes config.json into the bundle directory.
func (b Bundle) Save(c *Config) error {
	if err := os.MkdirAll(b.Dir, 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(b.ConfigPath(), raw, 0o644)
}

// Identity is fresh VM identity injected by the caller (engine generates it).
type Identity struct {
	MachineID []byte
	MAC       string
}

// Clone makes a copy-on-write clone of src into a new instance bundle and
// rotates the machine identifier + MAC so the clone can boot concurrently with
// its source. The disk and aux images are cloned with clonefile(2) via `cp -c`,
// which is metadata-only on APFS (~10 ms regardless of image size).
func Clone(src, dst Bundle, id Identity) error {
	cfg, err := src.Load()
	if err != nil {
		return err
	}
	if _, err := os.Stat(dst.ConfigPath()); err == nil {
		return miragerr.New(miragerr.SlugConflict, "bundle "+dst.Name+" already exists")
	}
	if err := os.MkdirAll(dst.Dir, 0o755); err != nil {
		return err
	}
	for _, p := range [][2]string{
		{src.DiskPath(), dst.DiskPath()},
		{src.AuxPath(), dst.AuxPath()},
	} {
		if out, err := exec.Command("cp", "-c", p[0], p[1]).CombinedOutput(); err != nil {
			return miragerr.New(miragerr.SlugHostEnv,
				"clonefile failed for "+filepath.Base(p[0])+": "+string(out)).WithCause(err)
		}
	}
	cfg.MachineID = id.MachineID
	cfg.MAC = id.MAC
	return dst.Save(cfg)
}

// Remove deletes a bundle directory.
func Remove(b Bundle) error {
	if _, err := os.Stat(b.Dir); os.IsNotExist(err) {
		return miragerr.New(miragerr.SlugNotFound, "no bundle named "+b.Name)
	}
	return os.RemoveAll(b.Dir)
}
