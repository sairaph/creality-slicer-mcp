package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRemovePathListEntry(t *testing.T) {
	dir := `C:\Users\me\AppData\Local\creality-slicer-mcp\bin`
	value := `C:\Users\me\AppData\Local\creality-slicer-mcp\bin;C:\tools;;C:\USERS\ME\APPDATA\LOCAL\CREALITY-SLICER-MCP\BIN\;C:\other`
	got, removed := removePathListEntry(value, dir, ";", true)
	if !removed || got != `C:\tools;;C:\other` {
		t.Fatalf("got %q, %v", got, removed)
	}
	if _, removed := removePathListEntry(`C:\tools`, dir, ";", true); removed {
		t.Fatal("removed an entry that was not there")
	}
	// Case-sensitive systems keep a differently cased entry.
	if got, removed := removePathListEntry("/home/me/.creality-slicer-mcp/bin:/usr/bin:/HOME/ME/.CREALITY-SLICER-MCP/BIN", "/home/me/.creality-slicer-mcp/bin", ":", false); !removed || got != "/usr/bin:/HOME/ME/.CREALITY-SLICER-MCP/BIN" {
		t.Fatalf("got %q, %v", got, removed)
	}
}

func TestRemoveRCBlockRemovesExactlyWhatInstallShAdded(t *testing.T) {
	dir := "/home/me/.creality-slicer-mcp/bin"
	before := "alias ll='ls -l'\nexport EDITOR=vim\n"
	added := before + "\n# added by creality-slicer-mcp installer\nexport PATH=\"" + dir + ":$PATH\"\n"
	got, removed := removeRCBlock(added, dir)
	if !removed || got != before {
		t.Fatalf("removed=%v\ngot:\n%q\nwant:\n%q", removed, got, before)
	}
	// A user's own line naming the directory is not touched.
	own := before + "export PATH=\"" + dir + ":$PATH\"\n"
	if got, removed := removeRCBlock(own, dir); removed || got != own {
		t.Fatalf("changed a line the installer did not add: %q", got)
	}
}

func TestRemovePathReportsAndDeletes(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "x.json")
	os.WriteFile(file, []byte("{}"), 0o600)
	var out bytes.Buffer
	if code := removePath(&out, file, "would remove", true); code != 0 {
		t.Fatal(code)
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatal("dry run deleted the file")
	}
	if code := removePath(&out, file, "removed", false); code != 0 {
		t.Fatal(code)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatal("file not deleted")
	}
	if code := removePath(&out, file, "removed", false); code != 0 {
		t.Fatal("a missing path is not an error")
	}
	if !strings.Contains(out.String(), "would remove "+file) || !strings.Contains(out.String(), "removed "+file) {
		t.Fatalf("output:\n%s", out.String())
	}
}

func TestRemovePathDropsTheEmptyDataFolderButNotOneWithProjects(t *testing.T) {
	root := t.TempDir()
	for _, tc := range []struct {
		name     string
		projects bool
	}{{"empty", false}, {"with projects", true}} {
		data := filepath.Join(root, tc.name, ".creality-slicer-mcp")
		cache := filepath.Join(data, "cache")
		if err := os.MkdirAll(filepath.Join(cache, "run"), 0o700); err != nil {
			t.Fatal(err)
		}
		if tc.projects {
			if err := os.MkdirAll(filepath.Join(data, "projects", "p1"), 0o700); err != nil {
				t.Fatal(err)
			}
		}
		var out bytes.Buffer
		if code := removePath(&out, cache, "removed", false); code != 0 {
			t.Fatalf("%s: code %d", tc.name, code)
		}
		_, err := os.Stat(data)
		switch {
		case tc.projects && err != nil:
			t.Errorf("%s: the data folder holding projects was removed", tc.name)
		case !tc.projects && !os.IsNotExist(err):
			t.Errorf("%s: the empty data folder was left behind", tc.name)
		}
	}
}

func TestInstallRootOnUnixIsTheBinFolder(t *testing.T) {
	dir := filepath.Join("home", ".creality-slicer-mcp", "bin")
	got := installRoot(dir)
	if runtime.GOOS == "windows" {
		if want := filepath.Dir(dir); got != want {
			t.Errorf("installRoot = %q, want %q", got, want)
		}
		return
	}
	if got != dir {
		t.Errorf("installRoot = %q, want %q: the parent is the data folder that holds the projects", got, dir)
	}
}

func TestInInstallDir(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "creality-slicer-mcp")
	if !inInstallDir(exe, dir) {
		t.Error("a binary in the install directory was not recognised")
	}
	if inInstallDir(filepath.Join(t.TempDir(), "creality-slicer-mcp"), dir) {
		t.Error("a binary elsewhere was taken for the installed one")
	}
}
