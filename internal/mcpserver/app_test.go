package mcpserver

import (
	"errors"
	"github.com/sairaph/creality-slicer-mcp/internal/projects"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/sairaph/creality-slicer-mcp/internal/slicer"
)

// fakeLauncher records what the application would have been started with.
type fakeLauncher struct {
	mu    sync.Mutex
	exes  []string
	files []string
	err   error
}

func (f *fakeLauncher) Launch(exe, file string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return 0, f.err
	}
	f.exes, f.files = append(f.exes, exe), append(f.files, file)
	return 4200 + len(f.files), nil
}

func (f *fakeLauncher) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.files)
}

func TestOpenInAppPreviewCopiesTheSliceAndLaunchesTheAppOnOneFile(t *testing.T) {
	pf := newProjFixture(t)
	id := pf.withModel(t, "Look")
	// No slice yet: a conflict that says to slice first, and nothing is started.
	e := pf.errText(t, "open_in_app", map[string]any{"project": id})
	contains(t, "unsliced", e, "conflict", "slice_project")
	if pf.launcher.calls() != 0 {
		t.Fatal("the app was launched for a plate that was not sliced")
	}
	pf.ok(t, "slice_project", map[string]any{"project": id, "wait": 30})

	out := pf.ok(t, "open_in_app", map[string]any{"project": id})
	front := frontOf(t, out)
	if front["mode"] != "preview" || front["plate"] != 1 || front["pid"] != 4201 || front["app_version"] != "7.2.2" || front["file"] == nil {
		t.Errorf("front = %v", front)
	}
	if pf.launcher.calls() != 1 {
		t.Fatalf("launches: %d", pf.launcher.calls())
	}
	file := pf.launcher.files[0]
	if file != front["file"] || !strings.HasSuffix(file, ".gcode") || !strings.Contains(file, string(filepath.Separator)+"view"+string(filepath.Separator)+"plate1_r") {
		t.Errorf("file = %s", file)
	}
	if !strings.HasSuffix(pf.launcher.exes[0], "CrealityPrint.exe") {
		t.Errorf("exe = %s", pf.launcher.exes[0])
	}
	// The G-code is a copy of the slice, not the slice output itself.
	got, err := os.ReadFile(file)
	if err != nil || !strings.Contains(string(got), "EXECUTABLE_BLOCK_START") {
		t.Errorf("the copy: %v, %.30q", err, got)
	}
	contains(t, "body", bodyOf(out), "new Creality Print 7.2.2 window", "Preview tab", "Other Creality Print windows are untouched", "closes the new window", "mode project")
}

func TestOpenInAppProjectModeAndErrors(t *testing.T) {
	pf := newProjFixture(t)
	id := pf.withModel(t, "Edit me")
	out := pf.ok(t, "open_in_app", map[string]any{"project": id, "mode": "project"})
	file := pf.launcher.files[0]
	if !strings.HasSuffix(file, ".3mf") || frontOf(t, out)["mode"] != "project" {
		t.Errorf("file = %s, front = %v", file, frontOf(t, out))
	}
	if st, err := os.Stat(file); err != nil || st.Size() == 0 {
		t.Errorf("the project file: %v", err)
	}
	contains(t, "project mode", bodyOf(out), "3D editor", "File > Save Project", "open_project", "into `"+id+"`", file)

	if e := pf.errText(t, "open_in_app", map[string]any{"project": id, "plate": 9, "mode": "project"}); !strings.Contains(e, "invalid_input") {
		t.Errorf("unknown plate: %s", e)
	}
	if res := call(t, pf.cs, "open_in_app", map[string]any{"project": id, "mode": "sideways"}); !res.IsError {
		t.Error("an unknown mode was accepted")
	}
	if e := pf.errText(t, "open_in_app", map[string]any{"project": "nope"}); !strings.Contains(e, "not_found") {
		t.Errorf("unknown project: %s", e)
	}
	before := pf.launcher.calls()
	pf.launcher.err = errors.New("the file is not an executable")
	if e := pf.errText(t, "open_in_app", map[string]any{"project": id, "mode": "project"}); !strings.Contains(e, "unavailable") || !strings.Contains(e, "Could not start Creality Print") {
		t.Errorf("a failed launch: %s", e)
	}
	if pf.launcher.calls() != before {
		t.Error("a failed launch was counted")
	}
}

