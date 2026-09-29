package slicer

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// fakeMachine is an in-memory Platform.
type fakeMachine struct {
	root     string
	entries  []UninstallEntry
	files    map[string]string // key(path) -> content
	dirs     map[string]bool
	versions map[string]string // key(exe) -> file version
	pids     []int
	env      map[string]string

	registryCalls atomic.Int32
}

func key(p string) string { return strings.ToLower(filepath.Clean(p)) }

func newFakeMachine() *fakeMachine {
	root := filepath.VolumeName(os.TempDir()) + string(filepath.Separator) + "fakemachine"
	return &fakeMachine{
		root:     root,
		files:    map[string]string{},
		dirs:     map[string]bool{},
		versions: map[string]string{},
		env: map[string]string{
			"APPDATA":      filepath.Join(root, "AppData", "Roaming"),
			"ProgramFiles": filepath.Join(root, "Program Files"),
		},
	}
}

func (m *fakeMachine) abs(parts ...string) string {
	return filepath.Join(append([]string{m.root}, parts...)...)
}

func (m *fakeMachine) addFile(path, content string) {
	m.files[key(path)] = content
	for d := filepath.Dir(path); d != filepath.Dir(d); d = filepath.Dir(d) {
		m.dirs[key(d)] = true
	}
}

// install adds an installation directory with its exe, file version and
// registry entry, returning the directory.
func (m *fakeMachine) install(name, displayVersion, fileVersion string, withRegistry bool) string {
	dir := m.abs("Program Files", "Creality", name)
	exe := filepath.Join(dir, "CrealityPrint.exe")
	m.addFile(exe, "exe")
	if fileVersion != "" {
		m.versions[key(exe)] = fileVersion
	}
	if withRegistry {
		m.entries = append(m.entries, UninstallEntry{
			Key:             strings.ReplaceAll(name, " ", "") + "-x",
			DisplayName:     name,
			DisplayVersion:  displayVersion,
			UninstallString: `"` + filepath.Join(dir, "Uninstall.exe") + `"`,
		})
	}
	return dir
}

func (m *fakeMachine) platform() *Platform {
	return &Platform{
		Supported:   true,
		Registry:    fakeRegistry{m},
		FileVersion: fakeVersions{m},
		Processes:   fakeProcs{m},
		FS:          fakeFS{m},
		Getenv:      func(k string) string { return m.env[k] },
	}
}

type fakeRegistry struct{ m *fakeMachine }

func (r fakeRegistry) UninstallEntries() ([]UninstallEntry, error) {
	r.m.registryCalls.Add(1)
	return r.m.entries, nil
}

type fakeVersions struct{ m *fakeMachine }

func (v fakeVersions) FileVersion(path string) (string, error) {
	if s, ok := v.m.versions[key(path)]; ok {
		return s, nil
	}
	return "", errors.New("no version resource")
}

type fakeProcs struct{ m *fakeMachine }

func (p fakeProcs) PIDsByName(name string) ([]int, error) {
	if name != GUIProcessName {
		return nil, nil
	}
	return append([]int(nil), p.m.pids...), nil
}

type fakeFS struct{ m *fakeMachine }

func (f fakeFS) IsFile(p string) bool { _, ok := f.m.files[key(p)]; return ok }
func (f fakeFS) IsDir(p string) bool  { return f.m.dirs[key(p)] }
func (f fakeFS) ReadFile(p string) ([]byte, error) {
	if s, ok := f.m.files[key(p)]; ok {
		return []byte(s), nil
	}
	return nil, os.ErrNotExist
}
func (f fakeFS) Glob(pattern string) []string {
	var out []string
	for d := range f.m.dirs {
		if ok, _ := filepath.Match(strings.ToLower(filepath.Clean(pattern)), d); ok {
			out = append(out, d)
		}
	}
	return out
}

// realLike sets up the machine the code owner has: key CrealityPrint-7,
// DisplayName "Creality Print 7.2", DisplayVersion 7.2.2, no InstallLocation.
func realLike(t *testing.T) (*fakeMachine, string) {
	t.Helper()
	m := newFakeMachine()
	dir := m.install("Creality Print 7.2", "7.2.2", "7.2.2.5483", true)
	m.entries[0].Key = "CrealityPrint-7"
	m.addFile(filepath.Join(dir, "resources", "profiles", "Creality.json"), `{"name":"Creality","version":"26.08.29.19"}`)
	data := filepath.Join(m.env["APPDATA"], "Creality", "Creality Print", "7.0")
	m.addFile(filepath.Join(data, "Creality.conf"), "{}")
	m.addFile(filepath.Join(data, "system", "Creality.json"), `{"name":"Creality","version":"26.07.18.17"}`)
	m.pids = []int{4242, 17}
	return m, dir
}

