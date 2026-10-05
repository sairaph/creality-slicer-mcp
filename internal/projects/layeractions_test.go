package projects

import (
	"archive/zip"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sairaph/creality-slicer-mcp/internal/threemf"
)

func gcodeXML(t *testing.T, e *testEnv, id string) string {
	t.Helper()
	zr, err := zip.OpenReader(filepath.Join(e.st.dir(id), projectFile))
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.Name == "Metadata/custom_gcode_per_layer.xml" {
			rc, err := f.Open()
			if err != nil {
				t.Fatal(err)
			}
			defer rc.Close()
			var sb strings.Builder
			buf := make([]byte, 4096)
			for {
				n, rerr := rc.Read(buf)
				sb.Write(buf[:n])
				if rerr != nil {
					break
				}
			}
			return sb.String()
		}
	}
	t.Fatal("no custom_gcode_per_layer.xml in the project")
	return ""
}

// The K2 has no colour change G-code: a colour change becomes a filament (tool)
// change, as the app writes it for the CFS.
func TestColourChangeBecomesAToolChangeWithoutGCode(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "CFS")
	e.addBox(t, info.ID, "cube", 20, 20, 20)
	// A plate left in the single extruder mode is switched for the tool change.
	if err := e.st.write(info.ID, func(h *handle) error {
		h.touchPlate(1)
		return h.p.SetCustomGCodes(1, threemf.ModeSingleExtruder, nil)
	}); err != nil {
		t.Fatal(err)
	}
	res, err := e.st.SetLayerActions(info.ID, 1, []LayerAction{{Layer: 10, Kind: ActionColorChange, Filament: 2, Colour: "#123456"}})
	if err != nil {
		t.Fatal(err)
	}
	a := res.Plates[0].Actions[0]
	if a.Kind != ActionToolChange || a.Filament != 2 || a.Colour != "#000000" {
		t.Fatalf("stored action %+v, want a tool change to filament 2 in its colour", a)
	}
	if hasWarning(res, "color_change_no_gcode") {
		t.Errorf("a stored tool change warns: %+v", res.Warnings)
	}
	gc := openSaved(t, e, info.ID).CustomGCodes(1)
	if gc.Mode != threemf.ModeMultiExtruder || len(gc.Items) != 1 || gc.Items[0].Type != threemf.GCodeToolChange || gc.Items[0].Extruder != 2 || gc.Items[0].Color != "#000000" {
		t.Fatalf("saved %+v", gc)
	}
	x := gcodeXML(t, e, info.ID)
	for _, want := range []string{`type="2"`, `extruder="2"`, `color="#000000"`, `value="MultiExtruder"`} {
		if !strings.Contains(x, want) {
			t.Errorf("custom_gcode_per_layer.xml lacks %s:\n%s", want, x)
		}
	}
	// An explicit tool change gets the filament's colour too.
	res, err = e.st.SetLayerActions(info.ID, 1, []LayerAction{{Layer: 5, Kind: ActionToolChange, Filament: 1}})
	if err != nil || res.Plates[0].Actions[0].Colour != "#FFFFFF" {
		t.Fatalf("tool change: %v %+v", err, res.Plates[0].Actions)
	}
}

func TestColourChangeNeedsASecondFilamentWithoutGCode(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "One", FilamentSpec{Preset: testPLA, Colour: "#FFFFFF"})
	e.addBox(t, info.ID, "cube", 20, 20, 20)
	_, err := e.st.SetLayerActions(info.ID, 1, []LayerAction{{Layer: 10, Kind: ActionColorChange, Colour: "#FF0000"}})
	ae := wantCode(t, err, CodeInvalidInput)
	if !strings.Contains(ae.Message, "no colour change G-code (M600), so a colour change needs a second filament slot") || !strings.Contains(ae.Hint, "set_presets") {
		t.Fatalf("error %q hint %q", ae.Message, ae.Hint)
	}
}