// Without a supported install nothing is started.
func TestOpenInAppNeedsASupportedInstall(t *testing.T) {
	fl := &fakeLauncher{}
	f := newFixture(t, func(fi *fakeInstall, d *Deps) {
		fi.install = slicer.Install{Reason: "not installed"}
		d.Launcher = fl
	})
	e := f.errText(t, "open_in_app", map[string]any{"project": "x"})
	contains(t, "no install", e, "unavailable", "not installed")
	if fl.calls() != 0 {
		t.Error("the app was launched without an install")
	}
}

// The default launcher is the real one, which refuses to run under go test: a
// test that forgets to inject a fake starts nothing.
func TestOpenInAppRealLauncherRefusesUnderTest(t *testing.T) {
	pf := newProjFixture(t, func(c *Config) { c.Deps.Launcher = nil })
	id := pf.withModel(t, "Real")
	e := pf.errText(t, "open_in_app", map[string]any{"project": id, "mode": "project"})
	contains(t, "real launcher", e, "unavailable", "does not run under go test")
}

// A file the user saved in the view folder is reported with how to bring it
// back, and is not overwritten.
func TestOpenInAppReportsSavedFiles(t *testing.T) {
	pf := newProjFixture(t)
	id := pf.withModel(t, "Saved")
	first := pf.ok(t, "open_in_app", map[string]any{"project": id, "mode": "project"})
	if _, has := frontOf(t, first)["saved_files"]; has {
		t.Errorf("saved files before any save: %v", frontOf(t, first))
	}
	saved := pf.launcher.files[0]
	if err := os.WriteFile(saved, []byte("the user saved this in the app"), 0o644); err != nil {
		t.Fatal(err)
	}
	second := pf.ok(t, "open_in_app", map[string]any{"project": id, "mode": "project"})
	contains(t, "saved", bodyOf(second), "You saved changes in `"+saved+"`: bring them back with open_project with {\"path\": \""+saved+"\", \"into\": \""+id+"\"}")
	if got, _ := os.ReadFile(saved); string(got) != "the user saved this in the app" {
		t.Error("the saved file was overwritten")
	}
	if pf.launcher.files[1] == saved {
		t.Error("the app was opened on the file the user saved")
	}
}

// savedFilesStore answers PrepareView with saved files, as the projects layer
// does for files the user changed.
type savedFilesStore struct {
	ProjectStore
	saved []string
}

func (s savedFilesStore) PrepareView(ref string, plate int, mode string) (*projects.ViewFile, error) {
	vf, err := s.ProjectStore.PrepareView(ref, plate, mode)
	if err == nil {
		vf.SavedFiles = s.saved
	}
	return vf, err
}

func TestOpenInAppFormatsSavedFilesFromTheStore(t *testing.T) {
	pf := newProjFixture(t)
	a, b := filepath.Join(t.TempDir(), "one.3mf"), filepath.Join(t.TempDir(), "two.3mf")
	pf.wrap = func(s ProjectStore) ProjectStore { return savedFilesStore{s, []string{a, b}} }
	id := pf.withModel(t, "Fake")
	out := pf.ok(t, "open_in_app", map[string]any{"project": id, "mode": "project"})
	for _, f := range []string{a, b} {
		contains(t, "saved line", bodyOf(out), "You saved changes in `"+f+"`: bring them back with open_project with {\"path\": \""+f+"\", \"into\": \""+id+"\"}")
	}
	list, _ := frontOf(t, out)["saved_files"].([]any)
	if len(list) != 2 || list[0] != a || list[1] != b {
		t.Errorf("saved_files = %v", list)
	}
}
