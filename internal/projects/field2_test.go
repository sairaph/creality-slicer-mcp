package projects

import (
	"fmt"
	"math"

	"archive/zip"
	"bytes"
	"github.com/sairaph/creality-slicer-mcp/internal/mesh"
	"github.com/sairaph/creality-slicer-mcp/internal/threemf"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// R2-1: the filament of a part and of a height range.
func TestExtruderOnPartsAndRanges(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "PartFilament")
	e.addBox(t, info.ID, "box", 30, 30, 30)
	if _, err := e.st.AddModifier(info.ID, ModifierRequest{Object: "box", Name: "mod", Shape: ShapeBox, Size: [3]float64{10, 10, 10}, Settings: map[string]any{"wall_loops": 2}}); err != nil {
		t.Fatal(err)
	}
	set := func(scope, target string, v any) error {
		_, err := e.st.UpdateSettings(info.ID, SettingsRequest{Scope: scope, Target: target, Values: map[string]any{"extruder": v}})
		return err
	}
	// Part: 0 (the object's own) to the number of filaments.
	if err := set("part", "box/mod", 2); err != nil {
		t.Fatal(err)
	}
	if err := set("part", "box/mod", 3); err == nil || !strings.Contains(err.Error(), "from 0 to 2") {
		t.Fatalf("filament 3: %v", err)
	}
	if err := set("part", "box/mod", 0); err != nil {
		t.Fatalf("filament 0 on a part: %v", err)
	}
	if err := set("part", "box/mod", 2); err != nil {
		t.Fatal(err)
	}
	// Object: 1 to the number of filaments, as before.
	if err := set("object", "box", 0); err == nil {
		t.Fatal("filament 0 on an object accepted")
	}
	// Height ranges, through set_height_ranges and update_settings.
	if _, err := e.st.SetHeightRanges(info.ID, "box", []RangeSpec{{From: 0, To: 10, Settings: map[string]any{"extruder": 2}}, {From: 10, To: 20, Settings: map[string]any{"layer_height": 0.3}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.SetHeightRanges(info.ID, "box", []RangeSpec{{From: 0, To: 10, Settings: map[string]any{"extruder": 5}}}); err == nil {
		t.Fatal("range filament 5 accepted")
	}
	if err := set("layer_range", "box/2", 1); err != nil {
		t.Fatal(err)
	}
	if err := set("layer_range", "box/2", 9); err == nil {
		t.Fatal("range filament 9 accepted")
	}
	got, err := e.st.GetProject(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	o := got.Objects[0]
	var partFil int
	for _, p := range o.Parts {
		if p.Name == "mod" {
			partFil = p.Filament
		}
	}
	if partFil != 2 || len(o.HeightRanges) != 2 || o.HeightRanges[0].Filament != 2 || o.HeightRanges[1].Filament != 1 {
		t.Fatalf("part filament %d, ranges %+v", partFil, o.HeightRanges)
	}
	// Both are filament uses: the by layer crash warning and the slot check see them.
	if err := e.st.read(info.ID, func(h *handle) error {
		if n := h.plateFilamentCount(1); n != 2 {
			t.Errorf("plate filament count %d, want 2", n)
		}
		if slot, _ := h.highestSlot(); slot != 2 {
			t.Errorf("highest slot %d", slot)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.SetPresets(info.ID, PresetsRequest{Filaments: []FilamentSpec{{Preset: testPLA, Colour: "#FFFFFF"}}}); err == nil {
		t.Error("shrinking below a filament used by a part or range was allowed")
	}
}

// R2-7: one object whose filament changes are layer actions: by object does not apply.
func TestPurgeWarningForLayerActionsOnOneObject(t *testing.T) {
	data := purgeGCode(t)
	e := newEnv(t)
	info := e.newProject(t, "Stripes")
	e.addBox(t, info.ID, "a", 20, 20, 20)
	if _, err := e.st.SetLayerActions(info.ID, 1, []LayerAction{{Layer: 5, Kind: ActionColorChange, Filament: 2}, {Layer: 10, Kind: ActionColorChange, Filament: 1}}); err != nil {
		t.Fatal(err)
	}
	e.exec.gcode = func(int) string { return data }
	res, err := e.st.Slice(info.ID, SliceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	w := res.Last.Plates[0].PurgeWarning
	for _, want := range []string{"Purge waste ", "2 layer filament change(s)", "flush_multiplier", "by object does not apply"} {
		if !strings.Contains(strings.ToLower(w), strings.ToLower(want)) {
			t.Errorf("%q lacks %q", w, want)
		}
	}
	if strings.Contains(w, "mix filaments") || strings.Contains(w, "avoids") || strings.Contains(w, "Each object here") {
		t.Errorf("by object advice for one object: %q", w)
	}
}

func TestSpoolMemberHasAModifiedTime(t *testing.T) {
	e := newEnv(t)
	info := spooledProject(t, e, "Time")
	data, err := os.ReadFile(filepath.Join(e.st.dir(info.ID), projectFile))
	if err != nil {
		t.Fatal(err)
	}
	out, err := withSpoolMember(data, []SpoolLink{{Slot: "T1A", Preset: "p"}})
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(out), int64(len(out)))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range zr.File {
		if f.Name == spoolMember && f.Modified.Year() < 2020 {
			t.Fatalf("modified time %v", f.Modified)
		}
	}
}

func worldBounds(t *testing.T, e *testEnv, id string, objectName string) map[string]mesh.BBox {
	t.Helper()
	out := map[string]mesh.BBox{}
	if err := e.st.read(id, func(h *handle) error {
		o, err := h.objectByRef(objectName)
		if err != nil {
			return err
		}
		item := h.p.ItemsOf(o.ID)[0].Transform
		for _, pt := range o.Parts {
			m, err := h.p.LoadMesh(pt)
			if err != nil {
				return err
			}
			b, _ := bboxOf(m, pt.ComponentTransform.Then(item))
			out[pt.Name] = b
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

func sameBox(a, b mesh.BBox) bool {
	for i := 0; i < 3; i++ {
		if math.Abs(float64(a.Min[i]-b.Min[i])) > 1e-4 || math.Abs(float64(a.Max[i]-b.Max[i])) > 1e-4 {
			return false
		}
	}
	return true
}

// R2-2: two objects with different filaments and rotations become one object.
func TestGroupObjects(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Group")
	e.addBox(t, info.ID, "base", 20, 20, 10)
	if _, err := e.st.AddModel(info.ID, AddModelRequest{Path: writeSTL(t, "b", 30, 10, 8), Name: "second", Filament: 2}); err != nil {
		t.Fatal(err)
	}
	rot := [3]float64{0, 0, 33}
	if _, err := e.st.UpdateObject(info.ID, UpdateObjectRequest{Object: "second", Rotation: &rot}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.UpdateSettings(info.ID, SettingsRequest{Scope: "object", Target: "second", Values: map[string]any{"wall_loops": 5, "enable_support": true}}); err != nil {
		t.Fatal(err)
	}
	before := worldBounds(t, e, info.ID, "second")
	baseBefore := worldBounds(t, e, info.ID, "base")
	res, err := e.st.GroupObjects(info.ID, GroupRequest{Objects: []string{"base", "second"}, Name: "pair"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Info.Objects) != 1 || res.Object.Name != "pair" || len(res.Parts) != 2 {
		t.Fatalf("result %+v", res)
	}
	if res.Parts[0].Filament != 1 || res.Parts[1].Name != "second" || res.Parts[1].Filament != 2 {
		t.Fatalf("parts %+v", res.Parts)
	}
	after := worldBounds(t, e, info.ID, "pair")
	if !sameBox(after["second"], before["second"]) || !sameBox(after["base"], baseBefore["base"]) {
		t.Fatalf("bounds moved: %v vs %v and %v vs %v", after["second"], before["second"], after["base"], baseBefore["base"])
	}
	// wall_loops is valid on a part and went with it; enable_support is not.
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "enable_support") {
		t.Fatalf("warnings %v", res.Warnings)
	}
	saved := openSaved(t, e, info.ID)
	var moved *threemf.Part
	for _, p := range saved.Objects[0].Parts {
		if p.Name == "second" {
			moved = p
		}
	}
	if moved == nil || moved.Config.Value("extruder") != "2" || moved.Config.Value("wall_loops") != "5" {
		t.Fatalf("saved part %+v", moved)
	}
	// The file reloads and slices.
	if _, err := e.st.Slice(info.ID, SliceOptions{}); err != nil {
		t.Fatalf("slice after grouping: %v", err)
	}
}

func TestGroupObjectsRefusals(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "GroupBad")
	e.addBox(t, info.ID, "a", 20, 20, 10)
	e.addBox(t, info.ID, "b", 20, 20, 10)
	for name, objs := range map[string][]string{"one": {"a"}, "twice": {"a", "a"}, "unknown": {"a", "zzz"}} {
		if _, err := e.st.GroupObjects(info.ID, GroupRequest{Objects: objs}); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	// An object with two instances (only an opened file has them): checked on a
	// handle with a second build item added in memory.
	if err := e.st.read(info.ID, func(h *handle) error {
		o, _ := h.objectByRef("a")
		h.p.Items = append(h.p.Items, &threemf.BuildItem{ObjectID: o.ID, Transform: mesh.Identity(), Printable: true})
		return h.groupObjects(GroupRequest{Objects: []string{"a", "b"}}, &GroupResult{}, new(int))
	}); err == nil || !strings.Contains(err.Error(), "2 instances") {
		t.Fatalf("two instances: %v", err)
	}
	var err error
	// Different plates.
	if _, err := e.st.ManagePlates(info.ID, PlatesRequest{Action: PlateAdd}); err != nil {
		t.Fatal(err)
	}
	two := 2
	if _, err := e.st.UpdateObject(info.ID, UpdateObjectRequest{Object: "b", Plate: &two}); err != nil {
		t.Fatal(err)
	}
	_, err = e.st.GroupObjects(info.ID, GroupRequest{Objects: []string{"a", "b"}})
	wantCode(t, err, CodeInvalidInput)
	if got, _ := e.st.GetProject(info.ID); len(got.Objects) != 2 {
		t.Fatalf("a refused group changed the project: %d objects", len(got.Objects))
	}
}

// freecadShaped writes a 3MF the way FreeCAD does: no object names, two build
// items with transforms that place the cubes on the bed.
func freecadShaped(t *testing.T, name string, x2 float64) string {
	t.Helper()
	cube := func(id int, s float64) string {
		v := func(x, y, z float64) string { return fmt.Sprintf(`<vertex x="%g" y="%g" z="%g"/>`, x*s, y*s, z*s) }
		return fmt.Sprintf(`<object id="%d" type="model"><mesh><vertices>%s%s%s%s%s%s%s%s</vertices><triangles>
<triangle v1="0" v2="2" v3="1"/><triangle v1="0" v2="3" v3="2"/><triangle v1="4" v2="5" v3="6"/><triangle v1="4" v2="6" v3="7"/>
<triangle v1="0" v2="1" v3="5"/><triangle v1="0" v2="5" v3="4"/><triangle v1="1" v2="2" v3="6"/><triangle v1="1" v2="6" v3="5"/>
<triangle v1="2" v2="3" v3="7"/><triangle v1="2" v2="7" v3="6"/><triangle v1="3" v2="0" v3="4"/><triangle v1="3" v2="4" v3="7"/>
</triangles></mesh></object>`, id, v(0, 0, 0), v(1, 0, 0), v(1, 1, 0), v(0, 1, 0), v(0, 0, 1), v(1, 0, 1), v(1, 1, 1), v(0, 1, 1))
	}
	model := `<?xml version="1.0"?><model unit="millimeter" xmlns="http://schemas.microsoft.com/3dmanufacturing/core/2015/02"><resources>` +
		cube(1, 20) + cube(2, 10) + `</resources><build>` +
		`<item objectid="1" transform="1 0 0 0 1 0 0 0 1 100 50 5"/>` +
		`<item objectid="2" transform="1 0 0 0 1 0 0 0 1 ` + fmt.Sprint(x2) + ` 120 0"/></build></model>`
	path := filepath.Join(t.TempDir(), name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for n, body := range map[string]string{
		"[Content_Types].xml": `<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="model" ContentType="application/vnd.ms-package.3dmanufacturing-3dmodel+xml"/></Types>`,
		"_rels/.rels":         `<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Target="/3D/3dmodel.model" Id="rel0" Type="http://schemas.microsoft.com/3dmanufacturing/2013/01/3dmodel"/></Relationships>`,
		"3D/3dmodel.model":    model,
	} {
		w, _ := zw.Create(n)
		w.Write([]byte(body))
	}
	zw.Close()
	f.Close()
	return path
}

// R2-3: names, the default name of an unnamed object, keep_positions.
func TestAddModelKeepPositionsAndNames(t *testing.T) {
	e := newEnv(t)
	file := freecadShaped(t, "FullRound.3mf", 180)
	info := e.newProject(t, "Keep")
	res, err := e.st.AddModel(info.ID, AddModelRequest{Path: file, KeepPositions: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Added) != 2 || res.Added[0].Name != "FullRound 1" || res.Added[1].Name != "FullRound 2" {
		t.Fatalf("names %+v", res.Added)
	}
	a, b := res.Added[0], res.Added[1]
	// The file centres are (110, 60) and (185, 125) from the plate corner; Z drops to the bed.
	if math.Abs(a.Position[0]-110) > 1e-3 || math.Abs(a.Position[1]-60) > 1e-3 || math.Abs(a.Position[2]) > 1e-6 || math.Abs(b.Position[0]-185) > 1e-3 || math.Abs(b.Position[1]-125) > 1e-3 {
		t.Fatalf("positions %v and %v", a.Position, b.Position)
	}
	// Names win, one per object; a wrong count is refused.
	info2 := e.newProject(t, "Names")
	res, err = e.st.AddModel(info2.ID, AddModelRequest{Path: file, Names: []string{"left", "right"}, KeepPositions: true})
	if err != nil || res.Added[0].Name != "left" || res.Added[1].Name != "right" {
		t.Fatalf("names: %v %+v", err, res)
	}
	info3 := e.newProject(t, "BadNames")
	_, err = e.st.AddModel(info3.ID, AddModelRequest{Path: file, Names: []string{"only one"}})
	if ae := wantCode(t, err, CodeInvalidInput); !strings.Contains(ae.Message, "names has 1 entries but 2") {
		t.Fatalf("message %q", ae.Message)
	}
	// With objects filtering the count follows the selection.
	res, err = e.st.AddModel(info3.ID, AddModelRequest{Path: file, Objects: []string{"FullRound 2"}, Names: []string{"small"}})
	if err != nil || len(res.Added) != 1 || res.Added[0].Name != "small" {
		t.Fatalf("objects+names: %v %+v", err, res)
	}
	// keep_positions refuses position and copies.
	x, y := 50.0, 50.0
	_, err = e.st.AddModel(info3.ID, AddModelRequest{Path: file, KeepPositions: true, X: &x, Y: &y})
	wantCode(t, err, CodeInvalidInput)
	_, err = e.st.AddModel(info3.ID, AddModelRequest{Path: file, KeepPositions: true, Copies: 2})
	wantCode(t, err, CodeInvalidInput)
}

func TestKeepPositionsWarnsOutsideTheBed(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Outside")
	res, err := e.st.AddModel(info.ID, AddModelRequest{Path: freecadShaped(t, "Far.3mf", 400), KeepPositions: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Added) != 2 || len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "Far 2") || !strings.Contains(res.Warnings[0], "not fully inside the printable area") {
		t.Fatalf("warnings %v", res.Warnings)
	}
	if res.Added[1].Position[0] < 400 {
		t.Fatalf("the object was moved: %v", res.Added[1].Position)
	}
}

// R2-4: one update for several targets.
func TestUpdateSettingsManyTargets(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Many")
	for _, n := range []string{"a", "b", "c"} {
		e.addBox(t, info.ID, n, 20, 20, 10)
	}
	before, _ := e.st.GetProject(info.ID)
	res, err := e.st.UpdateSettings(info.ID, SettingsRequest{Scope: "object", Targets: []string{"a", "b", "c"}, Values: map[string]any{"wall_loops": 6}})
	if err != nil {
		t.Fatal(err)
	}
	targets := map[string]bool{}
	for _, c := range res.Changed {
		targets[c.Target] = true
	}
	if len(targets) != 3 || res.Info.Revision != before.Revision+1 {
		t.Fatalf("targets %v, revision %d -> %d", targets, before.Revision, res.Info.Revision)
	}
	for _, o := range res.Info.Objects {
		if o.Overrides != 1 {
			t.Errorf("object %s has %d overrides", o.Name, o.Overrides)
		}
	}
	// One bad target refuses all: nothing changes, no revision.
	mid := res.Info.Revision
	_, err = e.st.UpdateSettings(info.ID, SettingsRequest{Scope: "object", Targets: []string{"a", "nope", "c"}, Values: map[string]any{"sparse_infill_density": "30%"}})
	ae := wantCode(t, err, CodeNotFound)
	if !strings.Contains(ae.Message, `target "nope"`) {
		t.Errorf("message %q", ae.Message)
	}
	after, _ := e.st.GetProject(info.ID)
	if after.Revision != mid {
		t.Fatalf("revision moved %d -> %d", mid, after.Revision)
	}
	for _, o := range after.Objects {
		if o.Overrides != 1 {
			t.Errorf("object %s changed: %d overrides", o.Name, o.Overrides)
		}
	}
	// target and targets together, and targets on the project scope, are refused.
	_, err = e.st.UpdateSettings(info.ID, SettingsRequest{Scope: "object", Target: "a", Targets: []string{"b"}, Values: map[string]any{"wall_loops": 2}})
	wantCode(t, err, CodeInvalidInput)
	_, err = e.st.UpdateSettings(info.ID, SettingsRequest{Targets: []string{"a"}, Values: map[string]any{"wall_loops": 2}})
	wantCode(t, err, CodeInvalidInput)
}

// R2-5: overrides of every scope are read back for a sliced plate.
func TestOverridesReport(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Overrides")
	e.addBox(t, info.ID, "box", 30, 30, 30)
	if _, err := e.st.UpdateSettings(info.ID, SettingsRequest{Scope: "object", Target: "box", Values: map[string]any{"wall_loops": 6}}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.AddModifier(info.ID, ModifierRequest{Object: "box", Name: "mod", Shape: ShapeBox, Size: [3]float64{10, 10, 10}, Settings: map[string]any{"sparse_infill_density": "40%", "extruder": 2}}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.SetHeightRanges(info.ID, "box", []RangeSpec{{From: 0, To: 10, Settings: map[string]any{"extruder": 2, "layer_height": 0.3}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.UpdateSettings(info.ID, SettingsRequest{Scope: "plate", Target: "1", Values: map[string]any{"curr_bed_type": "Textured PEI Plate"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.Slice(info.ID, SliceOptions{}); err != nil {
		t.Fatal(err)
	}
	// Changes after the slice must not show: the values are the sliced ones.
	if _, err := e.st.UpdateSettings(info.ID, SettingsRequest{Scope: "object", Target: "box", Values: map[string]any{"wall_loops": 2}}); err != nil {
		t.Fatal(err)
	}
	rep, err := e.st.Overrides(info.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.FromSlice || !rep.Stale {
		t.Fatalf("source: from slice %v, stale %v", rep.FromSlice, rep.Stale)
	}
	if len(rep.PlateLines) != 1 || rep.PlateLines[0].Key != "curr_bed_type" || rep.PlateLines[0].Value != "Textured PEI Plate" {
		t.Fatalf("plate lines %+v", rep.PlateLines)
	}
	if len(rep.Objects) != 1 {
		t.Fatalf("objects %+v", rep.Objects)
	}
	o := rep.Objects[0]
	if len(o.Lines) != 1 || o.Lines[0].Key != "wall_loops" || o.Lines[0].Value != "6" {
		t.Fatalf("object lines %+v", o.Lines)
	}
	if len(o.Parts) != 1 || o.Parts[0].Name != "mod" || o.Parts[0].Filament != 2 || len(o.Parts[0].Lines) != 1 || o.Parts[0].Lines[0].Key != "sparse_infill_density" {
		t.Fatalf("parts %+v", o.Parts)
	}
	if len(o.Ranges) != 1 || o.Ranges[0].Filament != 2 || len(o.Ranges[0].Lines) != 1 || o.Ranges[0].Lines[0].Key != "layer_height" {
		t.Fatalf("ranges %+v", o.Ranges)
	}
}

// R2-6: the bed Creality Print last used for the printer.
func TestDefaultBedTypeFromTheApp(t *testing.T) {
	e := newEnv(t)
	conf := func(body string) {
		t.Helper()
		dir := t.TempDir()
		if body != "" {
			if err := os.WriteFile(filepath.Join(dir, "Creality.conf"), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		e.st.cfg.Install.DataDir = dir
	}
	entry := func(machine, bed string) string {
		return fmt.Sprintf(`{"machine": %q, "curr_bed_type": %q, "token": "secret"}`, machine, bed)
	}
	file := func(entries ...string) string {
		// A JSON object and a checksum comment line, as the app writes it.
		return `{"app": {"x": 1}, "orca_presets": [` + strings.Join(entries, ",") + `]}` + "\n# MD5 checksum 0123456789abcdef\n"
	}
	create := func(name, bed string) *Info {
		t.Helper()
		info, err := e.st.CreateProject(CreateRequest{Name: name, Printer: testPrinter, Process: testProcess, Filaments: twoFilaments(), BedType: bed})
		if err != nil {
			t.Fatal(err)
		}
		return info
	}
	conf(file(entry("Creality Ender-3 S1 Pro 0.4 nozzle", "3"), entry(testPrinter, "4")))
	info := create("FromApp", "")
	if info.Plates[0].BedType != "Textured PEI Plate" || !info.BedTypeFromApp {
		t.Fatalf("bed %q from app %v", info.Plates[0].BedType, info.BedTypeFromApp)
	}
	// An explicit bed type wins.
	info = create("Explicit", "Cool Plate")
	if info.Plates[0].BedType != "Cool Plate" || info.BedTypeFromApp {
		t.Fatalf("explicit: %q %v", info.Plates[0].BedType, info.BedTypeFromApp)
	}
	// The enum maps as the source names it.
	for n, want := range map[string]string{"1": "Cool Plate", "2": "Engineering Plate", "3": "High Temp Plate", "5": "Customized Plate", "6": "Epoxy Resin Plate"} {
		conf(file(entry(testPrinter, n)))
		if got, ok := e.st.appBedType(testPrinter); !ok || got != want {
			t.Errorf("enum %s: %q %v, want %q", n, got, ok, want)
		}
	}
	// Missing conf, unknown printer, Default Plate (0), unknown value, garbage: today's default.
	for name, body := range map[string]string{
		"missing":  "",
		"other":    file(entry("Creality K1C 0.4 nozzle", "4")),
		"default":  file(entry(testPrinter, "0")),
		"unknown":  file(entry(testPrinter, "9")),
		"notanint": file(entry(testPrinter, "wide")),
		"garbage":  "not json at all",
	} {
		conf(body)
		info := create("Fallback "+name, "")
		if info.BedTypeFromApp {
			t.Errorf("%s: taken from the app", name)
		}
		if _, ok := e.st.appBedType(testPrinter); ok {
			t.Errorf("%s: appBedType found a value", name)
		}
	}
	// No data folder at all.
	e.st.cfg.Install.DataDir = ""
	if info := create("NoData", ""); info.BedTypeFromApp {
		t.Error("no data dir but from app")
	}
}

// A mirrored item (negative determinant, as an app-saved file can hold one)
// groups without turning the part inside out: the app stores the matrix beside
// the unchanged mesh and the readers (ours too) flip the winding when they apply it.
func TestGroupObjectsMirrored(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Mirror")
	e.addBox(t, info.ID, "base", 20, 20, 10)
	if _, err := e.st.AddModel(info.ID, AddModelRequest{Path: writeSTL(t, "m", 30, 10, 8), Name: "mirrored"}); err != nil {
		t.Fatal(err)
	}
	var wantBox mesh.BBox
	var wantVol float64
	if err := e.st.write(info.ID, func(h *handle) error {
		o, _ := h.objectByRef("mirrored")
		tr := h.p.ItemsOf(o.ID)[0].Transform
		tr = mesh.Compose(mesh.Scale(-1, 1, 1), tr) // mirror in X, then the placement
		if tr.Determinant() >= 0 {
			t.Fatalf("not mirrored: %v", tr.Determinant())
		}
		if err := h.p.SetTransform(o.ID, 0, tr); err != nil {
			return err
		}
		h.touchObject(o.ID)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := e.st.read(info.ID, func(h *handle) error {
		o, _ := h.objectByRef("mirrored")
		m, err := h.objectMesh(o)
		if err != nil {
			return err
		}
		world := m.Transformed(h.p.ItemsOf(o.ID)[0].Transform)
		wantBox, _ = world.BBox()
		wantVol = world.Volume()
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if wantVol <= 0 {
		t.Fatalf("the mirrored object reads inside out before grouping: volume %v", wantVol)
	}
	if _, err := e.st.GroupObjects(info.ID, GroupRequest{Objects: []string{"base", "mirrored"}}); err != nil {
		t.Fatal(err)
	}
	if err := e.st.read(info.ID, func(h *handle) error {
		o, _ := h.objectByRef("base")
		var part *threemf.Part
		for _, p := range o.Parts {
			if p.Name == "mirrored" {
				part = p
			}
		}
		raw, err := h.p.LoadMesh(part)
		if err != nil {
			return err
		}
		world := raw.Transformed(part.ComponentTransform.Then(h.p.ItemsOf(o.ID)[0].Transform))
		box, _ := world.BBox()
		if !sameBox(box, wantBox) || math.Abs(world.Volume()-wantVol) > 1e-3*wantVol || world.Volume() <= 0 {
			t.Errorf("grouped mirrored part: box %v want %v, volume %v want %v", box, wantBox, world.Volume(), wantVol)
		}
		if part.ComponentTransform.Determinant() >= 0 {
			t.Errorf("the mirror is not kept in the part matrix")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// R2-6 nit: the last orca_presets entry of a machine wins.
func TestAppBedTypeLastEntryWins(t *testing.T) {
	e := newEnv(t)
	dir := t.TempDir()
	conf := `{"orca_presets": [{"machine": "` + testPrinter + `", "curr_bed_type": "3"}, {"machine": "other", "curr_bed_type": "1"}, {"machine": "` + testPrinter + `", "curr_bed_type": "4"}]}` + "\n# MD5 x\n"
	if err := os.WriteFile(filepath.Join(dir, "Creality.conf"), []byte(conf), 0o644); err != nil {
		t.Fatal(err)
	}
	e.st.cfg.Install.DataDir = dir
	if got, ok := e.st.appBedType(testPrinter); !ok || got != "Textured PEI Plate" {
		t.Fatalf("got %q %v, want the last entry (4)", got, ok)
	}
	// A last entry with an unknown value does not fall back to an earlier one.
	conf = strings.Replace(conf, `"curr_bed_type": "4"`, `"curr_bed_type": "9"`, 1)
	if err := os.WriteFile(filepath.Join(dir, "Creality.conf"), []byte(conf), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, ok := e.st.appBedType(testPrinter); ok {
		t.Fatalf("got %q for an unknown last value", got)
	}
}

// keep_positions on a slicer project: an object on plate 2 lands at its plate
// relative XY, not at the scene position of the second plate.
func TestKeepPositionsOnASlicerProject(t *testing.T) {
	e := newEnv(t)
	src := e.newProject(t, "TwoPlates")
	if _, err := e.st.ManagePlates(src.ID, PlatesRequest{Action: PlateAdd}); err != nil {
		t.Fatal(err)
	}
	x1, y1, x2, y2 := 50.0, 60.0, 100.0, 120.0
	if _, err := e.st.AddModel(src.ID, AddModelRequest{Path: writeSTL(t, "a", 20, 20, 10), Name: "onone", X: &x1, Y: &y1, Plate: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.AddModel(src.ID, AddModelRequest{Path: writeSTL(t, "b", 20, 20, 10), Name: "ontwo", X: &x2, Y: &y2, Plate: 2}); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "two.3mf")
	if _, err := e.st.Export(src.ID, file, false); err != nil {
		t.Fatal(err)
	}
	dst := e.newProject(t, "Target")
	res, err := e.st.AddModel(dst.ID, AddModelRequest{Path: file, KeepPositions: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Added) != 2 {
		t.Fatalf("added %+v", res.Added)
	}
	for _, o := range res.Added {
		want := [2]float64{x1, y1}
		if o.Name == "ontwo" {
			want = [2]float64{x2, y2}
		}
		if math.Abs(o.Position[0]-want[0]) > 1e-3 || math.Abs(o.Position[1]-want[1]) > 1e-3 || o.Plate != 1 {
			t.Errorf("%s at %v on plate %d, want %v on plate 1", o.Name, o.Position, o.Plate, want)
		}
	}
	if len(res.Warnings) != 0 {
		t.Errorf("warnings %v", res.Warnings)
	}
}

func TestOverridesWithAnEmptySliceList(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Empty")
	e.addBox(t, info.ID, "a", 20, 20, 10)
	if err := e.st.write(info.ID, func(h *handle) error { return nil }); err != nil {
		t.Fatal(err)
	}
	m, _ := e.st.readMeta(info.ID)
	m.LastSlice = &LastSlice{}
	if err := e.st.writeMeta(info.ID, m); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.Overrides(info.ID, 0); err == nil {
		t.Fatal("no error for a slice without plates")
	}
	if _, err := e.st.Report(info.ID, 0); err == nil {
		t.Fatal("no error from Report for a slice without plates")
	}
}
