package projects

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sairaph/creality-slicer-mcp/internal/catalog"
	"github.com/sairaph/creality-slicer-mcp/internal/mesh"
	"github.com/sairaph/creality-slicer-mcp/internal/profiles"
	"github.com/sairaph/creality-slicer-mcp/internal/slicer"
)

const (
	testPrinter     = DefaultPrinter
	testPrinter6    = "Creality K2 0.6 nozzle"
	testProcess     = "0.20mm Standard @Creality K2 0.4 nozzle"
	testPLA         = "TP-PLA @Creality K2 0.4 nozzle"
	testPETG        = "TP-PETG @Creality K2 0.4 nozzle"
	testOther       = "TP-PLA @Creality K2 0.6 nozzle"
	testGenericPLA  = "Generic PLA @Creality K2 0.4 nozzle"
	testGenericPETG = "Generic PETG @Creality K2 0.4 nozzle"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeBundle writes a small synthetic Creality profile bundle.
func writeBundle(t *testing.T, processExtra string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "profiles")
	type entry struct{ name, sub string }
	var machines, processes, filaments []entry
	add := func(kind, name, body string) {
		file := name + ".json"
		writeFile(t, filepath.Join(root, "Creality", kind, file), body)
		e := entry{name, kind + "/" + file}
		switch kind {
		case "machine":
			machines = append(machines, e)
		case "process":
			processes = append(processes, e)
		default:
			filaments = append(filaments, e)
		}
	}
	printer := func(name, nozzle string) string {
		return fmt.Sprintf(`{"type":"machine","name":%q,"from":"system","instantiation":"true","setting_id":"9%s",
"printer_model":"Creality K2","printer_variant":%q,"nozzle_diameter":[%q],
"printable_area":"0x0,260x0,260x260,0x260","printable_height":"250","nozzle_volume":"183",
"enable_long_retraction_when_cut":"2","long_retractions_when_cut":"1","retraction_distances_when_cut":"30",
"default_print_profile":%q,"default_filament_profile":[%q],"thumbnails":"96x96/PNG, 300x300/PNG"}`,
			name, nozzle, nozzle, nozzle, testProcess, testPLA)
	}
	add("machine", testPrinter, printer(testPrinter, "0.4"))
	add("machine", testPrinter6, printer(testPrinter6, "0.6"))
	add("process", testProcess, fmt.Sprintf(`{"type":"process","name":%q,"from":"system","instantiation":"true","setting_id":"GP1",
"layer_height":"0.2","initial_layer_print_height":"0.2","wall_loops":"3",%s"compatible_printers":[%q]}`, testProcess, processExtra, testPrinter))
	fil := func(name, id, typ, colour, printer string) string {
		return fmt.Sprintf(`{"type":"filament","name":%q,"from":"system","instantiation":"true","filament_id":%q,
"filament_type":[%q],"default_filament_colour":[%q],"nozzle_temperature":["215"],"cool_plate_temp_initial_layer":["60"],"textured_plate_temp_initial_layer":["70"],"compatible_printers":[%q]}`, name, id, typ, colour, printer)
	}
	add("filament", testPLA, fil(testPLA, "P001", "PLA", "#FFFFFF", testPrinter))
	add("filament", testPETG, fil(testPETG, "P002", "PETG", "#000000", testPrinter))
	add("filament", testOther, fil(testOther, "P003", "PLA", "#FFFFFF", testPrinter6))
	add("filament", testGenericPLA, fil(testGenericPLA, "GFL99", "PLA", "#FFFFFF", testPrinter))
	add("filament", testGenericPETG, fil(testGenericPETG, "GFG99", "PETG", "#FFFFFF", testPrinter))
	var idx strings.Builder
	list := func(key string, es []entry) {
		idx.WriteString(fmt.Sprintf("%q: [", key))
		for i, e := range es {
			if i > 0 {
				idx.WriteString(",")
			}
			idx.WriteString(fmt.Sprintf(`{"name":%q,"sub_path":%q}`, e.name, e.sub))
		}
		idx.WriteString("]")
	}
	idx.WriteString(`{"name":"Creality","version":"9.0.0.1","description":"synthetic",`)
	list("machine_list", machines)
	idx.WriteString(",")
	list("process_list", processes)
	idx.WriteString(",")
	list("filament_list", filaments)
	idx.WriteString("}")
	writeFile(t, filepath.Join(root, "Creality.json"), idx.String())
	return root
}

// fakeExec stands in for the slicer.
type fakeExec struct {
	mu    sync.Mutex
	calls [][]string
	// fn produces the run; the default writes a plain G-code for plate 1.
	fn func(spec slicer.ExecSpec) (slicer.ExecResult, error)
	// gcode builds the G-code of a plate.
	gcode func(plate int) string
	// block, when set, is waited on before the run ends.
	block chan struct{}
	// ctxFn, when set, runs instead of everything else and sees the run context.
	ctxFn func(ctx context.Context, spec slicer.ExecSpec) (slicer.ExecResult, error)
}

func (f *fakeExec) Run(ctx context.Context, spec slicer.ExecSpec) (slicer.ExecResult, error) {
	f.mu.Lock()
	f.calls = append(f.calls, append([]string(nil), spec.Args...))
	f.mu.Unlock()
	if f.ctxFn != nil {
		return f.ctxFn(ctx, spec)
	}
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return slicer.ExecResult{Killed: true}, nil
		}
	}
	if f.fn != nil {
		return f.fn(spec)
	}
	plate := 1
	out := ""
	for i, a := range spec.Args {
		switch a {
		case "--slice":
			if n, _ := strconv.Atoi(spec.Args[i+1]); n > 0 {
				plate = n
			}
		case "--outputdir":
			out = spec.Args[i+1]
		}
	}
	text := defaultGCode
	if f.gcode != nil {
		text = f.gcode(plate)
	}
	if err := os.WriteFile(filepath.Join(out, fmt.Sprintf("plate_%d.gcode", plate)), []byte(text), 0o644); err != nil {
		return slicer.ExecResult{}, err
	}
	return slicer.ExecResult{}, nil
}

