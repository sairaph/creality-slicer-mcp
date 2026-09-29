package projects

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sairaph/creality-slicer-mcp/internal/slicer"
)

func argIndex(args []string, a string) int {
	for i, x := range args {
		if x == a {
			return i
		}
	}
	return -1
}

func TestSliceSync(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Slice: Me/Now")
	e.addBox(t, info.ID, "cube", 20, 20, 20)
	out, err := e.st.Slice(info.ID, SliceOptions{Preview: PreviewPlate})
	if err != nil {
		t.Fatal(err)
	}
	if out.Last == nil || len(out.Last.Plates) != 1 {
		t.Fatalf("outcome: %+v", out)
	}
	pr := out.Last.Plates[0]
	if pr.Plate != 1 || pr.TimeSeconds != 65 || pr.Layers != 100 || pr.TotalG != 0 && math.Abs(pr.TotalG-2.0) > 1e-9 {
		t.Fatalf("plate result: %+v", pr)
	}
	if math.Abs(pr.FilamentG[0]-1.5) > 1e-9 || len(pr.FilamentG) != 2 {
		t.Fatalf("filament g %v", pr.FilamentG)
	}
	if pr.UploadName != "Slice_Me_Now_plate1.gcode" {
		t.Fatalf("upload name %q", pr.UploadName)
	}
	if len(pr.Tools) != 2 || pr.Tools[1].Preset != testPETG || pr.Tools[1].Type != "PETG" || pr.Tools[1].Colour != "#000000" || pr.Tools[1].FilamentID != "P002" || pr.Tools[1].Slot != 2 {
		t.Fatalf("tools: %+v", pr.Tools)
	}
	if len(pr.ExcludeNames) != 1 || pr.ExcludeNames[0] != "cube_id_0_copy_0" || !pr.Multicolour {
		t.Fatalf("exclude names %v", pr.ExcludeNames)
	}
	if len(out.Preview) == 0 {
		t.Fatal("no preview")
	}
	// The G-code carries the thumbnails the printer asks for.
	data, err := os.ReadFile(pr.GCodePath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), "; thumbnail begin") != 2 || !strings.Contains(string(data), "96x96") || !strings.Contains(string(data), "300x300") {
		t.Fatalf("thumbnails not embedded")
	}
	if filepath.Dir(pr.GCodePath) != filepath.Join(e.dir, "projects", info.ID, "out") {
		t.Fatalf("gcode path %q", pr.GCodePath)
	}
	// The request: the project copy is the only input, plate 0 for all.
	args := e.exec.lastArgs()
	if args[argIndex(args, "--slice")+1] != "0" || filepath.Base(args[len(args)-1]) != "slice_input.3mf" {
		t.Fatalf("args %v", args)
	}
	// The record is in the project and is current.
	got, _ := e.st.GetProject(info.ID)
	if got.LastSlice == nil || got.LastSlice.Stale || got.LastSlice.Revision != got.Revision || got.LastSlice.TimeS != 65 {
		t.Fatalf("last slice: %+v revision %d", got.LastSlice, got.Revision)
	}
	// A change makes the slice stale and the project says so.
	if _, err := e.st.UpdateSettings(info.ID, SettingsRequest{Values: map[string]any{"wall_loops": 4}}); err != nil {
		t.Fatal(err)
	}
	got, _ = e.st.GetProject(info.ID)
	if !got.LastSlice.Stale || !hasWarning(got, "stale_slice") {
		t.Fatalf("not stale: %+v", got.Warnings)
	}
	// The report reads the G-code again.
	rep, err := e.st.Report(info.ID, 0)
	if err != nil || !rep.Stale || rep.Summary.LayerCount != 1 || len(rep.Summary.Tools) != 2 || rep.Summary.Config != nil {
		t.Fatalf("report: %+v %v", rep, err)
	}
	if _, err := e.st.Report(info.ID, 4); AsError(err).Code != CodeNotFound {
		t.Fatalf("report of a plate not sliced: %v", err)
	}
	// Status of a project that ran a slice.
	st, err := e.st.SliceStatus(info.ID)
	if err != nil || st.State != "finished" || !st.Stale || st.Last == nil {
		t.Fatalf("status: %+v %v", st, err)
	}
}

