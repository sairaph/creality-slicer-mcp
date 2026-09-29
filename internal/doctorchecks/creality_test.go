package doctorchecks

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sairaph/mcp-wizard/doctor"

	"github.com/sairaph/creality-slicer-mcp/internal/catalog"
	"github.com/sairaph/creality-slicer-mcp/internal/domain"
	"github.com/sairaph/creality-slicer-mcp/internal/profiles"
	"github.com/sairaph/creality-slicer-mcp/internal/slicer"
)

// fakeProfiles serves canned presets.
type fakeProfiles struct {
	process, filament []profiles.Descriptor
	presets           map[string]profiles.Preset
	listErr           error
	keys              []string // KeysSetBy result
	keysErr           error
	modelAsked        string
}

func (f *fakeProfiles) KeysSetBy(model string) ([]string, error) {
	f.modelAsked = model
	return f.keys, f.keysErr
}

func (f *fakeProfiles) List(t profiles.Type, _ profiles.Filter) ([]profiles.Descriptor, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	if t == profiles.TypeProcess {
		return f.process, nil
	}
	return f.filament, nil
}

func (f *fakeProfiles) Get(t profiles.Type, name string) (profiles.Preset, error) {
	if p, ok := f.presets[string(t)+"/"+name]; ok {
		return p, nil
	}
	return profiles.Preset{}, fmt.Errorf("no %s preset %q", t, name)
}

type fakeTexts struct{ all bool }

func (f fakeTexts) Text(uint64) (string, bool) { return "text", f.all }
func (fakeTexts) Len() int                     { return 900 }
func (fakeTexts) Langs() []string              { return []string{"de", "en", "fr"} }

// fixture is a set of fakes plus a record of what the checks asked for.
type fixture struct {
	deps       Deps
	install    slicer.Install
	detectErr  error
	detects    int
	gotCmd     string
	gotRoots   profiles.Roots
	store      *fakeProfiles
	openErr    error
	texts      Texts
	textsErr   error
	textsDir   string
	catVersion string
	dirEntries int
	dirErr     error
	settings   domain.Settings
}

func newFixture() *fixture {
	f := &fixture{
		install: slicer.Install{
			Found: true, Supported: true, Version: "7.2.2", Build: "5483", Dialect: "v72",
			Dir: filepath.Join("apps", "Creality Print 7.2"), Exe: filepath.Join("apps", "Creality Print 7.2", "CrealityPrint.exe"),
			DataDir: filepath.Join("data", "7.0"), ProfileVersion: "26.08.29.19", ProfileRoot: filepath.Join("apps", "Creality Print 7.2", "resources", "profiles"),
		},
		store: &fakeProfiles{
			keys:     []string{"enable_support", "filament_type", "layer_height", "wall_loops"},
			process:  make([]profiles.Descriptor, 8),
			filament: make([]profiles.Descriptor, 47),
			presets: map[string]profiles.Preset{
				"printer/" + k2Printer: {Values: map[string]any{
					"layer_height_placeholder": "x", "nozzle_diameter": []string{"0.4"}, "name": k2Printer,
					"default_print_profile": "0.20mm Standard", "default_filament_profile": "Hyper PLA",
				}},
				"process/0.20mm Standard": {Values: map[string]any{"layer_height": "0.2", "wall_loops": "2", "inherits": "x"}},
				"filament/Hyper PLA":      {Values: map[string]any{"filament_type": []string{"PLA"}, "filament_id": "01001"}},
			},
		},
		texts:      fakeTexts{all: true},
		dirEntries: 14,
	}
	// A key the catalog knows stands for the printer's own settings.
	delete(f.store.presets["printer/"+k2Printer].Values, "layer_height_placeholder")
	f.deps = Deps{
		Settings: func() (domain.Settings, error) { return f.settings, nil },
		Detect: func(_ context.Context, cmd string) (slicer.Install, error) {
			f.detects++
			f.gotCmd = cmd
			return f.install, f.detectErr
		},
		ReadDir: func(string) ([]os.DirEntry, error) {
			return make([]os.DirEntry, f.dirEntries), f.dirErr
		},
		OpenProfiles: func(r profiles.Roots) (Profiles, error) {
			f.gotRoots = r
			return f.store, f.openErr
		},
		LoadCatalog: func(version string) (*catalog.Catalog, error) {
			f.catVersion = version
			return catalog.Load(version)
		},
		LoadTexts: func(dir string) (Texts, error) {
			f.textsDir = dir
			return f.texts, f.textsErr
		},
	}
	return f
}

// run returns the result of the check with the given name from ChecksWith.
func (f *fixture) run(t *testing.T, name string) doctor.Result {
	t.Helper()
	for _, c := range ChecksWith(f.deps) {
		if c.Name() == name {
			return c.Run(context.Background())
		}
	}
	t.Fatalf("no check named %q", name)
	return doctor.Result{}
}