func TestDetectRealLike(t *testing.T) {
	m, dir := realLike(t)
	in, err := Detect(context.Background(), Options{Platform: m.platform()})
	if err != nil {
		t.Fatal(err)
	}
	if !in.Found || !in.Supported || in.Dialect != "v72" || in.Reason != "" || in.ReasonCode != "" {
		t.Fatalf("%+v", in)
	}
	if in.Version != "7.2.2" || in.Build != "5483" {
		t.Errorf("version %q build %q", in.Version, in.Build)
	}
	if in.Dir != dir || in.Exe != filepath.Join(dir, "CrealityPrint.exe") {
		t.Errorf("dir %q exe %q", in.Dir, in.Exe)
	}
	wantData := filepath.Join(m.env["APPDATA"], "Creality", "Creality Print", "7.0")
	if in.DataDir != wantData {
		t.Errorf("data dir %q", in.DataDir)
	}
	if in.ProfileVersionInstall != "26.08.29.19" || in.ProfileVersionData != "26.07.18.17" || in.ProfileVersion != "26.08.29.19" {
		t.Errorf("profile versions %q %q effective %q", in.ProfileVersionInstall, in.ProfileVersionData, in.ProfileVersion)
	}
	if in.ProfileRoot != filepath.Join(dir, "resources", "profiles") {
		t.Errorf("profile root %q", in.ProfileRoot)
	}
	if !in.GUIRunning || len(in.GUIPIDs) != 2 || in.GUIPIDs[0] != 17 {
		t.Errorf("gui %v %v", in.GUIRunning, in.GUIPIDs)
	}
	if len(in.Others) != 0 {
		t.Errorf("others %v", in.Others)
	}
}

func TestDetectDataProfilesNewer(t *testing.T) {
	m, dir := realLike(t)
	data := filepath.Join(m.env["APPDATA"], "Creality", "Creality Print", "7.0")
	m.addFile(filepath.Join(data, "system", "Creality.json"), `{"version":"26.09.01.1"}`)
	in, _ := Detect(context.Background(), Options{Platform: m.platform()})
	if in.ProfileVersion != "26.09.01.1" || in.ProfileRoot != filepath.Join(data, "system") {
		t.Errorf("%q %q", in.ProfileVersion, in.ProfileRoot)
	}
	_ = dir
}

func TestDetectMissingDataDirAndNoGUI(t *testing.T) {
	m := newFakeMachine()
	dir := m.install("Creality Print 7.2", "7.2.2", "7.2.2.5483", true)
	m.addFile(filepath.Join(dir, "resources", "profiles", "Creality.json"), `{"version":"26.08.29.19"}`)
	in, _ := Detect(context.Background(), Options{Platform: m.platform()})
	if !in.Found || in.DataDir != "" || in.ProfileVersionData != "" || in.ProfileVersion != "26.08.29.19" || in.GUIRunning || in.GUIPIDs != nil {
		t.Errorf("%+v", in)
	}
}

func TestDetectExeVersionBeatsRegistry(t *testing.T) {
	m := newFakeMachine()
	m.install("Creality Print 7.2", "7.1.0", "7.2.2.5483", true)
	in, _ := Detect(context.Background(), Options{Platform: m.platform()})
	if in.Version != "7.2.2" || in.Build != "5483" {
		t.Errorf("%q %q", in.Version, in.Build)
	}
}

func TestDetectFallsBackToRegistryVersionWithoutFileVersion(t *testing.T) {
	m := newFakeMachine()
	m.install("Creality Print 7.2", "7.2.2", "", true)
	in, _ := Detect(context.Background(), Options{Platform: m.platform()})
	if in.Version != "7.2.2" || in.Build != "" || !in.Supported {
		t.Errorf("%+v", in)
	}
}

func TestDetectInstallLocationAndUninstallStringForms(t *testing.T) {
	m := newFakeMachine()
	dir := m.abs("Elsewhere", "CP")
	m.addFile(filepath.Join(dir, "CrealityPrint.exe"), "exe")
	m.versions[key(filepath.Join(dir, "CrealityPrint.exe"))] = "7.2.1.100"
	m.entries = []UninstallEntry{
		{DisplayName: "Other Product", InstallLocation: m.abs("Nope")},
		{DisplayName: "creality print 7.2", InstallLocation: `"` + dir + `"`, UninstallString: "garbage"},
	}
	in, _ := Detect(context.Background(), Options{Platform: m.platform()})
	if !in.Found || in.Dir != dir {
		t.Fatalf("%+v", in)
	}
	// Unquoted uninstaller with arguments.
	m2 := newFakeMachine()
	dir2 := m2.abs("Elsewhere", "CP2")
	m2.addFile(filepath.Join(dir2, "CrealityPrint.exe"), "exe")
	m2.entries = []UninstallEntry{{DisplayName: "Creality Print 7.2", DisplayVersion: "7.2.0", UninstallString: filepath.Join(dir2, "unins000.exe") + " /SILENT"}}
	in2, _ := Detect(context.Background(), Options{Platform: m2.platform()})
	if !in2.Found || in2.Dir != dir2 {
		t.Fatalf("%+v", in2)
	}
}

