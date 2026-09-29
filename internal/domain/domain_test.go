package domain

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/sairaph/creality-slicer-mcp/internal/userhome"
)

func TestAssetName(t *testing.T) {
	cases := []struct{ goos, arch, want string }{
		{"windows", "amd64", "creality-slicer-mcp-windows-amd64.exe"},
		{"linux", "arm64", "creality-slicer-mcp-linux-arm64"},
		{"darwin", "amd64", "creality-slicer-mcp-darwin-amd64"},
	}
	for _, c := range cases {
		if got := AssetName(c.goos, c.arch); got != c.want {
			t.Errorf("AssetName(%s, %s) = %q, want %q", c.goos, c.arch, got, c.want)
		}
	}
}

func TestDefaultEnvHoldsOnlyTheTransport(t *testing.T) {
	env := DefaultEnv()
	if len(env) != 1 || env["TRANSPORT"] != "stdio" {
		t.Fatalf("DefaultEnv() = %v", env)
	}
}

func TestFoldersLiveUnderTheUserHome(t *testing.T) {
	home, err := userhome.Dir()
	if err != nil {
		t.Fatal(err)
	}
	data, err := DataDir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".creality-slicer-mcp"); data != want {
		t.Errorf("DataDir() = %q, want %q", data, want)
	}
	cache, _ := CacheDir()
	if want := filepath.Join(data, "cache"); cache != want {
		t.Errorf("CacheDir() = %q, want %q", cache, want)
	}
	projects, _ := ProjectsDir()
	if want := filepath.Join(data, "projects"); projects != want {
		t.Errorf("ProjectsDir() = %q, want %q", projects, want)
	}
	if _, err := os.Stat(data); err == nil {
		t.Errorf("DataDir() created %s: resolving a path must not touch the disk", data)
	}
}

func TestInstallDir(t *testing.T) {
	dir, err := InstallDir()
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		if !strings.HasSuffix(dir, filepath.Join("creality-slicer-mcp", "bin")) {
			t.Errorf("InstallDir() = %q", dir)
		}
		return
	}
	data, _ := DataDir()
	if want := filepath.Join(data, "bin"); dir != want {
		t.Errorf("InstallDir() = %q, want %q", dir, want)
	}
}

func TestSettingsFromEnv(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "CrealityPrint.exe")
	if err := os.WriteFile(exe, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	folder := t.TempDir()

	t.Run("unset means detect", func(t *testing.T) {
		t.Setenv(EnvCmd, "")
		s, err := SettingsFromEnv()
		if err != nil || s.Cmd != "" {
			t.Fatalf("SettingsFromEnv() = %+v, %v", s, err)
		}
	})
	t.Run("a good path", func(t *testing.T) {
		t.Setenv(EnvCmd, "  "+exe+"  ")
		s, err := SettingsFromEnv()
		if err != nil || s.Cmd != exe {
			t.Fatalf("SettingsFromEnv() = %+v, %v", s, err)
		}
	})
	bad := []struct{ name, value, want string }{
		{"relative", filepath.Join("bin", "CrealityPrint.exe"), "not an absolute path"},
		{"missing", filepath.Join(t.TempDir(), "gone.exe"), "does not exist"},
		{"folder", folder, "is a folder"},
	}
	for _, c := range bad {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv(EnvCmd, c.value)
			_, err := SettingsFromEnv()
			if err == nil {
				t.Fatal("no error")
			}
			if !strings.Contains(err.Error(), EnvCmd) || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error = %q, want it to name %s and say %q", err, EnvCmd, c.want)
			}
		})
	}
}

func TestWriteFileAtomicCreatesAndReplaces(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a", "b", "file.txt")
	if err := WriteFileAtomic(path, []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(path, []byte("two"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "two" {
		t.Fatalf("read = %q, %v", got, err)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatalf("temporary files left behind: %v", entries)
	}
}

func TestSettingsOnlyTextFeedback(t *testing.T) {
	for value, want := range map[string]bool{"": false, "1": true, "TRUE": true, " yes ": true, "on": true, "0": false, "false": false, "no": false, "off": false} {
		t.Setenv(EnvCmd, "")
		t.Setenv(EnvOnlyTextFeedback, value)
		s, err := SettingsFromEnv()
		if err != nil || s.OnlyTextFeedback != want {
			t.Errorf("%q: %+v, %v (want %v)", value, s, err, want)
		}
	}
	t.Setenv(EnvOnlyTextFeedback, "maybe")
	if _, err := SettingsFromEnv(); err == nil || !strings.Contains(err.Error(), EnvOnlyTextFeedback) {
		t.Errorf("a bad value: %v", err)
	}
}