func (f *fakeExec) lastArgs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		return nil
	}
	return f.calls[len(f.calls)-1]
}

func (f *fakeExec) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// gcodeFor builds a synthetic G-code with the given object definitions.
func gcodeFor(objects ...string) string {
	var b strings.Builder
	b.WriteString("; HEADER_BLOCK_START\n; generated by Creality_Print V7.2.2.5483 on 2026-09-29 at 07:03:52\n; total layer number: 100\n")
	b.WriteString("; max_z_height: 20.00\n; creality_uuid: aaaa\n; HEADER_BLOCK_END\n\n")
	b.WriteString("; MINX = 10.00\n; MINY = 10.00\n; MINZ = 0.00\n; MAXX = 30.00\n; MAXY = 30.00\n; MAXZ = 20.00\n\n")
	b.WriteString("; multicolor_method = 0 \n; external perimeters extrusion width = 0.42mm\n\n; EXECUTABLE_BLOCK_START\n")
	for _, o := range objects {
		b.WriteString(o + "\n")
	}
	b.WriteString("T0\nG1 X10 Y10 E1\nT1\nG1 X20 Y20 E1\n;LAYER_CHANGE\n; EXECUTABLE_BLOCK_END\n\n")
	b.WriteString("; filament used [mm] = 100.00, 50.00\n; filament used [g] = 1.50, 0.50\n; total filament cost = 0.10\n")
	b.WriteString("; total layers count = 100\n; estimated printing time (normal mode) = 1m 5s\n\n")
	b.WriteString("; CONFIG_BLOCK_START\n; thumbnails = 96x96/PNG, 300x300/PNG\n; CONFIG_BLOCK_END\n")
	return b.String()
}

var defaultGCode = gcodeFor("EXCLUDE_OBJECT_DEFINE NAME=cube_id_0_copy_0 CENTER=20,20 POLYGON=[[10,10],[30,10],[30,30],[10,30],[10,10]]")

type testEnv struct {
	st   *Store
	exec *fakeExec
	cat  *catalog.Catalog
	dir  string
	now  time.Time
	mu   sync.Mutex
}

// newEnv makes a Store over a synthetic bundle and a fake slicer.
func newEnv(t *testing.T) *testEnv { return newEnvWith(t, "") }

func newEnvWith(t *testing.T, processExtra string) *testEnv {
	t.Helper()
	cat, err := catalog.Load("7.2.1")
	if err != nil {
		t.Fatal(err)
	}
	ps, err := profiles.Open(profiles.Roots{InstallProfiles: writeBundle(t, processExtra)})
	if err != nil {
		t.Fatal(err)
	}
	e := &testEnv{cat: cat, exec: &fakeExec{}, dir: t.TempDir(), now: time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)}
	d, err := slicer.NewDialect("v72", cat.CLIFlag)
	if err != nil {
		t.Fatal(err)
	}
	runner := &slicer.Runner{Exe: `C:\fake\CrealityPrint.exe`, Dialect: d, Exec: e.exec, TempDir: func() string { return t.TempDir() }}
	jobs := slicer.NewJobs(runner, filepath.Join(e.dir, "jobs"))
	e.st, err = New(Config{
		Root: filepath.Join(e.dir, "projects"), Install: slicer.Install{Found: true, Supported: true, Version: "7.2.2", Build: "5483"},
		Runner: runner, Jobs: jobs, Profiles: ps, Catalog: cat,
		Now: func() time.Time { e.mu.Lock(); defer e.mu.Unlock(); e.now = e.now.Add(time.Second); return e.now },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(jobs.StopAll)
	return e
}

func twoFilaments() []FilamentSpec {
	return []FilamentSpec{{Preset: testPLA, Colour: "#FFFFFF"}, {Preset: testPETG, Colour: "#000000"}}
}

// newProject creates a project of the given filament count.
func (e *testEnv) newProject(t *testing.T, name string, filaments ...FilamentSpec) *Info {
	t.Helper()
	if len(filaments) == 0 {
		filaments = twoFilaments()
	}
	info, err := e.st.CreateProject(CreateRequest{Name: name, Printer: testPrinter, Process: testProcess, Filaments: filaments})
	if err != nil {
		t.Fatal(err)
	}
	return info
}

// writeSTL writes a box as a binary STL and returns its path.
func writeSTL(t *testing.T, name string, x, y, z float64) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name+".stl")
	if err := os.WriteFile(p, mesh.WriteBinarySTL(mesh.Box(x, y, z)), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func (e *testEnv) addBox(t *testing.T, id, name string, x, y, z float64) *AddModelResult {
	t.Helper()
	res, err := e.st.AddModel(id, AddModelRequest{Path: writeSTL(t, name, x, y, z)})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func wantCode(t *testing.T, err error, code string) *Error {
	t.Helper()
	if err == nil {
		t.Fatalf("want a %s error, got none", code)
	}
	ae := AsError(err)
	if ae.Code != code {
		t.Fatalf("want code %s, got %s: %v", code, ae.Code, err)
	}
	return ae
}

func hasWarning(in *Info, code string) bool {
	for _, w := range in.Warnings {
		if w.Code == code {
			return true
		}
	}
	return false
}
