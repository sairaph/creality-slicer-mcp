package projects

import (
	"github.com/sairaph/creality-slicer-mcp/internal/profiles"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func spoolTrio() []SpoolSpec {
	return []SpoolSpec{
		{Slot: "T1A", CatalogID: "P002", Material: "PETG", Colour: "#000000", Status: "defined", Name: "Black PETG"},
		{Slot: "T1B", CatalogID: "P001", Material: "PLA", Colour: "ffffff", Status: "rfid"},
		{Slot: "T1C", CatalogID: "99999", Material: "PLA", Status: "defined"},
	}
}

func TestSpoolsExactAndGenericMatch(t *testing.T) {
	e := newEnv(t)
	info, err := e.st.CreateProject(CreateRequest{Name: "Spools", Printer: testPrinter, Process: testProcess, Spools: spoolTrio()})
	if err != nil {
		t.Fatal(err)
	}
	want := []struct{ preset, colour, match, slot string }{
		{testPETG, "#000000", MatchExact, "T1A"},
		{testPLA, "#FFFFFF", MatchExact, "T1B"},
		{testGenericPLA, "#FFFFFF", MatchGeneric, "T1C"},
	}
	if len(info.Filaments) != 3 {
		t.Fatalf("filaments %+v", info.Filaments)
	}
	for i, w := range want {
		f := info.Filaments[i]
		if f.Preset != w.preset || !strings.EqualFold(f.Colour, w.colour) || f.Spool == nil || f.Spool.Match != w.match || f.Spool.Slot != w.slot {
			t.Errorf("filament %d: %+v spool %+v, want %+v", i+1, f, f.Spool, w)
		}
	}
	if !strings.Contains(info.Filaments[2].Spool.Note, "no colour") {
		t.Errorf("no note for the missing colour: %+v", info.Filaments[2].Spool)
	}
	// The links are in job.json: a fresh read shows them.
	got, err := e.st.GetProject(info.ID)
	if err != nil || got.Filaments[1].Spool == nil || got.Filaments[1].Spool.CatalogID != "P001" {
		t.Fatalf("get_project: %v %+v", err, got.Filaments)
	}
}

func TestSpoolsRefusals(t *testing.T) {
	e := newEnv(t)
	create := func(sp []SpoolSpec, fil []FilamentSpec) error {
		_, err := e.st.CreateProject(CreateRequest{Name: "R", Printer: testPrinter, Process: testProcess, Spools: sp, Filaments: fil})
		return err
	}
	wantCode(t, create([]SpoolSpec{{Slot: "T1A", Material: "PLA", Status: "undefined"}}, nil), CodeInvalidInput)
	wantCode(t, create([]SpoolSpec{{Slot: "T1A", Material: "PLA", Status: "unknown"}}, nil), CodeInvalidInput)
	wantCode(t, create([]SpoolSpec{{Slot: "T1A", Material: "", Status: "defined"}}, nil), CodeInvalidInput)
	ae := wantCode(t, create([]SpoolSpec{{Slot: "T1A", CatalogID: "77777", Material: "ABS", Status: "defined"}}, nil), CodeInvalidInput)
	if !strings.Contains(ae.Message, "Generic ABS") {
		t.Errorf("message %q", ae.Message)
	}
	wantCode(t, create(spoolTrio(), []FilamentSpec{{Preset: testPLA, Colour: "#FFFFFF"}}), CodeInvalidInput)
}

func TestSpoolColourNormalisation(t *testing.T) {
	for in, want := range map[string]string{"#abc": "#AABBCC", "0f0f0f": "#0F0F0F", "#11223344": "#112233", " #FF0000 ": "#FF0000"} {
		if got, ok := normColour(in); !ok || got != want {
			t.Errorf("normColour(%q) = %q %v, want %q", in, got, ok, want)
		}
	}
	for _, bad := range []string{"", "red", "#12"} {
		if _, ok := normColour(bad); ok {
			t.Errorf("normColour(%q) accepted", bad)
		}
	}
}

func TestSetPresetsWithSpools(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "SP")
	res, err := e.st.SetPresets(info.ID, PresetsRequest{Spools: spoolTrio()[:2]})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Info.Filaments) != 2 || res.Info.Filaments[0].Preset != testPETG || res.Info.Filaments[0].Spool == nil {
		t.Fatalf("filaments %+v", res.Info.Filaments)
	}
	// The same spools again change nothing.
	_, err = e.st.SetPresets(info.ID, PresetsRequest{Spools: spoolTrio()[:2]})
	wantCode(t, err, CodeInvalidInput)
	// Both lists at once are refused.
	_, err = e.st.SetPresets(info.ID, PresetsRequest{Spools: spoolTrio(), Filaments: []FilamentSpec{{Preset: testPLA, Colour: "#FFFFFF"}}})
	wantCode(t, err, CodeInvalidInput)
	// Filaments given by hand no longer come from spools.
	res, err = e.st.SetPresets(info.ID, PresetsRequest{Filaments: []FilamentSpec{{Preset: testPLA, Colour: "#00FF00"}, {Preset: testPETG, Colour: "#FF0000"}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range res.Info.Filaments {
		if f.Spool != nil {
			t.Fatalf("stale spool link: %+v", f)
		}
	}
}

func TestSliceHandoffCarriesTheSpoolSlots(t *testing.T) {
	data, err := os.ReadFile("../gcodeinfo/testdata/cubes_2filaments_73.gcode")
	if err != nil {
		t.Fatal(err)
	}
	e := newEnv(t)
	info, err := e.st.CreateProject(CreateRequest{Name: "Slots", Printer: testPrinter, Process: testProcess, Spools: []SpoolSpec{
		{Slot: "T2C", CatalogID: "P001", Material: "PLA", Colour: "#FFFFFF", Status: "defined"},
		{Slot: "T1A", CatalogID: "P002", Material: "PETG", Colour: "#000000", Status: "defined"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	e.addBox(t, info.ID, "a", 20, 20, 10)
	if _, err := e.st.AddModel(info.ID, AddModelRequest{Path: writeSTL(t, "b", 20, 20, 10), Filament: 2}); err != nil {
		t.Fatal(err)
	}
	e.exec.gcode = func(int) string { return string(data) }
	res, err := e.st.Slice(info.ID, SliceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	tools := res.Last.Plates[0].Tools
	if len(tools) != 2 || tools[0].SpoolSlot != "T2C" || tools[1].SpoolSlot != "T1A" {
		t.Fatalf("tools %+v", tools)
	}
	// After a preset change by hand the slot is no longer claimed.
	if _, err := e.st.SetPresets(info.ID, PresetsRequest{Filaments: []FilamentSpec{{Preset: testPLA, Colour: "#EEEEEE"}, {Preset: testPETG, Colour: "#111111"}}}); err != nil {
		t.Fatal(err)
	}
	if res, err = e.st.Slice(info.ID, SliceOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, tl := range res.Last.Plates[0].Tools {
		if tl.SpoolSlot != "" {
			t.Fatalf("slot without spools: %+v", tl)
		}
	}
}

func TestOpenProjectInto(t *testing.T) {
	e := newEnv(t)
	info, err := e.st.CreateProject(CreateRequest{Name: "Into", Printer: testPrinter, Process: testProcess, Spools: spoolTrio()[:2]})
	if err != nil {
		t.Fatal(err)
	}
	e.addBox(t, info.ID, "one", 20, 20, 10)
	saved := filepath.Join(t.TempDir(), "app-saved.3mf")
	if _, err := e.st.Export(info.ID, saved, false); err != nil {
		t.Fatal(err)
	}
	e.addBox(t, info.ID, "two", 20, 20, 10)
	if _, err := e.st.Slice(info.ID, SliceOptions{}); err != nil {
		t.Fatal(err)
	}
	before, _ := e.st.GetProject(info.ID)
	res, err := e.st.OpenProject(OpenRequest{Path: saved, Into: info.ID, Name: "ignored"})
	if err != nil {
		t.Fatal(err)
	}
	got := res.Info
	if got.ID != info.ID || got.Name != "Into" || got.Revision != before.Revision+1 || len(got.Objects) != 1 || got.LastSlice != nil {
		t.Fatalf("after into: id %s name %q rev %d (was %d) objects %d last %v", got.ID, got.Name, got.Revision, before.Revision, len(got.Objects), got.LastSlice)
	}
	if got.SourcePath != saved {
		t.Errorf("source %q", got.SourcePath)
	}
	// Same presets: the spool links stay.
	if got.Filaments[0].Spool == nil || got.Filaments[0].Spool.Slot != "T1A" {
		t.Errorf("spool link lost: %+v", got.Filaments)
	}
	if list, _ := e.st.List(); len(list) != 1 {
		t.Errorf("projects after into: %d", len(list))
	}
	if _, err := os.Stat(filepath.Join(e.st.dir(info.ID), projectFile+".old")); err == nil {
		t.Error("the aside copy of the old project file was left behind")
	}
	_, err = e.st.OpenProject(OpenRequest{Path: saved, Into: "nope-123456"})
	wantCode(t, err, CodeNotFound)
	// A missing target leaves no half made folder behind.
	entries, _ := os.ReadDir(e.st.cfg.Root)
	for _, en := range entries {
		if strings.HasPrefix(en.Name(), "opening-") {
			t.Errorf("leftover %s", en.Name())
		}
	}
}

func TestPrepareView(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "View")
	e.addBox(t, info.ID, "a", 20, 20, 10)
	_, err := e.st.PrepareView(info.ID, 1, "preview")
	wantCode(t, err, CodeConflict)
	_, err = e.st.PrepareView(info.ID, 9, "project")
	wantCode(t, err, CodeInvalidInput)
	_, err = e.st.PrepareView(info.ID, 1, "video")
	wantCode(t, err, CodeInvalidInput)
	res, err := e.st.Slice(info.ID, SliceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	vf, err := e.st.PrepareView(info.ID, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if vf.Mode != ViewPreview || vf.Plate != 1 || filepath.Base(vf.Path) != "plate1_r"+strconv.Itoa(vf.Revision)+".gcode" || filepath.Base(filepath.Dir(vf.Path)) != "view" {
		t.Fatalf("view %+v", vf)
	}
	src, _ := os.ReadFile(res.Last.Plates[0].GCodePath)
	cp, err := os.ReadFile(vf.Path)
	if err != nil || string(cp) != string(src) || vf.Path == res.Last.Plates[0].GCodePath {
		t.Fatalf("the view is not a copy of the G-code: %v", err)
	}
	pv, err := e.st.PrepareView(info.ID, 1, ViewProject)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(pv.Path, "_r"+strconv.Itoa(pv.Revision)+".3mf") {
		t.Fatalf("project view %+v", pv)
	}
	if _, err := os.Stat(vf.Path); err != nil {
		t.Errorf("a view of the same revision was removed: %v", err)
	}
	// A change makes the slice stale; the project view of the new revision
	// removes the older files.
	e.addBox(t, info.ID, "b", 20, 20, 10)
	_, err = e.st.PrepareView(info.ID, 1, ViewPreview)
	wantCode(t, err, CodeConflict)
	pv2, err := e.st.PrepareView(info.ID, 1, ViewProject)
	if err != nil || pv2.Revision <= pv.Revision {
		t.Fatalf("second project view: %v %+v", err, pv2)
	}
	for _, old := range []string{vf.Path, pv.Path} {
		if _, err := os.Stat(old); err == nil {
			t.Errorf("older view file kept: %s", old)
		}
	}
}

// Local check against the profiles of a real install: set REAL_PROFILES to its
// resources\profiles folder. Nothing is written there.
func TestSpoolsRealProfiles(t *testing.T) {
	dir := os.Getenv("REAL_PROFILES")
	if dir == "" {
		t.Skip("REAL_PROFILES is not set")
	}
	e := newEnv(t)
	ps, err := profilesOpen(dir)
	if err != nil {
		t.Fatal(err)
	}
	e.st.cfg.Profiles = ps
	info, err := e.st.CreateProject(CreateRequest{Name: "Real", Spools: []SpoolSpec{
		{Slot: "T1A", CatalogID: "06001", Material: "PETG", Colour: "#000000", Status: "defined"},
		{Slot: "T1B", CatalogID: "01001", Material: "PLA", Colour: "#FFFFFF", Status: "defined"},
		{Slot: "T1C", CatalogID: "99999", Material: "PLA", Status: "defined"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range info.Filaments {
		t.Logf("filament %d: %s | %s | %s | spool %s %s", f.Index, f.Preset, f.Type, f.Colour, f.Spool.Slot, f.Spool.Match)
	}
}

func profilesOpen(dir string) (*profiles.Store, error) {
	return profiles.Open(profiles.Roots{InstallProfiles: dir})
}

// M1: a view file the user saved into is never overwritten or deleted.
func TestPrepareViewKeepsWhatTheUserSaved(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Saved")
	e.addBox(t, info.ID, "a", 20, 20, 10)
	first, err := e.st.PrepareView(info.ID, 1, ViewProject)
	if err != nil || len(first.SavedFiles) != 0 {
		t.Fatalf("first view: %v %+v", err, first)
	}
	// The user saves changes from the app into the file.
	edited := append(mustRead(t, first.Path), []byte("user edit")...)
	if err := os.WriteFile(first.Path, edited, 0o644); err != nil {
		t.Fatal(err)
	}
	// Same revision: the saved file stays, the new copy takes the next number.
	second, err := e.st.PrepareView(info.ID, 1, ViewProject)
	if err != nil {
		t.Fatal(err)
	}
	if second.Path == first.Path || !strings.HasSuffix(second.Path, "_2.3mf") {
		t.Fatalf("second view %q, first %q", second.Path, first.Path)
	}
	if string(mustRead(t, first.Path)) != string(edited) {
		t.Fatal("the saved file was overwritten")
	}
	if len(second.SavedFiles) != 1 || second.SavedFiles[0] != first.Path {
		t.Fatalf("saved files %v", second.SavedFiles)
	}
	// A later revision: older unchanged copies go, the saved file stays and is reported.
	e.addBox(t, info.ID, "b", 20, 20, 10)
	third, err := e.st.PrepareView(info.ID, 1, ViewProject)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(first.Path); err != nil {
		t.Fatalf("the saved file was deleted: %v", err)
	}
	if _, err := os.Stat(second.Path); err == nil {
		t.Errorf("an unchanged older copy was kept: %s", second.Path)
	}
	if len(third.SavedFiles) != 1 || third.SavedFiles[0] != first.Path {
		t.Fatalf("saved files %v", third.SavedFiles)
	}
	// The same for preview copies.
	if _, err := e.st.Slice(info.ID, SliceOptions{}); err != nil {
		t.Fatal(err)
	}
	p1, err := e.st.PrepareView(info.ID, 1, ViewPreview)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p1.Path, []byte("edited gcode"), 0o644); err != nil {
		t.Fatal(err)
	}
	p2, err := e.st.PrepareView(info.ID, 1, ViewPreview)
	if err != nil || p2.Path == p1.Path || !strings.HasSuffix(p2.Path, "_2.gcode") || string(mustRead(t, p1.Path)) != "edited gcode" {
		t.Fatalf("preview: %v %+v", err, p2)
	}
	found := false
	for _, f := range p2.SavedFiles {
		found = found || f == p1.Path
	}
	if !found {
		t.Errorf("saved preview not reported: %v", p2.SavedFiles)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// m2: no view while the project is being sliced.
func TestPrepareViewRefusedWhileSlicing(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Busy")
	e.st.mu.Lock()
	e.st.running[info.ID] = "job-1"
	e.st.mu.Unlock()
	_, err := e.st.PrepareView(info.ID, 1, ViewProject)
	wantCode(t, err, CodeConflict)
	e.st.mu.Lock()
	delete(e.st.running, info.ID)
	e.st.mu.Unlock()
	if _, err := e.st.PrepareView(info.ID, 1, ViewProject); err != nil {
		t.Fatal(err)
	}
}

// m3: slots are T1A to T4D, each once.
func TestSpoolSlotsAreValidated(t *testing.T) {
	e := newEnv(t)
	create := func(a, b string) error {
		_, err := e.st.CreateProject(CreateRequest{Name: "S", Printer: testPrinter, Process: testProcess, Spools: []SpoolSpec{
			{Slot: a, CatalogID: "P001", Material: "PLA", Status: "defined"}, {Slot: b, CatalogID: "P002", Material: "PETG", Status: "defined"}}})
		return err
	}
	wantCode(t, create("T5A", "T1B"), CodeInvalidInput)
	wantCode(t, create("T1E", "T1B"), CodeInvalidInput)
	wantCode(t, create("slot1", "T1B"), CodeInvalidInput)
	ae := wantCode(t, create("T1A", "t1a"), CodeInvalidInput)
	if !strings.Contains(ae.Message, "twice") {
		t.Errorf("message %q", ae.Message)
	}
	if err := create("t1a", "T4D"); err != nil {
		t.Fatal(err)
	}
}
