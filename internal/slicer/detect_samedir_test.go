package slicer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSameDirResolvesLinks(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "Real Folder")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if !sameDir(real, filepath.Join(root, "REAL FOLDER")) {
		t.Error("case")
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks are not available: %v", err)
	}
	if !sameDir(real, link) {
		t.Error("a link to the folder is the same folder")
	}
	other := filepath.Join(root, "other")
	os.Mkdir(other, 0o755)
	if sameDir(real, other) || sameDir(real, filepath.Join(root, "missing")) {
		t.Error("different folders are the same")
	}
}
