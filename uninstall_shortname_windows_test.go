package main

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// A binary reached through a Windows short (8.3) folder name, as temp folders
// on some machines are, is still the installed one.
func TestInInstallDirThroughAShortName(t *testing.T) {
	long := filepath.Join(t.TempDir(), "LongInstallFolderName")
	if err := os.Mkdir(long, 0o755); err != nil {
		t.Fatal(err)
	}
	short, err := shortPath(long)
	if err != nil || strings.EqualFold(short, long) {
		t.Skip("8.3 names are not generated on this volume")
	}
	if !inInstallDir(filepath.Join(short, "creality-slicer-mcp.exe"), long) {
		t.Errorf("a binary under the short name %q was not recognised", short)
	}
	if !sameExecutable(filepath.Join(short, "creality-slicer-mcp.exe"), filepath.Join(long, "creality-slicer-mcp.exe")) {
		t.Error("the same binary under its short folder name was taken for another")
	}
}

func shortPath(p string) (string, error) {
	in, err := syscall.UTF16PtrFromString(p)
	if err != nil {
		return "", err
	}
	buf := make([]uint16, 1024)
	n, err := syscall.GetShortPathName(in, &buf[0], uint32(len(buf)))
	if err != nil {
		return "", err
	}
	return syscall.UTF16ToString(buf[:n]), nil
}
