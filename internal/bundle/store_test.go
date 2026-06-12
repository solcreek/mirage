package bundle

import "testing"

func saveBundle(t *testing.T, k Kind, name string) {
	t.Helper()
	b := Resolve(k, name)
	if err := b.Save(&Config{SchemaVersion: 1, OS: "macos", CPU: 4, MemoryMB: 4096}); err != nil {
		t.Fatal(err)
	}
}

func TestResolveAndFind(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	if _, _, ok := Find("nope"); ok {
		t.Error("Find should miss for a non-existent bundle")
	}

	saveBundle(t, Image, "golden")
	saveBundle(t, VM, "inst")

	if b, k, ok := Find("golden"); !ok || k != Image || b.Name != "golden" {
		t.Errorf("Find(golden) = %+v kind=%v ok=%v", b, k, ok)
	}
	if _, k, ok := Find("inst"); !ok || k != VM {
		t.Errorf("Find(inst) kind=%v ok=%v, want VM", k, ok)
	}
}

func TestFindPrefersVMOverImage(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	// Same name as both an image and a VM: Find resolves the VM first.
	saveBundle(t, Image, "dup")
	saveBundle(t, VM, "dup")
	if _, k, ok := Find("dup"); !ok || k != VM {
		t.Errorf("Find should prefer VM, got kind=%v ok=%v", k, ok)
	}
}

func TestList(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	if got, _ := List(Image); len(got) != 0 {
		t.Errorf("empty store: got %d images", len(got))
	}
	saveBundle(t, Image, "b")
	saveBundle(t, Image, "a")
	got, err := List(Image)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "a" || got[1].Name != "b" {
		t.Errorf("List not sorted/complete: %+v", got)
	}
}
