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
}

// Bundle is a resolved on-disk bundle directory.
type Bundle struct {
	Name string
	Dir  string
}

func (b Bundle) ConfigPath() string { return filepath.Join(b.Dir, "config.json") }
func (b Bundle) DiskPath() string   { return filepath.Join(b.Dir, "disk.img") }
func (b Bundle) AuxPath() string    { return filepath.Join(b.Dir, "aux.img") }

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
