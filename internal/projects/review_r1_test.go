package projects

import (
	"context"
	"math"
	"os"
	"path/filepath"
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