func TestDetectRegistryEntryWithoutExeIsIgnored(t *testing.T) {
	m := newFakeMachine()
	m.entries = []UninstallEntry{{DisplayName: "Creality Print 7.2", DisplayVersion: "7.2.2", UninstallString: `"` + m.abs("gone", "Uninstall.exe") + `"`}}
	in, _ := Detect(context.Background(), Options{Platform: m.platform()})
	if in.Found || in.ReasonCode != ReasonNotInstalled || in.Reason == "" {
		t.Errorf("%+v", in)
	}
}

func TestDetectFallbackProgramFiles(t *testing.T) {
	m := newFakeMachine()
	m.install("Creality Print 7.2", "", "7.2.2.5483", false)
	in, _ := Detect(context.Background(), Options{Platform: m.platform()})
	if !in.Found || in.Version != "7.2.2" || !in.Supported {
		t.Errorf("%+v", in)
	}
}

func TestDetectNotInstalledAndUnsupportedPlatform(t *testing.T) {
	m := newFakeMachine()
	in, err := Detect(context.Background(), Options{Platform: m.platform()})
	if err != nil || in.Found || in.ReasonCode != ReasonNotInstalled || in.Reason == "" {
		t.Errorf("%+v %v", in, err)
	}
	p := m.platform()
	p.Supported = false
	in, err = Detect(context.Background(), Options{Platform: p})
	if err != nil || in.Found || in.ReasonCode != ReasonUnsupportedPlatform || in.Reason == "" {
		t.Errorf("%+v %v", in, err)
	}
}

func TestDetectCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Detect(ctx, Options{Platform: newFakeMachine().platform()}); !errors.Is(err, context.Canceled) {
		t.Errorf("%v", err)
	}
}

func TestDetectCmdOverride(t *testing.T) {
	m, dir := realLike(t)
	other := m.install("Creality Print 7.3", "7.3.0", "7.3.0.9", true)
	// The override wins over a newer registry install; the registry entry is
	// matched by directory only to supply DisplayVersion.
	in, _ := Detect(context.Background(), Options{Cmd: filepath.Join(dir, "CrealityPrint.exe"), Platform: m.platform()})
	if !in.Found || in.Dir != dir || in.Version != "7.2.2" || in.Build != "5483" || len(in.Others) != 0 {
		t.Fatalf("%+v (other %s)", in, other)
	}
	// An exe outside every registry entry still works; version from the file.
	odd := m.abs("Portable", "CrealityPrint.exe")
	m.addFile(odd, "exe")
	m.versions[key(odd)] = "7.2.5.77"
	in, _ = Detect(context.Background(), Options{Cmd: odd, Platform: m.platform()})
	if !in.Found || in.Dir != filepath.Dir(odd) || in.Version != "7.2.5" || in.Build != "77" || !in.Supported {
		t.Fatalf("%+v", in)
	}
	// Without a file version the matching registry entry supplies it.
	m2 := newFakeMachine()
	d2 := m2.install("Creality Print 7.2", "7.2.2", "", true)
	in, _ = Detect(context.Background(), Options{Cmd: filepath.Join(d2, "CrealityPrint.exe"), Platform: m2.platform()})
	if in.Version != "7.2.2" {
		t.Errorf("%+v", in)
	}
}

func TestDetectCmdOverrideInvalid(t *testing.T) {
	m, dir := realLike(t)
	for name, cmd := range map[string]string{
		"relative": "CrealityPrint.exe",
		"missing":  filepath.Join(dir, "missing.exe"),
		"a dir":    dir,
	} {
		in, err := Detect(context.Background(), Options{Cmd: cmd, Platform: m.platform()})
		if err != nil || in.Found || in.Reason == "" || in.ReasonCode != ReasonNotInstalled {
			t.Errorf("%s: %+v %v", name, in, err)
		}
	}
}

