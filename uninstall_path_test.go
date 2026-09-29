package main

import (
	"os"
	"path/filepath"
	"testing"
)

// A binary reached through a symlinked folder is still the installed one
// (macOS resolves /var to /private/var in temp folders, for example).
func TestInInstallDirThroughASymlink(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable here: %v", err)
	}
	if !inInstallDir(filepath.Join(link, "creality-slicer-mcp"), real) {
		t.Error("a binary reached through a symlinked folder was not recognised")
	}
	if !inInstallDir(filepath.Join(real, "creality-slicer-mcp"), link) {
		t.Error("an install folder given through a symlink was not recognised")
	}
	if !sameExecutable(filepath.Join(link, "creality-slicer-mcp"), filepath.Join(real, "creality-slicer-mcp")) {
		t.Error("the same binary reached through a symlinked folder was taken for another")
	}
}