func want(t *testing.T, r doctor.Result, status doctor.Status, parts ...string) {
	t.Helper()
	if r.Status != status {
		t.Errorf("%s: status %s, want %s (%s)", r.Name, r.Status, status, r.Detail)
	}
	for _, p := range parts {
		if !strings.Contains(r.Detail, p) {
			t.Errorf("%s: detail lacks %q:\n%s", r.Name, p, r.Detail)
		}
	}
}

func TestChecksWithListsTheChecksInReportOrder(t *testing.T) {
	var names []string
	for _, c := range ChecksWith(newFixture().deps) {
		names = append(names, c.Name())
	}
	got := strings.Join(names, " | ")
	wantNames := "Settings | Data folder | Creality Print | Creality Print data | Profiles | Setting descriptions | Settings catalog"
	if got != wantNames {
		t.Errorf("checks = %s\nwant    %s", got, wantNames)
	}
}

func TestCrealityPrintFound(t *testing.T) {
	f := newFixture()
	r := f.run(t, "Creality Print")
	want(t, r, doctor.OK, "7.2.2", "build 5483", "dialect v72", "CrealityPrint.exe")
	if strings.Contains(r.Detail, domain.EnvCmd) {
		t.Errorf("an auto-detected install claims to come from %s", domain.EnvCmd)
	}
	f.install.GUIRunning = true
	want(t, f.run(t, "Creality Print"), doctor.OK, "window is open")
}

func TestCrealityPrintFromTheEnvironmentPassesTheCommand(t *testing.T) {
	f := newFixture()
	f.settings = domain.Settings{Cmd: filepath.Join("x", "CrealityPrint.exe")}
	want(t, f.run(t, "Creality Print"), doctor.OK, "(from "+domain.EnvCmd+")")
	if f.gotCmd != f.settings.Cmd {
		t.Errorf("Detect got cmd %q, want %q", f.gotCmd, f.settings.Cmd)
	}
	// A bad value is the Settings check's failure; here detection just runs
	// without it.
	f = newFixture()
	f.deps.Settings = func() (domain.Settings, error) { return domain.Settings{}, errors.New("bad") }
	want(t, f.run(t, "Creality Print"), doctor.OK)
	if f.gotCmd != "" {
		t.Errorf("a bad setting still reached Detect: %q", f.gotCmd)
	}
}

func TestCrealityPrintUnsupportedOrMissingWarnsSlicingDisabled(t *testing.T) {
	f := newFixture()
	f.install.Supported, f.install.Dialect, f.install.Version = false, "", "7.3.0"
	f.install.Reason = "version 7.3.0 is not supported yet; supported: 7.2.x"
	want(t, f.run(t, "Creality Print"), doctor.Warn, "7.3.0", "not supported yet", "slicing disabled")

	f = newFixture()
	f.install = slicer.Install{Reason: "Creality Print is not installed", ReasonCode: slicer.ReasonNotInstalled}
	want(t, f.run(t, "Creality Print"), doctor.Warn, "not installed", "slicing disabled", domain.EnvCmd)

	f = newFixture()
	f.install = slicer.Install{}
	want(t, f.run(t, "Creality Print"), doctor.Warn, "was not found", "slicing disabled")

	f = newFixture()
	f.detectErr = context.Canceled
	want(t, f.run(t, "Creality Print"), doctor.Warn, "interrupted")
}

func TestDetectionRunsOnceForAllChecks(t *testing.T) {
	f := newFixture()
	for _, c := range ChecksWith(f.deps) {
		c.Run(context.Background())
	}
	// Each ChecksWith call shares one detection; the loop above is one call.
	if f.detects != 1 {
		t.Errorf("Detect ran %d times, want once per doctor run", f.detects)
	}
}

func TestOtherChecksSayTheyWereNotCheckedWithoutAnInstall(t *testing.T) {
	f := newFixture()
	f.install = slicer.Install{}
	for _, name := range []string{"Creality Print data", "Profiles", "Setting descriptions", "Settings catalog"} {
		r := f.run(t, name)
		want(t, r, doctor.Warn, "not checked")
	}
	if f.gotRoots != (profiles.Roots{}) || f.textsDir != "" {
		t.Error("a check read the disk although no install was found")
	}
}

func TestAppDataFolder(t *testing.T) {
	f := newFixture()
	want(t, f.run(t, "Creality Print data"), doctor.OK, f.install.DataDir, "14 entries")

	f.install.DataDir = ""
	want(t, f.run(t, "Creality Print data"), doctor.Warn, "does not exist yet", "start Creality Print once")

	f = newFixture()
	f.dirErr = errors.New("access denied")
	want(t, f.run(t, "Creality Print data"), doctor.Warn, "cannot be read", "access denied")
}

