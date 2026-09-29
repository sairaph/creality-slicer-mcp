package gcodeinfo

import (
	"bytes"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	oneFilament  = "testdata/cube_1filament.gcode"
	twoFilaments = "testdata/cubes_2filaments.gcode"
)

func near(a, b, eps float64) bool { return math.Abs(a-b) <= eps }

func TestSummaryOneFilament(t *testing.T) {
	s, err := ReadSummary(oneFilament)
	if err != nil {
		t.Fatal(err)
	}
	if s.App != "Creality_Print" || s.Version != "7.2.2" || s.Build != "5483" {
		t.Errorf("generator: %q %q %q", s.App, s.Version, s.Build)
	}
	if s.GeneratedAt != "2026-09-29 at 07:03:52" {
		t.Errorf("generated at %q", s.GeneratedAt)
	}
	if s.TotalLayerNumber != 100 || s.LayerCount != 100 || s.TotalLayersCount != 100 {
		t.Errorf("layers: header %d markers %d footer %d", s.TotalLayerNumber, s.LayerCount, s.TotalLayersCount)
	}
	if s.MaxZHeight != 20 || s.UUID != "ff6d678e-dae8-4525-8565-3b7c1ed3e21a" {
		t.Errorf("max z %v uuid %q", s.MaxZHeight, s.UUID)
	}
	if len(s.FilamentDensity) != 1 || s.FilamentDensity[0] != 1.25 || s.FilamentDiameter[0] != 1.75 {
		t.Errorf("density/diameter: %v %v", s.FilamentDensity, s.FilamentDiameter)
	}
	if s.MulticolorMethod == nil || *s.MulticolorMethod != 0 {
		t.Errorf("multicolor_method: %v", s.MulticolorMethod)
	}
	want := Bounds{MinX: 120, MinY: 120, MinZ: 0, MaxX: 140, MaxY: 140, MaxZ: 20}
	if s.Bounds == nil || *s.Bounds != want {
		t.Errorf("bounds: %+v", s.Bounds)
	}
	if s.ObjectsBounding != nil || s.WipeTowerBounding != nil || len(s.Thumbnails) != 0 {
		t.Errorf("unexpected optional header blocks: %v %v %v", s.ObjectsBounding, s.WipeTowerBounding, s.Thumbnails)
	}
	if len(s.Objects) != 1 || s.Objects[0].Name != "cube.stl_id_0_copy_0" ||
		s.Objects[0].CenterX != 130 || s.Objects[0].CenterY != 130 || len(s.Objects[0].Polygon) != 5 ||
		s.Objects[0].Polygon[2] != [2]float64{140, 140} {
		t.Errorf("objects: %+v", s.Objects)
	}
	if len(s.Tools) != 1 || s.Tools[0].Tool != 0 || s.Tools[0].Count != 1 || s.Tools[0].FirstLine != 37 {
		t.Errorf("tools: %+v", s.Tools)
	}
	if s.M8200 {
		t.Error("M8200 must not be reported: it only appears inside config strings")
	}
	if s.TimeText != "21m 25s" || s.TimeSeconds != 21*60+25 {
		t.Errorf("time %q = %d", s.TimeText, s.TimeSeconds)
	}
	if len(s.FilamentUsedG) != 1 || s.FilamentUsedG[0] != 3.58 || s.TotalFilamentG != 3.58 {
		t.Errorf("grams: %v total %v", s.FilamentUsedG, s.TotalFilamentG)
	}
	if len(s.FilamentUsedMM) != 1 || s.FilamentUsedMM[0] != 1190.28 || s.FilamentUsedCM3[0] != 2.86 {
		t.Errorf("used: %v %v", s.FilamentUsedMM, s.FilamentUsedCM3)
	}
	if s.TotalFilamentCost != 0.09 || s.TotalFilamentChange != 0 {
		t.Errorf("cost %v change %d", s.TotalFilamentCost, s.TotalFilamentChange)
	}
	wantFeatures := []string{"Custom", "Inner wall", "Outer wall", "Bottom surface", "Internal solid infill", "Sparse infill", "Internal Bridge", "Top surface"}
	if !sameSet(s.Features, wantFeatures) || s.Features[0] != "Custom" {
		t.Errorf("features: %v", s.Features)
	}
	if s.Lines != 9175 {
		t.Errorf("lines %d", s.Lines)
	}
	if info, _ := os.Stat(oneFilament); s.Size != info.Size() {
		t.Errorf("size %d", s.Size)
	}
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	m := map[string]bool{}
	for _, x := range a {
		m[x] = true
	}
	for _, x := range b {
		if !m[x] {
			return false
		}
	}
	return true
}