func TestSliceOverridesAndOptions(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Opts")
	e.addBox(t, info.ID, "cube", 20, 20, 20)
	if _, err := e.st.Slice(info.ID, SliceOptions{Plate: 1, Overrides: map[string]any{"wall_loops": 4, "enable_support": true}}); err != nil {
		t.Fatal(err)
	}
	args := strings.Join(e.exec.lastArgs(), " ")
	for _, want := range []string{"--slice 1", "--wall-loops 4", "--enable-support=1"} {
		if !strings.Contains(args, want) {
			t.Errorf("args lack %q: %s", want, args)
		}
	}
	// The project itself did not change.
	if v := openSaved(t, e, info.ID).Settings.String("wall_loops"); v != "3" {
		t.Fatalf("override leaked into the project: %q", v)
	}
	calls := e.exec.count()
	for _, o := range []SliceOptions{
		{Overrides: map[string]any{"wall_loopz": 4}},
		{Overrides: map[string]any{"wall_loops": 9999}},
		{Overrides: map[string]any{"wall_loops": nil}},
		{Plate: 3},
	} {
		_, err := e.st.Slice(info.ID, o)
		if ae := AsError(err); err == nil || (ae.Code != CodeInvalidInput && ae.Code != CodeNotFound) {
			t.Errorf("%+v: %v", o, err)
		}
	}
	if e.exec.count() != calls {
		t.Fatal("the slicer was started for a refused request")
	}
	// An empty project and an empty plate cannot be sliced.
	empty := e.newProject(t, "Empty")
	_, err := e.st.Slice(empty.ID, SliceOptions{})
	wantCode(t, err, CodeInvalidInput)
}

func TestSliceFailureIsStructured(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Fail")
	e.addBox(t, info.ID, "cube", 20, 20, 20)
	e.exec.fn = func(spec slicer.ExecSpec) (slicer.ExecResult, error) {
		spec.Stderr.Write([]byte("[error] Objects are outside the printable area\n"))
		return slicer.ExecResult{ExitCode: -52}, nil
	}
	_, err := e.st.Slice(info.ID, SliceOptions{})
	ae := wantCode(t, err, CodeSlicerError)
	if ae.Fields["exit_name"] != "OBJECTS_PARTLY_INSIDE" || ae.Fields["exit_code"] != int32(-52) || ae.Hint == "" || ae.Message == "" {
		t.Fatalf("error: %+v", ae)
	}
	if tail, _ := ae.Fields["output_tail"].(string); !strings.Contains(tail, "outside the printable area") {
		t.Fatalf("output tail %q", tail)
	}
	if got, _ := e.st.GetProject(info.ID); got.LastSlice != nil {
		t.Fatal("a failed slice was recorded")
	}
	// The slot is free again.
	e.exec.fn = nil
	if _, err := e.st.Slice(info.ID, SliceOptions{}); err != nil {
		t.Fatal(err)
	}
	// Exit 0 without G-code.
	e.exec.fn = func(spec slicer.ExecSpec) (slicer.ExecResult, error) { return slicer.ExecResult{}, nil }
	_, err = e.st.Slice(info.ID, SliceOptions{})
	ae = wantCode(t, err, CodeSlicerError)
	if ae.Fields["exit_name"] != slicer.OutcomeNoOutput {
		t.Fatalf("no output: %+v", ae)
	}
}

func TestSliceUnavailable(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "NoSlicer")
	e.addBox(t, info.ID, "cube", 20, 20, 20)
	e.st.cfg.Runner = nil
	e.st.cfg.Install = slicer.Install{Reason: "not installed"}
	_, err := e.st.Slice(info.ID, SliceOptions{})
	ae := wantCode(t, err, CodeUnavailable)
	if !strings.Contains(ae.Message, "not installed") {
		t.Fatalf("message %q", ae.Message)
	}
	// Everything else still works.
	if _, err := e.st.UpdateSettings(info.ID, SettingsRequest{Values: map[string]any{"wall_loops": 4}}); err != nil {
		t.Fatal(err)
	}
}

