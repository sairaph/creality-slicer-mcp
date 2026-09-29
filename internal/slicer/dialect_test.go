package slicer

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func testLookup(key string) (string, bool, bool) {
	switch key {
	case "wall_loops":
		return "wall-loops", false, true
	case "enable_support":
		return "enable-support", true, true
	case "layer_height":
		return "--layer-height", false, true
	case "help":
		return "help|h", true, true
	case "nocli_option":
		return "", false, false
	case "datadir":
		return "datadir", false, true
	case "z_offset":
		return "", false, true // derive from the key
	}
	return "", false, false
}

func v72Dialect(t *testing.T) Dialect {
	t.Helper()
	d, err := NewDialect("v72", testLookup)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func intp(n int) *int { return &n }

func TestBuildSliceArgsProject(t *testing.T) {
	d := v72Dialect(t)
	in := filepath.Join(t.TempDir(), "job1.3MF")
	out := filepath.Join(t.TempDir(), "out")
	logFile := filepath.Join(out, "slice.log")
	got, err := d.BuildSliceArgs(SliceRequest{Inputs: []string{in}, Plate: 1, OutputDir: out, LogFile: logFile})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"--slice", "1", "--outputdir", out, "--debug", "3", "--logfile", logFile, in}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

func TestBuildSliceArgsSTLRouteFull(t *testing.T) {
	d := v72Dialect(t)
	got, err := d.BuildSliceArgs(SliceRequest{
		Inputs:          []string{abs("in/a.stl"), abs("in/b.stl")},
		Plate:           0,
		OutputDir:       abs("out"),
		DebugLevel:      5,
		Settings:        []string{abs("p/machine.json"), abs("p/process.json")},
		Filaments:       []string{abs("p/f1.json"), abs("p/f2.json")},
		FilamentIDs:     []int{1, 2},
		FilamentColours: []string{"#FFFFFF", "#000000"},
		Arrange:         intp(1),
		Orient:          intp(0),
		Overrides:       map[string]string{"wall_loops": "3", "enable_support": "true", "layer_height": "0.16", "z_offset": "-0.1"},
		SkipObjects:     []int{3, 5},
		AllowNewer:      true,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"--slice", "0", "--outputdir", abs("out"), "--debug", "5",
		"--load-settings", abs("p/machine.json") + ";" + abs("p/process.json"),
		"--load-filaments", abs("p/f1.json") + ";" + abs("p/f2.json"),
		"--load-filament-ids", "1,2",
		"--filament-colour", "#FFFFFF;#000000",
		"--arrange", "1", "--orient", "0",
		"--enable-support=1", "--layer-height", "0.16", "--wall-loops", "3", "--z-offset", "-0.1",
		"--skip-objects", "3,5",
		"--allow-newer-file=1",
		abs("in/a.stl"), abs("in/b.stl"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

func TestBuildSliceArgsBoolAliasAndFalse(t *testing.T) {
	d := v72Dialect(t)
	got, err := d.BuildSliceArgs(SliceRequest{Inputs: []string{abs("a.stl")}, OutputDir: abs("o"), Overrides: map[string]string{"help": "0"}})
	if err != nil {
		t.Fatal(err)
	}
	if !contains(got, "--help=0") {
		t.Errorf("%q", got)
	}
	got, _ = d.BuildSliceArgs(SliceRequest{Inputs: []string{abs("a.stl")}, OutputDir: abs("o"), Overrides: map[string]string{"enable_support": "No"}})
	if !contains(got, "--enable-support=0") {
		t.Errorf("%q", got)
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

func TestBuildSliceArgsRejects(t *testing.T) {
	d := v72Dialect(t)
	base := func() SliceRequest {
		return SliceRequest{Inputs: []string{abs("a.stl")}, OutputDir: abs("o")}
	}
	cases := map[string]func(*SliceRequest){
		"no inputs":        func(r *SliceRequest) { r.Inputs = nil },
		"negative plate":   func(r *SliceRequest) { r.Plate = -1 },
		"no output dir":    func(r *SliceRequest) { r.OutputDir = " " },
		"bad debug":        func(r *SliceRequest) { r.DebugLevel = 9 },
		"3mf not first":    func(r *SliceRequest) { r.Inputs = []string{abs("a.stl"), abs("b.3mf")} },
		"dash input":       func(r *SliceRequest) { r.Inputs = []string{"--export-3mf"} },
		"newline in input": func(r *SliceRequest) { r.Inputs = []string{abs("a\n.stl")} },
		"ids with project": func(r *SliceRequest) {
			r.Inputs = []string{abs("a.3mf")}
			r.FilamentIDs = []int{1}
			r.Filaments = []string{abs("f.json")}
		},
		"ids count":            func(r *SliceRequest) { r.FilamentIDs = []int{1, 2}; r.Filaments = []string{abs("f.json")} },
		"ids without filament": func(r *SliceRequest) { r.FilamentIDs = []int{1} },
		"ids zero":             func(r *SliceRequest) { r.FilamentIDs = []int{0}; r.Filaments = []string{abs("f.json")} },
		"semicolon path":       func(r *SliceRequest) { r.Settings = []string{abs("a;b.json")} },
		"empty path":           func(r *SliceRequest) { r.Filaments = []string{" "} },
		"bad colour":           func(r *SliceRequest) { r.FilamentColours = []string{"red"} },
		"short colour":         func(r *SliceRequest) { r.FilamentColours = []string{"#FFF"} },
		"unknown override":     func(r *SliceRequest) { r.Overrides = map[string]string{"nocli_option": "1"} },
		"unknown key":          func(r *SliceRequest) { r.Overrides = map[string]string{"no_such_key": "1"} },
		"forbidden override":   func(r *SliceRequest) { r.Overrides = map[string]string{"datadir": abs("x")} },
		"empty value":          func(r *SliceRequest) { r.Overrides = map[string]string{"wall_loops": " "} },
		"value newline":        func(r *SliceRequest) { r.Overrides = map[string]string{"wall_loops": "3\n--pipe x"} },
		"bad bool":             func(r *SliceRequest) { r.Overrides = map[string]string{"enable_support": "maybe"} },
		"logfile newline":      func(r *SliceRequest) { r.LogFile = abs("l\nog") },
	}
	for name, mutate := range cases {
		r := base()
		mutate(&r)
		if _, err := d.BuildSliceArgs(r); err == nil || !errors.Is(err, ErrInvalidRequest) {
			t.Errorf("%s: want ErrInvalidRequest, got %v", name, err)
		}
	}
}

func TestOverridesWithoutCatalog(t *testing.T) {
	d, _ := NewDialect("v72", nil)
	_, err := d.BuildSliceArgs(SliceRequest{Inputs: []string{abs("a.stl")}, OutputDir: abs("o"), Overrides: map[string]string{"wall_loops": "3"}})
	if err == nil {
		t.Error("overrides need a catalog lookup")
	}
	if _, err := d.BuildSliceArgs(SliceRequest{Inputs: []string{abs("a.stl")}, OutputDir: abs("o")}); err != nil {
		t.Errorf("no overrides needs no catalog: %v", err)
	}
}

func TestCheckArgs(t *testing.T) {
	for _, bad := range [][]string{
		{"--slice", "0", "--export-3mf", "x.3mf"},
		{"--slice", "0", "--export-stl"},
		{"--slice", "0", "--export-stls", "d"},
		{"--slice", "0", "--pipe", "p"},
		{"--slice", "0", "--datadir", "d"},
		{"--slice", "0", "--datadir=d"},
		{"--slice", "0", "--uptodate"},
		{"--slice", "0", "--uptodate-settings", "a"},
		{"--slice", "0", "--sw-renderer"},
		{"--slice", "0", "--no-sw-renderer"},
		{"--slice", "0", "--EXPORT-3MF=1"},
		{"--outputdir", "d", "a.stl"},
		{},
	} {
		if err := CheckArgs(bad); err == nil {
			t.Errorf("CheckArgs(%q) must fail", bad)
		}
	}
	if err := CheckArgs([]string{"--slice", "1", "--allow-newer-file=1", "x.3mf"}); err != nil {
		t.Error(err)
	}
}

func TestNewDialectUnknown(t *testing.T) {
	if _, err := NewDialect("v99", nil); err == nil {
		t.Error("unknown dialect must fail")
	}
	if d := v72Dialect(t); d.Name() != "v72" {
		t.Error(d.Name())
	}
}

func TestClassify(t *testing.T) {
	d := v72Dialect(t)
	if o := d.Classify(0, ""); !o.OK || o.Code != OutcomeOK {
		t.Errorf("%+v", o)
	}
	o := d.Classify(-17, "")
	if o.OK || o.Code != OutcomeFailed || o.Name != "PROCESS_NOT_COMPATIBLE" || !strings.Contains(o.Message, "-17") || o.Hint == "" {
		t.Errorf("%+v", o)
	}
	o = d.Classify(-100, "Empty layers detected\nmore")
	if o.Name != "SLICING_ERROR" || !strings.Contains(o.Message, "Empty layers detected") || strings.Contains(o.Message, "more") {
		t.Errorf("%+v", o)
	}
	o = d.Classify(-2, "Invalid option --frobnicate\nusage")
	if o.Name != "INVALID_OPTION" || !strings.Contains(o.Message, "--frobnicate") {
		t.Errorf("%+v", o)
	}
	o = d.Classify(-3, "No such file: 1")
	if o.Name != "FILE_NOTFOUND" || !strings.Contains(o.Message, "No such file: 1") {
		t.Errorf("%+v", o)
	}
	o = d.Classify(-1073741819, "")
	if o.Code != OutcomeCrashed || o.Name != "EXCEPTION_ACCESS_VIOLATION" || !strings.Contains(o.Message, "0xC0000005") {
		t.Errorf("%+v", o)
	}
	o = d.Classify(-777, "boom")
	if o.Code != OutcomeFailed || o.Name != "" || !strings.Contains(o.Message, "-777") || !strings.Contains(o.Message, "boom") {
		t.Errorf("%+v", o)
	}
}

func TestExitCodeTable(t *testing.T) {
	// Every code of v7.2.1 Utils.hpp must be present, with all text fields.
	for _, code := range []int32{-1, -2, -3, -4, -5, -6, -7, -8, -9, -10, -11, -12, -13, -14, -15, -16, -17, -18, -19, -20, -21, -22, -23, -24,
		-50, -51, -52, -53, -54, -55, -56, -57, -58, -59, -60, -61, -62, -63, -64, -65, -100, -101} {
		e, ok := LookupExitCode(code)
		if !ok || e.Name == "" || e.Meaning == "" || e.Hint == "" {
			t.Errorf("code %d: %+v ok=%v", code, e, ok)
		}
	}
	if len(ExitCodes()) != len(exitCodes) {
		t.Error("ExitCodes copy")
	}
	seen := map[int32]bool{}
	for _, e := range exitCodes {
		if seen[e.Code] {
			t.Errorf("duplicate code %d", e.Code)
		}
		seen[e.Code] = true
		for _, s := range []string{e.Name, e.Meaning, e.Hint} {
			if strings.ContainsAny(s, "\u2013\u2014") {
				t.Errorf("dash character in %q", s)
			}
		}
	}
}

func TestCrashName(t *testing.T) {
	for code, want := range map[int32]string{
		-1073741819: "EXCEPTION_ACCESS_VIOLATION",
		-1073741571: "EXCEPTION_STACK_OVERFLOW",
		-1073740791: "STATUS_STACK_BUFFER_OVERRUN",
		-1073741515: "STATUS_DLL_NOT_FOUND",
		-1073741000: "NTSTATUS 0xC0000338",
	} {
		if got, ok := CrashName(code); !ok || got != want {
			t.Errorf("CrashName(%d) = %q %v, want %q", code, got, ok, want)
		}
	}
	for _, code := range []int32{0, -1, -2, -100, 1, 3} {
		if _, ok := CrashName(code); ok {
			t.Errorf("code %d is not a crash", code)
		}
	}
}

// abs builds an absolute path that is absolute on every platform: the volume
// of the temp folder (C:) on Windows, the root elsewhere.
func abs(rel string) string {
	return filepath.Join(filepath.VolumeName(os.TempDir())+string(filepath.Separator), filepath.FromSlash(rel))
}

func TestPathsMustBeAbsoluteAndNonEmpty(t *testing.T) {
	d := v72Dialect(t)
	base := func() SliceRequest { return SliceRequest{Inputs: []string{abs("a.stl")}, OutputDir: abs("o")} }
	cases := map[string]func(*SliceRequest){
		"empty input":       func(r *SliceRequest) { r.Inputs = []string{""} },
		"blank input":       func(r *SliceRequest) { r.Inputs = []string{"  "} },
		"relative input":    func(r *SliceRequest) { r.Inputs = []string{"a.stl"} },
		"dash input":        func(r *SliceRequest) { r.Inputs = []string{"-x.stl"} },
		"relative output":   func(r *SliceRequest) { r.OutputDir = "out" },
		"relative log":      func(r *SliceRequest) { r.LogFile = "slice.log" },
		"relative settings": func(r *SliceRequest) { r.Settings = []string{"m.json"} },
		"empty filament":    func(r *SliceRequest) { r.Filaments = []string{""} },
		"relative filament": func(r *SliceRequest) { r.Filaments = []string{"f.json"} },
	}
	for name, mutate := range cases {
		r := base()
		mutate(&r)
		if _, err := d.BuildSliceArgs(r); err == nil || !errors.Is(err, ErrInvalidRequest) {
			t.Errorf("%s: want ErrInvalidRequest, got %v", name, err)
		}
	}
}

func TestAProjectMustBeTheOnlyInput(t *testing.T) {
	d := v72Dialect(t)
	if _, err := d.BuildSliceArgs(SliceRequest{Inputs: []string{abs("p.3mf")}, OutputDir: abs("o")}); err != nil {
		t.Errorf("a lone project: %v", err)
	}
	for _, inputs := range [][]string{
		{abs("p.3mf"), abs("a.stl")},
		{abs("a.stl"), abs("p.3mf")},
		{abs("p.3MF"), abs("q.3mf")},
	} {
		if _, err := d.BuildSliceArgs(SliceRequest{Inputs: inputs, OutputDir: abs("o")}); !errors.Is(err, ErrInvalidRequest) || !strings.Contains(err.Error(), "only input") {
			t.Errorf("%v: %v", inputs, err)
		}
	}
	// Several STL files are fine.
	if _, err := d.BuildSliceArgs(SliceRequest{Inputs: []string{abs("a.stl"), abs("b.stl")}, OutputDir: abs("o")}); err != nil {
		t.Error(err)
	}
}

func vectorLookup(key string) (string, bool, bool) {
	switch key {
	case "filament_is_support":
		return "--filament-is-support", true, true // a vector of booleans: still one token
	case "enable_support":
		return "--enable-support", true, true
	case "wall_loops":
		return "--wall-loops", false, true
	case "filament_colour":
		return "--filament-colour", false, true
	case "printer_settings_id", "filament_settings_id", "print_settings_id", "preset_name", "preset_names", "printer_select_mac", "filament_ids":
		return "--" + strings.ReplaceAll(key, "_", "-"), false, true
	case "arrange", "orient", "allow_newer_file":
		return "--" + strings.ReplaceAll(key, "_", "-"), false, true
	}
	return "", false, false
}

// The CLI reads no separate value token for coBool and coBools and splits
// vector items on commas (Config.hpp deserialize): --flag=v1,v2,...
func TestVectorBooleansAreOneTokenWithCommas(t *testing.T) {
	d, _ := NewDialect("v72", vectorLookup)
	for value, want := range map[string]string{
		"0,1":         "--filament-is-support=0,1",
		"1;0;1":       "--filament-is-support=1,0,1",
		"true, false": "--filament-is-support=1,0",
		"1":           "--filament-is-support=1",
		"no,yes,off":  "--filament-is-support=0,1,0",
	} {
		got, err := d.BuildSliceArgs(SliceRequest{Inputs: []string{abs("a.stl")}, OutputDir: abs("o"), Overrides: map[string]string{"filament_is_support": value}})
		if err != nil {
			t.Fatal(err)
		}
		if !contains(got, want) {
			t.Errorf("%q: %q does not contain %q", value, got, want)
		}
		// Never a separate value token: the argument after the flag is the next flag or an input.
		for i, a := range got {
			if strings.HasPrefix(a, "--filament-is-support") && !strings.Contains(a, "=") {
				t.Errorf("flag without =: %q %d", got, i)
			}
		}
	}
	for _, bad := range []string{"", " ", ",", "maybe", "1,2", "1,,x"} {
		if _, err := d.BuildSliceArgs(SliceRequest{Inputs: []string{abs("a.stl")}, OutputDir: abs("o"), Overrides: map[string]string{"filament_is_support": bad}}); err == nil {
			t.Errorf("%q must fail", bad)
		}
	}
}

func TestOverridesThatCollideWithRequestFieldsAreRejected(t *testing.T) {
	d, _ := NewDialect("v72", vectorLookup)
	base := func() SliceRequest { return SliceRequest{Inputs: []string{abs("a.stl")}, OutputDir: abs("o")} }
	one := 1
	for name, mutate := range map[string]func(*SliceRequest){
		"colours twice": func(r *SliceRequest) {
			r.FilamentColours = []string{"#FFFFFF"}
			r.Overrides = map[string]string{"filament_colour": "#000000"}
		},
		"printer id":        func(r *SliceRequest) { r.Overrides = map[string]string{"printer_settings_id": "x"} },
		"print id":          func(r *SliceRequest) { r.Overrides = map[string]string{"print_settings_id": "x"} },
		"filament id names": func(r *SliceRequest) { r.Overrides = map[string]string{"filament_settings_id": "x"} },
		"preset name":       func(r *SliceRequest) { r.Overrides = map[string]string{"preset_name": "x"} },
		"preset names":      func(r *SliceRequest) { r.Overrides = map[string]string{"preset_names": "x"} },
		"mac":               func(r *SliceRequest) { r.Overrides = map[string]string{"printer_select_mac": "x"} },
		"filament ids":      func(r *SliceRequest) { r.Overrides = map[string]string{"filament_ids": "x"} },
		"arrange twice":     func(r *SliceRequest) { r.Arrange = &one; r.Overrides = map[string]string{"arrange": "0"} },
		"orient twice":      func(r *SliceRequest) { r.Orient = &one; r.Overrides = map[string]string{"orient": "0"} },
		"newer file twice":  func(r *SliceRequest) { r.AllowNewer = true; r.Overrides = map[string]string{"allow_newer_file": "1"} },
	} {
		r := base()
		mutate(&r)
		if _, err := d.BuildSliceArgs(r); !errors.Is(err, ErrInvalidRequest) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// Without the dedicated field the same keys are fine (the colour list is the
	// caller's choice), except the identity keys which never are.
	r := base()
	r.Overrides = map[string]string{"filament_colour": "#000000", "wall_loops": "3"}
	if args, err := d.BuildSliceArgs(r); err != nil || !contains(args, "--filament-colour") {
		t.Errorf("%v %v", args, err)
	}
}