func TestConfigBlock(t *testing.T) {
	s, err := ReadSummary(oneFilament)
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		"filament_settings_id": `"CR-PLA @Creality K2 0.4 nozzle"`,
		"print_settings_id":    "0.20mm Standard @Creality K2 0.4 nozzle",
		"filament_type":        "PLA",
		"nozzle_diameter":      "0.4",
		"printer_model":        "Creality K2",
		"first_layer_height":   "0.200",
		"bed_shape":            "0x0,260x0,260x260,0x260",
		"multicolor_method":    "0",
		"filament_ids":         "04001",
		"thumbnails":           "96x96/PNG, 300x300/PNG",
		"filament_diameter":    "1.75",
	} {
		if got, ok := s.Config[key]; !ok || got != want {
			t.Errorf("config[%q] = %q (present %v), want %q", key, got, ok, want)
		}
	}
	if _, ok := s.Config["MINX"]; ok {
		t.Error("header keys leaked into the config map")
	}
	if got, _ := s.Config.Vector("filament_settings_id", ';'); len(got) != 1 || got[0] != "CR-PLA @Creality K2 0.4 nozzle" {
		t.Errorf("vector: %q", got)
	}
	if _, err := s.Config.Vector("nope", ';'); err == nil {
		t.Error("missing key must error")
	}
	if _, err := s.Config.Floats("printer_model", ','); err == nil {
		t.Error("non-numeric value must error")
	}
}

