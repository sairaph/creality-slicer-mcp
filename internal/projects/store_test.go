package projects

import (
	"archive/zip"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/sairaph/creality-slicer-mcp/internal/threemf"
)

func TestCreateProject(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Benchy Test")
	if info.Revision != 1 || info.Printer != testPrinter || info.Process != testProcess {
		t.Fatalf("info = %+v", info)
	}
	if len(info.Filaments) != 2 || info.Filaments[1].Preset != testPETG || info.Filaments[1].Colour != "#000000" || info.Filaments[1].Type != "PETG" || info.Filaments[1].FilamentID != "P002" {
		t.Fatalf("filaments = %+v", info.Filaments)
	}
	if len(info.Plates) != 1 || len(info.Objects) != 0 || info.FlushMode != "auto" {
		t.Fatalf("plates/objects/flush = %+v %+v %q", info.Plates, info.Objects, info.FlushMode)
	}
	if len(info.FlushMatrix) != 4 || info.FlushMatrix[0] != "0" || info.FlushMatrix[3] != "0" {
		t.Fatalf("flush matrix = %v", info.FlushMatrix)
	}
	if !strings.HasPrefix(info.ID, "benchy-test-") {
		t.Fatalf("id = %q", info.ID)
	}
	dir := filepath.Join(e.dir, "projects", info.ID)
	for _, f := range []string{"project.3mf", "job.json"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
	p, err := threemf.Open(filepath.Join(dir, "project.3mf"))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if !p.IsSlicerProject || p.Settings.String("printer_settings_id") != testPrinter || len(p.Settings.List("filament_colour")) != 2 {
		t.Fatalf("saved project is wrong")
	}
	if len(p.Settings.List("different_settings_to_system")) != 4 { // process, 2 filaments, printer
		t.Fatalf("diffs = %v", p.Settings.List("different_settings_to_system"))
	}
	if img, err := p.Thumbnail(1, threemf.ThumbPlate); err != nil || len(img) == 0 {
		t.Fatalf("no plate thumbnail: %v", err)
	}
	// The process preset value reaches the project.
	if p.Settings.String("wall_loops") != "3" || p.Settings.String("layer_height") != "0.2" {
		t.Fatalf("preset values missing: wall_loops=%q", p.Settings.String("wall_loops"))
	}
}

func TestCreateErrors(t *testing.T) {
	e := newEnv(t)
	cases := []struct {
		name string
		req  CreateRequest
		want string
	}{
		{"no name", CreateRequest{Printer: testPrinter, Filaments: twoFilaments()}, "a project needs a name"},
		{"no filaments", CreateRequest{Name: "x", Printer: testPrinter}, "at least one filament"},
		{"bad colour", CreateRequest{Name: "x", Printer: testPrinter, Filaments: []FilamentSpec{{Preset: testPLA, Colour: "white"}}}, "#RRGGBB"},
		{"unknown printer", CreateRequest{Name: "x", Printer: "Nope", Filaments: twoFilaments()}, "was not found"},
		{"unknown filament", CreateRequest{Name: "x", Printer: testPrinter, Filaments: []FilamentSpec{{Preset: "Nope", Colour: "#FFFFFF"}}}, "was not found"},
		{"incompatible filament", CreateRequest{Name: "x", Printer: testPrinter, Process: testProcess, Filaments: []FilamentSpec{{Preset: testOther, Colour: "#FFFFFF"}}}, "not compatible"},
		{"bad bed", CreateRequest{Name: "x", Printer: testPrinter, Filaments: twoFilaments(), BedType: "Glass"}, "not a bed type"},
	}
	for _, c := range cases {
		_, err := e.st.CreateProject(c.req)
		ae := wantCode(t, err, CodeInvalidInput)
		if !strings.Contains(ae.Message+" "+ae.Hint, c.want) {
			t.Errorf("%s: message %q lacks %q", c.name, ae.Message, c.want)
		}
	}
	if items, _ := e.st.List(); len(items) != 0 {
		t.Fatalf("failed creates left projects behind: %+v", items)
	}
	_, err := e.st.CreateProject(CreateRequest{Name: "x", Printer: testPrinter, Process: testProcess, Filaments: []FilamentSpec{{Preset: testOther, Colour: "#FFFFFF"}}})
	if ae := AsError(err); ae.Hint == "" {
		t.Errorf("no hint on %v", err)
	}
}

func TestResolveListDeleteExport(t *testing.T) {
	e := newEnv(t)
	a := e.newProject(t, "Alpha")
	b := e.newProject(t, "Beta")
	if _, err := e.st.GetProject("alpha"); err != nil { // by name, case-insensitive
		t.Fatal(err)
	}
	if got, err := e.st.GetProject(b.ID); err != nil || got.Name != "Beta" {
		t.Fatalf("by id: %v %v", got, err)
	}
	wantCode(t, func() error { _, err := e.st.GetProject("zzz"); return err }(), CodeNotFound)
	wantCode(t, func() error { _, err := e.st.GetProject(`..\x`); return err }(), CodeInvalidInput)
	wantCode(t, func() error { _, err := e.st.GetProject(""); return err }(), CodeInvalidInput)
	e.newProject(t, "Alpha") // a second project with the same name
	wantCode(t, func() error { _, err := e.st.GetProject("Alpha"); return err }(), CodeInvalidInput)
	if _, err := e.st.GetProject(a.ID); err != nil {
		t.Fatal(err)
	}
	// Most recently changed first.
	items, err := e.st.List()
	if err != nil || len(items) != 3 {
		t.Fatalf("list: %v %v", items, err)
	}
	if !items[0].Updated.After(items[2].Updated) && !items[0].Updated.Equal(items[2].Updated) {
		t.Fatalf("not ordered: %+v", items)
	}
	if _, err := e.st.UpdateSettings(a.ID, SettingsRequest{Values: map[string]any{"wall_loops": 4}}); err != nil {
		t.Fatal(err)
	}
	items, _ = e.st.List()
	if items[0].ID != a.ID || items[0].Revision != 2 {
		t.Fatalf("changed project should lead: %+v", items[0])
	}

	// Export: overwrite rule.
	out := filepath.Join(t.TempDir(), "sub", "a.3mf")
	res, err := e.st.Export(a.ID, out, false)
	if err != nil || !res.CreatedFolder || res.Bytes == 0 {
		t.Fatalf("export: %+v %v", res, err)
	}
	_, err = e.st.Export(a.ID, out, false)
	wantCode(t, err, CodeConflict)
	if _, err := e.st.Export(a.ID, out, true); err != nil {
		t.Fatal(err)
	}
	_, err = e.st.Export(a.ID, filepath.Join(t.TempDir(), "x.txt"), false)
	wantCode(t, err, CodeInvalidInput)
	_, err = e.st.Export(a.ID, "relative.3mf", false)
	wantCode(t, err, CodeInvalidInput)

	// Open the export again: a new project with the same content.
	op, err := e.st.OpenProject(OpenRequest{Path: out, Name: "Reopened"})
	if err != nil {
		t.Fatal(err)
	}
	if op.Info.Name != "Reopened" || op.Info.Printer != testPrinter || op.Info.ID == a.ID {
		t.Fatalf("open: %+v", op.Info)
	}
	if v, err := e.st.UpdateSettings(op.Info.ID, SettingsRequest{Values: map[string]any{"wall_loops": 5}}); err != nil || v.Info.Revision != 2 {
		t.Fatalf("update after open: %v", err)
	}
	if got, _ := e.st.GetProject(a.ID); got.Revision != 2 {
		t.Fatalf("the original changed: %+v", got)
	}

	// Delete needs the id as confirmation.
	_, err = e.st.Delete(a.ID, "yes")
	wantCode(t, err, CodeInvalidInput)
	if id, err := e.st.Delete(a.ID, a.ID); err != nil || id != a.ID {
		t.Fatalf("delete: %v", err)
	}
	if _, err := os.Stat(filepath.Join(e.dir, "projects", a.ID)); !os.IsNotExist(err) {
		t.Fatalf("folder still there: %v", err)
	}
	wantCode(t, func() error { _, err := e.st.GetProject(a.ID); return err }(), CodeNotFound)
}

func TestOpenRefusesPlain3MF(t *testing.T) {
	e := newEnv(t)
	p := filepath.Join(t.TempDir(), "plain.3mf")
	f, _ := os.Create(p)
	zw := zip.NewWriter(f)
	w, _ := zw.Create("3D/3dmodel.model")
	w.Write([]byte(`<?xml version="1.0"?><model unit="millimeter" xmlns="http://schemas.microsoft.com/3dmanufacturing/core/2015/02"><resources/><build/></model>`))
	zw.Close()
	f.Close()
	_, err := e.st.OpenProject(OpenRequest{Path: p})
	ae := wantCode(t, err, CodeInvalidInput)
	if !strings.Contains(ae.Message, "plain 3MF") {
		t.Fatalf("message: %v", ae.Message)
	}
	_, err = e.st.OpenProject(OpenRequest{Path: filepath.Join(t.TempDir(), "missing.3mf")})
	wantCode(t, err, CodeInvalidInput)
	_, err = e.st.OpenProject(OpenRequest{Path: "rel.3mf"})
	wantCode(t, err, CodeInvalidInput)
	if items, _ := e.st.List(); len(items) != 0 {
		t.Fatalf("refused opens left projects: %+v", items)
	}
}

func TestConcurrentChangesKeepEveryRevision(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Busy")
	var wg sync.WaitGroup
	const n = 6
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := e.st.ManagePlates(info.ID, PlatesRequest{Action: PlateAdd, Name: "p"}); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	got, err := e.st.GetProject(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Revision != 1+n || len(got.Plates) != 1+n {
		t.Fatalf("revision %d, plates %d", got.Revision, len(got.Plates))
	}
}

func TestAddModelPlacement(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Pack")
	path := writeSTL(t, "cube", 40, 40, 20)
	res, err := e.st.AddModel(info.ID, AddModelRequest{Path: path, Copies: 4})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Added) != 4 {
		t.Fatalf("added %d", len(res.Added))
	}
	var rects []rect
	for _, o := range res.Added {
		if o.Size != [3]float64{40, 40, 20} {
			t.Fatalf("size %v", o.Size)
		}
		if math.Abs(o.Position[2]) > 1e-6 {
			t.Fatalf("not on the bed: %v", o.Position)
		}
		r := rect{o.Position[0] - 20, o.Position[1] - 20, o.Position[0] + 20, o.Position[1] + 20}
		if r.x0 < 10-1e-6 || r.y0 < 10-1e-6 || r.x1 > 250+1e-6 || r.y1 > 250+1e-6 {
			t.Fatalf("outside the margin: %+v", r)
		}
		for _, q := range rects {
			if r.overlaps(q.inflate(PlacementGap - 1e-6)) {
				t.Fatalf("closer than the gap: %+v %+v", r, q)
			}
		}
		rects = append(rects, r)
		if o.Filament != 1 {
			t.Fatalf("filament %d", o.Filament)
		}
	}
	if res.Info.Objects[0].Name != "cube_1" || res.Info.Revision != 2 {
		t.Fatalf("names/revision: %+v", res.Info.Objects[0])
	}
	if hasWarning(res.Info, "outside_bed") {
		t.Fatal("unexpected outside_bed warning")
	}

	// Too big for the free area.
	big := writeSTL(t, "big", 300, 20, 10)
	_, err = e.st.AddModel(info.ID, AddModelRequest{Path: big})
	ae := wantCode(t, err, CodeConflict)
	if !strings.Contains(ae.Message, "does not fit") || ae.Hint == "" {
		t.Fatalf("conflict: %+v", ae)
	}
	// Nothing was added by the failed call.
	if got, _ := e.st.GetProject(info.ID); len(got.Objects) != 4 || got.Revision != 2 {
		t.Fatalf("failed add changed the project: %d objects, revision %d", len(got.Objects), got.Revision)
	}
	// Second filament and an explicit position.
	x, y := 130.0, 200.0
	one, err := e.st.AddModel(info.ID, AddModelRequest{Path: writeSTL(t, "small", 10, 10, 10), Filament: 2, X: &x, Y: &y, Name: "peg"})
	if err != nil {
		t.Fatal(err)
	}
	if o := one.Added[0]; o.Name != "peg" || o.Filament != 2 || math.Abs(o.Position[0]-130) > 1e-6 || math.Abs(o.Position[1]-200) > 1e-6 {
		t.Fatalf("explicit: %+v", o)
	}
	// Errors.
	_, err = e.st.AddModel(info.ID, AddModelRequest{Path: path, Filament: 3})
	wantCode(t, err, CodeInvalidInput)
	_, err = e.st.AddModel(info.ID, AddModelRequest{Path: path, X: &x})
	wantCode(t, err, CodeInvalidInput)
	_, err = e.st.AddModel(info.ID, AddModelRequest{Path: "nofile.stl"})
	wantCode(t, err, CodeInvalidInput)
	_, err = e.st.AddModel(info.ID, AddModelRequest{Path: filepath.Join(t.TempDir(), "x.txt")})
	wantCode(t, err, CodeInvalidInput)
	_, err = e.st.AddModel(info.ID, AddModelRequest{Path: path, Overrides: map[string]any{"bogus": 1}})
	wantCode(t, err, CodeInvalidInput)
}

func TestWipeTowerStaysFree(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Tower")
	if _, err := e.st.UpdateSettings(info.ID, SettingsRequest{Values: map[string]any{"enable_prime_tower": true}}); err != nil {
		t.Fatal(err)
	}
	var tower rect
	if err := e.st.read(info.ID, func(h *handle) error {
		var ok bool
		tower, ok = h.towerRect(1)
		if !ok {
			t.Fatal("no tower rect with two filaments and the tower on")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	res, err := e.st.AddModel(info.ID, AddModelRequest{Path: writeSTL(t, "c", 30, 30, 10), Copies: 6})
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range res.Added {
		r := rect{o.Position[0] - 15, o.Position[1] - 15, o.Position[0] + 15, o.Position[1] + 15}
		if r.overlaps(tower) {
			t.Fatalf("%+v overlaps the wipe tower %+v", r, tower)
		}
	}
}

func TestUpdateObject(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Move")
	added := e.addBox(t, info.ID, "block", 20, 10, 5)
	id := added.Added[0].Name
	x, y, z := 100.0, 120.0, 2.0
	rot := [3]float64{0, 0, 90}
	res, err := e.st.UpdateObject(info.ID, UpdateObjectRequest{Object: id, X: &x, Y: &y, Z: &z, Rotation: &rot})
	if err != nil {
		t.Fatal(err)
	}
	o := res.Object
	if math.Abs(o.Position[0]-100) > 1e-4 || math.Abs(o.Position[1]-120) > 1e-4 || math.Abs(o.Position[2]-2) > 1e-4 {
		t.Fatalf("position %v", o.Position)
	}
	if math.Abs(o.Size[0]-10) > 1e-4 || math.Abs(o.Size[1]-20) > 1e-4 || math.Abs(o.Rotation[2]-90) > 1e-4 {
		t.Fatalf("size %v rotation %v", o.Size, o.Rotation)
	}
	// Scale keeps the position.
	res, err = e.st.UpdateObject(info.ID, UpdateObjectRequest{Object: id, ScaleAll: ptr(2.0)})
	if err != nil {
		t.Fatal(err)
	}
	o = res.Object
	if math.Abs(o.Size[0]-20) > 1e-4 || math.Abs(o.Size[2]-10) > 1e-4 || math.Abs(o.Position[0]-100) > 1e-4 || math.Abs(o.Position[2]-2) > 1e-4 || math.Abs(o.Scale[0]-2) > 1e-6 {
		t.Fatalf("scaled: %+v", o)
	}
	// Rename, filament, plate move.
	if _, err := e.st.ManagePlates(info.ID, PlatesRequest{Action: PlateAdd}); err != nil {
		t.Fatal(err)
	}
	res, err = e.st.UpdateObject(info.ID, UpdateObjectRequest{Object: id, Name: ptr("renamed"), Filament: ptr(2), Plate: ptr(2)})
	if err != nil {
		t.Fatal(err)
	}
	if res.Object.Name != "renamed" || res.Object.Filament != 2 || res.Object.Plate != 2 {
		t.Fatalf("object: %+v", res.Object)
	}
	// Lay flat: rotate a box on its side, then lay flat puts the biggest face down.
	rot = [3]float64{90, 0, 0}
	if _, err := e.st.UpdateObject(info.ID, UpdateObjectRequest{Object: "renamed", Rotation: &rot, ScaleAll: ptr(1.0)}); err != nil {
		t.Fatal(err)
	}
	res, err = e.st.UpdateObject(info.ID, UpdateObjectRequest{Object: "renamed", LayFlat: true})
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(res.Object.Size[2]-5) > 1e-4 || math.Abs(res.Object.Position[2]) > 1e-4 {
		t.Fatalf("lay flat: size %v position %v", res.Object.Size, res.Object.Position)
	}
	// Errors.
	_, err = e.st.UpdateObject(info.ID, UpdateObjectRequest{Object: "ghost"})
	wantCode(t, err, CodeNotFound)
	_, err = e.st.UpdateObject(info.ID, UpdateObjectRequest{Object: "renamed", Filament: ptr(9)})
	wantCode(t, err, CodeInvalidInput)
	_, err = e.st.UpdateObject(info.ID, UpdateObjectRequest{Object: "renamed", ScaleAll: ptr(-1.0)})
	wantCode(t, err, CodeInvalidInput)
	_, err = e.st.UpdateObject(info.ID, UpdateObjectRequest{Object: "renamed", Plate: ptr(7)})
	wantCode(t, err, CodeNotFound)

	// Moving outside the bed is allowed but warned about.
	far := 400.0
	res, err = e.st.UpdateObject(info.ID, UpdateObjectRequest{Object: "renamed", X: &far})
	if err != nil || !hasWarning(res.Info, "outside_bed") {
		t.Fatalf("outside warning: %v %v", err, res.Info.Warnings)
	}
	// Remove.
	info2, err := e.st.RemoveObject(info.ID, "renamed")
	if err != nil || len(info2.Objects) != 0 {
		t.Fatalf("remove: %v", err)
	}
	_, err = e.st.RemoveObject(info.ID, "renamed")
	wantCode(t, err, CodeNotFound)
}

func ptr[T any](v T) *T { return &v }

func TestCreateWithDefaults(t *testing.T) {
	e := newEnv(t)
	info, err := e.st.CreateProject(CreateRequest{Name: "Defaults", Filaments: []FilamentSpec{{Preset: testPLA, Colour: "#112233"}}})
	if err != nil {
		t.Fatal(err)
	}
	if info.Printer != DefaultPrinter || info.Process != testProcess {
		t.Fatalf("defaults: %q %q", info.Printer, info.Process)
	}
	if info.Filaments[0].Colour != "#112233" || len(info.FlushMatrix) != 1 {
		t.Fatalf("filaments: %+v", info.Filaments)
	}
}

// The auto flush matrix follows the rule of the GUI: with the printer allowing
// a 30 mm long retraction per filament and the filaments leaving their flag
// unset, the base is 183 - 72 = 110 mm3, which is what the GUI wrote into the
// golden project (D-C1-3): the same colours give the same matrix exactly.
func TestAutoFlushMatrixIsTheGoldenOne(t *testing.T) {
	e := newEnv(t)
	info, err := e.st.CreateProject(CreateRequest{Name: "Golden colours", Printer: testPrinter, Process: testProcess, Filaments: []FilamentSpec{
		{Preset: testPETG, Colour: "#000000"}, {Preset: testPETG, Colour: "#F4E076"}, {Preset: testPETG, Colour: "#FFFFFF"}}})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(info.FlushMatrix, ","), "0,677,670,232,0,333,190,229,0"; got != want {
		t.Fatalf("matrix %s, want the golden %s", got, want)
	}
	if info.FlushMode != "auto" || info.FlushMultiplier == "" {
		t.Fatalf("%+v", info)
	}
}
