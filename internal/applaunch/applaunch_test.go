package applaunch

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// The real launcher never starts anything under go test.
func TestRealLauncherRefusesUnderTest(t *testing.T) {
	dir := t.TempDir()
	exe, file := filepath.Join(dir, "app.exe"), filepath.Join(dir, "plate.gcode")
	for _, p := range []string{exe, file} {
		if err := os.WriteFile(p, []byte("x"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	pid, err := Real{}.Launch(exe, file)
	if !errors.Is(err, ErrUnderTest) || pid != 0 {
		t.Errorf("Launch = %d, %v", pid, err)
	}
}

// The application gets one file and no option.
func TestArgsAreTheFileAlone(t *testing.T) {
	got := Args(`C:\x\plate 1.gcode`)
	if len(got) != 1 || got[0] != `C:\x\plate 1.gcode` {
		t.Errorf("Args = %q", got)
	}
}

func TestCheckRefusesRelativeAndMissingPaths(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "f")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := check(file, file); err != nil {
		t.Errorf("good paths: %v", err)
	}
	for _, c := range [][2]string{{"app.exe", file}, {file, "plate.gcode"}, {dir, file}, {file, filepath.Join(dir, "gone")}} {
		if check(c[0], c[1]) == nil {
			t.Errorf("check(%q, %q) passed", c[0], c[1])
		}
	}
}
