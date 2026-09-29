package mcpserver

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sairaph/creality-slicer-mcp/internal/catalog"
)

// MC2: a panic in a handler is an internal_error result, and the server keeps
// serving.
func TestAPanicInAHandlerIsAnErrorResult(t *testing.T) {
	h := recoverPanics(func(context.Context, string, mcp.Request) (mcp.Result, error) { panic("boom") })
	res, err := h(context.Background(), "tools/call", nil)
	if err != nil {
		t.Fatal(err)
	}
	r, ok := res.(*mcp.CallToolResult)
	if !ok || !r.IsError || !strings.Contains(text(t, r), "internal_error") || !strings.Contains(text(t, r), "report it") {
		t.Fatalf("result = %+v", res)
	}
	// Not a tool call: the error goes back as a protocol error.
	if _, err := h(context.Background(), "tools/list", nil); err == nil {
		t.Error("a panic in another method was swallowed")
	}
}

// MC5: pictures already in a reply count against the budget of the next one.
func TestImageBudgetCountsImagesAlreadyInTheReply(t *testing.T) {
	res := successResult(struct{}{}, "text")
	before := imageBudget(res)
	res.Content = append(res.Content, &mcp.ImageContent{Data: make([]byte, 300<<10), MIMEType: "image/png"})
	after := imageBudget(res)
	if before-after < 290<<10 {
		t.Errorf("budget went from %d to %d after a 300 KiB image", before, after)
	}
	// Two noisy images together stay inside the reply limit.
	noisy := func() []byte {
		img := image.NewNRGBA(image.Rect(0, 0, 700, 700))
		rng := rand.New(rand.NewSource(1))
		for i := range img.Pix {
			img.Pix[i] = uint8(rng.Intn(256))
		}
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}
	out := successResult(struct{}{}, "text")
	out = attachImage(out, noisy(), "first")
	out = attachImage(out, noisy(), "second")
	total := 0
	for _, c := range out.Content {
		switch c := c.(type) {
		case *mcp.TextContent:
			total += len(c.Text)
		case *mcp.ImageContent:
			total += (len(c.Data) + 2) / 3 * 4
		}
	}
	if total > 1<<20 {
		t.Errorf("reply is %d bytes", total)
	}
	_ = color.NRGBA{}
}

func TestSliceStatusWaitAndUnknownJobs(t *testing.T) {
	pf := newProjFixture(t)
	id := pf.withModel(t, "Cube")
	pf.exec.block = make(chan struct{})
	job := frontOf(t, pf.ok(t, "slice_project", map[string]any{"project": id, "wait": 1, "background": true}))["job_id"].(string)

	// A short wait on a running job returns it still running.
	start := time.Now()
	got := pf.ok(t, "get_slice_status", map[string]any{"job_id": job, "wait": 1})
	if frontOf(t, got)["state"] != "running" || time.Since(start) < 900*time.Millisecond {
		t.Errorf("wait 1 returned after %v: %s", time.Since(start), got)
	}
	contains(t, "running status", got, "Next: get_slice_status")

	// The job ends while a call waits: the call returns the finished result.
	go func() { time.Sleep(300 * time.Millisecond); close(pf.exec.block) }()
	done := pf.ok(t, "get_slice_status", map[string]any{"job_id": job, "wait": 30})
	if frontOf(t, done)["state"] != "finished" {
		t.Fatalf("wait did not see the end:\n%s", done)
	}

	for _, bad := range []string{"slice-00000000", "nope", id} {
		e := pf.errText(t, "get_slice_status", map[string]any{"job_id": bad})
		contains(t, "unknown job "+bad, e, "not_found", "no slice job with id "+bad, "get_slice_status without job_id")
	}
	if res := call(t, pf.cs, "get_slice_status", map[string]any{"job_id": job, "wait": 601}); !res.IsError {
		t.Error("wait above 600 was accepted")
	}
}

