package projects

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/gofrs/flock"
)

func TestDeleteRemovesTheProjectWholly(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Gone")
	e.addBox(t, info.ID, "a", 20, 20, 10)
	if _, err := e.st.PrepareView(info.ID, 1, ViewProject); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.Delete(info.ID, info.ID); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(e.st.cfg.Root)
	if len(entries) != 0 {
		t.Fatalf("left in the store: %v", entries)
	}
	if list, _ := e.st.List(); len(list) != 0 {
		t.Fatalf("list %v", list)
	}
}

// A file of the project open in another program (a view copy in the app):
// nothing changes and the answer is a conflict; after the program lets go the
// delete works.
func TestDeleteWithAnOpenFileChangesNothing(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("only Windows refuses to rename a folder that holds an open file")
	}
	e := newEnv(t)
	info := e.newProject(t, "Busy")
	e.addBox(t, info.ID, "a", 20, 20, 10)
	vf, err := e.st.PrepareView(info.ID, 1, ViewProject)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(vf.Path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.st.Delete(info.ID, info.ID)
	wantCode(t, err, CodeConflict)
	if list, _ := e.st.List(); len(list) != 1 || list[0].ID != info.ID {
		t.Fatalf("the project vanished from the list: %v", list)
	}
	if _, err := e.st.GetProject(info.ID); err != nil {
		t.Fatalf("get_project after the refused delete: %v", err)
	}
	if _, err := os.Stat(vf.Path); err != nil {
		t.Fatalf("the view file is gone: %v", err)
	}
	// It still works as a project: a change goes through.
	e.addBox(t, info.ID, "b", 20, 20, 10)
	f.Close()
	if _, err := e.st.Delete(info.ID, info.ID); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(e.st.cfg.Root); len(entries) != 0 {
		t.Fatalf("left in the store: %v", entries)
	}
}

// Another process that holds the project lock: conflict, nothing changed.
func TestDeleteWhileAnotherProcessHoldsTheLock(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Locked")
	old := lockTimeout
	lockTimeout = 300 * time.Millisecond
	defer func() { lockTimeout = old }()
	fl := flock.New(filepath.Join(e.st.dir(info.ID), lockFile))
	if ok, err := fl.TryLock(); err != nil || !ok {
		t.Fatalf("test lock: %v %v", ok, err)
	}
	_, err := e.st.Delete(info.ID, info.ID)
	wantCode(t, err, CodeConflict)
	_ = fl.Unlock()
	if list, _ := e.st.List(); len(list) != 1 {
		t.Fatalf("list %v", list)
	}
	if _, err := e.st.Delete(info.ID, info.ID); err != nil {
		t.Fatal(err)
	}
}

// Leftovers of an earlier delete are swept at start and never shown.
func TestTrashIsHiddenAndSwept(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Keep")
	trash := filepath.Join(e.st.cfg.Root, trashPrefix+"old-1")
	if err := os.MkdirAll(trash, 0o755); err != nil {
		t.Fatal(err)
	}
	// A half deleted project: its job.json is still there.
	if data, err := os.ReadFile(filepath.Join(e.st.dir(info.ID), metaFile)); err == nil {
		_ = os.WriteFile(filepath.Join(trash, metaFile), data, 0o644)
	}
	if list, _ := e.st.List(); len(list) != 1 || list[0].ID != info.ID {
		t.Fatalf("the trash shows in the list: %v", list)
	}
	_, err := e.st.GetProject(trashPrefix + "old-1")
	wantCode(t, err, CodeInvalidInput)
	// A store started on the same folder sweeps it.
	if _, err := New(e.st.cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(trash); err == nil {
		t.Error("the trash was not swept at start")
	}
	// So does a later delete.
	if err := os.MkdirAll(trash, 0o755); err != nil {
		t.Fatal(err)
	}
	other := e.newProject(t, "Other")
	if _, err := e.st.Delete(other.ID, other.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(trash); err == nil {
		t.Error("the trash was not swept by a delete")
	}
}
