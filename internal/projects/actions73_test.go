package projects

import (
	"math"
	"os"
	"strconv"
	"testing"
)

// The layer z line is ";:<z>" in 7.2 and ";Z:<z>" in 7.3; both must confirm
// actions at the right height.
func TestScanActionsBothZFormats(t *testing.T) {
	custom := "M117 FIXTURE_ONE\nM117 FIXTURE_TWO"
	actions := []ActionInfo{
		{Layer: 25, Z: 5, Kind: ActionPause},
		{Layer: 50, Z: 10, Kind: ActionCustom, GCode: custom},
	}
	got := scanActions("../gcodeinfo/testdata/cubes_2filaments_73.gcode", actions)
	if len(got) != 2 {
		t.Fatalf("results %+v", got)
	}
	for _, r := range got {
		if !r.Found || r.AtZ < r.Z-0.011 || r.AtZ > r.Z+0.211 {
			t.Errorf("action not confirmed at its height: %+v", r)
		}
	}
}

// D4: objects are placed around the bed centre, each further out than the
// ones before, never in a corner.
func TestPlacementSpiralsOutward(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Spiral")
	last := -1.0
	for i := 0; i < 7; i++ {
		p := e.addBox(t, info.ID, "b"+strconv.Itoa(i), 30, 30, 10).Added[0].Position
		dist := math.Hypot(p[0]-130, p[1]-130)
		if dist < last-1e-6 {
			t.Fatalf("object %d at %v is nearer the centre than the previous one", i, p)
		}
		last = dist
		if i == 0 && dist > 1e-3 {
			t.Fatalf("first object at %v", p)
		}
		if i > 0 && dist > 150 {
			t.Fatalf("object %d far out at %v", i, p)
		}
		if p[0] < 40 && p[1] < 40 {
			t.Fatalf("object %d in the corner at %v", i, p)
		}
	}
}

func TestPrimeTowerWarning(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Tower")
	// The test process preset leaves the tower off (Creality's K2 presets turn it
	// on); switching it on is what the warning depends on.
	if hasWarning(info, "prime_tower") {
		t.Fatalf("tower off: %+v", info.Warnings)
	}
	up0, err := e.st.UpdateSettings(info.ID, SettingsRequest{Values: map[string]any{"enable_prime_tower": true}})
	if err != nil || !hasWarning(up0.Info, "prime_tower") {
		t.Fatalf("set_presets/update_settings with several filaments: %v %+v", err, up0.Info.Warnings)
	}
	e.addBox(t, info.ID, "a", 20, 20, 10)
	if got, _ := e.st.GetProject(info.ID); hasWarning(got, "prime_tower") {
		t.Fatalf("one filament used: %+v", got.Warnings)
	}
	if _, err := e.st.AddModel(info.ID, AddModelRequest{Path: writeSTL(t, "b", 20, 20, 10), Filament: 2}); err != nil {
		t.Fatal(err)
	}
	got, _ := e.st.GetProject(info.ID)
	if !hasWarning(got, "prime_tower") {
		t.Fatalf("get_project with two filaments in use: %+v", got.Warnings)
	}
	up, err := e.st.UpdateSettings(info.ID, SettingsRequest{Values: map[string]any{"print_sequence": "by object"}})
	if err != nil || hasWarning(up.Info, "prime_tower") {
		t.Fatalf("by object: %v %+v", err, up.Info.Warnings)
	}
	up, err = e.st.UpdateSettings(info.ID, SettingsRequest{Values: map[string]any{"print_sequence": "by layer"}})
	if err != nil || !hasWarning(up.Info, "prime_tower") {
		t.Fatalf("by layer again: %v %+v", err, up.Info.Warnings)
	}
	up, err = e.st.UpdateSettings(info.ID, SettingsRequest{Values: map[string]any{"enable_prime_tower": false}})
	if err != nil || hasWarning(up.Info, "prime_tower") {
		t.Fatalf("tower off: %v %+v", err, up.Info.Warnings)
	}
}

func TestSliceReportsThePrimeTower(t *testing.T) {
	data, err := os.ReadFile("../gcodeinfo/testdata/cubes_2filaments_73.gcode")
	if err != nil {
		t.Fatal(err)
	}
	e := newEnv(t)
	info := e.newProject(t, "TowerSlice")
	e.addBox(t, info.ID, "a", 20, 20, 10)
	if _, err := e.st.AddModel(info.ID, AddModelRequest{Path: writeSTL(t, "b", 20, 20, 10), Filament: 2}); err != nil {
		t.Fatal(err)
	}
	e.exec.gcode = func(int) string { return string(data) }
	res, err := e.st.Slice(info.ID, SliceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	p := res.Last.Plates[0]
	if p.PrimeTowerG < 0.5 || p.PrimeTowerG >= p.TotalG || p.PrimeTowerS <= 0 {
		t.Fatalf("prime tower %.2f g, %d s of %.2f g in total", p.PrimeTowerG, p.PrimeTowerS, p.TotalG)
	}
}

// On 7.2 a multi-filament by-layer plate crashes: only the crash warning shows.
func TestPrimeTowerWarningNotOn72(t *testing.T) {
	e := newEnv(t)
	e.st.cfg.Install.Dialect = "v72"
	info := e.newProject(t, "Tower72")
	up, err := e.st.UpdateSettings(info.ID, SettingsRequest{Values: map[string]any{"enable_prime_tower": true}})
	if err != nil {
		t.Fatal(err)
	}
	if !hasWarning(up.Info, "v72_by_layer_crash") || hasWarning(up.Info, "prime_tower") {
		t.Fatalf("warnings %+v", up.Info.Warnings)
	}
}