func TestChangingRepliesEndWithANextLine(t *testing.T) {
	pf := newProjFixture(t)
	id := pf.create(t, "Nexts")
	if got := pf.ok(t, "get_project", map[string]any{"project": id}); !strings.Contains(got, "Next: add_model") {
		t.Errorf("get_project without objects:\n%s", got)
	}
	pf.ok(t, "add_model", map[string]any{"project": id, "path": pf.stl, "name": "part", "copies": 2})
	for name, args := range map[string]map[string]any{
		"get_project":     {"project": id},
		"update_settings": {"project": id, "values": map[string]any{"wall_loops": 4}},
		"update_object":   {"project": id, "object": "part_1", "position": []float64{60, 60}},
		"remove_object":   {"project": id, "object": "part_2"},
		"manage_plates":   {"project": id, "action": "add"},
		"set_presets":     {"project": id, "flush_multiplier": 0.9},
		"add_modifier": {"project": id, "object": "part_1", "kind": "modifier", "shape": "box", "size": []float64{5, 5, 5},
			"values": map[string]any{"wall_loops": 6}},
		"set_height_ranges": {"project": id, "object": "part_1", "ranges": []map[string]any{{"from_z": 1, "to_z": 3, "values": map[string]any{"wall_loops": 5}}}},
		"set_layer_actions": {"project": id, "actions": []map[string]any{{"z": 10, "type": "pause"}}},
		"export_project":    {"project": id, "path": filepath.Join(t.TempDir(), "n.3mf")},
	} {
		if got := pf.ok(t, name, args); !strings.Contains(bodyOf(got), "Next:") {
			t.Errorf("%s has no Next line:\n%s", name, got)
		}
	}
	del := pf.ok(t, "delete_project", map[string]any{"project": id, "confirm": id})
	contains(t, "delete_project", del, "Next:")
}

func TestObjectsShowTheirExclusionLabelAfterASlice(t *testing.T) {
	pf := newProjFixture(t)
	id := pf.withModel(t, "cube")
	if got := pf.ok(t, "get_project", map[string]any{"project": id}); strings.Contains(got, "exclusion label") {
		t.Errorf("a label before any slice:\n%s", got)
	}
	slice := pf.ok(t, "slice_project", map[string]any{"project": id, "wait": 30})
	contains(t, "slice reply", slice, "Exclusion labels", "cube | cube_id_0_copy_0", "Tools (plate")
	// The handoff appears once: the G-code path is in the front and in one table row only.
	if n := strings.Count(bodyOf(slice), "plate_1.gcode"); n != 1 {
		t.Errorf("the G-code path appears %d times in the body:\n%s", n, slice)
	}
	got := pf.ok(t, "get_project", map[string]any{"project": id})
	contains(t, "get_project", got, "exclusion label", "cube_id_0_copy_0", "Next: get_view")
}

func TestOpenAndSlicePreviewSizes(t *testing.T) {
	pf := newProjFixture(t)
	id := pf.withModel(t, "Cube")
	// open_project takes none or small only.
	file := filepath.Join(t.TempDir(), "p.3mf")
	pf.ok(t, "export_project", map[string]any{"project": id, "path": file})
	if res := call(t, pf.cs, "open_project", map[string]any{"path": file, "preview": "large"}); !res.IsError {
		t.Error("open_project accepted preview large")
	}
	if res := call(t, pf.cs, "open_project", map[string]any{"path": file, "preview": "small"}); res.IsError {
		t.Errorf("open_project preview small failed:\n%s", text(t, res))
	}
	// slice_project attaches the plate and the first layer at the asked size.
	res := call(t, pf.cs, "slice_project", map[string]any{"project": id, "preview": "large", "wait": 30})
	if res.IsError || len(images(res)) != 2 {
		t.Fatalf("slice preview large: error %v, %d image(s)", res.IsError, len(images(res)))
	}
}

