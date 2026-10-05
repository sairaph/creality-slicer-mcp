package projects

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func zipHas(t *testing.T, path, member string) bool {
	t.Helper()
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.Name == member {
			return true
		}
	}
	return false
}

func spooledProject(t *testing.T, e *testEnv, name string) *Info {
	t.Helper()
	info, err := e.st.CreateProject(CreateRequest{Name: name, Printer: testPrinter, Process: testProcess, Spools: []SpoolSpec{
		{Slot: "T2C", CatalogID: "P001", Material: "PLA", Colour: "#FFFFFF", Status: "defined"},
		{Slot: "T1A", CatalogID: "P002", Material: "PETG", Colour: "#000000", Status: "defined"},
		{Slot: "T3B", CatalogID: "99999", Material: "PLA", Colour: "#00FF00", Status: "defined"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	e.addBox(t, info.ID, "a", 20, 20, 10)
	return info
}

func slotsOf(info *Info) []string {
	var out []string
	for _, f := range info.Filaments {
		if f.Spool != nil {
			out = append(out, f.Spool.Slot)
		} else {
			out = append(out, "-")
		}
	}
	return out
}

func TestExportOpenKeepsTheSpoolSlots(t *testing.T) {
	e := newEnv(t)
	info := spooledProject(t, e, "Round")
	file := filepath.Join(t.TempDir(), "round.3mf")
	if _, err := e.st.Export(info.ID, file, false); err != nil {
		t.Fatal(err)
	}
	if !zipHas(t, file, spoolMember) {
		t.Fatal("the export carries no spool member")
	}
	if zipHas(t, filepath.Join(e.st.dir(info.ID), projectFile), spoolMember) {
		t.Fatal("the store's own file was changed")
	}
	res, err := e.st.OpenProject(OpenRequest{Path: file, Name: "Reopened"})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(slotsOf(res.Info), ","); got != "T2C,T1A,T3B" || res.SpoolsFrom != "the file" {
		t.Fatalf("slots %s from %q", got, res.SpoolsFrom)
	}
	// The slice handoff of the reopened project has its slots.
	data, err := os.ReadFile("../gcodeinfo/testdata/cubes_2filaments_73.gcode")
	if err != nil {
		t.Fatal(err)
	}
	e.exec.gcode = func(int) string { return string(data) }
	out, err := e.st.Slice(res.Info.ID, SliceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	tools := out.Last.Plates[0].Tools
	if len(tools) != 2 || tools[0].SpoolSlot != "T2C" || tools[1].SpoolSlot != "T1A" {
		t.Fatalf("tools %+v", tools)
	}
}

// The app drops the member when it saves: the export record finds the links.
func TestResavedFileIsRestoredThroughExports(t *testing.T) {
	e := newEnv(t)
	info := spooledProject(t, e, "Resaved")
	file := filepath.Join(t.TempDir(), "resaved.3mf")
	if _, err := e.st.Export(info.ID, file, false); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(file)
	stripped, err := withSpoolMember(data, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, stripped, 0o644); err != nil {
		t.Fatal(err)
	}
	if zipHas(t, file, spoolMember) {
		t.Fatal("the member is still there")
	}
	// A new store on the same folder (a restart) still knows the export.
	st2, err := New(e.st.cfg)
	if err != nil {
		t.Fatal(err)
	}
	res, err := st2.OpenProject(OpenRequest{Path: strings.ToUpper(file[:1]) + file[1:]})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(slotsOf(res.Info), ","); got != "T2C,T1A,T3B" || res.SpoolsFrom != "project "+info.ID+", which exported it" {
		t.Fatalf("slots %s from %q", got, res.SpoolsFrom)
	}
	// An unrelated file gets no slots.
	other := filepath.Join(t.TempDir(), "other.3mf")
	if err := os.WriteFile(other, stripped, 0o644); err != nil {
		t.Fatal(err)
	}
	res, err = e.st.OpenProject(OpenRequest{Path: other})
	if err != nil || res.SpoolsFrom != "" || strings.Join(slotsOf(res.Info), ",") != "-,-,-" {
		t.Fatalf("other file: %v %+v", err, slotsOf(res.Info))
	}
	// The export is recorded once and without a new revision.
	m, _ := e.st.readMeta(info.ID)
	before, _ := e.st.GetProject(info.ID)
	if _, err := e.st.Export(info.ID, file, true); err != nil {
		t.Fatal(err)
	}
	m, _ = e.st.readMeta(info.ID)
	after, _ := e.st.GetProject(info.ID)
	if len(m.Exports) != 1 || after.Revision != before.Revision {
		t.Fatalf("exports %v, revision %d -> %d", m.Exports, before.Revision, after.Revision)
	}
}

// A preset changed at position k drops the links from k on.
func TestSpoolLinksStopAtAChangedPreset(t *testing.T) {
	e := newEnv(t)
	info := spooledProject(t, e, "Changed")
	file := filepath.Join(t.TempDir(), "changed.3mf")
	if _, err := e.st.Export(info.ID, file, false); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(file)
	links := []SpoolLink{{Slot: "T2C", Preset: testPLA, Match: MatchExact}, {Slot: "T1A", Preset: "Some other preset", Match: MatchExact}, {Slot: "T3B", Preset: testGenericPLA, Match: MatchGeneric}}
	tampered, err := withSpoolMember(data, links)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, tampered, 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := e.st.OpenProject(OpenRequest{Path: file})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(slotsOf(res.Info), ","); got != "T2C,-,-" {
		t.Fatalf("slots %s", got)
	}
}

// An export of a project without spools never carries a stale member.
func TestExportStripsAStaleSpoolMember(t *testing.T) {
	e := newEnv(t)
	info := spooledProject(t, e, "Stale")
	first := filepath.Join(t.TempDir(), "first.3mf")
	if _, err := e.st.Export(info.ID, first, false); err != nil {
		t.Fatal(err)
	}
	// Opening that file puts the member into the store copy of the new project.
	res, err := e.st.OpenProject(OpenRequest{Path: first, Name: "Copy"})
	if err != nil {
		t.Fatal(err)
	}
	if !zipHas(t, filepath.Join(e.st.dir(res.Info.ID), projectFile), spoolMember) {
		t.Skip("the store copy keeps no member, nothing to strip")
	}
	// Filaments by hand end the links; the next export has no member.
	if _, err := e.st.SetPresets(res.Info.ID, PresetsRequest{Filaments: []FilamentSpec{{Preset: testPLA, Colour: "#EEEEEE"}, {Preset: testPETG, Colour: "#111111"}, {Preset: testGenericPLA, Colour: "#222222"}}}); err != nil {
		t.Fatal(err)
	}
	second := filepath.Join(t.TempDir(), "second.3mf")
	if _, err := e.st.Export(res.Info.ID, second, false); err != nil {
		t.Fatal(err)
	}
	if zipHas(t, second, spoolMember) {
		t.Fatal("a stale spool member was exported")
	}
}

func TestWithSpoolMemberKeepsTheOtherEntries(t *testing.T) {
	e := newEnv(t)
	info := spooledProject(t, e, "Entries")
	data, err := os.ReadFile(filepath.Join(e.st.dir(info.ID), projectFile))
	if err != nil {
		t.Fatal(err)
	}
	out, err := withSpoolMember(data, []SpoolLink{{Slot: "T1A", Preset: "p"}})
	if err != nil {
		t.Fatal(err)
	}
	names := func(b []byte) []string {
		zr, err := zip.NewReader(strings.NewReader(string(b)), int64(len(b)))
		if err != nil {
			t.Fatal(err)
		}
		var n []string
		for _, f := range zr.File {
			n = append(n, f.Name)
		}
		return n
	}
	a, b := names(data), names(out)
	if len(b) != len(a)+1 || b[len(b)-1] != spoolMember || strings.Join(a, "|") != strings.Join(b[:len(a)], "|") {
		t.Fatalf("entries %v then %v", a, b)
	}
}

// openInto: a target without links takes the ones the file carries.
func TestOpenIntoTakesTheEmbeddedSpools(t *testing.T) {
	e := newEnv(t)
	src := spooledProject(t, e, "Source")
	file := filepath.Join(t.TempDir(), "src.3mf")
	if _, err := e.st.Export(src.ID, file, false); err != nil {
		t.Fatal(err)
	}
	target := e.newProject(t, "Target", FilamentSpec{Preset: testPLA, Colour: "#FFFFFF"}, FilamentSpec{Preset: testPETG, Colour: "#000000"}, FilamentSpec{Preset: testGenericPLA, Colour: "#00FF00"})
	res, err := e.st.OpenProject(OpenRequest{Path: file, Into: target.ID})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(slotsOf(res.Info), ","); got != "T2C,T1A,T3B" || res.SpoolsFrom != "the file" {
		t.Fatalf("slots %s from %q", got, res.SpoolsFrom)
	}
}

// A file opened through a link to its folder is still the exported one.
func TestExportedPathMatchesThroughALink(t *testing.T) {
	e := newEnv(t)
	info := spooledProject(t, e, "Linked")
	real := filepath.Join(t.TempDir(), "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("cannot create a symbolic link here: %v", err)
	}
	file := filepath.Join(real, "x.3mf")
	if _, err := e.st.Export(info.ID, file, false); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(file)
	stripped, err := withSpoolMember(data, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, stripped, 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := e.st.OpenProject(OpenRequest{Path: filepath.Join(link, "x.3mf")})
	if err != nil {
		t.Fatal(err)
	}
	if res.SpoolsFrom == "" || strings.Join(slotsOf(res.Info), ",") != "T2C,T1A,T3B" {
		t.Fatalf("slots %v from %q", slotsOf(res.Info), res.SpoolsFrom)
	}
}