func TestSummaryTwoFilaments(t *testing.T) {
	s, err := ReadSummary(twoFilaments)
	if err != nil {
		t.Fatal(err)
	}
	if s.TotalLayerNumber != 100 || s.LayerCount != 100 {
		t.Errorf("layers %d %d", s.TotalLayerNumber, s.LayerCount)
	}
	if len(s.Tools) != 2 || s.Tools[0] != (ToolUse{Tool: 0, Count: 51, FirstLine: 47}) || s.Tools[1] != (ToolUse{Tool: 1, Count: 50, FirstLine: 311}) {
		t.Errorf("tools: %+v", s.Tools)
	}
	if s.M8200 {
		t.Error("no real M8200 command in this file")
	}
	if len(s.Objects) != 2 || s.Objects[1].Name != "cube_b.stl_id_1_copy_0" || s.Objects[1].CenterY != 119 {
		t.Errorf("objects: %+v", s.Objects)
	}
	if s.TimeText != "3h 44m 0s" || s.TimeSeconds != 3*3600+44*60 {
		t.Errorf("time %q = %d", s.TimeText, s.TimeSeconds)
	}
	if len(s.FilamentUsedG) != 2 || s.FilamentUsedG[0] != 53.86 || s.FilamentUsedG[1] != 23.83 || !near(s.TotalFilamentG, 77.69, 1e-9) {
		t.Errorf("grams %v total %v", s.FilamentUsedG, s.TotalFilamentG)
	}
	if s.TotalFilamentChange != 100 || s.TotalFilamentCost != 1.8 || len(s.FilamentCost) != 2 {
		t.Errorf("change %d cost %v %v", s.TotalFilamentChange, s.TotalFilamentCost, s.FilamentCost)
	}
	if len(s.FilamentDensity) != 2 || s.FilamentDensity[1] != 1.3 {
		t.Errorf("density %v", s.FilamentDensity)
	}
	colours, _ := s.Config.Vector("filament_colour", ';')
	if len(colours) != 2 || colours[0] != "#FFFFFF" || colours[1] != "#000000" {
		t.Errorf("colours %v", colours)
	}
	flush, err := s.Config.Floats("flush_volumes_matrix", ',')
	if err != nil || len(flush) != 4 || flush[1] != 263 || flush[2] != 743 {
		t.Errorf("flush %v %v", flush, err)
	}
	names, _ := s.Config.Vector("filament_settings_id", ';')
	if len(names) != 2 || names[1] != "CR-PLA Matte @Creality K2 0.4 nozzle" {
		t.Errorf("names %q", names)
	}
	if !contains(s.Features, "Prime tower") {
		t.Errorf("features %v", s.Features)
	}
	if s.Bounds == nil || s.Bounds.MaxX != 181.5 || s.Bounds.MaxY != 266.5 {
		t.Errorf("bounds %+v", s.Bounds)
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func TestLayersIndex(t *testing.T) {
	layers, err := Layers(oneFilament)
	if err != nil {
		t.Fatal(err)
	}
	if len(layers) != 100 {
		t.Fatalf("layers %d", len(layers))
	}
	data, _ := os.ReadFile(oneFilament)
	for i, l := range layers {
		if l.Index != i || !near(l.Z, 0.2*float64(i+1), 1e-9) || !near(l.Height, 0.2, 1e-3) {
			t.Fatalf("layer %d: %+v", i, l)
		}
		if !bytes.HasPrefix(data[l.Offset:], []byte(";LAYER_CHANGE\n")) {
			t.Fatalf("layer %d offset %d does not point at ;LAYER_CHANGE", i, l.Offset)
		}
		if i > 0 && layers[i-1].End != l.Offset {
			t.Fatalf("layer %d does not start where %d ends", i, i-1)
		}
	}
	if layers[0].Line != 65 || layers[0].Offset >= layers[1].Offset {
		t.Errorf("first layer %+v", layers[0])
	}
	last := layers[99]
	if !bytes.HasPrefix(data[last.End:], []byte("; EXECUTABLE_BLOCK_END")) {
		t.Errorf("last layer ends at %d: %q", last.End, data[last.End:last.End+30])
	}
	if layers[0].Start.Tool != 0 || layers[0].Start.AbsoluteE || !layers[0].Start.AbsoluteXYZ {
		t.Errorf("start state %+v", layers[0].Start)
	}
}

func TestLayerMovesCube(t *testing.T) {
	layers, err := Layers(oneFilament)
	if err != nil {
		t.Fatal(err)
	}
	moves, err := LayerMoves(oneFilament, layers[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(moves) < 50 {
		t.Fatalf("only %d moves", len(moves))
	}
	extruding, features := 0, map[string]bool{}
	for _, m := range moves {
		features[m.Feature] = true
		if !m.Extruding {
			continue
		}
		extruding++
		if m.Z != 0.2 {
			t.Fatalf("extrusion at z %v: %+v", m.Z, m)
		}
		for _, v := range []float64{m.X0, m.X1, m.Y0, m.Y1} {
			if v < 120-0.01 || v > 140+0.01 {
				t.Fatalf("extrusion coordinate %v outside the cube: %+v", v, m)
			}
		}
		if m.Tool != 0 || m.Speed <= 0 {
			t.Fatalf("tool/speed: %+v", m)
		}
	}
	if extruding < 40 || !features["Inner wall"] || !features["Outer wall"] || !features["Bottom surface"] {
		t.Errorf("extruding %d features %v", extruding, features)
	}
	// The first move of the layer starts where the previous machine state was.
	if moves[0].X0 != layers[0].Start.X || moves[0].Y0 != layers[0].Start.Y {
		t.Errorf("first move %+v vs start %+v", moves[0], layers[0].Start)
	}
}

func TestLayerMovesFilamentTotalMatchesFooter(t *testing.T) {
	layers, err := Layers(oneFilament)
	if err != nil {
		t.Fatal(err)
	}
	var mm float64
	for _, l := range layers {
		moves, err := LayerMoves(oneFilament, l)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range moves {
			if m.Extruding {
				mm += m.E
			}
		}
	}
	// The footer also counts the purge line and the unretracts, hence the margin.
	if mm < 1190.28*0.95 || mm > 1190.28*1.001 {
		t.Errorf("extruded %v mm, footer says 1190.28", mm)
	}
}

func TestLayerMovesTwoFilamentsTools(t *testing.T) {
	layers, err := Layers(twoFilaments)
	if err != nil {
		t.Fatal(err)
	}
	if len(layers) != 100 {
		t.Fatalf("layers %d", len(layers))
	}
	moves, err := LayerMoves(twoFilaments, layers[0])
	if err != nil {
		t.Fatal(err)
	}
	tools := map[int]bool{}
	towers := 0
	for _, m := range moves {
		tools[m.Tool] = true
		if m.Feature == "Prime tower" && m.Extruding {
			towers++
		}
	}
	if !tools[0] || !tools[1] || towers == 0 {
		t.Errorf("tools %v tower moves %d", tools, towers)
	}
}

func write(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "x.gcode")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestAbsoluteExtrusionRelativeMovesAndArcs(t *testing.T) {
	p := write(t, strings.Join([]string{
		"; HEADER_BLOCK_START",
		"; generated by Creality_Print V7.2.2 on today",
		"; EXECUTABLE_BLOCK_START",
		"M82",
		"G90",
		"T2",
		";LAYER_CHANGE",
		";:0.3",
		";HEIGHT:0.3",
		";TYPE:Outer wall",
		"G92 E0",
		"G1 X10 Y10 F3000",
		"G1 X20 Y10 E1.5",
		"G1 X20 Y20 E1.0 ; absolute E goes back: a retraction",
		"G92 E0",
		"G1 X30 Y20 E2",
		"G91",
		"G1 X5 Y5",
		"G90",
		"G3 X40 Y30 I0 J5 E3",
		"G1 Z0.6",
		"; EXECUTABLE_BLOCK_END",
	}, "\r\n")+"\r\n")
	layers, err := Layers(p)
	if err != nil || len(layers) != 1 {
		t.Fatalf("%v %d", err, len(layers))
	}
	moves, err := LayerMoves(p, layers[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(moves) != 6 {
		t.Fatalf("moves: %+v", moves)
	}
	type exp struct {
		x1, y1, e float64
		ext       bool
	}
	for i, w := range []exp{{10, 10, 0, false}, {20, 10, 1.5, true}, {20, 20, -0.5, false}, {30, 20, 2, true}, {35, 25, 0, false}, {40, 30, 1, true}} {
		m := moves[i]
		if m.X1 != w.x1 || m.Y1 != w.y1 || !near(m.E, w.e, 1e-9) || m.Extruding != w.ext {
			t.Errorf("move %d = %+v, want %+v", i, m, w)
		}
		if m.Tool != 2 || m.Feature != "Outer wall" {
			t.Errorf("move %d tool/feature %+v", i, m)
		}
	}
	if !moves[5].Arc || moves[5].Clockwise || moves[5].J != 5 || moves[5].Speed != 3000 {
		t.Errorf("arc %+v", moves[5])
	}
	if layers[0].Z != 0.3 || layers[0].Height != 0.3 {
		t.Errorf("layer %+v", layers[0])
	}
	s, _ := ReadSummary(p)
	if len(s.Tools) != 1 || s.Tools[0].Tool != 2 || s.Version != "7.2.2" || s.Build != "" {
		t.Errorf("%+v", s)
	}
}

func TestVeryLongLinesAndNoTrailingNewline(t *testing.T) {
	long := strings.Repeat("x", 600<<10)
	p := write(t, "; EXECUTABLE_BLOCK_END\n; CONFIG_BLOCK_START\n; huge = "+long+"\n; small = 1\n; CONFIG_BLOCK_END\n; last_key = 2")
	s, err := ReadSummary(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Config["huge"]) != len(long) || s.Config["small"] != "1" || s.Config["last_key"] != "2" || s.Lines != 6 {
		t.Errorf("config keys %d, small %q last %q lines %d", len(s.Config["huge"]), s.Config["small"], s.Config["last_key"], s.Lines)
	}
}

func TestTrailingKeyValueLinesWithoutConfigBlock(t *testing.T) {
	p := write(t, "; EXECUTABLE_BLOCK_START\nG1 X1\n; EXECUTABLE_BLOCK_END\n; filament used [g] = 1.5, 2.5\n; estimated printing time (normal mode) = 1d 2h 3m 4s\n; printer_model = Creality K2\n")
	s, err := ReadSummary(p)
	if err != nil {
		t.Fatal(err)
	}
	if s.Config["printer_model"] != "Creality K2" || s.TotalFilamentG != 4 || s.TimeSeconds != 86400+7200+180+4 {
		t.Errorf("%+v", s)
	}
}

func TestHeaderBlocksAndThumbnails(t *testing.T) {
	p := write(t, strings.Join([]string{
		"; HEADER_BLOCK_START",
		"; total layer number: 3",
		"; HEADER_BLOCK_END",
		"; OBJECTS_BOUNDING = 1,2,0,3,4,5",
		"; WIPE_TOWER_BOUNDING = 10,20,0,30,40,5",
		"; THUMBNAIL_BLOCK_START",
		"; thumbnail begin 96x96 1234",
		"; AAAA",
		"; thumbnail end",
		"; THUMBNAIL_BLOCK_END",
		"; multicolor_method = 1",
		"; EXECUTABLE_BLOCK_START",
		"M8200 P1",
		"; EXECUTABLE_BLOCK_END",
	}, "\n"))
	s, err := ReadSummary(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.ObjectsBounding) != 6 || s.ObjectsBounding[3] != 3 || len(s.WipeTowerBounding) != 6 || s.WipeTowerBounding[1] != 20 {
		t.Errorf("bounding %v %v", s.ObjectsBounding, s.WipeTowerBounding)
	}
	if len(s.Thumbnails) != 1 || s.Thumbnails[0] != (Thumbnail{Width: 96, Height: 96, Length: 1234, Line: 7}) {
		t.Errorf("thumbnails %+v", s.Thumbnails)
	}
	if !s.M8200 || s.MulticolorMethod == nil || *s.MulticolorMethod != 1 {
		t.Errorf("m8200 %v method %v", s.M8200, s.MulticolorMethod)
	}
}

func TestParseFloat(t *testing.T) {
	for in, want := range map[string]float64{"1": 1, "-1": -1, ".3742": 0.3742, "12.5": 12.5, "+3": 3, "1e3": 1000, "0.600": 0.6, "-.5": -0.5} {
		got, ok := parseFloat([]byte(in))
		if !ok || got != want {
			t.Errorf("parseFloat(%q) = %v %v, want %v", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "-", ".", "abc", "1.2.3"} {
		if _, ok := parseFloat([]byte(in)); ok {
			t.Errorf("parseFloat(%q) must fail", in)
		}
	}
}

func TestParseDuration(t *testing.T) {
	for in, want := range map[string]int{"21m 25s": 1285, "1h 2m 3s": 3723, "3h 44m 0s": 13440, "45s": 45, "2d 1h": 176400, "": 0, "n/a": 0} {
		if got := ParseDuration(in); got != want {
			t.Errorf("ParseDuration(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestMissingFile(t *testing.T) {
	if _, err := ReadSummary(filepath.Join(t.TempDir(), "none.gcode")); err == nil {
		t.Error("expected an error")
	}
}