func TestProfilesCountsTheK2Presets(t *testing.T) {
	f := newFixture()
	want(t, f.run(t, "Profiles"), doctor.OK, "8 process and 47 filament presets", k2Printer, "26.08.29.19")
	wantInstall := filepath.Join(f.install.Dir, "resources", "profiles")
	if f.gotRoots.InstallProfiles != wantInstall || f.gotRoots.DataDir != f.install.DataDir || f.gotRoots.Bundle != f.install.ProfileRoot {
		t.Errorf("roots = %+v", f.gotRoots)
	}

	f.openErr = errors.New("no Creality.json")
	want(t, f.run(t, "Profiles"), doctor.Warn, "cannot be read", "no Creality.json")

	f = newFixture()
	f.store.process = nil
	want(t, f.run(t, "Profiles"), doctor.Warn, "0 process", "expected some of each")

	f = newFixture()
	f.store.listErr = errors.New("boom")
	want(t, f.run(t, "Profiles"), doctor.Warn, "listing presets failed", "boom")
}

func TestSettingDescriptionCoverage(t *testing.T) {
	f := newFixture()
	r := f.run(t, "Setting descriptions")
	want(t, r, doctor.OK, "settings have a description", "100%", "3 languages")
	if f.textsDir != filepath.Join(f.install.Dir, "resources", "i18n") {
		t.Errorf("texts read from %q", f.textsDir)
	}

	f.texts = fakeTexts{all: false}
	want(t, f.run(t, "Setting descriptions"), doctor.Warn, "0 of", "no description")

	f = newFixture()
	f.textsErr = errors.New("no catalogs")
	want(t, f.run(t, "Setting descriptions"), doctor.Warn, "no message catalogs", "no catalogs", "no description")

	f = newFixture()
	f.deps.LoadCatalog = func(string) (*catalog.Catalog, error) { return nil, errors.New("corrupt") }
	want(t, f.run(t, "Setting descriptions"), doctor.Fail, "corrupt")
}

func TestCatalogDrift(t *testing.T) {
	f := newFixture()
	want(t, f.run(t, "Settings catalog"), doctor.OK, "catalog "+catalogVersion, "knows all 4 settings")
	if f.store.modelAsked != k2Model {
		t.Errorf("KeysSetBy asked for %q, want %q", f.store.modelAsked, k2Model)
	}

	f.store.keys = append(f.store.keys, "new_printer_key", "new_filament_key", "new_process_key")
	r := f.run(t, "Settings catalog")
	want(t, r, doctor.Warn, "3 setting(s)", "new_filament_key, new_printer_key, new_process_key", "7.2.2", "passed through untouched")
	if strings.Contains(r.Detail, "more") {
		t.Errorf("three keys were abbreviated: %s", r.Detail)
	}
}

func TestCatalogDriftListsAtMostTenKeys(t *testing.T) {
	f := newFixture()
	for i := 0; i < 12; i++ {
		f.store.keys = append(f.store.keys, fmt.Sprintf("zz_new_key_%02d", i))
	}
	r := f.run(t, "Settings catalog")
	want(t, r, doctor.Warn, "12 setting(s)", "zz_new_key_09", "and 2 more")
	if strings.Contains(r.Detail, "zz_new_key_10") {
		t.Errorf("more than ten keys listed: %s", r.Detail)
	}
}

func TestCatalogDriftNeedsPresets(t *testing.T) {
	f := newFixture()
	f.store.keys = nil
	want(t, f.run(t, "Settings catalog"), doctor.Warn, "not checked", "no Creality K2 presets")
	f = newFixture()
	f.store.keysErr = errors.New("cannot list")
	want(t, f.run(t, "Settings catalog"), doctor.Warn, "not checked", "cannot list")
	f = newFixture()
	f.openErr = errors.New("unreadable")
	want(t, f.run(t, "Settings catalog"), doctor.Warn, "not checked", "unreadable")
}

func TestReportShapeAndExitCode(t *testing.T) {
	f := newFixture()
	f.install.Supported, f.install.Dialect = false, ""
	f.install.Reason = "version 7.3.0 is not supported yet; supported: 7.2.x"
	dir := t.TempDir()
	checks := ChecksWith(f.deps)
	// Point the data folder check at a temporary folder.
	for i, c := range checks {
		if _, ok := c.(DataDirCheck); ok {
			checks[i] = DataDirCheck{Dir: dir}
		}
	}
	var out bytes.Buffer
	code := doctor.New(checks...).Run(context.Background(), &out)
	if code != 0 {
		t.Errorf("exit code %d: warnings must not fail doctor:\n%s", code, out.String())
	}
	for _, line := range []string{"[ok]   Settings", "[ok]   Data folder", "[warn] Creality Print", "slicing disabled"} {
		if !strings.Contains(out.String(), line) {
			t.Errorf("report lacks %q:\n%s", line, out.String())
		}
	}
}

// The catalog is the one of the detected install version, not a fixed one.
func TestCatalogFollowsTheInstalledVersion(t *testing.T) {
	f := newFixture()
	f.install.Version = "7.3.0"
	f.run(t, "Settings catalog")
	if f.catVersion != "7.3.0" {
		t.Errorf("catalog requested for version %q, want 7.3.0", f.catVersion)
	}
}
