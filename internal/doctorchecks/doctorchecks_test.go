package doctorchecks

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sairaph/mcp-wizard/doctor"

	"github.com/sairaph/creality-slicer-mcp/internal/domain"
)

func TestChecksListsEveryCheckOnce(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range Checks() {
		if c.Name() == "" || seen[c.Name()] {
			t.Errorf("check name %q is empty or repeated", c.Name())
		}
		seen[c.Name()] = true
	}
	if !seen["Settings"] || !seen["Data folder"] {
		t.Errorf("checks = %v", seen)
	}
}

func TestSettingsCheck(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "CrealityPrint.exe")
	if err := os.WriteFile(exe, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, value string
		status      doctor.Status
		detail      string
	}{
		{"unset", "", doctor.OK, "is not set"},
		{"valid", exe, doctor.OK, "(from " + domain.EnvCmd + ")"},
		{"invalid", filepath.Join(t.TempDir(), "gone.exe"), doctor.Fail, "the MCP server will not start"},
	}
	for _, c := range cases {
		t.Setenv(domain.EnvCmd, c.value)
		res := SettingsCheck{}.Run(context.Background())
		if res.Status != c.status || !strings.Contains(res.Detail, c.detail) {
			t.Errorf("%s: %+v, want %s containing %q", c.name, res, c.status, c.detail)
		}
		if c.status == doctor.Fail && !strings.Contains(res.Detail, domain.EnvCmd) {
			t.Errorf("%s: the failure does not name %s: %q", c.name, domain.EnvCmd, res.Detail)
		}
	}
}

func TestDataDirCheckExistingFolder(t *testing.T) {
	dir := t.TempDir()
	res := DataDirCheck{Dir: dir}.Run(context.Background())
	if res.Status != doctor.OK || !strings.Contains(res.Detail, "is writable") {
		t.Errorf("%+v", res)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("the probe left files behind: %v", entries)
	}
}

func TestDataDirCheckMissingFolderIsNotCreated(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a", "b")
	res := DataDirCheck{Dir: dir}.Run(context.Background())
	if res.Status != doctor.OK || !strings.Contains(res.Detail, "does not exist yet") {
		t.Errorf("%+v", res)
	}
	if _, err := os.Stat(filepath.Join(dir, "..")); err == nil {
		t.Error("the check created a folder")
	}
}

func TestDataDirCheckDefaultsToTheDataDir(t *testing.T) {
	want, _ := domain.DataDir()
	res := DataDirCheck{}.Run(context.Background())
	if res.Status != doctor.OK || !strings.Contains(res.Detail, want) {
		t.Errorf("%+v, want it to mention %s", res, want)
	}
}

func TestDataDirCheckFailsWhenAFileBlocksTheFolder(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{file, filepath.Join(file, "data")} {
		res := DataDirCheck{Dir: dir}.Run(context.Background())
		if res.Status != doctor.Fail || !strings.Contains(res.Detail, "is not writable") {
			t.Errorf("%s: %+v", dir, res)
		}
	}
}

func TestNearestExisting(t *testing.T) {
	root := t.TempDir()
	if got, exists := nearestExisting(root); got != root || !exists {
		t.Errorf("existing: %q, %v", got, exists)
	}
	if got, exists := nearestExisting(filepath.Join(root, "x", "y")); got != root || exists {
		t.Errorf("missing: %q, %v", got, exists)
	}
}
