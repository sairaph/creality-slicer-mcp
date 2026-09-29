package projects

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sairaph/creality-slicer-mcp/internal/slicer"
	"github.com/sairaph/creality-slicer-mcp/internal/threemf"
)

func twoPlateProject(t *testing.T, e *testEnv, name string) string {
	t.Helper()
	info := e.newProject(t, name)
	if _, err := e.st.ManagePlates(info.ID, PlatesRequest{Action: PlateAdd}); err != nil {
		t.Fatal(err)
	}
	for i, plate := range []int{1, 2} {
		x, y := 100.0, 100.0
		if _, err := e.st.AddModel(info.ID, AddModelRequest{Path: writeSTL(t, "c", 20, 20, 10), Plate: plate, X: &x, Y: &y, Name: []string{"one", "two"}[i]}); err != nil {
			t.Fatal(err)
		}
	}
	return info.ID
}

// PR1: a slice replaces the records of the plates it sliced and keeps the
// others, each with its own revision; only a changed plate is stale.
func TestPerPlateSliceRecords(t *testing.T) {
	e := newEnv(t)
	id := twoPlateProject(t, e, "PerPlate")
	if _, err := e.st.Slice(id, SliceOptions{Plate: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.Slice(id, SliceOptions{Plate: 2}); err != nil {
		t.Fatal(err)
	}
	// Both plates have a record and a report; neither is stale.
	for _, plate := range []int{1, 2} {
		rep, err := e.st.Report(id, plate)
		if err != nil || rep.Stale || rep.Plate.Plate != plate {
			t.Fatalf("plate %d: %+v %v", plate, rep, err)
		}
	}
	got, _ := e.st.GetProject(id)
	if got.LastSlice == nil || got.LastSlice.Plates != 2 || got.LastSlice.Stale {
		t.Fatalf("stamp %+v", got.LastSlice)
	}
	st, _ := e.st.SliceStatus(id)
	if st.Last == nil || len(st.Last.Plates) != 2 || st.Stale {
		t.Fatalf("status %+v", st)
	}
	// The handoff data of plate 1 (upload name, tools, exclude names) is kept.
	r1, _ := e.st.Report(id, 1)
	if r1.Plate.UploadName != "PerPlate_plate1.gcode" || len(r1.Plate.Tools) != 2 || len(r1.Plate.ExcludeNames) != 1 {
		t.Fatalf("plate 1 handoff lost: %+v", r1.Plate)
	}
	// A change to plate 1 makes plate 1 stale and plate 2 not.
	if _, err := e.st.UpdateObject(id, UpdateObjectRequest{Object: "one", X: ptr(60.0)}); err != nil {
		t.Fatal(err)
	}
	if r, _ := e.st.Report(id, 1); !r.Stale {
		t.Fatal("plate 1 should be stale")
	}
	if r, _ := e.st.Report(id, 2); r.Stale {
		t.Fatal("plate 2 should not be stale")
	}
	got, _ = e.st.GetProject(id)
	if got.LastSlice == nil || len(got.LastSlice.StalePlates) != 1 || got.LastSlice.StalePlates[0] != 1 || !hasWarning(got, "stale_slice") {
		t.Fatalf("stamp %+v warnings %v", got.LastSlice, got.Warnings)
	}
	// A setting change touches every plate.
	if _, err := e.st.UpdateSettings(id, SettingsRequest{Values: map[string]any{"wall_loops": 4}}); err != nil {
		t.Fatal(err)
	}
	if r, _ := e.st.Report(id, 2); !r.Stale {
		t.Fatal("plate 2 should be stale after a setting change")
	}
	// Slicing plate 1 again makes it current and leaves plate 2's record.
	if _, err := e.st.Slice(id, SliceOptions{Plate: 1}); err != nil {
		t.Fatal(err)
	}
	if r, _ := e.st.Report(id, 1); r.Stale {
		t.Fatal("plate 1 was sliced again")
	}
	if _, err := e.st.Report(id, 2); err != nil {
		t.Fatalf("plate 2 record lost: %v", err)
	}
	// A plate that was never sliced says which were.
	if _, err := e.st.ManagePlates(id, PlatesRequest{Action: PlateAdd}); err != nil {
		t.Fatal(err)
	}
	_, err := e.st.Report(id, 3)
	ae := wantCode(t, err, CodeNotFound)
	if !strings.Contains(ae.Hint, "1, 2") {
		t.Fatalf("hint %q", ae.Hint)
	}
	// Removing a plate drops the records of plates that no longer exist.
	if _, err := e.st.ManagePlates(id, PlatesRequest{Action: PlateRemove, Plate: 3}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.UpdateObject(id, UpdateObjectRequest{Object: "two", Plate: ptr(1)}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.ManagePlates(id, PlatesRequest{Action: PlateRemove, Plate: 2}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.Report(id, 2); AsError(err).Code != CodeNotFound {
		t.Fatalf("record of a removed plate: %v", err)
	}
}

// PR2: a save waits for readers (the project file is open while they read).
func TestWriterWaitsForReaders(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Readers")
	inRead, release := make(chan struct{}), make(chan struct{})
	go func() {
		e.st.read(info.ID, func(h *handle) error {
			close(inRead)
			<-release
			return nil
		})
	}()
	<-inRead
	done := make(chan error, 1)
	go func() {
		_, err := e.st.UpdateSettings(info.ID, SettingsRequest{Values: map[string]any{"wall_loops": 4}})
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("the writer did not wait for the reader: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	// Another reader is not blocked by the waiting writer's peers.
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the writer never ran")
	}
	// Readers share the lock.
	var wg sync.WaitGroup
	inside := make(chan struct{}, 2)
	gate := make(chan struct{})
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e.st.read(info.ID, func(h *handle) error { inside <- struct{}{}; <-gate; return nil })
		}()
	}
	for i := 0; i < 2; i++ {
		select {
		case <-inside:
		case <-time.After(10 * time.Second):
			t.Fatal("two readers cannot read at once")
		}
	}
	close(gate)
	wg.Wait()
}

// PR3: a second server (a second Store on the same folder) never slices the
// project of a running slice, and cannot delete it.
func TestSliceGuardAcrossStores(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Shared")
	e.addBox(t, info.ID, "cube", 20, 20, 20)
	other, err := New(e.st.cfg)
	if err != nil {
		t.Fatal(err)
	}
	e.exec.block = make(chan struct{})
	out, err := e.st.Slice(info.ID, SliceOptions{Background: true})
	if err != nil || !out.Running {
		t.Fatalf("%+v %v", out, err)
	}
	_, err = other.Slice(info.ID, SliceOptions{Background: true})
	ae := wantCode(t, err, CodeConflict)
	if !strings.Contains(ae.Message, "another process") {
		t.Fatalf("message %q", ae.Message)
	}
	_, err = other.Delete(info.ID, info.ID)
	wantCode(t, err, CodeConflict)
	close(e.exec.block)
	<-e.st.cfg.Jobs.Get(out.JobID).Done()
	// Released after the post processing: the other store can slice and delete.
	if _, err := other.Slice(info.ID, SliceOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := other.Delete(info.ID, info.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.st.locks[info.ID]; ok {
		t.Log("lock entry of the first store stays (only the deleting store forgets it)")
	}
	if _, ok := other.locks[info.ID]; ok {
		t.Fatal("the lock table keeps a deleted project")
	}
}

// PR4: listing reads job.json, not the project files.
func TestListDoesNotOpenTheProjects(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Summary")
	e.addBox(t, info.ID, "cube", 20, 20, 20)
	if err := os.WriteFile(filepath.Join(e.dir, "projects", info.ID, "project.3mf"), []byte("not a zip"), 0o644); err != nil {
		t.Fatal(err)
	}
	items, err := e.st.List()
	if err != nil || len(items) != 1 || items[0].Objects != 1 || items[0].Plates != 1 || items[0].Printer != testPrinter {
		t.Fatalf("%+v %v", items, err)
	}
	if _, err := e.st.resolve("summary"); err != nil {
		t.Fatalf("resolve by name: %v", err)
	}
}

// PR6: a folder that exists is never reused; old half made "opening" folders go.
func TestNewProjectNeverReusesAFolder(t *testing.T) {
	e := newEnv(t)
	a := e.newProject(t, "Same")
	b := e.newProject(t, "Same")
	if a.ID == b.ID {
		t.Fatal("two projects share an id")
	}
	root := filepath.Join(e.dir, "projects")
	stale := filepath.Join(root, "opening-abcdef")
	os.MkdirAll(filepath.Join(stale, "out"), 0o755)
	old := time.Now().Add(-3 * time.Hour)
	os.Chtimes(stale, old, old)
	fresh := filepath.Join(root, "opening-123456")
	os.MkdirAll(fresh, 0o755)
	e.st.now()
	// The store clock of the fixture is fixed in 2026: use a real clock for the sweep.
	e.st.cfg.Now = time.Now
	e.st.sweepOnce = sync.Once{}
	e.newProject(t, "Third")
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale opening folder kept: %v", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatalf("a fresh opening folder was swept: %v", err)
	}
}

// PR7: the copy of the project made for the slicer is removed afterwards.
func TestSliceInputCopyIsRemoved(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Tidy")
	e.addBox(t, info.ID, "cube", 20, 20, 20)
	if _, err := e.st.Slice(info.ID, SliceOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.dir, "projects", info.ID, "out", "slice_input.3mf")); !os.IsNotExist(err) {
		t.Fatalf("slice_input.3mf stays: %v", err)
	}
	if _, err := os.Stat(filepath.Join(e.dir, "projects", info.ID, "out", "plate_1.gcode")); err != nil {
		t.Fatal(err)
	}
}

// PR8: names that lose their letters get the id suffix in the upload name.
func TestUploadNames(t *testing.T) {
	for _, c := range []struct{ name, id, want string }{
		{"Slice: Me/Now", "slice-me-now-0a798f", "Slice_Me_Now"},
		{"My Cube", "my-cube-123456", "My_Cube"},
		{"Geät", "geat-abcdef", "Ge_t_abcdef"},
		{"日本語", "project-00ff00", "project_00ff00"},
		{"   ", "project-00ff00", "project_00ff00"},
	} {
		if got := uploadBase(c.name, c.id); got != c.want {
			t.Errorf("uploadBase(%q) = %q, want %q", c.name, got, c.want)
		}
	}
}

// PR9: x without y is refused for manage_plates move.
func TestMoveNeedsBothCoordinates(t *testing.T) {
	e := newEnv(t)
	id := twoPlateProject(t, e, "Coords")
	_, err := e.st.ManagePlates(id, PlatesRequest{Action: PlateMove, Object: "one", Plate: 2, X: ptr(50.0)})
	wantCode(t, err, CodeInvalidInput)
}

// MC10: negative scale factors are refused like in update_object.
func TestAddModelRefusesNegativeScale(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Scale")
	_, err := e.st.AddModel(info.ID, AddModelRequest{Path: writeSTL(t, "c", 10, 10, 10), Scale: [3]float64{1, -1, 1}})
	wantCode(t, err, CodeInvalidInput)
	_, err = e.st.AddModel(info.ID, AddModelRequest{Path: writeSTL(t, "c", 10, 10, 10), ScaleAll: -2})
	wantCode(t, err, CodeInvalidInput)
}

// MC8: the modifier offset is resolved inside the store, by the store's name rule.
func TestModifierOffsetIsRelativeToTheObject(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Offset")
	e.addBox(t, info.ID, "Cube", 40, 40, 40)
	if _, err := e.st.UpdateObject(info.ID, UpdateObjectRequest{Object: "cube", X: ptr(100.0), Y: ptr(120.0)}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.AddModifier(info.ID, ModifierRequest{Object: "CUBE", Subtype: "negative", Shape: ShapeBox, Size: [3]float64{10, 10, 10}, Relative: &[3]float64{5, -5, 0}}); err != nil {
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
			c := center3(b)
			if math.Abs(c[0]-105) > 1e-3 || math.Abs(c[1]-115) > 1e-3 || math.Abs(c[2]-20) > 1e-3 {
				t.Fatalf("centre %v, want 105, 115, 20", c)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// PR5: a custom action carries its text in "extra" (what the slicer reads), and
// actions that cannot work are warned about.
func TestLayerActionWarningsAndCustomText(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Actions2")
	e.addBox(t, info.ID, "cube", 20, 20, 10)
	res, err := e.st.SetLayerActions(info.ID, 1, []LayerAction{
		{Layer: 3, Kind: ActionCustom, GCode: "M117 hello"},
		{Layer: 200, Kind: ActionPause},
		{Layer: 4, Kind: ActionColorChange, Colour: "#FF0000"},
	})
	if err != nil {
		t.Fatal(err)
	}
	gc := openSaved(t, e, info.ID).CustomGCodes(1)
	if len(gc.Items) != 3 || gc.Items[0].Extra != "M117 hello" {
		t.Fatalf("custom text not in extra: %+v", gc.Items)
	}
	if res.Plates[0].Actions[0].GCode != "M117 hello" {
		t.Fatalf("shown %+v", res.Plates[0].Actions[0])
	}
	if !hasWarning(res, "action_above_model") || !hasWarning(res, "color_change_no_gcode") {
		t.Fatalf("warnings %+v", res.Warnings)
	}
}

// MC6: a wait ends when the caller's context does; the job goes on.
func TestWaitEndsWithTheCallersContext(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Ctx")
	e.addBox(t, info.ID, "cube", 20, 20, 20)
	e.exec.block = make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(200 * time.Millisecond); cancel() }()
	start := time.Now()
	out, err := e.st.Slice(info.ID, SliceOptions{Wait: time.Hour, Ctx: ctx})
	if err != nil || !out.Running || out.JobID == "" || time.Since(start) > 30*time.Second {
		t.Fatalf("%+v %v after %s", out, err, time.Since(start))
	}
	close(e.exec.block)
	<-e.st.cfg.Jobs.Get(out.JobID).Done()
	if st, _ := e.st.SliceStatus(out.JobID); st.State != slicer.StateFinished || st.Last == nil {
		t.Fatalf("the job did not go on: %+v", st)
	}
}

// MC6: the -24 retry runs under the job's context: cancelling the job stops it.
func TestNewerFileRetryIsCancellable(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Retry")
	e.addBox(t, info.ID, "cube", 20, 20, 20)
	var mu sync.Mutex
	calls := 0
	second := make(chan struct{})
	e.exec.ctxFn = func(ctx context.Context, spec slicer.ExecSpec) (slicer.ExecResult, error) {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		if n == 1 {
			return slicer.ExecResult{ExitCode: -24}, nil
		}
		close(second) // the retry runs: it only ends with the job's context
		<-ctx.Done()
		return slicer.ExecResult{Killed: true}, nil
	}
	out, err := e.st.Slice(info.ID, SliceOptions{Background: true})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-second:
	case <-time.After(30 * time.Second):
		t.Fatal("the retry did not start")
	}
	if ok, err := e.st.CancelSlice(out.JobID); err != nil || !ok {
		t.Fatalf("cancel: %v %v", ok, err)
	}
	select {
	case <-e.st.cfg.Jobs.Get(out.JobID).Done():
	case <-time.After(30 * time.Second):
		t.Fatal("the cancelled retry kept running")
	}
	if st, _ := e.st.SliceStatus(out.JobID); st.State == slicer.StateRunning || st.Last != nil {
		t.Fatalf("state %+v", st)
	}
}

// TM1: a control character in a name is invalid input at the tool layer.
func TestControlCharacterInANameIsInvalidInput(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Names")
	e.addBox(t, info.ID, "cube", 20, 20, 20)
	_, err := e.st.UpdateObject(info.ID, UpdateObjectRequest{Object: "cube", Name: ptr("a\x0bb")})
	wantCode(t, err, CodeInvalidInput)
	_, err = e.st.ManagePlates(info.ID, PlatesRequest{Action: PlateRename, Plate: 1, Name: "p\x00"})
	wantCode(t, err, CodeInvalidInput)
	_, err = e.st.AddModel(info.ID, AddModelRequest{Path: writeSTL(t, "c", 10, 10, 10), Name: "n\x1f"})
	wantCode(t, err, CodeInvalidInput)
}

// A run that already uses the output folder is a conflict with polling advice,
// for the background start and for the foreground run.
func TestBusyOutputFolderIsAConflict(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Busy2")
	e.addBox(t, info.ID, "cube", 20, 20, 20)
	out := filepath.Join(e.dir, "projects", info.ID, "out")
	e.exec.block = make(chan struct{})
	started := make(chan struct{})
	e.exec.ctxFn = func(ctx context.Context, spec slicer.ExecSpec) (slicer.ExecResult, error) {
		close(started)
		<-e.exec.block
		return slicer.ExecResult{}, nil
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		e.st.cfg.Runner.Run(context.Background(), slicer.SliceRequest{Inputs: []string{filepath.Join(out, "other.3mf")}, OutputDir: out, Plate: 1})
	}()
	<-started
	e.exec.ctxFn = nil
	for _, o := range []SliceOptions{{Background: true}, {}} {
		_, err := e.st.Slice(info.ID, o)
		ae := wantCode(t, err, CodeConflict)
		if !strings.Contains(ae.Message, "already being sliced") || !strings.Contains(ae.Hint, "get_slice_status") {
			t.Fatalf("%+v: %+v", o, ae)
		}
	}
	close(e.exec.block)
	<-done
}

func TestAddModelMessagesAndScaleAxes(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Msgs")
	_, err := e.st.AddModel(info.ID, AddModelRequest{Path: filepath.Join(t.TempDir(), "gone.stl")})
	ae := wantCode(t, err, CodeInvalidInput)
	if !strings.Contains(ae.Message, "does not exist") || !strings.Contains(ae.Hint, "absolute path to an existing .stl, .obj or .3mf") {
		t.Fatalf("%+v", ae)
	}
	_, err = e.st.AddModel(info.ID, AddModelRequest{Path: "gone.stl"})
	wantCode(t, err, CodeInvalidInput)
	// An array with any axis given needs every axis above zero.
	_, err = e.st.AddModel(info.ID, AddModelRequest{Path: writeSTL(t, "c", 10, 10, 10), Scale: [3]float64{2, 0, 2}})
	wantCode(t, err, CodeInvalidInput)
	if _, err := e.st.AddModel(info.ID, AddModelRequest{Path: writeSTL(t, "c", 10, 10, 10), Scale: [3]float64{2, 2, 2}}); err != nil {
		t.Fatal(err)
	}
}

// D1: the slicer crashes on a height range without layer_height (verified with
// 7.2.2 and 7.3): every range written by the tools carries one.
func TestHeightRangesAlwaysCarryLayerHeight(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "D1")
	e.addBox(t, info.ID, "cube", 20, 20, 20)
	res, err := e.st.SetHeightRanges(info.ID, "cube", []RangeSpec{
		{From: 4, To: 8, Settings: map[string]any{"wall_loops": 4}},
		{From: 10, To: 14, Settings: map[string]any{"layer_height": 0.1, "wall_loops": 3}},
	})
	if err != nil {
		t.Fatal(err)
	}
	hr := res.Objects[0].HeightRanges
	if hr[0].Settings["layer_height"] != "0.2" || hr[1].Settings["layer_height"] != "0.1" {
		t.Fatalf("ranges %+v", hr)
	}
	saved := openSaved(t, e, info.ID).Objects[0].LayerRanges
	if saved[0].Options.Value("layer_height") != "0.2" || saved[0].Options.Value("wall_loops") != "4" {
		t.Fatalf("saved %+v", saved)
	}
	// An object with its own layer height gives its value to the ranges.
	if _, err := e.st.UpdateSettings(info.ID, SettingsRequest{Scope: ScopeObject, Target: "cube", Values: map[string]any{"layer_height": 0.12}}); err != nil {
		t.Fatal(err)
	}
	res, _ = e.st.SetHeightRanges(info.ID, "cube", []RangeSpec{{From: 4, To: 8, Settings: map[string]any{"wall_loops": 4}}})
	if got := res.Objects[0].HeightRanges[0].Settings["layer_height"]; got != "0.12" {
		t.Fatalf("object layer height not used: %q", got)
	}
	// layer_height cannot be removed from a range.
	_, err = e.st.UpdateSettings(info.ID, SettingsRequest{Scope: ScopeLayerRange, Target: "cube/1", Values: map[string]any{"layer_height": nil}})
	wantCode(t, err, CodeInvalidInput)
	if _, err := e.st.UpdateSettings(info.ID, SettingsRequest{Scope: ScopeLayerRange, Target: "cube/1", Values: map[string]any{"layer_height": 0.16}}); err != nil {
		t.Fatal(err)
	}
	// A range from another tool without it is warned about.
	if err := e.st.write(info.ID, func(h *handle) error {
		lr := threemf.LayerRange{MinZ: 1, MaxZ: 2}
		lr.Options.Set("wall_loops", "5")
		h.touchAll()
		return h.p.SetLayerRanges(h.p.Objects[0].ID, []threemf.LayerRange{lr})
	}); err != nil {
		t.Fatal(err)
	}
	got, _ := e.st.GetProject(info.ID)
	if !hasWarning(got, "range_no_layer_height") {
		t.Fatalf("warnings %+v", got.Warnings)
	}
}

// D2: printing by object keeps the printer's extruder clearance (radius 64 mm on
// the K2): placement and arrange space the objects, the warning and the -63
// hint state the needed distance and never send 7.2 with several filaments to
// print by layer.
func TestByObjectClearance(t *testing.T) {
	e := newEnv(t)
	e.st.cfg.Install.Dialect = "v72"
	// The K2 values; the synthetic printer preset has none of them.
	info := e.newProject(t, "Seq")
	if _, err := e.st.UpdateSettings(info.ID, SettingsRequest{Values: map[string]any{"print_sequence": "by object", "extruder_clearance_radius": 64, "extruder_clearance_height_to_lid": 118, "extruder_clearance_height_to_rod": 24}, AllowLocked: true}); err != nil {
		t.Fatal(err)
	}
	// Automatic placement keeps the clearance: at least 2 * (32 - 0.1) mm between the objects.
	res, err := e.st.AddModel(info.ID, AddModelRequest{Path: writeSTL(t, "tall", 20, 20, 30), Copies: 3, Filament: 1})
	if err != nil {
		t.Fatal(err)
	}
	for i := range res.Added {
		for j := i + 1; j < len(res.Added); j++ {
			a, b := res.Added[i], res.Added[j]
			ra := rect{a.Position[0] - 10, a.Position[1] - 10, a.Position[0] + 10, a.Position[1] + 10}
			rb := rect{b.Position[0] - 10, b.Position[1] - 10, b.Position[0] + 10, b.Position[1] + 10}
			if d := rectDistance(ra, rb); d < 63.8 {
				t.Fatalf("%s and %s only %.1f mm apart", a.Name, b.Name, d)
			}
		}
	}
	if hasWarning(res.Info, "sequence_clearance") {
		t.Fatalf("placed objects warn: %+v", res.Info.Warnings)
	}
	// Move one object close to another: the warning names the distance needed.
	up, err := e.st.UpdateObject(info.ID, UpdateObjectRequest{Object: "tall_2", X: ptr(res.Added[0].Position[0] + 30), Y: ptr(res.Added[0].Position[1])})
	if err != nil {
		t.Fatal(err)
	}
	var msg string
	for _, w := range up.Info.Warnings {
		if w.Code == "sequence_clearance" {
			msg = w.Message
		}
	}
	if !strings.Contains(msg, "-63") || !strings.Contains(msg, "needs 64 mm") || !strings.Contains(msg, "arrange") {
		t.Fatalf("warning %q", msg)
	}
	// The failure hint: distance, objects, arrange; no by-layer advice on 7.2 with two filaments.
	if _, err := e.st.UpdateObject(info.ID, UpdateObjectRequest{Object: "tall_3", Filament: ptr(2)}); err != nil {
		t.Fatal(err)
	}
	e.exec.fn = func(spec slicer.ExecSpec) (slicer.ExecResult, error) { return slicer.ExecResult{ExitCode: -63}, nil }
	_, err = e.st.Slice(info.ID, SliceOptions{})
	ae := wantCode(t, err, CodeSlicerError)
	if !strings.Contains(ae.Hint, "64 mm") || !strings.Contains(ae.Hint, "arrange true") || strings.Contains(ae.Hint, "by layer") {
		t.Fatalf("hint %q", ae.Hint)
	}
	// Arrange packs the plate again with the clearance.
	if _, err := e.st.AutoPlace(info.ID, 1, false, true); err != nil {
		t.Fatal(err)
	}
	got, _ := e.st.GetProject(info.ID)
	if hasWarning(got, "sequence_clearance") {
		t.Fatalf("after arrange: %+v", got.Warnings)
	}
	// With one filament, or on 7.3, by layer is offered.
	e.st.cfg.Install.Dialect = "v73"
	e.exec.fn = func(spec slicer.ExecSpec) (slicer.ExecResult, error) { return slicer.ExecResult{ExitCode: -63}, nil }
	_, err = e.st.Slice(info.ID, SliceOptions{})
	if ae := wantCode(t, err, CodeSlicerError); !strings.Contains(ae.Hint, "by layer") {
		t.Fatalf("7.3 hint %q", ae.Hint)
	}
}

// D2: a tall object printed before others in reach of the rod is too tall.
func TestByObjectTooTall(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Tall")
	if _, err := e.st.UpdateSettings(info.ID, SettingsRequest{Values: map[string]any{"print_sequence": "by object", "extruder_clearance_radius": 40, "extruder_clearance_height_to_lid": 100, "extruder_clearance_height_to_rod": 24}, AllowLocked: true}); err != nil {
		t.Fatal(err)
	}
	a, b := 60.0, 130.0
	y := 100.0
	if _, err := e.st.AddModel(info.ID, AddModelRequest{Path: writeSTL(t, "t", 20, 60, 50), X: &a, Y: &y, Name: "first"}); err != nil {
		t.Fatal(err)
	}
	res, err := e.st.AddModel(info.ID, AddModelRequest{Path: writeSTL(t, "s", 20, 60, 10), X: &b, Y: &y, Name: "second"})
	if err != nil {
		t.Fatal(err)
	}
	var msg string
	for _, w := range res.Info.Warnings {
		if w.Code == "sequence_clearance" {
			msg = w.Message
		}
	}
	if !strings.Contains(msg, `"first" is 50 mm tall but only 24 mm is allowed`) {
		t.Fatalf("warning %q", msg)
	}
}

// D3: 7.2 with two or more filaments on a plate printed by layer is warned about.
func TestV72ByLayerCrashWarning(t *testing.T) {
	e := newEnv(t)
	e.st.cfg.Install.Dialect = "v72"
	info := e.newProject(t, "Warn72")
	if !hasWarning(info, "v72_by_layer_crash") {
		t.Fatalf("create_project: %+v", info.Warnings)
	}
	e.addBox(t, info.ID, "a", 20, 20, 10)
	got, _ := e.st.GetProject(info.ID)
	if hasWarning(got, "v72_by_layer_crash") {
		t.Fatalf("one filament used: %+v", got.Warnings)
	}
	if _, err := e.st.AddModel(info.ID, AddModelRequest{Path: writeSTL(t, "b", 20, 20, 10), Filament: 2}); err != nil {
		t.Fatal(err)
	}
	got, _ = e.st.GetProject(info.ID)
	if !hasWarning(got, "v72_by_layer_crash") {
		t.Fatalf("two filaments: %+v", got.Warnings)
	}
	up, err := e.st.UpdateSettings(info.ID, SettingsRequest{Values: map[string]any{"print_sequence": "by object"}})
	if err != nil || hasWarning(up.Info, "v72_by_layer_crash") {
		t.Fatalf("by object: %v %+v", err, up.Info.Warnings)
	}
	e.st.cfg.Install.Dialect = "v73"
	if _, err := e.st.UpdateSettings(info.ID, SettingsRequest{Values: map[string]any{"print_sequence": "by layer"}}); err != nil {
		t.Fatal(err)
	}
	if got, _ = e.st.GetProject(info.ID); hasWarning(got, "v72_by_layer_crash") {
		t.Fatal("7.3 must not warn")
	}
}

// The first object of a plate goes to the middle of the bed.
func TestFirstObjectAtTheBedCentre(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Centre")
	res := e.addBox(t, info.ID, "one", 40, 20, 10)
	p := res.Added[0].Position
	if math.Abs(p[0]-130) > 1e-3 || math.Abs(p[1]-130) > 1e-3 {
		t.Fatalf("first object at %v, want the bed centre 130, 130", p)
	}
	res2 := e.addBox(t, info.ID, "two", 40, 20, 10)
	q := res2.Added[0].Position
	a := rect{p[0] - 20, p[1] - 10, p[0] + 20, p[1] + 10}
	b := rect{q[0] - 20, q[1] - 10, q[0] + 20, q[1] + 10}
	if a.overlaps(b.inflate(PlacementGap - 1e-6)) {
		t.Fatalf("second object %v too close to the first %v", q, p)
	}
}

// D5: each exclusion label belongs to one object, also when names repeat.
func TestObjectLabelsPerObject(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Labels")
	for i := 0; i < 3; i++ {
		x, y := 60.0+70*float64(i), 100.0
		if _, err := e.st.AddModel(info.ID, AddModelRequest{Path: writeSTL(t, "b", 20, 20, 10), Name: "Body", X: &x, Y: &y}); err != nil {
			t.Fatal(err)
		}
	}
	e.exec.gcode = func(int) string {
		return gcodeFor(
			"EXCLUDE_OBJECT_DEFINE NAME=Body_id_0_copy_0 CENTER=60,100 POLYGON=[[50,90]]",
			"EXCLUDE_OBJECT_DEFINE NAME=Body_id_1_copy_0 CENTER=130,100 POLYGON=[[120,90]]",
			"EXCLUDE_OBJECT_DEFINE NAME=Body_id_2_copy_0 CENTER=200,100 POLYGON=[[190,90]]")
	}
	out, err := e.st.Slice(info.ID, SliceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := e.st.GetProject(info.ID)
	labels := out.Last.Plates[0].ObjectLabels
	if len(labels) != 3 {
		t.Fatalf("labels %+v", labels)
	}
	for i, l := range labels {
		if l.ObjectID != got.Objects[i].ID || l.Label != "Body_id_"+strconv.Itoa(i)+"_copy_0" {
			t.Fatalf("label %d: %+v (object %d)", i, l, got.Objects[i].ID)
		}
	}
}

// D12 and the failed slice log: the elapsed time is recorded, a failure carries
// the path of the slicer's log and its last meaningful lines.
func TestElapsedAndFailureLog(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Logs")
	e.addBox(t, info.ID, "cube", 20, 20, 20)
	out, err := e.st.Slice(info.ID, SliceOptions{})
	if err != nil || out.Last.ElapsedS < 0 {
		t.Fatalf("%+v %v", out, err)
	}
	e.exec.fn = func(spec slicer.ExecSpec) (slicer.ExecResult, error) {
		// The slicer writes its own log (--logfile).
		var logPath string
		for i, a := range spec.Args {
			if a == "--logfile" {
				logPath = spec.Args[i+1]
			}
		}
		var b strings.Builder
		for i := 0; i < 40; i++ {
			b.WriteString("[2026-09-29 10:00:00.000001] [0x00000001] [trace]   noise " + strconv.Itoa(i) + "\n")
			b.WriteString("[2026-09-29 10:00:00.000002] [0x00000001] [info]    step " + strconv.Itoa(i) + "\n")
		}
		b.WriteString("[2026-09-29 10:00:01.000000] [0x00000001] [error]   Object collides with the bed\n")
		os.WriteFile(logPath, []byte(b.String()), 0o644)
		return slicer.ExecResult{ExitCode: -100}, nil
	}
	_, err = e.st.Slice(info.ID, SliceOptions{})
	ae := wantCode(t, err, CodeSlicerError)
	tail, _ := ae.Fields["log_tail"].([]string)
	if len(tail) != 15 || tail[14] != "error: Object collides with the bed" || strings.Contains(strings.Join(tail, "|"), "noise") || tail[13] != "info: step 39" {
		t.Fatalf("tail %q", tail)
	}
	if p, _ := ae.Fields["log_file"].(string); !strings.HasSuffix(p, "slice.log") {
		t.Fatalf("log file %v", ae.Fields["log_file"])
	}
}

// Layer actions of the slice reply: found in the G-code, by z.
func TestSliceReportsLayerActionsFound(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Found")
	e.addBox(t, info.ID, "cube", 20, 20, 20)
	if _, err := e.st.SetLayerActions(info.ID, 1, []LayerAction{
		{Layer: 3, Kind: ActionPause}, {Layer: 5, Kind: ActionCustom, GCode: "M117 hello\nM117 again"}, {Layer: 7, Kind: ActionToolChange, Filament: 2}, {Layer: 9, Kind: ActionColorChange, Colour: "#FF0000"},
	}); err != nil {
		t.Fatal(err)
	}
	e.exec.gcode = func(int) string {
		var b strings.Builder
		b.WriteString(strings.Split(defaultGCode, "T0\n")[0])
		b.WriteString("T0\n")
		for layer, z := range []string{"0.2", "0.4", "0.6", "0.8", "1", "1.2", "1.4", "1.6"} {
			b.WriteString(";LAYER_CHANGE\n;:" + z + "\n;HEIGHT:0.2\n")
			switch layer {
			case 2:
				b.WriteString(";PAUSE_PRINT\nPAUSE\n")
			case 4:
				b.WriteString("M117 hello\nM117 again\n")
			case 6:
				b.WriteString("T1\n")
			}
		}
		b.WriteString("; EXECUTABLE_BLOCK_END\n; filament used [g] = 1.00\n; estimated printing time (normal mode) = 1m 5s\n")
		return b.String()
	}
	out, err := e.st.Slice(info.ID, SliceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	acts := out.Last.Plates[0].Actions
	if len(acts) != 4 {
		t.Fatalf("actions %+v", acts)
	}
	found := map[string]bool{}
	for _, a := range acts {
		found[a.Kind] = a.Found
	}
	if !found[ActionPause] || !found[ActionCustom] || !found[ActionToolChange] || found[ActionColorChange] {
		t.Fatalf("found %+v", acts)
	}
	for _, a := range acts {
		if a.Kind == ActionPause && math.Abs(a.AtZ-0.6) > 1e-9 {
			t.Fatalf("pause at %v", a.AtZ)
		}
	}
}

// D6: a job that ended in a failed slice is failed in the list.
func TestJobListStates(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Jobs")
	e.addBox(t, info.ID, "cube", 20, 20, 20)
	ok, err := e.st.Slice(info.ID, SliceOptions{Background: true})
	if err != nil {
		t.Fatal(err)
	}
	<-e.st.cfg.Jobs.Get(ok.JobID).Done()
	e.exec.fn = func(spec slicer.ExecSpec) (slicer.ExecResult, error) { return slicer.ExecResult{ExitCode: -63}, nil }
	bad, err := e.st.Slice(info.ID, SliceOptions{Background: true})
	if err != nil {
		t.Fatal(err)
	}
	<-e.st.cfg.Jobs.Get(bad.JobID).Done()
	states := map[string]JobInfo{}
	for _, j := range e.st.ListJobs() {
		states[j.ID] = j
	}
	if states[ok.JobID].State != slicer.StateFinished || states[bad.JobID].State != "failed" || states[bad.JobID].ExitName != "OBJECT_COLLISION_IN_SEQ_PRINT" {
		t.Fatalf("%+v", states)
	}
}

// D4: the report has the same filament change count as the slice.
func TestReportUsesTheSliceChangeCount(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Changes")
	e.addBox(t, info.ID, "cube", 20, 20, 20)
	out, err := e.st.Slice(info.ID, SliceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	rep, err := e.st.Report(info.ID, 1)
	if err != nil || rep.Summary.TotalFilamentChange != out.Last.Plates[0].Changes || rep.Summary.TotalFilamentChange != 1 {
		t.Fatalf("report %d, slice %d: %v", rep.Summary.TotalFilamentChange, out.Last.Plates[0].Changes, err)
	}
}

// D10: differences from the process preset say where they come from.
func TestExplainSettings(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Explain")
	if _, err := e.st.UpdateSettings(info.ID, SettingsRequest{Values: map[string]any{"wall_loops": 5}}); err != nil {
		t.Fatal(err)
	}
	diffs, err := e.st.ExplainSettings(info.ID, map[string]string{"wall_loops": "5", "layer_height": "0.3", "initial_layer_print_height": "0.2"})
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]SettingDiff{}
	for _, d := range diffs {
		by[d.Key] = d
	}
	if d := by["wall_loops"]; d.Origin != "project" || d.Preset != "3" || d.Used != "5" || d.Why == "" {
		t.Fatalf("wall_loops %+v", d)
	}
	if d := by["layer_height"]; d.Origin != "other" || d.Why == "" {
		t.Fatalf("layer_height %+v", d)
	}
	if _, ok := by["initial_layer_print_height"]; ok {
		t.Fatal("an equal setting is listed")
	}
}

func TestRemovePart(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Parts")
	e.addBox(t, info.ID, "box", 40, 40, 40)
	mod, err := e.st.AddModifier(info.ID, ModifierRequest{Object: "box", Subtype: "negative", Shape: ShapeBox, Size: [3]float64{5, 5, 5}, Name: "hole"})
	if err != nil {
		t.Fatal(err)
	}
	if len(mod.Info.Objects[0].Parts) != 2 {
		t.Fatalf("parts %+v", mod.Info.Objects[0].Parts)
	}
	rev := mod.Info.Revision
	res, err := e.st.RemovePart(info.ID, "box", "hole")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Info.Objects[0].Parts) != 1 || res.Removed != "hole" || res.Part.Subtype != threemf.SubtypeNegative || res.Info.Revision != rev+1 {
		t.Fatalf("%+v", res)
	}
	// By id, and the model part cannot go.
	mod2, _ := e.st.AddModifier(info.ID, ModifierRequest{Object: "box", Subtype: "support_blocker", Shape: ShapeSphere, Size: [3]float64{6, 0, 0}})
	if _, err := e.st.RemovePart(info.ID, "box", strconv.Itoa(mod2.PartID)); err != nil {
		t.Fatal(err)
	}
	_, err = e.st.RemovePart(info.ID, "box", "box")
	ae := wantCode(t, err, CodeInvalidInput)
	if !strings.Contains(ae.Hint, "remove_object") {
		t.Fatalf("hint %q", ae.Hint)
	}
	_, err = e.st.RemovePart(info.ID, "box", "ghost")
	wantCode(t, err, CodeNotFound)
	_, err = e.st.RemovePart(info.ID, "nobody", "hole")
	wantCode(t, err, CodeNotFound)
	// The saved file has one part again and still slices.
	if got := len(openSaved(t, e, info.ID).Objects[0].Parts); got != 1 {
		t.Fatalf("saved parts %d", got)
	}
}

// B1: labels use the slicer's own name sanitising (sanitize_instance_name).
func TestSanitizeInstanceName(t *testing.T) {
	for in, want := range map[string]string{
		"my part":          "my_part",
		"  my  part  ":     "my_part",
		"a!b@c#d$e%f^g&h":  "a_b_c_d_e_f_g_h",
		"a(b)c=d+e[f]g{h}": "a_b_c_d_e_f_g_h",
		`a;b:c"d,e'f`:      "a_b_c_d_e_f",
		"under_score-dot.": "under_score-dot.",
		"__x__":            "_x_", // only one underscore goes from each end
		"":                 "",
		"Body18.2.stl":     "Body18.2.stl",
	} {
		if got := SanitizeInstanceName(in); got != want {
			t.Errorf("SanitizeInstanceName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestObjectLabelsOfSanitisedNames(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Sane")
	names := []string{"my part", "a:b,c", "plain"}
	for i, n := range names {
		x, y := 60.0+70*float64(i), 100.0
		if _, err := e.st.AddModel(info.ID, AddModelRequest{Path: writeSTL(t, "b", 20, 20, 10), Name: n, X: &x, Y: &y}); err != nil {
			t.Fatal(err)
		}
	}
	e.exec.gcode = func(int) string {
		return gcodeFor(
			"EXCLUDE_OBJECT_DEFINE NAME=my_part_id_0_copy_0 CENTER=60,100 POLYGON=[[50,90]]",
			"EXCLUDE_OBJECT_DEFINE NAME=a_b_c_id_1_copy_0 CENTER=130,100 POLYGON=[[120,90]]",
			"EXCLUDE_OBJECT_DEFINE NAME=plain_id_2_copy_0 CENTER=200,100 POLYGON=[[190,90]]")
	}
	out, err := e.st.Slice(info.ID, SliceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	labels := out.Last.Plates[0].ObjectLabels
	if len(labels) != 3 || labels[0].Label != "my_part_id_0_copy_0" || labels[1].Label != "a_b_c_id_1_copy_0" {
		t.Fatalf("labels %+v", labels)
	}
}

func seqProject(t *testing.T, e *testEnv, name string, extra map[string]any) string {
	t.Helper()
	info := e.newProject(t, name)
	vals := map[string]any{"print_sequence": "by object", "extruder_clearance_radius": 64, "extruder_clearance_height_to_lid": 118, "extruder_clearance_height_to_rod": 24, "nozzle_height": 30}
	for k, v := range extra {
		vals[k] = v
	}
	if _, err := e.st.UpdateSettings(info.ID, SettingsRequest{Values: vals, AllowLocked: true}); err != nil {
		t.Fatal(err)
	}
	return info.ID
}

// N1: a skirt only widens the clearance when one is printed around every object.
func TestSeqSkirtOnlyForPerObject(t *testing.T) {
	e := newEnv(t)
	measure := func(extra map[string]any) float64 {
		id := seqProject(t, e, "Skirt", extra)
		var d float64
		e.st.read(id, func(h *handle) error { d = h.seqHalf([]seqBox{{z: 50}}); return nil })
		return d
	}
	base := measure(nil)
	if math.Abs(base-31.9) > 1e-9 {
		t.Fatalf("no skirt: %v", base)
	}
	if got := measure(map[string]any{"skirt_type": "combined", "skirt_loops": 3, "skirt_distance": 5}); got != base {
		t.Fatalf("a combined skirt changed the clearance: %v", got)
	}
	if got := measure(map[string]any{"skirt_type": "perobject", "skirt_loops": 0, "skirt_distance": 5}); got != base {
		t.Fatalf("no loops: %v", got)
	}
	// Short objects: max(2, skirt) - 0.1 with the skirt, 1.9 without.
	e.st.read(seqProject(t, e, "Short", nil), func(h *handle) error {
		if got := h.seqHalf([]seqBox{{z: 10}}); math.Abs(got-1.9) > 1e-9 {
			t.Errorf("short, no skirt: %v", got)
		}
		return nil
	})
	id := seqProject(t, e, "ShortSkirt", map[string]any{"skirt_type": "perobject", "skirt_loops": 2, "skirt_distance": 3})
	e.st.read(id, func(h *handle) error {
		if got := h.seqHalf([]seqBox{{z: 10}}); got <= 1.9+1 {
			t.Errorf("short with a per object skirt: %v", got)
		}
		return nil
	})
}

// N3: a tall object added next to short ones gets the tall clearance.
func TestPlacementUsesTheNewObjectsHeight(t *testing.T) {
	e := newEnv(t)
	id := seqProject(t, e, "Mixed", nil)
	for i := 0; i < 2; i++ {
		if _, err := e.st.AddModel(id, AddModelRequest{Path: writeSTL(t, "s", 20, 20, 10), Name: "short" + strconv.Itoa(i)}); err != nil {
			t.Fatal(err)
		}
	}
	res, err := e.st.AddModel(id, AddModelRequest{Path: writeSTL(t, "t", 20, 20, 50), Name: "tall"})
	if err != nil {
		t.Fatal(err)
	}
	if hasWarning(res.Info, "sequence_clearance") {
		t.Fatalf("the new object was placed too close: %+v", res.Info.Warnings)
	}
}

// N2: an object in the exclusion area of the bed is reported.
func TestByObjectExclusionArea(t *testing.T) {
	e := newEnv(t)
	id := seqProject(t, e, "Excl", map[string]any{"bed_exclude_area": []any{"100x100", "140x100", "140x140", "100x140"}})
	x, y := 120.0, 120.0
	res, err := e.st.AddModel(id, AddModelRequest{Path: writeSTL(t, "c", 20, 20, 10), X: &x, Y: &y})
	if err != nil {
		t.Fatal(err)
	}
	var msg string
	for _, w := range res.Info.Warnings {
		if w.Code == "sequence_clearance" {
			msg = w.Message
		}
	}
	if !strings.Contains(msg, "exclusion area") {
		t.Fatalf("warnings %+v", res.Info.Warnings)
	}
}
