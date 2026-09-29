package flush

import (
	"math"
	"os"
	"reflect"
	"testing"

	"github.com/sairaph/creality-slicer-mcp/internal/userhome/testhome"
)

func TestMain(m *testing.M) { os.Exit(testhome.Run(m)) }

// The live vectors: K2 0.4, PLA, nozzle_volume 183, no long retraction
// (dev_docs/10-cli.md section 20, P4c).
func TestLiveVectors(t *testing.T) {
	min := mustMin(t, Config{"nozzle_volume": "183", "filament_colour": []string{"#FFFFFF", "#000000"}})
	if !reflect.DeepEqual(min, []int{183, 183}) {
		t.Fatalf("min volumes %v", min)
	}
	m, err := Matrix([]string{"#FFFFFF", "#000000"}, min, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(m, []int{0, 263, 743, 0}) {
		t.Errorf("matrix %v, want the live 0,263,743,0", m)
	}
}

func TestMatrixShapeAndDiagonal(t *testing.T) {
	cols := []string{"#FF0000", "#00FF00", "#0000FF"}
	m, err := Matrix(cols, []int{100, 100, 100}, nil)
	if err != nil || len(m) != 9 {
		t.Fatal(m, err)
	}
	for i := 0; i < 3; i++ {
		if m[3*i+i] != 0 {
			t.Errorf("diagonal %d = %d", i, m[3*i+i])
		}
		for j := 0; j < 3; j++ {
			if i != j && (m[3*i+j] < 160 || m[3*i+j] > maxPairVolume) {
				t.Errorf("m[%d][%d] = %d out of range", i, j, m[3*i+j])
			}
		}
	}
	if m[1] == m[3] {
		t.Log("red->green equals green->red (possible, not required)")
	}
	if empty, err := Matrix(nil, nil, nil); err != nil || len(empty) != 0 {
		t.Errorf("no filaments: %v %v", empty, err)
	}
	one, _ := Matrix([]string{"#123456"}, nil, nil)
	if !reflect.DeepEqual(one, []int{0}) {
		t.Errorf("one filament: %v", one)
	}
}

func TestSameColourAndMinimum(t *testing.T) {
	// Identical colours: pair volume clamps to 60, plus the min flush.
	m, err := Matrix([]string{"#808080", "#808080"}, []int{183, 120}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if m[1] != 60+183 || m[2] != 60+120 {
		t.Errorf("%v", m)
	}
}

func TestMaximumClamp(t *testing.T) {
	m, _ := Matrix([]string{"#000000", "#FFFFFF"}, []int{2000, 0}, nil)
	if m[1] != 1200 {
		t.Errorf("clamp: %v", m)
	}
}

func TestSupportFilamentRules(t *testing.T) {
	cols := []string{"#FFFFFF", "#000000", "#FF0000"}
	min := []int{183, 183, 183}
	plain, _ := Matrix(cols, min, nil)
	m, err := Matrix(cols, min, []bool{false, false, true})
	if err != nil {
		t.Fatal(err)
	}
	// To the support filament (index 2): 230 for everyone.
	if m[0*3+2] != 230 || m[1*3+2] != 230 {
		t.Errorf("to support: %v", m)
	}
	// From the support filament: at least 700, larger values stay.
	m2, _ := Matrix(cols, min, []bool{false, false, true})
	if m2[2*3+0] < 700 || m2[2*3+1] < 700 {
		t.Errorf("from support: %v", m2)
	}
	// Unrelated pairs are unchanged.
	if m[0*3+1] != plain[0*3+1] || m[1*3+0] != plain[1*3+0] {
		t.Errorf("unrelated pairs changed: %v vs %v", m, plain)
	}
	// From support with a computed value above 700 keeps it: black -> white is 743.
	s, _ := Matrix([]string{"#000000", "#FFFFFF"}, []int{183, 183}, []bool{true, false})
	if s[1] != 743 {
		t.Errorf("max(700, 743) = %v", s)
	}
	// From a support filament with a small computed value rises to 700.
	s, _ = Matrix([]string{"#FFFFFF", "#FEFEFE"}, []int{183, 183}, []bool{true, false})
	if s[1] != 700 {
		t.Errorf("small from-support value must become 700: %v", s)
	}
	// Support to support: to-rule then from-rule.
	s, _ = Matrix([]string{"#FFFFFF", "#000000"}, []int{183, 183}, []bool{true, true})
	if s[1] != 700 || s[2] != 700 {
		t.Errorf("support to support: %v", s)
	}
}

func TestAlphaZeroIsWhite(t *testing.T) {
	transparent, err := Matrix([]string{"#123456", "#00000000"}, []int{183, 183}, nil)
	if err != nil {
		t.Fatal(err)
	}
	white, _ := Matrix([]string{"#123456", "#FFFFFF"}, []int{183, 183}, nil)
	if !reflect.DeepEqual(transparent, white) {
		t.Errorf("alpha 0: %v, white: %v", transparent, white)
	}
	// A visible alpha is ignored (colour only).
	opaque, _ := Matrix([]string{"#123456", "#000000FF"}, []int{183, 183}, nil)
	black, _ := Matrix([]string{"#123456", "#000000"}, []int{183, 183}, nil)
	if !reflect.DeepEqual(opaque, black) {
		t.Errorf("alpha ff: %v, black: %v", opaque, black)
	}
}

func TestMatrixErrors(t *testing.T) {
	for name, fn := range map[string]func() error{
		"bad colour":     func() error { _, e := Matrix([]string{"red"}, nil, nil); return e },
		"short colour":   func() error { _, e := Matrix([]string{"#FFF"}, nil, nil); return e },
		"non hex":        func() error { _, e := Matrix([]string{"#GGGGGG"}, nil, nil); return e },
		"min mismatch":   func() error { _, e := Matrix([]string{"#FFFFFF", "#000000"}, []int{1}, nil); return e },
		"support length": func() error { _, e := Matrix([]string{"#FFFFFF"}, nil, []bool{true, false}); return e },
	} {
		if fn() == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestHSV(t *testing.T) {
	for _, c := range []struct {
		in      string
		h, s, v float32
	}{
		{"#FF0000", 0, 1, 1},
		{"#00FF00", 120, 1, 1},
		{"#0000FF", 240, 1, 1},
		{"#FFFF00", 60, 1, 1},
		{"#FF00FF", 300, 1, 1},
		{"#808080", 0, 0, 128.0 / 255},
		{"#000000", 0, 0, 0},
	} {
		col, err := parseColour(c.in)
		if err != nil {
			t.Fatal(err)
		}
		h, s, v := col.hsv()
		if math.Abs(float64(h-c.h)) > 1e-3 || math.Abs(float64(s-c.s)) > 1e-4 || math.Abs(float64(v-c.v)) > 1e-4 {
			t.Errorf("%s: h %v s %v v %v", c.in, h, s, v)
		}
	}
}

func TestMinVolumesLongRetraction(t *testing.T) {
	base := Config{
		"nozzle_volume":                          "183",
		"filament_colour":                        []string{"#FFFFFF", "#000000"},
		"enable_long_retraction_when_cut":        "1",
		"long_retractions_when_cut":              []string{"1"},
		"retraction_distances_when_cut":          []string{"18"},
		"filament_long_retractions_when_cut":     []string{"1", "0"},
		"filament_retraction_distances_when_cut": []string{"10", "nan"},
	}
	// Level 1: the printer distance applies to a filament that opted in, 0 opts out.
	if got := mustMin(t, base); !reflect.DeepEqual(got, []int{139, 183}) {
		t.Errorf("level 1: %v", got)
	}
	// Level 2: the filament's own distance (10), NaN or missing falls back to the printer's.
	base["enable_long_retraction_when_cut"] = "2"
	base["filament_long_retractions_when_cut"] = []string{"1", "1"}
	if got := mustMin(t, base); !reflect.DeepEqual(got, []int{158, 139}) {
		t.Errorf("level 2: %v", got)
	}
	// Printer level 0: no retraction at all.
	base["enable_long_retraction_when_cut"] = "0"
	if got := mustMin(t, base); !reflect.DeepEqual(got, []int{183, 183}) {
		t.Errorf("level 0: %v", got)
	}
	// Printer opts out of long retraction (long_retractions_when_cut[0] != 1).
	base["enable_long_retraction_when_cut"] = "1"
	base["long_retractions_when_cut"] = []string{"0"}
	base["filament_long_retractions_when_cut"] = []string{"1", "1"}
	if got := mustMin(t, base); !reflect.DeepEqual(got, []int{183, 183}) {
		t.Errorf("printer opt-out: %v", got)
	}
}

func TestMinVolumesCountsAndDefaults(t *testing.T) {
	if _, err := MinVolumes(Config{}); err == nil {
		t.Error("a config without nozzle_volume must be an error, not a list of zeros")
	}
	for _, bad := range []any{"", "0", "-5", "abc", []string{}, "NaN"} {
		if got, err := MinVolumes(Config{"nozzle_volume": bad, "filament_colour": []string{"#000000"}}); err == nil {
			t.Errorf("nozzle_volume %#v gave %v", bad, got)
		}
	}
	if got := mustMin(t, Config{"nozzle_volume": "100"}); !reflect.DeepEqual(got, []int{100}) {
		t.Errorf("no filament vectors: %v", got)
	}
	if got := mustMin(t, Config{"nozzle_volume": []string{"183"}, "filament_type": []string{"PLA", "PETG", "ABS"}}); !reflect.DeepEqual(got, []int{183, 183, 183}) {
		t.Errorf("by filament_type: %v", got)
	}
	// The distance is cut to whole millimetres (17.5 -> 17), the result truncated: 183 - 2.4053*17 = 142.1 -> 142.
	cfg := Config{
		"nozzle_volume": "183", "filament_colour": []string{"#000000"},
		"enable_long_retraction_when_cut": "1", "long_retractions_when_cut": []string{"1"},
		"retraction_distances_when_cut": []string{"17.5"}, "filament_long_retractions_when_cut": []string{"nil"},
	}
	if got := mustMin(t, cfg); got[0] != 142 {
		t.Errorf("%v", got)
	}
}

func mustMin(t *testing.T, cfg Config) []int {
	t.Helper()
	got, err := MinVolumes(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// The GUI rule for the K2 (dev_docs D-C1-3): per filament mode (level 2), the
// printer allows long retraction, and the filaments leave the flag unset
// ("nil"): an unset filament follows the printer, so the 30 mm retraction
// applies (183 - 72.2 = 110). This is what the GUI wrote into the golden project.
func TestPerFilamentModeWithUnsetFlagsFollowsThePrinter(t *testing.T) {
	cfg := Config{
		"nozzle_volume":                          "183",
		"filament_colour":                        []string{"#FFFFFF", "#000000", "#FF0000"},
		"enable_long_retraction_when_cut":        "2",
		"long_retractions_when_cut":              []string{"1"},
		"retraction_distances_when_cut":          []string{"30"},
		"filament_long_retractions_when_cut":     []string{"nil", "nil", "nil"},
		"filament_retraction_distances_when_cut": []string{"nil", "nil", "nil"},
	}
	if got := mustMin(t, cfg); !reflect.DeepEqual(got, []int{110, 110, 110}) {
		t.Errorf("%v", got)
	}
	// A missing flag vector counts as all zeros: no retraction.
	delete(cfg, "filament_long_retractions_when_cut")
	if got := mustMin(t, cfg); !reflect.DeepEqual(got, []int{183, 183, 183}) {
		t.Errorf("absent flags: %v", got)
	}
	// 1 asks for it (no own distance: the printer's), 0 opts out, nil follows the printer.
	cfg["filament_long_retractions_when_cut"] = []string{"1", "nil", "0"}
	if got := mustMin(t, cfg); !reflect.DeepEqual(got, []int{110, 110, 183}) {
		t.Errorf("%v", got)
	}
	// Printer level 1: nil follows the printer too; a printer that does not activate it gives 0.
	cfg["enable_long_retraction_when_cut"] = "1"
	cfg["filament_long_retractions_when_cut"] = []string{"nil", "nil", "nil"}
	if got := mustMin(t, cfg); !reflect.DeepEqual(got, []int{110, 110, 110}) {
		t.Errorf("level 1: %v", got)
	}
	cfg["long_retractions_when_cut"] = []string{"0"}
	if got := mustMin(t, cfg); !reflect.DeepEqual(got, []int{183, 183, 183}) {
		t.Errorf("machine off: %v", got)
	}
}

// The golden project (GUI 7.2.2, K2 0.4, three CR-PETG, colours black, yellow,
// white) holds this matrix; the printer values are the K2 preset's.
func TestGoldenProjectMatrix(t *testing.T) {
	cols := []string{"#000000", "#F4E076", "#FFFFFF"}
	min := mustMin(t, Config{
		"nozzle_volume": "183", "filament_colour": cols,
		"enable_long_retraction_when_cut": "2", "long_retractions_when_cut": []string{"1"}, "retraction_distances_when_cut": []string{"30"},
		"filament_long_retractions_when_cut": []string{"nil", "nil", "nil"},
	})
	m, err := Matrix(cols, min, []bool{false, false, false})
	if err != nil {
		t.Fatal(err)
	}
	if want := []int{0, 677, 670, 232, 0, 333, 190, 229, 0}; !reflect.DeepEqual(m, want) {
		t.Fatalf("matrix %v, want the golden %v", m, want)
	}
}