// MC4, V2: after a refresh only job polling and cancelling reach the backend
// that started the job; every other call uses the new backend at once.
func TestRefreshRoutesOnlyJobsToTheOldBackend(t *testing.T) {
	pf := newProjFixture(t)
	id := pf.withModel(t, "Cube")
	pf.exec.block = make(chan struct{})
	pf.exec.started = make(chan struct{})
	job := frontOf(t, pf.ok(t, "slice_project", map[string]any{"project": id, "background": true}))["job_id"].(string)
	<-pf.exec.started
	if pf.builds != 1 {
		t.Fatalf("backends built before the refresh: %d", pf.builds)
	}
	pf.ok(t, "get_slicer_status", map[string]any{"refresh": true})

	// A call that is not about a job builds and uses the new backend.
	pf.ok(t, "list_projects", nil)
	if pf.builds != 2 {
		t.Errorf("backends built after the refresh and a project call: %d, want 2", pf.builds)
	}
	// The old job still answers, and is listed.
	got := pf.ok(t, "get_slice_status", map[string]any{"job_id": job})
	if frontOf(t, got)["state"] != "running" {
		t.Fatalf("the job is not known after a refresh:\n%s", got)
	}
	contains(t, "job list", pf.ok(t, "get_slice_status", nil), job)
	if pf.builds != 2 {
		t.Errorf("polling built another backend: %d", pf.builds)
	}
	// And it can be cancelled through the old backend.
	pf.exec.killed = make(chan struct{})
	cancelled := pf.ok(t, "get_slice_status", map[string]any{"job_id": job, "cancel": true})
	if frontOf(t, cancelled)["state"] != "cancelled" {
		t.Errorf("cancel after a refresh: %s", cancelled)
	}
	close(pf.exec.block)
}

// MC3: a refresh chooses the catalog again.
func TestRefreshChoosesTheCatalogAgain(t *testing.T) {
	calls := 0
	f := newFixture(t, func(_ *fakeInstall, d *Deps) {
		d.Catalog = catalogCounter(&calls, d)
	})
	f.ok(t, "search_settings", map[string]any{"query": "wall"})
	f.ok(t, "search_settings", map[string]any{"query": "wall"})
	if calls != 1 {
		t.Fatalf("catalog loaded %d times before the refresh", calls)
	}
	f.ok(t, "get_slicer_status", map[string]any{"refresh": true})
	f.ok(t, "search_settings", map[string]any{"query": "wall"})
	if calls != 2 {
		t.Errorf("catalog loaded %d times in all, want 2", calls)
	}
}

// MC6: a slice call the client gave up on ends at once and its job is
// cancelled when the store hands it over.
func TestCancelledSliceCallEndsAtOnce(t *testing.T) {
	pf := newProjFixture(t)
	id := pf.withModel(t, "Cube")
	pf.exec.block = make(chan struct{})
	pf.exec.killed = make(chan struct{})
	pf.exec.started = make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() {
		_, err := pf.cs.CallTool(ctx, &mcp.CallToolParams{Name: "slice_project", Arguments: map[string]any{"project": id, "wait": 600}})
		errc <- err
	}()
	<-pf.exec.started
	cancel()
	select {
	case <-errc:
	case <-time.After(20 * time.Second):
		t.Fatal("the call did not end after the client cancelled it")
	}
}

