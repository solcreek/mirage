package bundle

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigRoundTrip(t *testing.T) {
	dir := t.TempDir()
	b := Bundle{Name: "x", Dir: filepath.Join(dir, "x.mirage")}
	want := &Config{
		SchemaVersion: 1, OS: "macos", CPU: 4, MemoryMB: 4096,
		MAC: "06:00:00:00:00:01", HardwareModel: []byte{1, 2, 3}, MachineID: []byte{4, 5},
		Display: Display{Width: 1920, Height: 1080},
	}
	if err := b.Save(want); err != nil {
		t.Fatal(err)
	}
	got, err := b.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.MAC != want.MAC || got.CPU != want.CPU || string(got.HardwareModel) != string(want.HardwareModel) {
		t.Errorf("round-trip mismatch: got %+v", got)
	}
}

func TestCloneRotatesIdentity(t *testing.T) {
	dir := t.TempDir()
	src := Bundle{Name: "base", Dir: filepath.Join(dir, "base.mirage")}
	if err := src.Save(&Config{SchemaVersion: 1, OS: "macos", CPU: 4, MemoryMB: 4096, MAC: "06:00:00:00:00:01", MachineID: []byte{1}}); err != nil {
		t.Fatal(err)
	}
	// clonefile needs the data files to exist.
	for _, p := range []string{src.DiskPath(), src.AuxPath()} {
		if err := os.WriteFile(p, []byte("img"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dst := Bundle{Name: "clone", Dir: filepath.Join(dir, "clone.mirage")}
	id := Identity{MachineID: []byte{9, 9}, MAC: "06:00:00:00:00:02"}
	if err := Clone(src, dst, id); err != nil {
		t.Fatal(err)
	}
	got, err := dst.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.MAC != id.MAC || string(got.MachineID) != string(id.MachineID) {
		t.Errorf("clone did not rotate identity: %+v", got)
	}
	// Cloning onto an existing bundle must conflict.
	if err := Clone(src, dst, id); err == nil {
		t.Error("expected conflict cloning over existing bundle")
	}
}
