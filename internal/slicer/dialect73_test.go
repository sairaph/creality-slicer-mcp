package slicer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func newRunner73(t *testing.T, ex Exec, in Install) (*Runner, string) {
	t.Helper()
	d, err := NewDialect("v73", testLookup)
	if err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(t.TempDir(), "cli-data")
	dumps := t.TempDir()
	return &Runner{Exe: "CrealityPrint.exe", Dialect: d, Exec: ex, TempDir: func() string { return dumps }, Install: in, DataDir: data}, data
}

func req73(t *testing.T) SliceRequest {
	return SliceRequest{Inputs: []string{filepath.Join(t.TempDir(), "p.3mf")}, OutputDir: t.TempDir(), Plate: 1, DataDir: filepath.Join(t.TempDir(), "d")}
}

func TestV73Args(t *testing.T) {
	d, _ := NewDialect("v73", testLookup)
	if d.Name() != "v73" {
		t.Fatal(d.Name())
	}
	r := req73(t)
	args, err := d.BuildSliceArgs(r)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"--cli", "--datadir", r.DataDir, "--slice", "1", "--need-gcode-file", "--outputdir", r.OutputDir, "--debug", "3", r.Inputs[0]}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args\n%v\nwant\n%v", args, want)
	}
	// Optional parts; the project is last.
	r.LogFile = filepath.Join(t.TempDir(), "s.log")
	r.DebugLevel = 4
	r.FilamentColours = []string{"#FF0000", "#00FF0080"}
	r.Overrides = map[string]string{"layer_height": "0.16", "enable_support": "1"}
	r.SkipObjects = []int{3, 5}
	r.AllowNewer = true
	args, err = d.BuildSliceArgs(r)
	if err != nil {
		t.Fatal(err)
	}
	s := strings.Join(args, " ")
	for _, w := range []string{"--debug 4", "--logfile " + r.LogFile, "--filament-colour #FF0000;#00FF0080", "--layer-height 0.16", "--enable-support=1", "--skip-objects 3,5", "--allow-newer-file=1"} {
		if !strings.Contains(s, w) {
			t.Errorf("args lack %q: %s", w, s)
		}
	}
	if args[0] != "--cli" || args[len(args)-1] != r.Inputs[0] {
		t.Errorf("order: %v", args)
	}
}

