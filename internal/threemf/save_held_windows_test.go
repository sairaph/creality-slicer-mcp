package threemf

import (
	"path/filepath"
	"syscall"
	"testing"

	"github.com/sairaph/creality-slicer-mcp/internal/mesh"
)

// Another handle that does not share delete blocks the replacement: the save
// fails, nothing is lost, and the save after the handle is gone writes the edit.
func TestHeldFileDoesNotLoseTheEditOnWindows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "held.3mf")
	p := newProject(t)
	a, _ := p.AddObject(ObjectSpec{Name: "A", Mesh: mesh.Box(5, 5, 5)})
	if err := p.Save(path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	name, _ := syscall.UTF16PtrFromString(path)
	h, err := syscall.CreateFile(name, syscall.GENERIC_READ, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	released := false
	release := func() {
		if !released {
			syscall.CloseHandle(h)
			released = true
		}
	}
	t.Cleanup(release)
	if err := p.RenameObject(a.ID, "B"); err != nil {
		t.Fatal(err)
	}
	if err := p.Save(path); err == nil {
		t.Fatal("saved over a file another handle holds")
	}
	release()
	if err := p.Save(path); err != nil {
		t.Fatal(err)
	}
	if got := objectNames(t, path); len(got) != 1 || got[0] != "B" {
		t.Fatalf("the edit was lost: %v", got)
	}
}
