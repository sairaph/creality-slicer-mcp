package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestRemoveInstallRootKeepsTheDataFolderWithProjects(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("on Windows a helper process removes the folder after this one exits")
	}
	data := filepath.Join(t.TempDir(), ".creality-slicer-mcp")
	bin := filepath.Join(data, "bin")
	projects := filepath.Join(data, "projects", "p1")
	for _, d := range []string{bin, projects} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := removeInstallRoot(bin); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(bin); !os.IsNotExist(err) {
		t.Error("the bin folder was not removed")
	}
	if _, err := os.Stat(projects); err != nil {
		t.Error("the projects were removed with the program")
	}
	// Without projects the emptied data folder goes too.
	if err := os.RemoveAll(filepath.Join(data, "projects")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := removeInstallRoot(bin); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(data); !os.IsNotExist(err) {
		t.Error("the emptied data folder was left behind")
	}
}