func TestV73Refusals(t *testing.T) {
	d, _ := NewDialect("v73", testLookup)
	one := func(mut func(*SliceRequest)) error {
		r := req73(t)
		mut(&r)
		_, err := d.BuildSliceArgs(r)
		return err
	}
	zero := 0
	for name, mut := range map[string]func(*SliceRequest){
		"no input":       func(r *SliceRequest) { r.Inputs = nil },
		"two inputs":     func(r *SliceRequest) { r.Inputs = append(r.Inputs, filepath.Join(t.TempDir(), "b.3mf")) },
		"stl":            func(r *SliceRequest) { r.Inputs = []string{filepath.Join(t.TempDir(), "a.stl")} },
		"arrange":        func(r *SliceRequest) { r.Arrange = &zero },
		"orient":         func(r *SliceRequest) { r.Orient = &zero },
		"settings":       func(r *SliceRequest) { r.Settings = []string{filepath.Join(t.TempDir(), "m.json")} },
		"filament ids":   func(r *SliceRequest) { r.FilamentIDs = []int{1} },
		"negative plate": func(r *SliceRequest) { r.Plate = -1 },
		"no output":      func(r *SliceRequest) { r.OutputDir = "" },
		"no datadir":     func(r *SliceRequest) { r.DataDir = "" },
		"relative data":  func(r *SliceRequest) { r.DataDir = "data" },
		"bad debug":      func(r *SliceRequest) { r.DebugLevel = 9 },
		"bad colour":     func(r *SliceRequest) { r.FilamentColours = []string{"red"} },
		"unknown key":    func(r *SliceRequest) { r.Overrides = map[string]string{"nonsense": "1"} },
		"newline value":  func(r *SliceRequest) { r.Overrides = map[string]string{"layer_height": "1\n2"} },
	} {
		if err := one(mut); !errors.Is(err, ErrInvalidRequest) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// The argv check itself.
	c := d.(*v73)
	for _, bad := range [][]string{
		{"--slice", "1", "--datadir", "D", "x.3mf"},                    // no --cli
		{"--cli", "--datadir", "D", "x.3mf"},                           // no --slice
		{"--cli", "--slice", "1", "x.3mf"},                             // no --datadir
		{"--cli", "--slice", "1", "--datadir", "D", "--export-3mf=1"},  // export
		{"--cli", "--slice", "1", "--datadir", "D", "--arrange=1"},     // transform
		{"--cli", "--slice", "1", "--datadir", "D", "--pipe", "p"},     // pipe
		{"--cli", "--slice", "1", "--datadir", "D", "--uptodate=1"},    // uptodate
		{"--cli", "--slice", "1", "--datadir", "D", "--load-settings"}, // STL route
	} {
		if err := c.checkArgs(bad); err == nil {
			t.Errorf("accepted %v", bad)
		}
	}
}

func TestV73ExitCodes(t *testing.T) {
	d, _ := NewDialect("v73", testLookup)
	// The codes 7.3 dropped are unknown; the others keep their names.
	for _, dropped := range []int32{-9, -10, -11, -12, -13, -15, -16, -20, -22, -23} {
		if _, ok := LookupExitCode73(dropped); ok {
			t.Errorf("%d is not a 7.3 code", dropped)
		}
		if _, ok := LookupExitCode(dropped); !ok {
			t.Errorf("%d must stay a 7.2 code", dropped)
		}
	}
	for code, name := range map[int32]string{-2: "INVALID_PARAMS", -24: "FILE_VERSION_NOT_SUPPORTED", -50: "NO_SUITABLE_OBJECTS", -65: "SPIRAL_MODE_INVALID_PARAMS", -101: "GCODE_PATH_CONFLICTS", -4: "FILELIST_INVALID_ORDER", -5: "CONFIG_FILE_ERROR"} {
		e, ok := LookupExitCode73(code)
		if !ok || e.Name != name {
			t.Errorf("%d: %+v %v", code, e, ok)
		}
	}
	if o := d.Classify(-24, ""); o.Name != "FILE_VERSION_NOT_SUPPORTED" || o.Code != OutcomeFailed || o.Hint == "" {
		t.Errorf("%+v", o)
	}
	if o := d.Classify(-9, ""); o.Name != "" || !strings.Contains(o.Message, "unknown exit code -9") {
		t.Errorf("dropped code: %+v", o)
	}
	if o := d.Classify(-2, "Invalid option --arrange\nusage"); o.Name != "INVALID_OPTION" || !strings.Contains(o.Hint, "7.3") {
		t.Errorf("%+v", o)
	}
	if o := d.Classify(0, ""); !o.OK {
		t.Errorf("%+v", o)
	}
	if o := d.Classify(-1073741819, ""); o.Code != OutcomeCrashed {
		t.Errorf("%+v", o)
	}
}

func TestRunnerV73UsesItsOwnDataDir(t *testing.T) {
	var seen []string
	ex := &fakeExec{fn: func(_ context.Context, spec ExecSpec) (ExecResult, error) {
		seen = spec.Args
		writeOut(spec, "plate_1.gcode")
		return ExecResult{}, nil
	}}
	r, data := newRunner73(t, ex, Install{})
	req := req73(t)
	req.DataDir = "" // the Runner supplies it
	res, err := r.Run(context.Background(), req)
	if err != nil || !res.Outcome.OK {
		t.Fatalf("%v %+v", err, res)
	}
	if seen[0] != "--cli" || seen[1] != "--datadir" || seen[2] != data {
		t.Fatalf("argv %v", seen)
	}
	if info, err := os.Stat(filepath.Join(data, "Creality Print", "7.3")); err != nil || !info.IsDir() {
		t.Fatalf("data folder not prepared: %v", err)
	}
	// The data folder of the application itself is never used.
	r.Install = Install{DataDir: data}
	if _, err := r.Run(context.Background(), req); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("own folder: %v", err)
	}
	// No data folder configured: refuse and do not start.
	r.Install = Install{}
	r.DataDir = ""
	calls := ex.callCount()
	if _, err := r.Run(context.Background(), req); !errors.Is(err, ErrInvalidRequest) || ex.callCount() != calls {
		t.Fatalf("no data folder: %v", err)
	}
}

