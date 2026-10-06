package projects

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/sairaph/creality-slicer-mcp/internal/catalog"
)

// An elefant foot compensation above 1 mm is reset to 0 by the app: the
// catalog's forced_by rule fires and the reply says why.
func TestForcedByRuleFires(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Spiral")
	res, err := e.st.UpdateSettings(info.ID, SettingsRequest{Values: map[string]any{"elefant_foot_compensation": 2.5}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Forced) != 1 {
		t.Fatalf("forced: %+v", res.Forced)
	}
	c := res.Forced[0]
	if c.Key != "elefant_foot_compensation" || !c.Forced || c.New != "0" || c.Old != "2.5" ||
		c.Note != "the app sets this automatically when elefant_foot_compensation > 1" {
		t.Errorf("forced change: %+v", c)
	}
}

func TestDescribeCondAndForcedValue(t *testing.T) {
	cond := &catalog.Cond{K: "and", A: []*catalog.Cond{
		{K: "opt", Key: "spiral_mode"},
		{K: "not", A: []*catalog.Cond{{K: "opt", Key: "enable_support"}}},
		{K: "cmp", Op: ">", A: []*catalog.Cond{{K: "opt", Key: "wall_loops"}, {K: "num", V: 1}}},
	}}
	if got := describeCond(cond); got != "(spiral_mode and not enable_support and wall_loops > 1)" {
		t.Errorf("describeCond: %q", got)
	}
	if describeCond(nil) != "" {
		t.Error("nil condition")
	}
	if got := condSentences([]*catalog.Cond{{K: "opt", Key: "a"}, {K: "opt", Key: "b"}}); strings.Join(got, "+") != "a+b" {
		t.Errorf("condSentences: %v", got)
	}
	b := &catalog.Option{ValueType: "bool"}
	for set, want := range map[string]string{"true": "1", "1": "1", "false": "0", "0": "0"} {
		v, ok := forcedValue(b, set)
		if !ok || v.First() != want {
			t.Errorf("bool %q: %v %v", set, v, ok)
		}
	}
	if _, ok := forcedValue(b, "maybe"); ok {
		t.Error("a non literal bool is not forced")
	}
	if _, ok := forcedValue(b, ""); ok {
		t.Error("an empty set is not forced")
	}
	vec := &catalog.Option{ValueType: "bool", IsVector: true}
	if v, ok := forcedValue(vec, "true"); !ok || len(v.List) != 1 {
		t.Errorf("vector bool: %v", v)
	}
	if v, ok := forcedValue(&catalog.Option{ValueType: "int"}, "3"); !ok || v.First() != "3" {
		t.Errorf("int: %v", v)
	}
	if _, ok := forcedValue(&catalog.Option{ValueType: "int"}, "EPSILON"); ok {
		t.Error("a symbol is not a literal")
	}
	en := &catalog.Option{ValueType: "enum", Enum: &catalog.Enum{Values: []string{"arachne", "classic"}}}
	if v, ok := forcedValue(en, "arachne"); !ok || v.First() != "arachne" {
		t.Errorf("enum: %v", v)
	}
	if _, ok := forcedValue(en, "nope"); ok {
		t.Error("an unknown choice is not forced")
	}
}

func TestOverridesReportCases(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Ovr")
	e.addBox(t, info.ID, "cube", 20, 20, 20)
	_, err := e.st.Overrides(info.ID, 1)
	wantCode(t, err, CodeNotFound)
	if _, err := e.st.Slice(info.ID, SliceOptions{}); err != nil {
		t.Fatal(err)
	}
	// plate 0 is the first sliced plate; nothing is overridden, so the lists are empty
	rep, err := e.st.Overrides(info.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Plate != 1 || len(rep.PlateLines) != 0 {
		t.Errorf("plate 0: %+v", rep)
	}
	for _, o := range rep.Objects {
		if len(o.Lines) != 0 || len(o.Parts) != 0 || len(o.Ranges) != 0 {
			t.Errorf("object overrides: %+v", o)
		}
	}
	_, err = e.st.Overrides(info.ID, 3)
	wantCode(t, err, CodeNotFound)
	_, err = e.st.Overrides("no-such-project", 1)
	wantCode(t, err, CodeNotFound)
}

// The bed temperature key of each bed type, as get_bed_temp_1st_layer_key of
// Creality Print 7.3.0 (PrintConfig.hpp:579-600) and the enum names of
// s_keys_map_BedType (PrintConfig.cpp:836-844) give it.
func TestBedTempKeyForEveryBedType(t *testing.T) {
	for bed, want := range map[string]string{
		"Cool Plate":         "cool_plate_temp_initial_layer",
		"Engineering Plate":  "eng_plate_temp_initial_layer",
		"High Temp Plate":    "hot_plate_temp_initial_layer",
		"Textured PEI Plate": "textured_plate_temp_initial_layer",
		"Epoxy Resin Plate":  "epoxy_resin_plate_temp_initial_layer",
		"Customized Plate":   "customized_plate_temp_initial_layer",
		"Default Plate":      "",
		"":                   "",
		"Unknown Plate":      "",
	} {
		if got := bedTempKey(bed); got != want {
			t.Errorf("%q: %q, want %q", bed, got, want)
		}
	}
}

// One folder has one id: another case, a trailing dot or the 8.3 short name
// reach the same project under its canonical id (or are refused), so the lock
// and the running-slice table never see two names.
func TestProjectReferenceAliasesResolveToTheCanonicalId(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Alias Check")
	id := info.ID
	got, err := e.st.resolve(strings.ToUpper(id))
	if err != nil || got != id {
		t.Fatalf("upper case: %q %v", got, err)
	}
	if runtime.GOOS == "windows" {
		if _, err := e.st.resolve(id + "."); err == nil {
			t.Error("a trailing dot reached the folder")
		} else {
			wantCode(t, err, CodeInvalidInput)
		}
		if short := shortOf(e.st.dir(id)); short != "" && !strings.EqualFold(filepath.Base(short), id) {
			if _, err := e.st.resolve(filepath.Base(short)); err == nil {
				t.Error("the 8.3 short name reached the folder")
			}
		}
	}
	if e.st.projLock(got) != e.st.projLock(id) {
		t.Error("two locks for one project")
	}
	// An upper case reference opens and changes the same project, and delete
	// works through it.
	if _, err := e.st.GetProject(strings.ToUpper(id)); err != nil {
		t.Fatal(err)
	}
}

// A crash between the pending marker and the final job.json leaves the marker
// set: the next read treats every sliced plate as changed.
func TestInterruptedSaveMarksSlicesStale(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Crash")
	e.addBox(t, info.ID, "cube", 20, 20, 20)
	if _, err := e.st.Slice(info.ID, SliceOptions{}); err != nil {
		t.Fatal(err)
	}
	before, _ := e.st.GetProject(info.ID)
	if before.LastSlice == nil || before.LastSlice.Stale {
		t.Fatalf("fresh slice: %+v", before.LastSlice)
	}
	// What commit leaves behind when the process dies after the marker.
	m, err := e.st.readMeta(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	m.Pending = true
	if err := e.st.writeMeta(info.ID, m); err != nil {
		t.Fatal(err)
	}
	after, err := e.st.GetProject(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.LastSlice == nil || !after.LastSlice.Stale || len(after.LastSlice.StalePlates) != 1 {
		t.Fatalf("an interrupted save left the slice looking current: %+v", after.LastSlice)
	}
	// The next change writes the repaired record without the marker.
	if _, err := e.st.UpdateSettings(info.ID, SettingsRequest{Values: map[string]any{"wall_loops": 3}}); err != nil {
		t.Fatal(err)
	}
	if m, _ := e.st.readMeta(info.ID); m.Pending {
		t.Error("the marker stays after a completed save")
	}
}

// A project.3mf moved aside by an interrupted open_project into comes back.
func TestInterruptedOpenIntoRestoresTheFile(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Aside")
	e.addBox(t, info.ID, "cube", 20, 20, 20)
	file := filepath.Join(e.st.dir(info.ID), projectFile)
	if err := os.Rename(file, file+".old"); err != nil {
		t.Fatal(err)
	}
	got, err := e.st.GetProject(info.ID)
	if err != nil || len(got.Objects) != 1 {
		t.Fatalf("project after an interrupted open into: %v %+v", err, got)
	}
	if _, err := os.Stat(file + ".old"); err == nil {
		t.Error("the aside copy is still there")
	}
}

func TestStoreStartSweepsOldLeftovers(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Sweep")
	dir := e.st.dir(info.ID)
	old := time.Now().Add(-time.Hour)
	fresh := time.Now()
	mk := func(path string, mod time.Time, isDir bool) {
		t.Helper()
		if isDir {
			if err := os.MkdirAll(path, 0o755); err != nil {
				t.Fatal(err)
			}
		} else if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, mod, mod); err != nil {
			t.Fatal(err)
		}
	}
	mk(filepath.Join(dir, ".tmp-111"), old, false)
	mk(filepath.Join(dir, ".project-222.tmp"), old, false)
	mk(filepath.Join(dir, ".project-333.tmp"), fresh, false) // maybe being written now
	if err := os.MkdirAll(filepath.Join(dir, "view"), 0o755); err != nil {
		t.Fatal(err)
	}
	mk(filepath.Join(dir, "view", ".thumbnails-444"), old, false)
	mk(filepath.Join(dir, "gcode", ".run-555"), old.Add(-3*time.Hour), true)
	mk(filepath.Join(dir, "gcode", ".run-666"), fresh, true) // a slice may be running
	mk(filepath.Join(dir, "keep.tmp"), old, false)           // not one of ours
	mk(filepath.Join(dir, "project.3mf.old"), old, false)
	e.st.sweepLeftovers()
	for _, gone := range []string{".tmp-111", ".project-222.tmp", "view/.thumbnails-444", "gcode/.run-555"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(gone))); err == nil {
			t.Errorf("%s was not swept", gone)
		}
	}
	for _, kept := range []string{".project-333.tmp", "gcode/.run-666", "keep.tmp", "project.3mf.old", "project.3mf", "job.json"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(kept))); err != nil {
			t.Errorf("%s was removed: %v", kept, err)
		}
	}
}

// A job id the store does not know is not_found, never a finished slice.
func TestUnknownSliceJobIsNotFound(t *testing.T) {
	e := newEnv(t)
	for _, ref := range []string{"slice-deadbeef", "job-0"} {
		_, err := e.st.SliceStatus(ref)
		wantCode(t, err, CodeNotFound)
	}
}
