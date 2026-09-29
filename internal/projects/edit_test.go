package projects

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sairaph/creality-slicer-mcp/internal/threemf"
)

func openSaved(t *testing.T, e *testEnv, id string) *threemf.Project {
	t.Helper()
	data, rerr := os.ReadFile(filepath.Join(e.dir, "projects", id, "project.3mf"))
	if rerr != nil {
		t.Fatal(rerr)
	}
	copyPath := filepath.Join(t.TempDir(), "copy.3mf") // a copy: an open zip would block the next save on Windows
	if werr := os.WriteFile(copyPath, data, 0o644); werr != nil {
		t.Fatal(werr)
	}
	p, err := threemf.Open(copyPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	return p
}

func TestUpdateSettingsProject(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Settings")
	res, err := e.st.UpdateSettings(info.ID, SettingsRequest{Values: map[string]any{"wall_loops": 5, "sparse_infill_density": "25%"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Changed) != 2 || res.Changed[1].Key != "wall_loops" && res.Changed[0].Key != "wall_loops" {
		t.Fatalf("changed: %+v", res.Changed)
	}
	for _, c := range res.Changed {
		if c.Key == "wall_loops" && (c.Old != "3" || c.New != "5") {
			t.Fatalf("wall_loops change %+v", c)
		}
	}
	p := openSaved(t, e, info.ID)
	if p.Settings.String("wall_loops") != "5" || p.Settings.String("sparse_infill_density") != "25%" {
		t.Fatalf("saved: %q %q", p.Settings.String("wall_loops"), p.Settings.String("sparse_infill_density"))
	}
	// The change shows up in the list of changes against the presets.
	if !strings.Contains(strings.Join(p.Settings.List("different_settings_to_system"), "|"), "wall_loops") {
		t.Fatalf("diffs: %v", p.Settings.List("different_settings_to_system"))
	}
	if res.Info.Overrides < 2 || res.Info.Revision != 2 {
		t.Fatalf("info: overrides %d revision %d", res.Info.Overrides, res.Info.Revision)
	}
	// nil goes back to the preset value.
	res, err = e.st.UpdateSettings(info.ID, SettingsRequest{Values: map[string]any{"wall_loops": nil}})
	if err != nil {
		t.Fatal(err)
	}
	p = openSaved(t, e, info.ID)
	if p.Settings.String("wall_loops") != "3" || !res.Changed[0].Removed {
		t.Fatalf("revert: %q %+v", p.Settings.String("wall_loops"), res.Changed)
	}
	if strings.Contains(strings.Join(p.Settings.List("different_settings_to_system"), "|"), "wall_loops") {
		t.Fatalf("still listed as changed: %v", p.Settings.List("different_settings_to_system"))
	}
	// A per filament setting given once fills every slot.
	if _, err := e.st.UpdateSettings(info.ID, SettingsRequest{Values: map[string]any{"nozzle_temperature": 230}}); err != nil {
		t.Fatal(err)
	}
	p = openSaved(t, e, info.ID)
	if got := p.Settings.List("nozzle_temperature"); len(got) != 2 || got[0] != "230" || got[1] != "230" {
		t.Fatalf("nozzle_temperature = %v", got)
	}
}

func TestUpdateSettingsRefusals(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Refuse")
	revision := func() int { g, _ := e.st.GetProject(info.ID); return g.Revision }
	cases := []struct {
		name string
		req  SettingsRequest
		want string
	}{
		{"unknown key", SettingsRequest{Values: map[string]any{"wall_loopz": 2}}, "wall_loops"},
		{"out of range", SettingsRequest{Values: map[string]any{"wall_loops": 9999}}, "wall_loops"},
		{"bad type", SettingsRequest{Values: map[string]any{"wall_loops": "many"}}, "wall_loops"},
		{"reserved", SettingsRequest{Values: map[string]any{"filament_colour": "#FFFFFF"}}, "set_presets"},
		{"empty", SettingsRequest{Values: map[string]any{}}, "no settings"},
		{"bad scope", SettingsRequest{Scope: "galaxy", Values: map[string]any{"wall_loops": 2}}, "scopes"},
		{"object scope needs a target", SettingsRequest{Scope: ScopeObject, Values: map[string]any{"wall_loops": 2}}, "object"},
		{"nil on a key without preset", SettingsRequest{Values: map[string]any{"nonexistent_zzz": nil}}, "nonexistent_zzz"},
	}
	for _, c := range cases {
		_, err := e.st.UpdateSettings(info.ID, c.req)
		ae := AsError(err)
		if err == nil || (ae.Code != CodeInvalidInput && ae.Code != CodeNotFound) || !strings.Contains(ae.Message+" "+ae.Hint, c.want) {
			t.Errorf("%s: %v", c.name, err)
		}
	}
	// One bad key stops the whole call.
	_, err := e.st.UpdateSettings(info.ID, SettingsRequest{Values: map[string]any{"wall_loops": 2, "nonsense": 1}})
	wantCode(t, err, CodeInvalidInput)
	p := openSaved(t, e, info.ID)
	if p.Settings.String("wall_loops") != "3" || revision() != 1 {
		t.Fatalf("a refused call changed the project: %q rev %d", p.Settings.String("wall_loops"), revision())
	}
}

func TestUpdateSettingsObjectPartRangePlate(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Scopes")
	e.addBox(t, info.ID, "box", 30, 30, 30)
	obj := "box"
	res, err := e.st.UpdateSettings(info.ID, SettingsRequest{Scope: ScopeObject, Target: obj, Values: map[string]any{"wall_loops": 6}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Changed[0].Old != "" || res.Changed[0].New != "6" || res.Info.Objects[0].Overrides != 1 {
		t.Fatalf("object override: %+v", res)
	}
	if v := openSaved(t, e, info.ID).Objects[0].Config.Value("wall_loops"); v != "6" {
		t.Fatalf("saved override %q", v)
	}
	if _, err := e.st.UpdateSettings(info.ID, SettingsRequest{Scope: ScopeObject, Target: obj, Values: map[string]any{"wall_loops": nil}}); err != nil {
		t.Fatal(err)
	}
	if v := openSaved(t, e, info.ID).Objects[0].Config.Value("wall_loops"); v != "" {
		t.Fatalf("override not removed: %q", v)
	}
	// extruder is checked against the filament count.
	_, err = e.st.UpdateSettings(info.ID, SettingsRequest{Scope: ScopeObject, Target: obj, Values: map[string]any{"extruder": 5}})
	wantCode(t, err, CodeInvalidInput)
	// A setting that is not per object.
	_, err = e.st.UpdateSettings(info.ID, SettingsRequest{Scope: ScopeObject, Target: obj, Values: map[string]any{"nozzle_temperature": 200}})
	wantCode(t, err, CodeInvalidInput)

	// Part: a modifier.
	mod, err := e.st.AddModifier(info.ID, ModifierRequest{Object: obj, Shape: ShapeBox, Size: [3]float64{10, 10, 10}, Settings: map[string]any{"wall_loops": 2}})
	if err != nil {
		t.Fatal(err)
	}
	if mod.PartID == 0 || len(mod.Info.Objects[0].Parts) != 2 || mod.Info.Objects[0].Parts[1].Subtype != threemf.SubtypeModifier || mod.Info.Objects[0].Parts[1].Overrides != 1 {
		t.Fatalf("modifier: %+v", mod.Info.Objects[0].Parts)
	}
	if _, err := e.st.UpdateSettings(info.ID, SettingsRequest{Scope: ScopePart, Target: obj + "/modifier", Values: map[string]any{"wall_loops": 4}}); err != nil {
		t.Fatal(err)
	}
	if v := openSaved(t, e, info.ID).Objects[0].Parts[1].Config.Value("wall_loops"); v != "4" {
		t.Fatalf("part override %q", v)
	}
	_, err = e.st.UpdateSettings(info.ID, SettingsRequest{Scope: ScopePart, Target: obj, Values: map[string]any{"wall_loops": 4}})
	wantCode(t, err, CodeInvalidInput)

	// Height range.
	_, err = e.st.UpdateSettings(info.ID, SettingsRequest{Scope: ScopeLayerRange, Target: obj + "/1", Values: map[string]any{"layer_height": 0.1}})
	wantCode(t, err, CodeNotFound)
	rinfo, err := e.st.SetHeightRanges(info.ID, obj, []RangeSpec{{From: 10, To: 20, Settings: map[string]any{"layer_height": 0.1}}})
	if err != nil {
		t.Fatal(err)
	}
	if hr := rinfo.Objects[0].HeightRanges; len(hr) != 1 || hr[0].From != 10 || hr[0].To != 20 || hr[0].Settings["layer_height"] != "0.1" {
		t.Fatalf("ranges: %+v", hr)
	}
	if _, err := e.st.UpdateSettings(info.ID, SettingsRequest{Scope: ScopeLayerRange, Target: obj + "/1", Values: map[string]any{"layer_height": 0.12}}); err != nil {
		t.Fatal(err)
	}
	if got := openSaved(t, e, info.ID).Objects[0].LayerRanges[0].Options.Value("layer_height"); got != "0.12" {
		t.Fatalf("range setting %q", got)
	}
	for _, bad := range [][]RangeSpec{
		{{From: 10, To: 5, Settings: map[string]any{"layer_height": 0.1}}},
		{{From: 0, To: 10, Settings: map[string]any{"layer_height": 0.1}}, {From: 5, To: 15, Settings: map[string]any{"layer_height": 0.1}}},
		{{From: 0, To: 10}},
		{{From: 0, To: 10, Settings: map[string]any{"wall_loopz": 1}}},
	} {
		_, err := e.st.SetHeightRanges(info.ID, obj, bad)
		wantCode(t, err, CodeInvalidInput)
	}
	if cleared, err := e.st.SetHeightRanges(info.ID, obj, nil); err != nil || len(cleared.Objects[0].HeightRanges) != 0 {
		t.Fatalf("clear: %v", err)
	}

	// Plate scope.
	pres, err := e.st.UpdateSettings(info.ID, SettingsRequest{Scope: ScopePlate, Target: "1", Values: map[string]any{"curr_bed_type": "Textured PEI Plate"}})
	if err != nil {
		t.Fatal(err)
	}
	if pres.Info.Plates[0].BedType != "Textured PEI Plate" {
		t.Fatalf("plate bed type %q", pres.Info.Plates[0].BedType)
	}
	if v := openSaved(t, e, info.ID).Plates[0].Config.Value("bed_type"); v != "Textured PEI Plate" {
		t.Fatalf("saved plate key %q", v)
	}
	_, err = e.st.UpdateSettings(info.ID, SettingsRequest{Scope: ScopePlate, Target: "1", Values: map[string]any{"wall_loops": 3}})
	wantCode(t, err, CodeInvalidInput)
	_, err = e.st.UpdateSettings(info.ID, SettingsRequest{Scope: ScopePlate, Target: "4", Values: map[string]any{"curr_bed_type": "Cool Plate"}})
	wantCode(t, err, CodeNotFound)
}

func TestModifierKinds(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Mods")
	e.addBox(t, info.ID, "box", 40, 40, 40)
	for _, c := range []struct {
		req  ModifierRequest
		want string
	}{
		{ModifierRequest{Object: "box", Subtype: "support_blocker", Shape: ShapeCylinder, Size: [3]float64{10, 0, 10}}, threemf.SubtypeSupportBlocker},
		{ModifierRequest{Object: "box", Subtype: "negative", Shape: ShapeSphere, Size: [3]float64{8, 0, 0}}, threemf.SubtypeNegative},
		{ModifierRequest{Object: "box", Subtype: "support_enforcer", Shape: ShapeBox, Size: [3]float64{5, 5, 5}}, threemf.SubtypeSupportEnforcer},
	} {
		res, err := e.st.AddModifier(info.ID, c.req)
		if err != nil {
			t.Fatal(err)
		}
		parts := res.Info.Objects[0].Parts
		if parts[len(parts)-1].Subtype != c.want {
			t.Fatalf("subtype %q, want %q", parts[len(parts)-1].Subtype, c.want)
		}
	}
	// The modifier volumes do not change the object's size.
	got, _ := e.st.GetProject(info.ID)
	if got.Objects[0].Size != [3]float64{40, 40, 40} {
		t.Fatalf("size %v", got.Objects[0].Size)
	}
	for _, bad := range []ModifierRequest{
		{Object: "box", Shape: ShapeBox, Size: [3]float64{5, 5, 5}}, // a modifier without settings
		{Object: "box", Subtype: "negative", Shape: ShapeBox, Size: [3]float64{5, 5, 5}, Settings: map[string]any{"wall_loops": 1}},
		{Object: "box", Subtype: "hole", Shape: ShapeBox, Size: [3]float64{5, 5, 5}},
		{Object: "box", Subtype: "negative", Shape: "cone", Size: [3]float64{5, 5, 5}},
		{Object: "box", Subtype: "negative", Shape: ShapeBox, Size: [3]float64{5, 0, 5}},
		{Object: "box", Subtype: "modifier", Shape: ShapeBox, Size: [3]float64{5, 5, 5}, Settings: map[string]any{"wall_loopz": 1}},
	} {
		_, err := e.st.AddModifier(info.ID, bad)
		wantCode(t, err, CodeInvalidInput)
	}
	_, err := e.st.AddModifier(info.ID, ModifierRequest{Object: "nope", Subtype: "negative", Shape: ShapeBox, Size: [3]float64{5, 5, 5}})
	wantCode(t, err, CodeNotFound)
}

func TestModifierPositionOnScaledObject(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "ModPos")
	e.addBox(t, info.ID, "box", 20, 20, 20)
	if _, err := e.st.UpdateObject(info.ID, UpdateObjectRequest{Object: "box", Scale: &[3]float64{2, 1, 1}}); err != nil {
		t.Fatal(err)
	}
	x, y, z := 90.0, 100.0, 5.0
	if _, err := e.st.AddModifier(info.ID, ModifierRequest{Object: "box", Subtype: "negative", Shape: ShapeBox, Size: [3]float64{10, 10, 10}, X: &x, Y: &y, Z: &z}); err != nil {
		t.Fatal(err)
	}
	// Read the saved part back: in bed coordinates the volume is a 10 mm box at the given centre.
	var seen bool
	if err := e.st.read(info.ID, func(h *handle) error {
		o := h.p.Objects[0]
		item := h.p.ItemsOf(o.ID)[0].Transform
		for _, part := range o.Parts {
			if part.Subtype != threemf.SubtypeNegative {
				continue
			}
			m, err := h.p.LoadMesh(part)
			if err != nil {
				return err
			}
			b, _ := bboxOf(m.Transformed(part.ComponentTransform), item)
			c := center3(b)
			s := size3(b)
			if math.Abs(c[0]-90) > 1e-3 || math.Abs(c[1]-100) > 1e-3 || math.Abs(c[2]-5) > 1e-3 || math.Abs(s[0]-10) > 1e-3 || math.Abs(s[1]-10) > 1e-3 {
				t.Fatalf("modifier in bed coordinates: centre %v size %v", c, s)
			}
			seen = true
		}
		return nil
	}); err != nil || !seen {
		t.Fatalf("modifier not found: %v", err)
	}
}

func TestPlates(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Plates")
	e.addBox(t, info.ID, "one", 20, 20, 10)
	res, err := e.st.ManagePlates(info.ID, PlatesRequest{Action: PlateAdd, Name: "second"})
	if err != nil || len(res.Info.Plates) != 2 || res.Info.Plates[1].Name != "second" {
		t.Fatalf("add: %v %+v", err, res)
	}
	if got := openSaved(t, e, info.ID).Settings.List("wipe_tower_x"); len(got) != 2 {
		t.Fatalf("per plate vector not grown: %v", got)
	}
	// Move an object and take it back.
	if _, err := e.st.ManagePlates(info.ID, PlatesRequest{Action: PlateMove, Object: "one", Plate: 2}); err != nil {
		t.Fatal(err)
	}
	g, _ := e.st.GetProject(info.ID)
	if g.Objects[0].Plate != 2 || g.Plates[1].Objects != 1 || g.Plates[0].Objects != 0 {
		t.Fatalf("after move: %+v", g.Plates)
	}
	_, err = e.st.ManagePlates(info.ID, PlatesRequest{Action: PlateRemove, Plate: 2})
	wantCode(t, err, CodeInvalidInput)
	if _, err := e.st.ManagePlates(info.ID, PlatesRequest{Action: PlateMove, Object: "one", Plate: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.ManagePlates(info.ID, PlatesRequest{Action: PlateRename, Plate: 2, Name: "spare"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.ManagePlates(info.ID, PlatesRequest{Action: PlateLock, Plate: 2}); err != nil {
		t.Fatal(err)
	}
	g, _ = e.st.GetProject(info.ID)
	if g.Plates[1].Name != "spare" || !g.Plates[1].Locked {
		t.Fatalf("rename/lock: %+v", g.Plates[1])
	}
	res, err = e.st.ManagePlates(info.ID, PlatesRequest{Action: PlateRemove, Plate: 2})
	if err != nil || len(res.Info.Plates) != 1 {
		t.Fatalf("remove: %v", err)
	}
	if got := openSaved(t, e, info.ID).Settings.List("wipe_tower_x"); len(got) != 1 {
		t.Fatalf("per plate vector not shrunk: %v", got)
	}
	_, err = e.st.ManagePlates(info.ID, PlatesRequest{Action: PlateRemove, Plate: 1})
	wantCode(t, err, CodeInvalidInput)
	_, err = e.st.ManagePlates(info.ID, PlatesRequest{Action: PlateRemove, Plate: 5})
	wantCode(t, err, CodeNotFound)
	_, err = e.st.ManagePlates(info.ID, PlatesRequest{Action: "shuffle"})
	wantCode(t, err, CodeInvalidInput)
	_, err = e.st.ManagePlates(info.ID, PlatesRequest{Action: PlateRename, Plate: 1})
	wantCode(t, err, CodeInvalidInput)
}

func TestLayerActions(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Actions")
	e.addBox(t, info.ID, "one", 20, 20, 10)
	res, err := e.st.SetLayerActions(info.ID, 1, []LayerAction{
		{Layer: 10, Kind: ActionColorChange, Colour: "#ff0000"},
		{Z: 3.2, Kind: ActionPause},
		{Layer: 20, Kind: ActionToolChange, Filament: 2},
		{Layer: 30, Kind: ActionCustom, GCode: "M117 hi"},
	})
	if err != nil {
		t.Fatal(err)
	}
	acts := res.Plates[0].Actions
	if len(acts) != 4 {
		t.Fatalf("actions: %+v", acts)
	}
	// layer 10 = 0.2 + 9 * 0.2 = 2.0
	if math.Abs(acts[0].Z-2.0) > 1e-9 || acts[0].Layer != 10 || acts[0].Kind != ActionColorChange || acts[0].Colour != "#FF0000" {
		t.Fatalf("first action: %+v", acts[0])
	}
	if acts[1].Kind != ActionPause || math.Abs(acts[1].Z-3.2) > 1e-9 || acts[1].Layer != 16 {
		t.Fatalf("pause: %+v", acts[1])
	}
	// It survives the save.
	gc := openSaved(t, e, info.ID).CustomGCodes(1)
	if len(gc.Items) != 4 || gc.Items[2].Extruder != 2 {
		t.Fatalf("saved gcodes: %+v", gc)
	}
	for _, bad := range [][]LayerAction{
		{{Layer: 5, Kind: "dance"}},
		{{Kind: ActionPause}},
		{{Layer: 5, Z: 1, Kind: ActionPause}},
		{{Layer: 5, Kind: ActionColorChange, Colour: "red"}},
		{{Layer: 5, Kind: ActionToolChange, Filament: 7}},
		{{Layer: 5, Kind: ActionCustom}},
	} {
		_, err := e.st.SetLayerActions(info.ID, 1, bad)
		wantCode(t, err, CodeInvalidInput)
	}
	_, err = e.st.SetLayerActions(info.ID, 3, nil)
	wantCode(t, err, CodeNotFound)
	if cleared, err := e.st.SetLayerActions(info.ID, 1, nil); err != nil || len(cleared.Plates[0].Actions) != 0 {
		t.Fatalf("clear: %v", err)
	}
}

func TestSetPresets(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Presets")
	e.addBox(t, info.ID, "one", 20, 20, 10)
	if _, err := e.st.UpdateSettings(info.ID, SettingsRequest{Values: map[string]any{"wall_loops": 5, "nozzle_temperature": 230}}); err != nil {
		t.Fatal(err)
	}
	// Changing a colour keeps every change and recomputes the flush matrix.
	res, err := e.st.SetPresets(info.ID, PresetsRequest{Filaments: []FilamentSpec{{}, {Colour: "#FFFF00"}}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Info.Filaments[1].Colour != "#FFFF00" || len(res.Dropped) != 0 {
		t.Fatalf("colour change: %+v dropped %v", res.Info.Filaments, res.Dropped)
	}
	p := openSaved(t, e, info.ID)
	if p.Settings.String("wall_loops") != "5" || p.Settings.List("nozzle_temperature")[0] != "230" {
		t.Fatal("changes were lost")
	}
	// Replacing the filament of slot 2 drops that slot's changes, keeps slot 1's, and the process ones stay.
	res, err = e.st.SetPresets(info.ID, PresetsRequest{Filaments: []FilamentSpec{{}, {Preset: testPLA}}})
	if err != nil {
		t.Fatal(err)
	}
	p = openSaved(t, e, info.ID)
	nt := p.Settings.List("nozzle_temperature")
	if nt[0] != "230" || nt[1] != "215" || p.Settings.List("filament_settings_id")[1] != testPLA || p.Settings.String("wall_loops") != "5" {
		t.Fatalf("after replacing slot 2: %v", nt)
	}
	if !strings.Contains(strings.Join(res.Dropped, ","), "nozzle_temperature") {
		t.Fatalf("dropped: %v", res.Dropped)
	}
	// With keep_changes the value moves to the new preset.
	if _, err := e.st.UpdateSettings(info.ID, SettingsRequest{Values: map[string]any{"nozzle_temperature": 225}}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.SetPresets(info.ID, PresetsRequest{Filaments: []FilamentSpec{{}, {Preset: testPETG}}, KeepChanges: true}); err != nil {
		t.Fatal(err)
	}
	if nt := openSaved(t, e, info.ID).Settings.List("nozzle_temperature"); nt[1] != "225" {
		t.Fatalf("keep_changes: %v", nt)
	}
	// Adding a slot, then removing one that an object uses is refused.
	if _, err := e.st.SetPresets(info.ID, PresetsRequest{Filaments: []FilamentSpec{{}, {}, {Preset: testPLA, Colour: "#00FF00"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.UpdateObject(info.ID, UpdateObjectRequest{Object: "one", Filament: ptr(3)}); err != nil {
		t.Fatal(err)
	}
	_, err = e.st.SetPresets(info.ID, PresetsRequest{Filaments: []FilamentSpec{{}, {}}})
	ae := wantCode(t, err, CodeConflict)
	if !strings.Contains(ae.Message, "filament 3") {
		t.Fatalf("message %q", ae.Message)
	}
	// Refusals.
	_, err = e.st.SetPresets(info.ID, PresetsRequest{})
	wantCode(t, err, CodeInvalidInput)
	_, err = e.st.SetPresets(info.ID, PresetsRequest{Process: "0.99mm Nothing"})
	wantCode(t, err, CodeInvalidInput)
	_, err = e.st.SetPresets(info.ID, PresetsRequest{Filaments: []FilamentSpec{{}, {}, {Preset: testOther}}})
	wantCode(t, err, CodeInvalidInput)
	_, err = e.st.SetPresets(info.ID, PresetsRequest{Printer: testPrinter6})
	wantCode(t, err, CodeInvalidInput) // its process and filaments do not fit the 0.6 printer
}

func TestFlushMatrixStaysWhenEdited(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Flush")
	// The colours drive the auto matrix: black to white needs less than white to black.
	if info.FlushMatrix[1] == info.FlushMatrix[2] {
		t.Logf("matrix %v", info.FlushMatrix)
	}
	res, err := e.st.SetPresets(info.ID, PresetsRequest{FlushMultiplier: "2"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Info.FlushMultiplier != "2" {
		t.Fatalf("multiplier %q", res.Info.FlushMultiplier)
	}
	// Recolouring keeps the multiplier the user set.
	res, err = e.st.SetPresets(info.ID, PresetsRequest{Filaments: []FilamentSpec{{}, {Colour: "#FFFF00"}}})
	if err != nil || res.Info.FlushMultiplier != "2" || res.Info.FlushMode != "auto" {
		t.Fatalf("multiplier after recolour: %v %+v", err, res.Info)
	}
}

func TestManualFlushMatrix(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Manual")
	res, err := e.st.SetPresets(info.ID, PresetsRequest{FlushMatrix: []int{0, 111, 222, 0}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Info.FlushMode != "manual" || strings.Join(res.Info.FlushMatrix, ",") != "0,111,222,0" {
		t.Fatalf("manual: %+v", res.Info)
	}
	// The edit survives a recolour, and a wrong size on a third filament falls back to auto.
	res, err = e.st.SetPresets(info.ID, PresetsRequest{Filaments: []FilamentSpec{{}, {Colour: "#00FF00"}}})
	if err != nil || res.Info.FlushMode != "manual" || res.Info.FlushMatrix[1] != "111" {
		t.Fatalf("recolour: %v %+v", err, res.Info)
	}
	res, err = e.st.SetPresets(info.ID, PresetsRequest{Filaments: []FilamentSpec{{}, {}, {Preset: testPLA, Colour: "#FF0000"}}})
	if err != nil || res.Info.FlushMode != "auto" || len(res.Info.FlushMatrix) != 9 || len(res.Warnings) == 0 {
		t.Fatalf("grow: %v %+v %v", err, res.Info.FlushMode, res.Warnings)
	}
	res, err = e.st.SetPresets(info.ID, PresetsRequest{FlushMatrix: make([]int, 9)})
	if err != nil || res.Info.FlushMode != "manual" {
		t.Fatal(err)
	}
	res, err = e.st.SetPresets(info.ID, PresetsRequest{AutoFlush: true})
	if err != nil || res.Info.FlushMode != "auto" {
		t.Fatalf("auto: %v", err)
	}
	for _, bad := range []PresetsRequest{{FlushMatrix: []int{1, 2}}, {FlushMatrix: []int{0, -1, 0, 0, 0, 0, 0, 0, 0}}, {FlushMatrix: make([]int, 9), AutoFlush: true}} {
		_, err := e.st.SetPresets(info.ID, bad)
		wantCode(t, err, CodeInvalidInput)
	}
}

func TestCatalogDriftIsReportedNotWritten(t *testing.T) {
	e := newEnvWith(t, `"zz_future_setting":"1",`)
	drift, err := e.st.CatalogDrift()
	if err != nil || len(drift) != 1 || drift[0] != "zz_future_setting" {
		t.Fatalf("drift: %v %v", drift, err)
	}
	info := e.newProject(t, "Drift")
	if !hasWarning(info, "catalog_drift") || len(info.Drift) != 1 {
		t.Fatalf("warnings %+v drift %v", info.Warnings, info.Drift)
	}
	if _, ok := openSaved(t, e, info.ID).Settings.Get("zz_future_setting"); ok {
		t.Fatal("an unknown setting was written into the project")
	}
	clean := newEnv(t)
	if d, _ := clean.st.CatalogDrift(); len(d) != 0 {
		t.Fatalf("clean bundle drift: %v", d)
	}
}

func TestNewerFile(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Newer")
	e.addBox(t, info.ID, "cube", 20, 20, 20)
	if got, _ := e.st.GetProject(info.ID); hasWarning(got, "newer_file") {
		t.Fatalf("same version warned: %+v", got.Warnings)
	}
	e.st.cfg.Install.Version = "7.1.0" // the file is now from a newer application
	got, _ := e.st.GetProject(info.ID)
	if !hasWarning(got, "newer_file") {
		t.Fatalf("no newer_file warning: %+v", got.Warnings)
	}
	if _, err := e.st.Slice(info.ID, SliceOptions{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(e.exec.lastArgs(), " "), "--allow-newer-file=1") {
		t.Fatalf("args %v", e.exec.lastArgs())
	}
}

// The build items of a project are in one scene in which every plate has an
// origin (TM1): the tools show plate relative positions and write scene ones.
func TestPlateOriginsAreWrittenAndCarried(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Origins")
	if _, err := e.st.ManagePlates(info.ID, PlatesRequest{Action: PlateAdd}); err != nil {
		t.Fatal(err)
	}
	x, y := 100.0, 120.0
	res, err := e.st.AddModel(info.ID, AddModelRequest{Path: writeSTL(t, "cube", 20, 20, 10), Plate: 2, X: &x, Y: &y, Name: "b"})
	if err != nil {
		t.Fatal(err)
	}
	if o := res.Added[0]; o.Plate != 2 || math.Abs(o.Position[0]-100) > 1e-4 || math.Abs(o.Position[1]-120) > 1e-4 {
		t.Fatalf("shown position %+v", o)
	}
	scene := func() [2]float64 {
		p := openSaved(t, e, info.ID)
		tr := p.ItemsOf(p.Objects[len(p.Objects)-1].ID)[0].Transform
		return [2]float64{tr[9], tr[10]}
	}
	// Two plates in two columns: the second is 312 mm (1.2 bed widths) to the right.
	if got := scene(); math.Abs(got[0]-412) > 1e-3 || math.Abs(got[1]-120) > 1e-3 {
		t.Fatalf("scene position %v, want 412, 120", got)
	}
	// The same object moved to plate 1 keeps its place on the plate.
	if _, err := e.st.ManagePlates(info.ID, PlatesRequest{Action: PlateMove, Object: "b", Plate: 1, X: &x, Y: &y}); err != nil {
		t.Fatal(err)
	}
	if got := scene(); math.Abs(got[0]-100) > 1e-3 || math.Abs(got[1]-120) > 1e-3 {
		t.Fatalf("on plate 1: %v", got)
	}
	// And back to plate 2 without a position: automatic placement, still plate relative.
	up, err := e.st.UpdateObject(info.ID, UpdateObjectRequest{Object: "b", Plate: ptr(2)})
	if err != nil {
		t.Fatal(err)
	}
	if up.Object.Plate != 2 || up.Object.Position[0] < 10 || up.Object.Position[0] > 250 {
		t.Fatalf("object %+v", up.Object)
	}
	pos := up.Object.Position
	if got := scene(); math.Abs(got[0]-(pos[0]+312)) > 1e-3 || math.Abs(got[1]-pos[1]) > 1e-3 {
		t.Fatalf("scene %v for plate position %v", got, pos)
	}
	// A third plate: 3 plates sit in two columns, plate 3 in the second row.
	// A fifth plate changes the layout to three columns: objects are carried along.
	for i := 0; i < 3; i++ {
		if _, err := e.st.ManagePlates(info.ID, PlatesRequest{Action: PlateAdd}); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := e.st.GetProject(info.ID)
	if len(got.Plates) != 5 {
		t.Fatalf("plates %d", len(got.Plates))
	}
	if o := got.Objects[0]; o.Plate != 2 || math.Abs(o.Position[0]-pos[0]) > 1e-3 || math.Abs(o.Position[1]-pos[1]) > 1e-3 {
		t.Fatalf("after adding plates: %+v", o)
	}
	if s := scene(); math.Abs(s[0]-(pos[0]+312)) > 1e-3 { // plate 2 stays in column 2
		t.Fatalf("scene %v", s)
	}
	// An object on plate 4 (second row of a 3 column layout, first column): x = plate x, y = -312.
	if _, err := e.st.AddModel(info.ID, AddModelRequest{Path: writeSTL(t, "c", 10, 10, 10), Plate: 4, X: &x, Y: &y, Name: "d"}); err != nil {
		t.Fatal(err)
	}
	if s := scene(); math.Abs(s[0]-100) > 1e-3 || math.Abs(s[1]-(120-312)) > 1e-3 {
		t.Fatalf("plate 4 scene %v", s)
	}
	// Removing the last plate shrinks the layout back; nothing moves on its plate.
	if _, err := e.st.ManagePlates(info.ID, PlatesRequest{Action: PlateRemove, Plate: 5}); err != nil {
		t.Fatal(err)
	}
	got, _ = e.st.GetProject(info.ID)
	for _, o := range got.Objects {
		if o.Name == "d" && (o.Plate != 4 || math.Abs(o.Position[0]-100) > 1e-3 || math.Abs(o.Position[1]-120) > 1e-3) {
			t.Fatalf("d after removing plate 5: %+v", o)
		}
	}
	// The wipe tower of a plate and the picture use plate relative coordinates too.
	if png, err := e.st.Preview(info.ID, PreviewPlate, 2); err != nil || len(png) == 0 {
		t.Fatal(err)
	}
}

// The wipe tower positions are scene coordinates in the file (verified with the
// slicer), plate relative in the tools, and follow the layout of the plates.
func TestWipeTowerPositionsArePlateRelative(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Towers")
	saved := func(key string) []string { return openSaved(t, e, info.ID).Settings.List(key) }
	if got := saved("wipe_tower_x"); len(got) != 1 || got[0] != "15" {
		t.Fatalf("one plate: %v", got)
	}
	if _, err := e.st.ManagePlates(info.ID, PlatesRequest{Action: PlateAdd}); err != nil {
		t.Fatal(err)
	}
	if x, y := saved("wipe_tower_x"), saved("wipe_tower_y"); strings.Join(x, ",") != "15,327" || strings.Join(y, ",") != "220,220" {
		t.Fatalf("two plates: %v %v", x, y)
	}
	res, err := e.st.UpdateSettings(info.ID, SettingsRequest{Values: map[string]any{"wipe_tower_x": []any{20, 30}, "wipe_tower_y": []any{100, 110}}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(saved("wipe_tower_x"), ",") != "20,342" || strings.Join(saved("wipe_tower_y"), ",") != "100,110" {
		t.Fatalf("saved %v %v", saved("wipe_tower_x"), saved("wipe_tower_y"))
	}
	for _, c := range res.Changed {
		if c.Key == "wipe_tower_x" && (c.Old != "[15, 15]" || c.New != "[20, 30]") {
			t.Fatalf("change shown as %q -> %q", c.Old, c.New)
		}
	}
	if v, err := e.st.SettingValue(info.ID, "wipe_tower_x"); err != nil || v.Value != "20, 30" {
		t.Fatalf("setting value %+v %v", v, err)
	}
	_, err = e.st.UpdateSettings(info.ID, SettingsRequest{Values: map[string]any{"wipe_tower_x": []any{20}}})
	ae := wantCode(t, err, CodeInvalidInput)
	if !strings.Contains(ae.Message, "one value per plate") {
		t.Fatalf("message %q", ae.Message)
	}
	// Five plates sit in three columns: plate 2 stays in the second column, plate 3 moves to the third.
	for i := 0; i < 3; i++ {
		if _, err := e.st.ManagePlates(info.ID, PlatesRequest{Action: PlateAdd}); err != nil {
			t.Fatal(err)
		}
	}
	x, y := saved("wipe_tower_x"), saved("wipe_tower_y")
	if x[1] != "342" || x[2] != "639" || y[3] != "-92" || x[3] != "15" {
		t.Fatalf("five plates: %v %v", x, y)
	}
	// Four plates again: two columns.
	if _, err := e.st.ManagePlates(info.ID, PlatesRequest{Action: PlateRemove, Plate: 5}); err != nil {
		t.Fatal(err)
	}
	x, y = saved("wipe_tower_x"), saved("wipe_tower_y")
	if len(x) != 4 || x[2] != "15" || y[2] != "-92" || x[1] != "342" {
		t.Fatalf("four plates: %v %v", x, y)
	}
	// Removing a plate in the middle drops its own tower and renumbers.
	if _, err := e.st.ManagePlates(info.ID, PlatesRequest{Action: PlateRemove, Plate: 2}); err != nil {
		t.Fatal(err)
	}
	if v, _ := e.st.SettingValue(info.ID, "wipe_tower_x"); v.Value != "20, 15, 15" {
		t.Fatalf("after removing plate 2: %q", v.Value)
	}
	// nil goes back to the default on every plate.
	if _, err := e.st.UpdateSettings(info.ID, SettingsRequest{Values: map[string]any{"wipe_tower_x": nil}}); err != nil {
		t.Fatal(err)
	}
	if v, _ := e.st.SettingValue(info.ID, "wipe_tower_x"); v.Value != "15, 15, 15" {
		t.Fatalf("default: %q", v.Value)
	}
	// SetPresets keeps them on their plates.
	if _, err := e.st.UpdateSettings(info.ID, SettingsRequest{Values: map[string]any{"wipe_tower_y": []any{50, 60, 70}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.SetPresets(info.ID, PresetsRequest{Filaments: []FilamentSpec{{}, {Colour: "#00FF00"}}}); err != nil {
		t.Fatal(err)
	}
	if v, _ := e.st.SettingValue(info.ID, "wipe_tower_y"); v.Value != "50, 60, 70" {
		t.Fatalf("after set_presets: %q", v.Value)
	}
}

// Objects keep clear of the wipe tower of their own plate (plate relative).
func TestPlacementAvoidsTheTowerOfThePlate(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Avoid")
	e.st.ManagePlates(info.ID, PlatesRequest{Action: PlateAdd})
	if _, err := e.st.UpdateSettings(info.ID, SettingsRequest{Values: map[string]any{"enable_prime_tower": true, "wipe_tower_x": []any{15, 100}, "wipe_tower_y": []any{220, 100}}}); err != nil {
		t.Fatal(err)
	}
	var tower rect
	e.st.read(info.ID, func(h *handle) error {
		var ok bool
		tower, ok = h.towerRect(2)
		if !ok || tower.x0 != 100 || tower.y0 != 100 {
			t.Fatalf("plate 2 tower %+v %v", tower, ok)
		}
		return nil
	})
	res, err := e.st.AddModel(info.ID, AddModelRequest{Path: writeSTL(t, "c", 40, 40, 10), Copies: 5, Plate: 2})
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range res.Added {
		r := rect{o.Position[0] - 20, o.Position[1] - 20, o.Position[0] + 20, o.Position[1] + 20}
		if r.overlaps(tower) {
			t.Fatalf("%+v overlaps the tower %+v", r, tower)
		}
	}
}

func TestAddModelTakesOnlyTheNamedObjectsOfA3MF(t *testing.T) {
	e := newEnv(t)
	src := e.newProject(t, "Source")
	e.addBox(t, src.ID, "Alpha", 20, 20, 10)
	e.addBox(t, src.ID, "Beta", 30, 30, 10)
	out := filepath.Join(t.TempDir(), "two.3mf")
	if _, err := e.st.Export(src.ID, out, false); err != nil {
		t.Fatal(err)
	}
	dst := e.newProject(t, "Dest")
	res, err := e.st.AddModel(dst.ID, AddModelRequest{Path: out, Objects: []string{"beta"}})
	if err != nil || len(res.Added) != 1 || res.Added[0].Name != "Beta" {
		t.Fatalf("%+v %v", res, err)
	}
	res, err = e.st.AddModel(dst.ID, AddModelRequest{Path: out})
	if err != nil || len(res.Added) != 2 {
		t.Fatalf("all: %+v %v", res, err)
	}
	_, err = e.st.AddModel(dst.ID, AddModelRequest{Path: out, Objects: []string{"Gamma"}})
	ae := wantCode(t, err, CodeInvalidInput)
	if !strings.Contains(ae.Hint, "Alpha") || !strings.Contains(ae.Hint, "Beta") {
		t.Fatalf("hint %q", ae.Hint)
	}
	_, err = e.st.AddModel(dst.ID, AddModelRequest{Path: writeSTL(t, "c", 10, 10, 10), Objects: []string{"x"}})
	wantCode(t, err, CodeInvalidInput)
}

func TestModifierRotation(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Rot")
	e.addBox(t, info.ID, "box", 40, 40, 40)
	x, y, z := 100.0, 100.0, 20.0
	if _, err := e.st.AddModifier(info.ID, ModifierRequest{Object: "box", Subtype: "negative", Shape: ShapeBox, Size: [3]float64{20, 4, 4}, X: &x, Y: &y, Z: &z, Rotation: [3]float64{0, 0, 90}}); err != nil {
		t.Fatal(err)
	}
	if err := e.st.read(info.ID, func(h *handle) error {
		o := h.p.Objects[0]
		item := h.p.ItemsOf(o.ID)[0].Transform
		for _, part := range o.Parts {
			if part.Subtype != threemf.SubtypeNegative {
				continue
			}
			m, err := h.p.LoadMesh(part)
			if err != nil {
				return err
			}
			b, _ := bboxOf(m.Transformed(part.ComponentTransform), item)
			s := size3(b)
			// The 20 x 4 x 4 box turned 90 degrees about Z is 4 x 20 x 4 on the bed.
			if math.Abs(s[0]-4) > 1e-3 || math.Abs(s[1]-20) > 1e-3 || math.Abs(s[2]-4) > 1e-3 {
				t.Fatalf("size %v", s)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