func TestSystemPresetMirror(t *testing.T) {
	app := t.TempDir()
	install := Install{DataDir: app, ProfileVersionInstall: "26.09.29.08", ProfileVersionData: "26.10.01.01"}
	write := func(dir, ver, extra string) {
		if err := os.MkdirAll(filepath.Join(dir, "Creality"), 0o755); err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(dir, "Creality.json"), []byte(`{"version":"`+ver+`"}`), 0o644)
		os.WriteFile(filepath.Join(dir, "Creality", "file.json"), []byte(extra), 0o644)
	}
	write(filepath.Join(app, "system"), "26.10.01.01", "app")
	dst := filepath.Join(t.TempDir(), "Creality Print", "7.3", "system")
	// The bundle of the application is newer: mirrored.
	if err := syncSystemPresets(install, dst); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "Creality", "file.json")); string(b) != "app" {
		t.Fatalf("not mirrored: %q", b)
	}
	// Same version: left alone even if a file changed (the version is the key).
	os.WriteFile(filepath.Join(app, "system", "Creality", "file.json"), []byte("changed"), 0o644)
	_ = syncSystemPresets(install, dst)
	if b, _ := os.ReadFile(filepath.Join(dst, "Creality", "file.json")); string(b) != "app" {
		t.Fatalf("mirrored again for the same version: %q", b)
	}
	// A newer bundle replaces it, no leftovers.
	write(filepath.Join(app, "system"), "26.10.05.01", "newer")
	install.ProfileVersionData = "26.10.05.01"
	if err := syncSystemPresets(install, dst); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "Creality", "file.json")); string(b) != "newer" {
		t.Fatalf("not replaced: %q", b)
	}
	for _, leftover := range []string{dst + ".new", dst + ".old"} {
		if _, err := os.Stat(leftover); err == nil {
			t.Errorf("%s left behind", leftover)
		}
	}
	// The install catches up: the mirror is removed.
	install.ProfileVersionInstall = "26.10.05.01"
	if err := syncSystemPresets(install, dst); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Fatalf("stale mirror kept: %v", err)
	}
	// The folder of the application was never written.
	if b, _ := os.ReadFile(filepath.Join(app, "system", "Creality", "file.json")); string(b) != "newer" {
		t.Fatalf("app folder changed: %q", b)
	}
}

// A run for one plate leaves the G-code of the other plates alone (SL1), and
// replaces only the plate it sliced.
func TestRunOnlyClearsAndReportsTheRequestedPlate(t *testing.T) {
	ex := &fakeExec{fn: func(_ context.Context, spec ExecSpec) (ExecResult, error) {
		writeOut(spec, "plate_2.gcode")
		return ExecResult{}, nil
	}}
	r, _ := newRunner(t, ex)
	req := request(t)
	req.Plate = 2
	os.WriteFile(filepath.Join(req.OutputDir, "plate_1.gcode"), []byte("keep"), 0o644)
	os.WriteFile(filepath.Join(req.OutputDir, "plate_2.gcode"), []byte("stale"), 0o644)
	os.WriteFile(filepath.Join(req.OutputDir, "plate_20.gcode"), []byte("keep"), 0o644)
	res, err := r.Run(context.Background(), req)
	if err != nil || !res.Outcome.OK {
		t.Fatalf("%v %+v", err, res)
	}
	if len(res.GCodeFiles) != 1 || filepath.Base(res.GCodeFiles[0]) != "plate_2.gcode" {
		t.Fatalf("reported %v", res.GCodeFiles)
	}
	for name, want := range map[string]string{"plate_1.gcode": "keep", "plate_20.gcode": "keep", "plate_2.gcode": "; fake\n"} {
		if b, _ := os.ReadFile(filepath.Join(req.OutputDir, name)); string(b) != want {
			t.Errorf("%s = %q, want %q", name, b, want)
		}
	}
	// Plate 0 reports what this run wrote and leaves the other files alone.
	req.Plate = 0
	os.WriteFile(filepath.Join(req.OutputDir, "plate_1.gcode"), []byte("old"), 0o644)
	res, _ = r.Run(context.Background(), req)
	if len(res.GCodeFiles) != 1 || filepath.Base(res.GCodeFiles[0]) != "plate_2.gcode" {
		t.Fatalf("plate 0: %v", res.GCodeFiles)
	}
	if b, _ := os.ReadFile(filepath.Join(req.OutputDir, "plate_1.gcode")); string(b) != "old" {
		t.Errorf("a plate the run did not slice was touched: %q", b)
	}
	// Nothing of the work folder stays behind.
	entries, _ := os.ReadDir(req.OutputDir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".run-") {
			t.Errorf("work folder %s left behind", e.Name())
		}
	}
}