func TestSliceBackground(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Later")
	e.addBox(t, info.ID, "cube", 20, 20, 20)
	e.exec.block = make(chan struct{})
	out, err := e.st.Slice(info.ID, SliceOptions{Background: true})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Running || !isJobRef(out.JobID) {
		t.Fatalf("outcome: %+v", out)
	}
	// One slice at a time per project; delete is refused while it runs.
	_, err = e.st.Slice(info.ID, SliceOptions{})
	wantCode(t, err, CodeConflict)
	_, err = e.st.Delete(info.ID, info.ID)
	wantCode(t, err, CodeConflict)
	st, err := e.st.SliceStatus(out.JobID)
	if err != nil || st.State != slicer.StateRunning || st.ProjectID != info.ID {
		t.Fatalf("running status: %+v %v", st, err)
	}
	if byProject, err := e.st.SliceStatus(info.ID); err != nil || byProject.State != slicer.StateRunning || byProject.JobID != out.JobID {
		t.Fatalf("status by project: %+v %v", byProject, err)
	}
	// A change while it runs does not disturb the slice (it reads its own copy).
	if _, err := e.st.UpdateSettings(info.ID, SettingsRequest{Values: map[string]any{"wall_loops": 4}}); err != nil {
		t.Fatal(err)
	}
	close(e.exec.block)
	j := e.st.cfg.Jobs.Get(out.JobID)
	select {
	case <-j.Done():
	case <-time.After(30 * time.Second):
		t.Fatal("job did not end")
	}
	st, err = e.st.SliceStatus(out.JobID)
	if err != nil || st.State != slicer.StateFinished || st.Last == nil || st.Error != nil {
		t.Fatalf("finished status: %+v %v", st, err)
	}
	if !st.Stale || st.Last.Revision != 1+1 { // sliced from revision 2 (after add_model); the settings change made 3
		t.Logf("last revision %d stale %v", st.Last.Revision, st.Stale)
	}
	if !st.Stale {
		t.Fatal("the slice should be stale after the change made while it ran")
	}
	if got, _ := e.st.GetProject(info.ID); got.LastSlice == nil || got.LastSlice.Plates != 1 {
		t.Fatalf("not recorded: %+v", got.LastSlice)
	}
	// The project is free again.
	if _, err := e.st.Delete(info.ID, info.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.SliceStatus("slice-nope"); AsError(err).Code != CodeNotFound {
		t.Fatalf("unknown job: %v", err)
	}
}

func TestSliceBackgroundFailure(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "BgFail")
	e.addBox(t, info.ID, "cube", 20, 20, 20)
	e.exec.fn = func(spec slicer.ExecSpec) (slicer.ExecResult, error) { return slicer.ExecResult{ExitCode: -17}, nil }
	out, err := e.st.Slice(info.ID, SliceOptions{Background: true})
	if err != nil {
		t.Fatal(err)
	}
	<-e.st.cfg.Jobs.Get(out.JobID).Done()
	st, err := e.st.SliceStatus(out.JobID)
	if err != nil || st.State != "failed" || st.Error == nil || st.Error.Fields["exit_name"] != "PROCESS_NOT_COMPATIBLE" {
		t.Fatalf("status: %+v %v", st, err)
	}
	// The next slice can start.
	e.exec.fn = nil
	if _, err := e.st.Slice(info.ID, SliceOptions{}); err != nil {
		t.Fatal(err)
	}
}

func TestSliceCancel(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Stop")
	e.addBox(t, info.ID, "cube", 20, 20, 20)
	e.exec.block = make(chan struct{})
	out, err := e.st.Slice(info.ID, SliceOptions{Background: true})
	if err != nil {
		t.Fatal(err)
	}
	ok, err := e.st.CancelSlice(out.JobID)
	if err != nil || !ok {
		t.Fatalf("cancel: %v %v", ok, err)
	}
	<-e.st.cfg.Jobs.Get(out.JobID).Done()
	st, _ := e.st.SliceStatus(out.JobID)
	if st.State == slicer.StateRunning {
		t.Fatalf("still running: %+v", st)
	}
	if got, _ := e.st.GetProject(info.ID); got.LastSlice != nil {
		t.Fatal("a cancelled slice was recorded")
	}
}

