package bundle

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Kind distinguishes sealed golden images from instance VMs. They live in
// separate directories but share the bundle format.
type Kind int

const (
	Image Kind = iota
	VM
)

// dataHome returns $XDG_DATA_HOME/mirage or ~/.local/share/mirage.
func dataHome() string {
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		return filepath.Join(x, "mirage")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "mirage")
}

// stateHome returns $XDG_STATE_HOME/mirage or ~/.local/state/mirage.
func stateHome() string {
	if x := os.Getenv("XDG_STATE_HOME"); x != "" {
		return filepath.Join(x, "mirage")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "mirage")
}

func ImagesDir() string  { return filepath.Join(dataHome(), "images") }
func VMsDir() string     { return filepath.Join(dataHome(), "vms") }
func IPSWDir() string    { return filepath.Join(dataHome(), "ipsw") }
func StateVMsDir() string { return filepath.Join(stateHome(), "vms") }

func kindDir(k Kind) string {
	if k == Image {
		return ImagesDir()
	}
	return VMsDir()
}

// Resolve returns the Bundle for a name of the given kind (no existence check).
func Resolve(k Kind, name string) Bundle {
	return Bundle{Name: name, Dir: filepath.Join(kindDir(k), name+".mirage")}
}

// Find locates a bundle by name, checking VMs first then images. Used by
// commands that accept either an instance or an image name.
func Find(name string) (Bundle, Kind, bool) {
	for _, k := range []Kind{VM, Image} {
		b := Resolve(k, name)
		if _, err := os.Stat(b.ConfigPath()); err == nil {
			return b, k, true
		}
	}
	return Bundle{}, 0, false
}

// List returns all bundles of a kind, sorted by name.
func List(k Kind) ([]Bundle, error) {
	dir := kindDir(k)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Bundle
	for _, e := range entries {
		if !e.IsDir() || !strings.HasSuffix(e.Name(), ".mirage") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".mirage")
		out = append(out, Bundle{Name: name, Dir: filepath.Join(dir, e.Name())})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