// Two runs into one output folder are refused (SL2), for Run and for jobs.
func TestOutputFolderIsUsedByOneRunAtATime(t *testing.T) {
	block := make(chan struct{})
	started := make(chan struct{})
	var once sync.Once
	ex := &fakeExec{fn: func(ctx context.Context, spec ExecSpec) (ExecResult, error) {
		once.Do(func() { close(started) })
		select {
		case <-block:
		case <-ctx.Done():
		}
		writeOut(spec, "plate_1.gcode")
		return ExecResult{}, nil
	}}
	r, _ := newRunner(t, ex)
	req := request(t)
	req.Plate = 1
	done := make(chan error, 1)
	go func() { _, err := r.Run(context.Background(), req); done <- err }()
	<-started
	if _, err := r.Run(context.Background(), req); !errors.Is(err, ErrBusy) {
		t.Fatalf("second run: %v", err)
	}
	jobs := NewJobs(r, t.TempDir())
	if _, err := jobs.Start(req, DefaultTimeout); !errors.Is(err, ErrBusy) {
		t.Fatalf("job: %v", err)
	}
	other := request(t) // another folder is fine
	other.Plate = 1
	close(block)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := r.Run(context.Background(), other); err != nil {
		t.Fatal(err)
	}
	// Released: the same folder can be used again.
	if _, err := r.Run(context.Background(), req); err != nil {
		t.Fatal(err)
	}
}

// The safety net of the process launcher knows both command lines.
func TestCheckArgsKnowsBothDialects(t *testing.T) {
	if err := CheckArgs([]string{"--cli", "--datadir", "D", "--slice", "1", "--need-gcode-file", "x.3mf"}); err != nil {
		t.Errorf("7.3 argv: %v", err)
	}
	if err := CheckArgs([]string{"--cli", "--slice", "1", "x.3mf"}); err == nil {
		t.Error("7.3 argv without a data folder must fail")
	}
	if err := CheckArgs([]string{"--slice", "1", "--datadir", "D", "x.3mf"}); err == nil {
		t.Error("7.2 argv with --datadir must still fail")
	}
}

// SL4: a mirror update waits for the runs that read the folder, and runs never
// overlap the update.
func TestMirrorUpdateWaitsForRunningRuns(t *testing.T) {
	app := t.TempDir()
	write := func(ver string) {
		os.MkdirAll(filepath.Join(app, "system", "Creality"), 0o755)
		os.WriteFile(filepath.Join(app, "system", "Creality.json"), []byte(`{"version":"`+ver+`"}`), 0o644)
		os.WriteFile(filepath.Join(app, "system", "Creality", "f.json"), []byte(ver), 0o644)
	}
	write("26.10.01.01")
	in := Install{DataDir: app, ProfileVersionInstall: "26.09.29.08", ProfileVersionData: "26.10.01.01"}
	d, _ := NewDialect("v73", testLookup)
	r := &Runner{Dialect: d, Install: in, DataDir: filepath.Join(t.TempDir(), "cli-data")}
	relA, err := r.prepareDataDir()
	if err != nil {
		t.Fatal(err)
	}
	// Run A is going; two more runs join it (shared): the mirror is current.
	relB, err := r.prepareDataDir()
	if err != nil {
		t.Fatal(err)
	}
	relB()
	// A newer bundle appears: the next run must replace the mirror, which
	// cannot happen while run A reads it.
	write("26.10.05.01")
	r2 := &Runner{Dialect: d, DataDir: r.DataDir, Install: Install{DataDir: app, ProfileVersionInstall: "26.09.29.08", ProfileVersionData: "26.10.05.01"}}
	done := make(chan func(), 1)
	go func() {
		rel, err := r2.prepareDataDir()
		if err != nil {
			t.Error(err)
			rel = func() {}
		}
		done <- rel
	}()
	select {
	case <-done:
		t.Fatal("the mirror was replaced under a running run")
	case <-time.After(400 * time.Millisecond):
	}
	relA()
	select {
	case rel := <-done:
		rel()
	case <-time.After(30 * time.Second):
		t.Fatal("the update never ran")
	}
	if v := bundleVersion(filepath.Join(r.DataDir, "Creality Print", "7.3", "system")); v != "26.10.05.01" {
		t.Fatalf("mirror version %q", v)
	}
}
