package threemf

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/sairaph/creality-slicer-mcp/internal/mesh"
)

func objectNames(t *testing.T, path string) []string {
	t.Helper()
	q, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	var out []string
	for _, o := range q.Objects {
		out = append(out, o.Name)
	}
	return out
}

// A save whose rename fails keeps the project's edits: the next Save writes
// them, and the file on disk is the old one meanwhile.
func TestFailedRenameKeepsThePendingEdit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keep.3mf")
	p := newProject(t)
	a, _ := p.AddObject(ObjectSpec{Name: "A", Mesh: mesh.Box(5, 5, 5)})
	if err := p.Save(path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	realRename := renameFile
	t.Cleanup(func() { renameFile = realRename })
	fails := 1 << 30
	calls := 0
	renameFile = func(from, to string) error {
		calls++
		if calls <= fails {
			return errors.New("simulated: access denied")
		}
		return realRename(from, to)
	}
	if err := p.RenameObject(a.ID, "B"); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	err := p.Save(path)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("the failed rename was reported as saved")
	}
	// one retry layer: about renameBudget in all, never a multiple of it
	if elapsed < renameBudget-300*time.Millisecond || elapsed > renameBudget+time.Second {
		t.Errorf("a held file blocked the save for %v, want about %v", elapsed, renameBudget)
	}
	t.Logf("a held file blocked one save for %v after %d rename attempts", elapsed, calls)
	if calls < 2 || !errors.Is(err, ErrReplace) {
		t.Errorf("rename tried %d times; err %v", calls, err)
	}
	if got := objectNames(t, path); len(got) != 1 || got[0] != "A" {
		t.Fatalf("the old file changed: %v", got)
	}
	// The project still reads and still has the edit pending.
	if p.Object(a.ID).Name != "B" {
		t.Fatalf("the edit is gone from memory: %q", p.Object(a.ID).Name)
	}
	fails = calls // the next rename works
	if err := p.Save(path); err != nil {
		t.Fatal(err)
	}
	if got := objectNames(t, path); len(got) != 1 || got[0] != "B" {
		t.Fatalf("the retry lost the edit: %v", got)
	}
}

// The rename succeeds on a later attempt inside one Save.
func TestRenameIsRetriedInsideSave(t *testing.T) {
	path := filepath.Join(t.TempDir(), "retry.3mf")
	p := newProject(t)
	p.AddObject(ObjectSpec{Name: "A", Mesh: mesh.Box(5, 5, 5)})
	realRename := renameFile
	t.Cleanup(func() { renameFile = realRename; p.Close() })
	calls := 0
	renameFile = func(from, to string) error {
		calls++
		if calls < 3 {
			return errors.New("simulated: sharing violation")
		}
		return realRename(from, to)
	}
	if err := p.Save(path); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Errorf("rename calls %d", calls)
	}
	if got := objectNames(t, path); len(got) != 1 {
		t.Fatalf("saved %v", got)
	}
}