func TestDetectorCachesUntilRefresh(t *testing.T) {
	m, _ := realLike(t)
	d := NewDetector(Options{Platform: m.platform()})
	a, err := d.Get(context.Background())
	if err != nil || !a.Found {
		t.Fatal(a, err)
	}
	m.pids = nil
	b, _ := d.Get(context.Background())
	if !b.GUIRunning || m.registryCalls.Load() != 1 {
		t.Errorf("cached result changed or detection repeated: %+v calls %d", b, m.registryCalls.Load())
	}
	c, _ := d.Refresh(context.Background())
	if c.GUIRunning || m.registryCalls.Load() != 2 {
		t.Errorf("refresh: %+v calls %d", c, m.registryCalls.Load())
	}
	e, _ := d.Get(context.Background())
	if e.GUIRunning {
		t.Error("Get after Refresh must return the refreshed result")
	}
}

func TestCompareVersions(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"26.08.29.19", "26.07.18.17", 1},
		{"26.7.18.17", "26.07.18.17", 0},
		{"7.2", "7.2.0", 0},
		{"7.10", "7.9", 1},
		{"", "1", -1},
		{"7.2.2", "7.2.10", -1},
	} {
		if got := compareVersions(c.a, c.b); got != c.want {
			t.Errorf("compareVersions(%q,%q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestUninstallExe(t *testing.T) {
	for in, want := range map[string]string{
		`"C:\A B\Uninstall.exe"`:       `C:\A B\Uninstall.exe`,
		`"C:\A B\Uninstall.exe" /S /x`: `C:\A B\Uninstall.exe`,
		`C:\AB\unins.exe /S`:           `C:\AB\unins.exe`,
		``:                             ``,
		`MsiExec.exe /X{1234}`:         `MsiExec.exe`,
	} {
		if got := uninstallExe(in); got != want {
			t.Errorf("uninstallExe(%q) = %q, want %q", in, got, want)
		}
	}
}

// On the installed 7.2.2 the exe's FileVersion string is only the build
// number; the version comes from the registry or the folder name.
func TestDetectBareBuildFileVersion(t *testing.T) {
	m := newFakeMachine()
	m.install("Creality Print 7.2", "7.2.2", "5483", true)
	in, _ := Detect(context.Background(), Options{Platform: m.platform()})
	if in.Version != "7.2.2" || in.Build != "5483" || !in.Supported {
		t.Errorf("%+v", in)
	}
	// Same exe, no registry entry: the folder name gives major.minor.
	m2 := newFakeMachine()
	m2.install("Creality Print 7.2", "", "5483", false)
	in, _ = Detect(context.Background(), Options{Platform: m2.platform()})
	if in.Version != "7.2" || in.Build != "5483" || !in.Supported || in.Dialect != "v72" {
		t.Errorf("%+v", in)
	}
}

func TestDetectUnknownVersion(t *testing.T) {
	m := newFakeMachine()
	dir := m.abs("Portable", "cp")
	m.addFile(filepath.Join(dir, "CrealityPrint.exe"), "exe")
	in, _ := Detect(context.Background(), Options{Cmd: filepath.Join(dir, "CrealityPrint.exe"), Platform: m.platform()})
	if !in.Found || in.Supported || in.Version != "" || !strings.Contains(in.Reason, "unknown") {
		t.Errorf("%+v", in)
	}
}

func (f fakeFS) Open(p string) (io.ReadCloser, error) {
	if s, ok := f.m.files[key(p)]; ok {
		return io.NopCloser(strings.NewReader(s)), nil
	}
	return nil, os.ErrNotExist
}

// The highest supported version wins; unsupported ones are only listed.
func TestDetectPicksHighestSupportedAndListsOthers(t *testing.T) {
	m, dir72 := realLike(t)
	old := m.install("Creality Print 6.0", "6.0.4", "6.0.4.1", true)
	newer := m.install("Creality Print 7.4", "7.4.0", "7.4.0.9", true)
	in, _ := Detect(context.Background(), Options{Platform: m.platform()})
	if in.Dir != dir72 || in.Version != "7.2.2" || !in.Supported || in.Dialect != "v72" {
		t.Fatalf("%+v", in)
	}
	found := map[string]bool{}
	for _, o := range in.Others {
		found[key(o)] = true
	}
	if len(in.Others) != 2 || !found[key(old)] || !found[key(newer)] {
		t.Errorf("others %v", in.Others)
	}
	// Nothing supported: the highest is reported with its reason.
	m2 := newFakeMachine()
	m2.install("Creality Print 6.0", "6.0.4", "6.0.4.1", true)
	n2 := m2.install("Creality Print 7.4", "7.4.0", "7.4.0.9", true)
	in, _ = Detect(context.Background(), Options{Platform: m2.platform()})
	if in.Dir != n2 || in.Supported || in.Dialect != "" || in.ReasonCode != ReasonUnsupportedVersion ||
		in.Reason != "version 7.4.0 is not supported yet; supported: 7.2.x and 7.3.x" {
		t.Fatalf("%+v", in)
	}
}

// 7.2 and 7.3 side by side: 7.3 wins, its data folder is the 7.3 one, 7.2 is listed.
func TestDetectSideBySide73(t *testing.T) {
	m, dir72 := realLike(t)
	dir73 := m.install("Creality Print 7.3", "7.3.0", "7.3.0.6149", true)
	m.addFile(filepath.Join(dir73, "resources", "profiles", "Creality.json"), `{"version":"26.09.29.08"}`)
	data72 := filepath.Join(m.env["APPDATA"], "Creality", "Creality Print", "7.0", "system", "Creality.json")
	m.addFile(data72, `{"version":"26.08.29.19"}`)
	in, _ := Detect(context.Background(), Options{Platform: m.platform()})
	if in.Dir != dir73 || in.Version != "7.3.0" || in.Build != "6149" || in.Dialect != "v73" || !in.Supported {
		t.Fatalf("%+v", in)
	}
	if len(in.Others) != 1 || key(in.Others[0]) != key(dir72) {
		t.Errorf("others %v", in.Others)
	}
	if in.DataDir != "" {
		t.Errorf("the 7.3 data folder does not exist, but DataDir = %q", in.DataDir)
	}
	// Once the app has run, its 7.3 folder is the data dir (7.2 keeps 7.0).
	data73 := filepath.Join(m.env["APPDATA"], "Creality", "Creality Print", "7.3", "system", "Creality.json")
	m.addFile(data73, `{"version":"26.10.01.01"}`)
	in, _ = Detect(context.Background(), Options{Platform: m.platform()})
	if filepath.Base(in.DataDir) != "7.3" || in.ProfileVersionData != "26.10.01.01" || in.ProfileRoot != filepath.Join(in.DataDir, "system") {
		t.Errorf("%+v", in)
	}
	in72, _ := Detect(context.Background(), Options{Cmd: filepath.Join(dir72, "CrealityPrint.exe"), Platform: m.platform()})
	if filepath.Base(in72.DataDir) != "7.0" || in72.Dialect != "v72" {
		t.Errorf("7.2: %+v", in72)
	}
}

// A portable build has no registry entry and a fixed file version of 7.0.0.0
// with the build as the FileVersion string: the version comes from the library.
func TestDetectPortableVersionFromLibrary(t *testing.T) {
	m := newFakeMachine()
	exe := m.abs("portable", "lib", "net45", "CrealityPrint.exe")
	m.addFile(exe, "exe")
	m.versions[key(exe)] = "6149"
	dll := filepath.Join(filepath.Dir(exe), "CrealityPrint_Slicer.dll")
	// The library holds other version strings, one of them near a block boundary.
	pad := strings.Repeat("x", 3<<20)
	m.addFile(dll, pad+"1.2.3.6149\x007.3.0.6149\x00"+pad)
	in, _ := Detect(context.Background(), Options{Cmd: exe, Platform: m.platform()})
	if in.Version != "7.3.0" || in.Build != "6149" || in.Dialect != "v73" || !in.Supported {
		t.Fatalf("%+v", in)
	}
	// The match straddling a 1 MiB block boundary is still found.
	m2 := newFakeMachine()
	exe2 := m2.abs("p", "CrealityPrint.exe")
	m2.addFile(exe2, "exe")
	m2.versions[key(exe2)] = "6149"
	m2.addFile(filepath.Join(filepath.Dir(exe2), "CrealityPrint_Slicer.dll"), strings.Repeat("y", 1<<20-5)+"7.3.0.6149"+strings.Repeat("y", 100))
	if in, _ := Detect(context.Background(), Options{Cmd: exe2, Platform: m2.platform()}); in.Version != "7.3.0" {
		t.Errorf("boundary: %+v", in)
	}
	// No library, no registry, no folder version: unknown, not supported, with a reason.
	m3 := newFakeMachine()
	exe3 := m3.abs("q", "CrealityPrint.exe")
	m3.addFile(exe3, "exe")
	m3.versions[key(exe3)] = "6149"
	in, _ = Detect(context.Background(), Options{Cmd: exe3, Platform: m3.platform()})
	if in.Supported || in.Version != "" || in.ReasonCode != ReasonUnsupportedVersion || !strings.Contains(in.Reason, "unknown") {
		t.Errorf("%+v", in)
	}
}
