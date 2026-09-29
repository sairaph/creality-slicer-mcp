package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

// failingFS serves its files except one, which fails to read.
type failingFS struct {
	fstest.MapFS
	bad string
}

func (f failingFS) ReadFile(name string) ([]byte, error) {
	if name == f.bad {
		return nil, errors.New("disk full")
	}
	return f.MapFS.ReadFile(name)
}

func (f failingFS) Open(name string) (fs.File, error) {
	if name == f.bad {
		return nil, errors.New("disk full")
	}
	return f.MapFS.Open(name)
}

func TestWriteSkillFromKeepsThePreviousCopyOnFailure(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "creality-slicer")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(old, []byte("good copy"), 0o644); err != nil {
		t.Fatal(err)
	}

	src := failingFS{MapFS: fstest.MapFS{"SKILL.md": {Data: []byte("new")}, "topic.md": {Data: []byte("t")}}, bad: "topic.md"}
	if err := writeSkillFrom(src, dir); err == nil {
		t.Fatal("a failing write reported success")
	}
	if got, _ := os.ReadFile(old); string(got) != "good copy" {
		t.Errorf("the previous copy was damaged: %q", got)
	}
	if _, err := os.Stat(dir + ".new"); !os.IsNotExist(err) {
		t.Error("the temporary folder was left behind")
	}

	// A good source replaces the copy and drops what it no longer has.
	if err := os.WriteFile(filepath.Join(dir, "gone.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	good := fstest.MapFS{"SKILL.md": {Data: []byte("new")}, "sub/topic.md": {Data: []byte("t")}}
	if err := writeSkillFrom(good, dir); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(old); string(got) != "new" {
		t.Errorf("SKILL.md = %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "sub", "topic.md")); err != nil {
		t.Error("a nested file was not written")
	}
	if _, err := os.Stat(filepath.Join(dir, "gone.md")); err == nil {
		t.Error("a file of the previous copy survived")
	}
	if _, err := os.Stat(dir + ".new"); !os.IsNotExist(err) {
		t.Error("the temporary folder was left behind")
	}
}

func TestIsOurBinaryIgnoresExtensionCase(t *testing.T) {
	for _, c := range []string{`C:\x\creality-slicer-mcp.Exe`, `C:\x\creality-slicer-mcp.EXE`, `C:\x\CREALITY-SLICER-MCP.exe`} {
		if !isOurBinary(c) {
			t.Errorf("isOurBinary(%q) = false", c)
		}
	}
}

func TestTestMainNeutralisesTheGuideFolderEnvironment(t *testing.T) {
	for _, v := range []string{"CONTINUE_GLOBAL_DIR", "ZDOTDIR"} {
		if got := os.Getenv(v); got != "" {
			t.Errorf("%s = %q under test: it would reach the real machine", v, got)
		}
	}
}