// MC1: the command never replaces a file without --overwrite, and writes
// atomically.
func TestSliceFileRefusesToOverwrite(t *testing.T) {
	pf := newProjFixture(t)
	id := pf.withModel(t, "Cube")
	file := filepath.Join(t.TempDir(), "cube.3mf")
	pf.ok(t, "export_project", map[string]any{"project": id, "path": file})
	out := t.TempDir()
	dest := filepath.Join(out, "cube_plate1.gcode")
	if err := os.WriteFile(dest, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := pf.srv.SliceFile(context.Background(), SliceFileArgs{Path: file, Out: out})
	contains(t, "refusal", text(t, res), "invalid_input", "--overwrite")
	if got, _ := os.ReadFile(dest); string(got) != "mine" {
		t.Errorf("the existing file changed: %q", got)
	}
	res = pf.srv.SliceFile(context.Background(), SliceFileArgs{Path: file, Out: out, Overwrite: true})
	if res.IsError {
		t.Fatalf("with overwrite:\n%s", text(t, res))
	}
	if got, _ := os.ReadFile(dest); !strings.Contains(string(got), "EXECUTABLE_BLOCK_START") {
		t.Errorf("the file was not replaced: %.40q", got)
	}
}

// MC7: Ctrl+C ends the command's slice.
func TestSliceFileStopsWhenTheContextEnds(t *testing.T) {
	pf := newProjFixture(t)
	id := pf.withModel(t, "Cube")
	file := filepath.Join(t.TempDir(), "cube.3mf")
	pf.ok(t, "export_project", map[string]any{"project": id, "path": file})
	pf.exec.block = make(chan struct{})
	pf.exec.killed = make(chan struct{})
	pf.exec.started = make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	resc := make(chan *mcp.CallToolResult, 1)
	go func() { resc <- pf.srv.SliceFile(ctx, SliceFileArgs{Path: file}) }()
	<-pf.exec.started
	cancel()
	select {
	case res := <-resc:
		if !res.IsError {
			t.Error("a cancelled slice succeeded")
		}
	case <-time.After(20 * time.Second):
		t.Fatal("SliceFile ignored the cancelled context")
	}
	select {
	case <-pf.exec.killed:
	case <-time.After(20 * time.Second):
		t.Error("the slicer run was not stopped")
	}
}

func catalogCounter(calls *int, d *Deps) func() (*catalog.Catalog, error) {
	return func() (*catalog.Catalog, error) {
		*calls++
		return catalog.Load(catalogVersion)
	}
}

// MC10: a zero or negative scale factor is refused at the schema.
func TestScaleArrayFactorsMustBePositive(t *testing.T) {
	pf := newProjFixture(t)
	id := pf.create(t, "Scales")
	for _, scale := range []any{[]float64{1, 0, 1}, []float64{1, -1, 1}, 0, -2} {
		if res := call(t, pf.cs, "add_model", map[string]any{"project": id, "path": pf.stl, "scale": scale}); !res.IsError {
			t.Errorf("scale %v was accepted", scale)
		}
	}
	pf.ok(t, "add_model", map[string]any{"project": id, "path": pf.stl, "scale": []float64{1, 1, 2}})
}

// MC8, V1: an object named in another case still gets its modifier at the
// requested offset from the object's centre: a positioned modifier is stored
// exactly that far from an unpositioned one (which sits at the centre).
func TestModifierPositionResolvesTheObjectIgnoringCase(t *testing.T) {
	pf := newProjFixture(t)
	id := pf.create(t, "Case")
	pf.ok(t, "add_model", map[string]any{"project": id, "path": pf.stl, "name": "cube", "position": []float64{100, 100}})
	pf.ok(t, "add_modifier", map[string]any{"project": id, "object": "CUBE", "kind": "support_blocker", "shape": "box", "size": []float64{4, 4, 4}, "name": "centre",
		"include_screenshot": false})
	pf.ok(t, "add_modifier", map[string]any{"project": id, "object": "CUBE", "kind": "support_blocker", "shape": "box", "size": []float64{4, 4, 4}, "name": "moved",
		"position": []float64{5, -3, 2}, "include_screenshot": false})
	info, err := pf.store.GetProject(id)
	if err != nil {
		t.Fatal(err)
	}
	centres := map[string][3]float64{}
	for _, o := range info.Objects {
		for _, p := range o.Parts {
			centres[p.Name] = p.Center
		}
	}
	c, m := centres["centre"], centres["moved"]
	if c == ([3]float64{}) || m == ([3]float64{}) {
		t.Fatalf("parts not found: %v", centres)
	}
	for i, want := range [3]float64{5, -3, 2} {
		if got := m[i] - c[i]; got < want-1e-3 || got > want+1e-3 {
			t.Errorf("axis %d: the positioned modifier is %.3f from the unpositioned one, want %.3f (centres %v and %v)", i, got, want, c, m)
		}
	}
	if c[0] < 99.9 || c[0] > 100.1 || c[1] < 99.9 || c[1] > 100.1 {
		t.Errorf("the unpositioned modifier is not at the object's centre: %v", c)
	}
}