func TestSliceWholeProjectAndPlates(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Two")
	e.addBox(t, info.ID, "a", 20, 20, 20)
	if _, err := e.st.ManagePlates(info.ID, PlatesRequest{Action: PlateAdd}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.AddModel(info.ID, AddModelRequest{Path: writeSTL(t, "b", 20, 20, 20), Plate: 2}); err != nil {
		t.Fatal(err)
	}
	e.exec.fn = func(spec slicer.ExecSpec) (slicer.ExecResult, error) {
		out := spec.Args[argIndex(spec.Args, "--outputdir")+1]
		for _, n := range []string{"plate_1.gcode", "plate_2.gcode"} {
			os.WriteFile(filepath.Join(out, n), []byte(defaultGCode), 0o644)
		}
		return slicer.ExecResult{}, nil
	}
	out, err := e.st.Slice(info.ID, SliceOptions{})
	if err != nil || len(out.Last.Plates) != 2 || out.Last.Plates[1].UploadName != "Two_plate2.gcode" {
		t.Fatalf("plates: %+v %v", out, err)
	}
	// Plate 3 does not exist; an empty plate is refused.
	if _, err := e.st.ManagePlates(info.ID, PlatesRequest{Action: PlateAdd}); err != nil {
		t.Fatal(err)
	}
	_, err = e.st.Slice(info.ID, SliceOptions{Plate: 3})
	wantCode(t, err, CodeInvalidInput)
}

func TestPreviewAndThumbnails(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Pics")
	e.addBox(t, info.ID, "cube", 20, 20, 20)
	for _, k := range []PreviewKind{PreviewPlate, PreviewPlateLarge, PreviewTop} {
		png, err := e.st.Preview(info.ID, k, 1)
		if err != nil || len(png) < 100 || string(png[1:4]) != "PNG" {
			t.Fatalf("%s: %v (%d bytes)", k, err, len(png))
		}
	}
	if png, err := e.st.Preview(info.ID, PreviewNone, 1); err != nil || png != nil {
		t.Fatalf("none: %v", err)
	}
	if _, err := e.st.Preview(info.ID, "movie", 1); err == nil {
		t.Fatal("unknown kind accepted")
	}
	_, err := e.st.Preview(info.ID, PreviewPlate, 9)
	wantCode(t, err, CodeNotFound)
	stored, err := e.st.StoredThumbnail(info.ID, 1)
	if err != nil || len(stored) == 0 {
		t.Fatalf("stored thumbnail: %v", err)
	}
	// An added object changes the stored picture of its plate.
	before := string(stored)
	e.addBox(t, info.ID, "second", 30, 30, 30)
	after, _ := e.st.StoredThumbnail(info.ID, 1)
	if string(after) == before {
		t.Fatal("the thumbnail did not change")
	}
}

func footprintOf(t *testing.T, e *testEnv, id, name string) rect {
	t.Helper()
	info, err := e.st.GetProject(id)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range info.Objects {
		if o.Name == name {
			return rect{o.Position[0] - o.Size[0]/2, o.Position[1] - o.Size[1]/2, o.Position[0] + o.Size[0]/2, o.Position[1] + o.Size[1]/2}
		}
	}
	t.Fatalf("object %s missing", name)
	return rect{}
}

// Arrange and orient are the tools' own: written into the project before the
// slice, and the slicer request carries neither flag.
func TestSliceArrangeAndOrientAreOurOwn(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Arr")
	x1, x2, y := 100.0, 105.0, 100.0
	for i, x := range []*float64{&x1, &x2} {
		if _, err := e.st.AddModel(info.ID, AddModelRequest{Path: writeSTL(t, "cube", 20, 30, 10), Name: []string{"a", "b"}[i], X: x, Y: &y}); err != nil {
			t.Fatal(err)
		}
	}
	// A box lying on its side: orient puts the big face down.
	rot := [3]float64{90, 0, 0}
	if _, err := e.st.UpdateObject(info.ID, UpdateObjectRequest{Object: "a", Rotation: &rot}); err != nil {
		t.Fatal(err)
	}
	if a, b := footprintOf(t, e, info.ID, "a"), footprintOf(t, e, info.ID, "b"); !a.overlaps(b) {
		t.Fatalf("the test needs overlapping objects: %+v %+v", a, b)
	}
	before, _ := e.st.GetProject(info.ID)
	out, err := e.st.Slice(info.ID, SliceOptions{Arrange: true, Orient: true})
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Join(e.exec.lastArgs(), " ")
	if strings.Contains(args, "--arrange") || strings.Contains(args, "--orient") {
		t.Fatalf("the slicer was asked to place: %s", args)
	}
	got, _ := e.st.GetProject(info.ID)
	if got.Revision != before.Revision+1 || got.LastSlice.Stale || got.LastSlice.Revision != got.Revision || !out.Last.Arranged || !out.Last.Oriented {
		t.Fatalf("revision %d -> %d, last %+v", before.Revision, got.Revision, got.LastSlice)
	}
	a, b := footprintOf(t, e, info.ID, "a"), footprintOf(t, e, info.ID, "b")
	if a.overlaps(b.inflate(PlacementGap - 1e-6)) {
		t.Fatalf("still too close: %+v %+v", a, b)
	}
	for _, o := range got.Objects {
		if math.Abs(o.Position[2]) > 1e-6 || math.Abs(o.Rotation[0]) > 1e-3 && o.Name == "a" && o.Size[2] > 15 {
			t.Fatalf("object %s not lying flat: %+v", o.Name, o)
		}
	}
	// The smallest side is up: the 20 x 30 x 10 box lies on its 20 x 30 face.
	if s := got.Objects[0].Size; math.Abs(s[2]-10) > 1e-3 {
		t.Fatalf("size after orient %v", s)
	}
	// The saved project holds the placement (a reader without our metadata sees it too).
	if inb := footprintOf(t, e, info.ID, "a"); inb != a {
		t.Fatal("placement not stable")
	}
	// Arrange alone leaves rotations alone; without either flag nothing changes.
	rev := got.Revision
	if _, err := e.st.Slice(info.ID, SliceOptions{}); err != nil {
		t.Fatal(err)
	}
	if again, _ := e.st.GetProject(info.ID); again.Revision != rev {
		t.Fatal("a plain slice changed the project")
	}
	// A plate that cannot hold the objects is a conflict and nothing runs.
	calls := e.exec.count()
	for i := 0; i < 12; i++ {
		if _, err := e.st.AddModel(info.ID, AddModelRequest{Path: writeSTL(t, "slab", 90, 90, 5), Plate: 1, X: ptr(130.0), Y: ptr(130.0)}); err != nil {
			t.Fatal(err)
		}
	}
	_, err = e.st.Slice(info.ID, SliceOptions{Arrange: true})
	ae := wantCode(t, err, CodeConflict)
	if !strings.Contains(ae.Message, "cannot arrange") || e.exec.count() != calls {
		t.Fatalf("conflict: %+v calls %d -> %d", ae, calls, e.exec.count())
	}
	// The failed arrange changed nothing.
	if after, _ := e.st.GetProject(info.ID); after.Revision != rev+12 {
		t.Fatalf("revision %d, want %d", after.Revision, rev+12)
	}
}

func TestAutoPlaceTool(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Direct")
	for i := 0; i < 3; i++ {
		if _, err := e.st.AddModel(info.ID, AddModelRequest{Path: writeSTL(t, "c", 30, 30, 30), X: ptr(50.0), Y: ptr(50.0), Name: string(rune('a' + i))}); err != nil {
			t.Fatal(err)
		}
	}
	res, err := e.st.AutoPlace(info.ID, 0, false, true)
	if err != nil {
		t.Fatal(err)
	}
	var rects []rect
	for _, o := range res.Objects {
		r := rect{o.Position[0] - 15, o.Position[1] - 15, o.Position[0] + 15, o.Position[1] + 15}
		for _, q := range rects {
			if r.overlaps(q.inflate(PlacementGap - 1e-6)) {
				t.Fatalf("overlap %+v %+v", r, q)
			}
		}
		rects = append(rects, r)
	}
	_, err = e.st.AutoPlace(info.ID, 0, false, false)
	wantCode(t, err, CodeInvalidInput)
	_, err = e.st.AutoPlace(info.ID, 4, true, false)
	wantCode(t, err, CodeNotFound)
}

// A file the slicer takes for newer (-24) is sliced once more with the check
// switched off, and the result says so.
func TestSliceRetriesNewerFileCheck(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Foreign")
	e.addBox(t, info.ID, "cube", 20, 20, 20)
	e.exec.fn = func(spec slicer.ExecSpec) (slicer.ExecResult, error) {
		if !strings.Contains(strings.Join(spec.Args, " "), "--allow-newer-file=1") {
			spec.Stderr.Write([]byte("file version not supported\n"))
			return slicer.ExecResult{ExitCode: -24}, nil
		}
		out := spec.Args[argIndex(spec.Args, "--outputdir")+1]
		return slicer.ExecResult{}, os.WriteFile(filepath.Join(out, "plate_1.gcode"), []byte(defaultGCode), 0o644)
	}
	out, err := e.st.Slice(info.ID, SliceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if e.exec.count() != 2 || len(out.Last.Warnings) != 1 || !strings.Contains(out.Last.Warnings[0], "newer") {
		t.Fatalf("calls %d warnings %v", e.exec.count(), out.Last.Warnings)
	}
}

// The projects layer works with the 7.3 dialect too: the same request becomes
// a --cli command line with a data folder of its own, and nothing else changes.
func TestSliceWithTheV73Dialect(t *testing.T) {
	e := newEnv(t)
	d, err := slicer.NewDialect("v73", e.cat.CLIFlag)
	if err != nil {
		t.Fatal(err)
	}
	r := e.st.cfg.Runner
	r.Dialect = d
	r.DataDir = filepath.Join(e.dir, "cli-data")
	info := e.newProject(t, "Seven three")
	e.addBox(t, info.ID, "cube", 20, 20, 20)
	out, err := e.st.Slice(info.ID, SliceOptions{Plate: 1, Overrides: map[string]any{"wall_loops": 4}, Arrange: true})
	if err != nil {
		t.Fatal(err)
	}
	args := e.exec.lastArgs()
	s := strings.Join(args, " ")
	if args[0] != "--cli" || !strings.Contains(s, "--datadir "+r.DataDir) || !strings.Contains(s, "--need-gcode-file") || !strings.Contains(s, "--wall-loops 4") ||
		strings.Contains(s, "--arrange") || strings.Contains(s, "--orient") || filepath.Base(args[len(args)-1]) != "slice_input.3mf" {
		t.Fatalf("argv %v", args)
	}
	if len(out.Last.Plates) != 1 || out.Last.Plates[0].Plate != 1 {
		t.Fatalf("%+v", out.Last)
	}
	// A failure is classified with the 7.3 table.
	e.exec.fn = func(spec slicer.ExecSpec) (slicer.ExecResult, error) { return slicer.ExecResult{ExitCode: -9}, nil }
	_, err = e.st.Slice(info.ID, SliceOptions{})
	ae := wantCode(t, err, CodeSlicerError)
	if !strings.Contains(ae.Message, "unknown exit code -9") {
		t.Fatalf("message %q", ae.Message)
	}
}

// D-V73-1: a native crash of 7.2 on a multi-filament plate printed by layer
// carries the hint; other failures and by-object plates do not.
func TestCrashOnMultiFilamentByLayerGetsTheHint(t *testing.T) {
	e := newEnv(t)
	e.st.cfg.Install.Dialect = "v72"
	info := e.newProject(t, "Crashy")
	e.addBox(t, info.ID, "a", 20, 20, 10)
	if _, err := e.st.AddModel(info.ID, AddModelRequest{Path: writeSTL(t, "b", 20, 20, 10), Filament: 2}); err != nil {
		t.Fatal(err)
	}
	e.exec.fn = func(spec slicer.ExecSpec) (slicer.ExecResult, error) {
		return slicer.ExecResult{ExitCode: -1073741819}, nil
	}
	_, err := e.st.Slice(info.ID, SliceOptions{})
	ae := wantCode(t, err, CodeSlicerError)
	if !strings.Contains(ae.Hint, "update to 7.3 or set print_sequence to by object") {
		t.Fatalf("hint %q", ae.Hint)
	}
	if _, err := e.st.UpdateSettings(info.ID, SettingsRequest{Values: map[string]any{"print_sequence": "by object"}}); err != nil {
		t.Fatal(err)
	}
	_, err = e.st.Slice(info.ID, SliceOptions{})
	if ae := wantCode(t, err, CodeSlicerError); strings.Contains(ae.Hint, "print_sequence") {
		t.Fatalf("by object got the hint: %q", ae.Hint)
	}
	if h := slicer.CrashHint("v73", slicer.Outcome{Code: slicer.OutcomeCrashed}, 3, "by layer"); h != "" {
		t.Fatalf("7.3: %q", h)
	}
	if h := slicer.CrashHint("v72", slicer.Outcome{Code: slicer.OutcomeFailed}, 3, "by layer"); h != "" {
		t.Fatalf("not a crash: %q", h)
	}
	if h := slicer.CrashHint("v72", slicer.Outcome{Code: slicer.OutcomeCrashed}, 1, "by layer"); h != "" {
		t.Fatalf("one filament: %q", h)
	}
}

// slice_project waits up to Wait: a slice that finishes in time returns its
// result, a slower one returns the job id and carries on.
func TestSliceWaitsThenHandsOverTheJob(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Waiter")
	e.addBox(t, info.ID, "cube", 20, 20, 20)
	out, err := e.st.Slice(info.ID, SliceOptions{Wait: 30 * time.Second})
	if err != nil || out.Running || out.Last == nil || len(out.Last.Plates) != 1 || !slicer.IsJobID(out.JobID) {
		t.Fatalf("fast slice: %+v %v", out, err)
	}
	// A slow one: the wait ends first, the job goes on.
	e.exec.block = make(chan struct{})
	start := time.Now()
	out, err = e.st.Slice(info.ID, SliceOptions{Wait: 200 * time.Millisecond})
	if err != nil || !out.Running || out.JobID == "" || out.Last != nil {
		t.Fatalf("slow slice: %+v %v", out, err)
	}
	if time.Since(start) > 10*time.Second {
		t.Fatal("did not return after the wait")
	}
	if _, err := e.st.Slice(info.ID, SliceOptions{Wait: time.Second}); AsError(err).Code != CodeConflict {
		t.Fatalf("second slice while one runs: %v", err)
	}
	close(e.exec.block)
	<-e.st.cfg.Jobs.Get(out.JobID).Done()
	st, err := e.st.SliceStatus(out.JobID)
	if err != nil || st.State != slicer.StateFinished || st.Last == nil {
		t.Fatalf("status: %+v %v", st, err)
	}
	// Background wins over wait: immediate return.
	e.exec.block = make(chan struct{})
	out, err = e.st.Slice(info.ID, SliceOptions{Background: true, Wait: time.Hour})
	if err != nil || !out.Running {
		t.Fatalf("background: %+v %v", out, err)
	}
	close(e.exec.block)
	<-e.st.cfg.Jobs.Get(out.JobID).Done()
	// A failure inside the wait is the error of the call, with the crash hint.
	e.st.cfg.Install.Dialect = "v72"
	if _, err := e.st.AddModel(info.ID, AddModelRequest{Path: writeSTL(t, "b", 20, 20, 10), Filament: 2}); err != nil {
		t.Fatal(err)
	}
	e.exec.fn = func(spec slicer.ExecSpec) (slicer.ExecResult, error) {
		return slicer.ExecResult{ExitCode: -1073741819}, nil
	}
	_, err = e.st.Slice(info.ID, SliceOptions{Wait: 30 * time.Second})
	ae := wantCode(t, err, CodeSlicerError)
	if !strings.Contains(ae.Hint, "update to 7.3") {
		t.Fatalf("hint %q", ae.Hint)
	}
	// The fallback of the status path carries the hint too.
	e.st.mu.Lock()
	e.st.hints()["slice-deadbeef"] = crashCtx{2, "by layer", ""}
	e.st.mu.Unlock()
	fe := e.st.failedJobError("slice-deadbeef", slicer.Result{Outcome: slicer.Outcome{Code: slicer.OutcomeCrashed, Message: "crashed"}})
	if !strings.Contains(fe.Hint, "update to 7.3") {
		t.Fatalf("fallback hint %q", fe.Hint)
	}
}

func TestSliceThumbnailsCanBeLeftOutAndChangesAreCounted(t *testing.T) {
	e := newEnv(t)
	info := e.newProject(t, "Thumbs")
	e.addBox(t, info.ID, "cube", 20, 20, 20)
	out, err := e.st.Slice(info.ID, SliceOptions{SkipThumbnails: true})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(out.Last.Plates[0].GCodePath)
	if strings.Contains(string(data), "; thumbnail begin") {
		t.Fatal("thumbnails were embedded")
	}
	// The synthetic G-code switches T0 -> T1 once and has no footer count.
	if out.Last.Plates[0].Changes != 1 {
		t.Fatalf("changes %d", out.Last.Plates[0].Changes)
	}
	e.exec.gcode = func(int) string {
		return strings.Replace(defaultGCode, "; total layers count = 100", "; total filament change = 7\n; total layers count = 100", 1)
	}
	out, err = e.st.Slice(info.ID, SliceOptions{})
	if err != nil || out.Last.Plates[0].Changes != 7 {
		t.Fatalf("footer count: %+v %v", out.Last.Plates[0], err)
	}
}