// A printer preset with colour change G-code keeps the classic colour change.
func TestColourChangeStaysWithGCode(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Classic")
	e.addBox(t, info.ID, "cube", 20, 20, 20)
	if err := e.st.write(info.ID, func(h *handle) error {
		h.p.Settings.SetString("color_change_gcode", "M600")
		h.touchAll()
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	res, err := e.st.SetLayerActions(info.ID, 1, []LayerAction{{Layer: 10, Kind: ActionColorChange, Filament: 2, Colour: "#ff0000"}})
	if err != nil {
		t.Fatal(err)
	}
	a := res.Plates[0].Actions[0]
	if a.Kind != ActionColorChange || a.Colour != "#FF0000" {
		t.Fatalf("stored %+v", a)
	}
	if x := gcodeXML(t, e, info.ID); !strings.Contains(x, `type="0"`) {
		t.Errorf("not type 0:\n%s", x)
	}
	if hasWarning(res, "color_change_no_gcode") {
		t.Errorf("warning although the printer has the G-code: %+v", res.Warnings)
	}
}

// A tool change in the G-code is found at its layer (7.3 writes a plain T line
// after the layer's ;Z: line).
func TestToolChangeActionFoundInRealG73Output(t *testing.T) {
	acts := []ActionInfo{
		{Layer: 25, Z: 5, Kind: ActionToolChange, Filament: 2},
		{Layer: 30, Z: 6, Kind: ActionToolChange, Filament: 5},
	}
	got := scanActions("../gcodeinfo/testdata/cubes_2filaments_73.gcode", acts)
	if len(got) != 2 || !got[0].Found || got[0].AtZ < 5 || got[1].Found {
		t.Fatalf("results %+v", got)
	}
}

// Creality Print drops layer tool changes unless every object of the plate
// prints with one filament.
func TestLayerToolChangeIgnoredWithMixedFilaments(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Mixed")
	e.addBox(t, info.ID, "a", 20, 20, 20)
	if _, err := e.st.AddModel(info.ID, AddModelRequest{Path: writeSTL(t, "b", 20, 20, 20), Filament: 2}); err != nil {
		t.Fatal(err)
	}
	res, err := e.st.SetLayerActions(info.ID, 1, []LayerAction{{Layer: 5, Kind: ActionColorChange, Filament: 2}, {Layer: 8, Kind: ActionToolChange, Filament: 1}})
	if err != nil {
		t.Fatalf("nothing is refused: %v", err)
	}
	var msg string
	for _, w := range res.Warnings {
		if w.Code == "layer_tool_change_ignored" {
			msg = w.Message
		}
	}
	for _, want := range []string{"layer(s) 5, 8 on plate 1 have no effect", "only when every object on the plate prints with one filament", "filaments 1, 2", "their own plate"} {
		if !strings.Contains(msg, want) {
			t.Errorf("warning %q lacks %q", msg, want)
		}
	}
	// A role filament on one object counts too.
	one := e.newProject(t, "RoleFilament")
	e.addBox(t, one.ID, "a", 20, 20, 20)
	if _, err := e.st.UpdateSettings(one.ID, SettingsRequest{Scope: "object", Target: "a", Values: map[string]any{"wall_filament": 2}}); err != nil {
		t.Fatal(err)
	}
	res, err = e.st.SetLayerActions(one.ID, 1, []LayerAction{{Layer: 5, Kind: ActionToolChange, Filament: 2}})
	if err != nil || !hasWarning(res, "layer_tool_change_ignored") {
		t.Fatalf("role filament: %v %+v", err, res.Warnings)
	}
}

func TestLayerToolChangeNotIgnoredWithOneFilament(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Single")
	e.addBox(t, info.ID, "a", 20, 20, 20)
	e.addBox(t, info.ID, "b", 20, 20, 20)
	res, err := e.st.SetLayerActions(info.ID, 1, []LayerAction{{Layer: 5, Kind: ActionColorChange, Filament: 2}})
	if err != nil {
		t.Fatal(err)
	}
	if hasWarning(res, "layer_tool_change_ignored") {
		t.Fatalf("warning for one filament: %+v", res.Warnings)
	}
	// No tool change at all: no warning either.
	mixed := e.newProject(t, "NoChange")
	e.addBox(t, mixed.ID, "a", 20, 20, 20)
	if _, err := e.st.AddModel(mixed.ID, AddModelRequest{Path: writeSTL(t, "b", 20, 20, 20), Filament: 2}); err != nil {
		t.Fatal(err)
	}
	res, err = e.st.SetLayerActions(mixed.ID, 1, []LayerAction{{Layer: 5, Kind: ActionPause}})
	if err != nil || hasWarning(res, "layer_tool_change_ignored") {
		t.Fatalf("pause only: %v %+v", err, res.Warnings)
	}
}

func ignoredAfterTool(t *testing.T, e *testEnv, id string) bool {
	t.Helper()
	res, err := e.st.SetLayerActions(id, 1, []LayerAction{{Layer: 5, Kind: ActionToolChange, Filament: 2}})
	if err != nil {
		t.Fatal(err)
	}
	return hasWarning(res, "layer_tool_change_ignored")
}

// Supports go through support_material_extruders, which the layer tool change
// code does not consult: no warning for a support filament.
func TestSupportFilamentDoesNotIgnoreToolChanges(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Support")
	e.addBox(t, info.ID, "a", 20, 20, 20)
	if _, err := e.st.UpdateSettings(info.ID, SettingsRequest{Scope: "object", Target: "a", Values: map[string]any{"support_filament": 2, "support_interface_filament": 2}}); err != nil {
		t.Fatal(err)
	}
	if ignoredAfterTool(t, e, info.ID) {
		t.Fatal("a support filament made the tool change ignored")
	}
}

// Only colour painting adds filaments.
func TestOnlyColourPaintingCountsForTheIgnoredWarning(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Painted")
	e.addBox(t, info.ID, "a", 20, 20, 20)
	check := func(p threemf.Painted) bool {
		var painted bool
		if err := e.st.read(info.ID, func(h *handle) error {
			h.p.Objects[0].Parts[0].Mesh.Painted = p
			_, painted = h.plateExtruders(1)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return painted
	}
	for name, p := range map[string]threemf.Painted{"seam": {Seam: true}, "supports": {Supports: true}, "fuzzy": {FuzzySkin: true}, "face": {FaceProperty: true}} {
		if check(p) {
			t.Errorf("%s painting counted as colour", name)
		}
	}
	if !check(threemf.Painted{Color: true}) {
		t.Error("colour painting not counted")
	}
}

// Mirror of PrintRegion::collect_object_printing_extruders: a role filament only
// counts when the region prints that feature.
func TestRoleFilamentCountsOnlyWhereItPrints(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Roles")
	e.addBox(t, info.ID, "a", 20, 20, 20)
	fils := func() []int {
		var f []int
		if err := e.st.read(info.ID, func(h *handle) error { f, _ = h.plateExtruders(1); return nil }); err != nil {
			t.Fatal(err)
		}
		return f
	}
	set := func(v map[string]any) {
		t.Helper()
		if _, err := e.st.UpdateSettings(info.ID, SettingsRequest{Scope: "object", Target: "a", Values: v}); err != nil {
			t.Fatal(err)
		}
	}
	set(map[string]any{"wall_filament": 2, "wall_loops": 0, "brim_type": "no_brim"}) // a brim also prints with the wall filament
	if f := fils(); len(f) != 1 {
		t.Fatalf("wall filament without walls: %v", f)
	}
	if ignoredAfterTool(t, e, info.ID) {
		t.Fatal("warning for a wall filament on an object without walls")
	}
	set(map[string]any{"wall_loops": 2})
	if f := fils(); len(f) != 2 {
		t.Fatalf("wall filament with walls: %v", f)
	}
	set(map[string]any{"wall_filament": nil, "wall_loops": nil, "sparse_infill_filament": 2, "sparse_infill_density": "0%"})
	if f := fils(); len(f) != 1 {
		t.Fatalf("infill filament without infill: %v", f)
	}
	set(map[string]any{"sparse_infill_density": "15%"})
	if f := fils(); len(f) != 2 {
		t.Fatalf("infill filament with infill: %v", f)
	}
	set(map[string]any{"sparse_infill_filament": nil, "solid_infill_filament": 2, "top_shell_layers": 0, "bottom_shell_layers": 0})
	if f := fils(); len(f) != 1 {
		t.Fatalf("solid filament without shells: %v", f)
	}
	set(map[string]any{"top_shell_layers": 3})
	if f := fils(); len(f) != 2 {
		t.Fatalf("solid filament with shells: %v", f)
	}
}
