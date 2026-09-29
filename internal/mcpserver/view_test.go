package mcpserver

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sairaph/creality-slicer-mcp/internal/projects"
)

func pngOf(t *testing.T, res *mcp.CallToolResult) *image.NRGBA {
	t.Helper()
	imgs := images(res)
	if len(imgs) != 1 {
		t.Fatalf("%d image(s) in the reply:\n%s", len(imgs), text(t, res))
	}
	src, err := png.Decode(bytes.NewReader(imgs[0].Data))
	if err != nil {
		t.Fatal(err)
	}
	out := image.NewNRGBA(src.Bounds())
	for y := 0; y < src.Bounds().Dy(); y++ {
		for x := 0; x < src.Bounds().Dx(); x++ {
			out.Set(x, y, src.At(x, y))
		}
	}
	return out
}

func count(img *image.NRGBA, f func(c color.NRGBA) bool) int {
	n := 0
	for y := 0; y < img.Bounds().Dy(); y++ {
		for x := 0; x < img.Bounds().Dx(); x++ {
			if f(img.NRGBAAt(x, y)) {
				n++
			}
		}
	}
	return n
}

// Project colours: filament 1 is purple (called red in the tests, as the axis is the only real red), filament 2 is blue.
func twoColourProject(t *testing.T, pf *projFixture, name string) string {
	t.Helper()
	out := pf.ok(t, "create_project", map[string]any{"name": name, "filaments": []map[string]any{
		{"preset": tpPLA, "colour": "#B028C8"}, {"preset": tpPETG, "colour": "#2850D0"}}})
	return frontOf(t, out)["project"].(string)
}

func isBlue(c color.NRGBA) bool { return int(c.B) > int(c.R)+70 && int(c.B) > int(c.G)+40 }
func isRed(c color.NRGBA) bool  { return int(c.R) > int(c.G)+80 && int(c.B) > int(c.G)+80 }

func TestGetViewDrawsTheNamedViewsWithParts(t *testing.T) {
	pf := newProjFixture(t)
	id := twoColourProject(t, pf, "views")
	pf.ok(t, "add_model", map[string]any{"project": id, "path": pf.stl, "name": "cube", "position": []float64{100, 100}, "include_screenshot": false})
	pf.ok(t, "add_model", map[string]any{"project": id, "path": pf.stl, "name": "other", "position": []float64{180, 60}, "filament": 2, "include_screenshot": false})
	pf.ok(t, "add_modifier", map[string]any{"project": id, "object": "cube", "kind": "modifier", "shape": "cylinder", "size": []float64{8, 8, 30},
		"values": map[string]any{"wall_loops": 6}, "include_screenshot": false})

	res := call(t, pf.cs, "get_view", map[string]any{"project": id})
	if res.IsError {
		t.Fatalf("get_view failed:\n%s", text(t, res))
	}
	front := frontOf(t, text(t, res))
	if front["view"] != "Isometric" || front["plate"] != 1 || front["objects"] != 2 || front["parts"] != 1 || front["width"] == nil || front["height"] == nil {
		t.Errorf("front = %v", front)
	}
	body := bodyOf(text(t, res))
	contains(t, "get_view body", body, "Legend", "yellow: modifier", "Next: get_view")
	iso := pngOf(t, res)
	if b := iso.Bounds(); max(b.Dx(), b.Dy()) != 1024 {
		t.Errorf("default size %v", b)
	}
	if count(iso, isBlue) == 0 || count(iso, isRed) == 0 {
		t.Error("the filament colours are not in the picture")
	}

	views := map[string]*image.NRGBA{}
	for _, v := range []string{"Isometric", "Front", "Top", "Right", "Back", "Left", "Bottom", "Dimetric", "Trimetric"} {
		r := call(t, pf.cs, "get_view", map[string]any{"project": id, "view_name": v, "width": 300})
		if r.IsError {
			t.Fatalf("%s failed:\n%s", v, text(t, r))
		}
		if frontOf(t, text(t, r))["view"] != v {
			t.Errorf("%s: front = %v", v, frontOf(t, text(t, r)))
		}
		views[v] = pngOf(t, r)
	}
	names := []string{"Isometric", "Front", "Top", "Right", "Back", "Left", "Bottom", "Dimetric", "Trimetric"}
	for i, a := range names {
		for _, b := range names[i+1:] {
			if views[a].Bounds() == views[b].Bounds() && bytes.Equal(views[a].Pix, views[b].Pix) {
				t.Errorf("views %s and %s are the same picture", a, b)
			}
		}
	}
	// A yellow modifier outline is in the picture with parts and not without.
	yellowOutline := func(c color.NRGBA) bool { return int(c.R) > 190 && int(c.G) > 140 && int(c.G) < 190 && c.B < 60 }
	withParts := call(t, pf.cs, "get_view", map[string]any{"project": id, "view_name": "Front", "focus": []string{"cube"}, "width": 400})
	noParts := call(t, pf.cs, "get_view", map[string]any{"project": id, "view_name": "Front", "focus": []string{"cube"}, "width": 400, "show_parts": false})
	if a, b := count(pngOf(t, withParts), yellowOutline), count(pngOf(t, noParts), yellowOutline); a < 30 || b != 0 {
		t.Errorf("modifier outline pixels: %d with parts, %d without", a, b)
	}
	if frontOf(t, text(t, noParts))["parts"] != 0 {
		t.Errorf("parts counted with show_parts false: %v", frontOf(t, text(t, noParts)))
	}
	if fo, _ := frontOf(t, text(t, withParts))["focus"].([]any); len(fo) != 1 || fo[0] != "cube" {
		t.Errorf("focus = %v", frontOf(t, text(t, withParts))["focus"])
	}
}

func TestGetViewHideIsolateFocusAndErrors(t *testing.T) {
	pf := newProjFixture(t)
	id := twoColourProject(t, pf, "sets")
	pf.ok(t, "add_model", map[string]any{"project": id, "path": pf.stl, "name": "red", "position": []float64{60, 60}, "include_screenshot": false})
	pf.ok(t, "add_model", map[string]any{"project": id, "path": pf.stl, "name": "blue", "position": []float64{180, 180}, "filament": 2, "include_screenshot": false})
	view := func(extra map[string]any) *mcp.CallToolResult {
		args := map[string]any{"project": id, "view_name": "Top", "width": 400, "height": 400, "show_labels": false}
		for k, v := range extra {
			args[k] = v
		}
		return call(t, pf.cs, "get_view", args)
	}
	all := view(nil)
	if count(pngOf(t, all), isBlue) == 0 || count(pngOf(t, all), isRed) == 0 {
		t.Fatal("both objects should be in the full picture")
	}
	hidden := view(map[string]any{"hide": []string{"blue"}})
	if count(pngOf(t, hidden), isBlue) != 0 || count(pngOf(t, hidden), isRed) == 0 || frontOf(t, text(t, hidden))["objects"] != 1 {
		t.Errorf("hide blue: front %v", frontOf(t, text(t, hidden)))
	}
	isolated := view(map[string]any{"isolate": []string{"blue"}})
	if count(pngOf(t, isolated), isRed) != 0 || count(pngOf(t, isolated), isBlue) == 0 {
		t.Error("isolate blue drew the red object or not the blue one")
	}
	// Ids work as well as names.
	byID := view(map[string]any{"isolate": []string{"2"}})
	if byID.IsError {
		t.Errorf("an object id:\n%s", text(t, byID))
	}
	// Focus frames tightly: the red object fills most of the picture.
	focused := pngOf(t, view(map[string]any{"focus": []string{"red"}, "view_name": "Front"}))
	minX, maxX := 1<<30, -1
	for y := 0; y < focused.Bounds().Dy(); y++ {
		for x := 0; x < focused.Bounds().Dx(); x++ {
			if isRed(focused.NRGBAAt(x, y)) {
				minX, maxX = min(minX, x), max(maxX, x)
			}
		}
	}
	if maxX-minX < 400*6/10 {
		t.Errorf("the focused object spans %d of 400 pixels", maxX-minX)
	}

	for name, args := range map[string]map[string]any{
		"unknown object":  {"focus": []string{"nothing"}},
		"unknown hide":    {"hide": []string{"nothing"}},
		"bad plate":       {"plate": 5},
		"focus is hidden": {"focus": []string{"blue"}, "hide": []string{"blue"}},
		"bad view":        {"view_name": "Sideways"},
		"too wide":        {"width": 5000},
	} {
		if res := view(args); !res.IsError {
			t.Errorf("%s was accepted", name)
		}
	}
	if e := text(t, view(map[string]any{"focus": []string{"nothing"}})); !strings.Contains(e, "not_found") {
		t.Errorf("unknown object: %s", e)
	}
}

func TestGetViewLabelsAndRanges(t *testing.T) {
	pf := newProjFixture(t)
	id := pf.withModel(t, "Labels")
	white := func(c color.NRGBA) bool { return c.R == 255 && c.G == 255 && c.B == 255 }
	with := call(t, pf.cs, "get_view", map[string]any{"project": id, "width": 500})
	without := call(t, pf.cs, "get_view", map[string]any{"project": id, "width": 500, "show_labels": false})
	if a, b := count(pngOf(t, with), white), count(pngOf(t, without), white); a < 200 || b != 0 {
		t.Errorf("label panel pixels: %d with, %d without", a, b)
	}
	pf.ok(t, "set_height_ranges", map[string]any{"project": id, "object": "cube", "ranges": []map[string]any{
		{"from_z": 2, "to_z": 8, "values": map[string]any{"wall_loops": 5}}}, "include_screenshot": false})
	orange := func(c color.NRGBA) bool { return int(c.R) > 200 && int(c.G) > 90 && int(c.G) < 130 && c.B < 40 }
	on := call(t, pf.cs, "get_view", map[string]any{"project": id, "view_name": "Front", "focus": []string{"cube"}, "show_ranges": true, "width": 400})
	off := call(t, pf.cs, "get_view", map[string]any{"project": id, "view_name": "Front", "focus": []string{"cube"}, "width": 400})
	if a, b := count(pngOf(t, on), orange), count(pngOf(t, off), orange); a < 50 || b != 0 {
		t.Errorf("range band pixels: %d with, %d without", a, b)
	}
}

func TestGetViewOfAnEmptyPlateAndErrors(t *testing.T) {
	pf := newProjFixture(t)
	id := pf.create(t, "Empty")
	res := call(t, pf.cs, "get_view", map[string]any{"project": id})
	if res.IsError || len(images(res)) != 1 || frontOf(t, text(t, res))["objects"] != 0 {
		t.Errorf("an empty plate: error %v, front %v", res.IsError, frontOf(t, text(t, res)))
	}
	if e := pf.errText(t, "get_view", map[string]any{"project": "nope"}); !strings.Contains(e, "not_found") {
		t.Errorf("unknown project: %s", e)
	}
}

// Changing tools attach a screenshot by default; include_screenshot false and
// the environment setting leave it out; get_view ignores the environment.
func TestScreenshotsOfChangingTools(t *testing.T) {
	pf := newProjFixture(t)
	id := pf.create(t, "Shots")
	shots := func(name string, args map[string]any) int {
		t.Helper()
		args["project"] = id
		res := call(t, pf.cs, name, args)
		if res.IsError {
			t.Fatalf("%s failed:\n%s", name, text(t, res))
		}
		return len(images(res))
	}
	if n := shots("add_model", map[string]any{"path": pf.stl, "name": "cube"}); n != 1 {
		t.Errorf("add_model: %d image(s) by default", n)
	}
	if n := shots("add_model", map[string]any{"path": pf.stl, "name": "quiet", "include_screenshot": false}); n != 0 {
		t.Errorf("add_model with include_screenshot false: %d image(s)", n)
	}
	// The screenshot is small: 512 px on the longest edge.
	res := call(t, pf.cs, "update_object", map[string]any{"project": id, "object": "cube", "position": []float64{100, 100}})
	shot := pngOf(t, res)
	if max(shot.Bounds().Dx(), shot.Bounds().Dy()) != 512 {
		t.Errorf("screenshot size %v", shot.Bounds())
	}
	tools := map[string]map[string]any{
		"update_object": {"object": "cube", "position": []float64{110, 110}},
		"add_modifier":  {"object": "cube", "kind": "modifier", "shape": "box", "size": []float64{5, 5, 5}, "values": map[string]any{"wall_loops": 6}},
		"set_height_ranges": {"object": "cube", "ranges": []map[string]any{
			{"from_z": 1, "to_z": 3, "values": map[string]any{"wall_loops": 5}}}},
		"manage_plates": {"action": "add"},
		"set_presets":   {"filaments": []map[string]any{{"preset": tpPLA, "colour": "#00FF00"}, {"preset": tpPETG, "colour": "#0000FF"}}},
		"remove_object": {"object": "quiet"},
	}
	for name, args := range tools {
		if n := shots(name, args); n != 1 {
			t.Errorf("%s: %d image(s) by default", name, n)
		}
	}
	// Not every change has a picture: renaming a plate or a preset change
	// without filaments do not.
	if n := shots("manage_plates", map[string]any{"action": "rename", "plate": 1, "name": "first"}); n != 0 {
		t.Errorf("rename plate: %d image(s)", n)
	}
	if n := shots("set_presets", map[string]any{"flush_multiplier": 0.9}); n != 0 {
		t.Errorf("set_presets without filaments: %d image(s)", n)
	}
}

func TestOnlyTextFeedbackTurnsScreenshotsOff(t *testing.T) {
	pf := newProjFixture(t, func(c *Config) { c.Settings.OnlyTextFeedback = true })
	id := pf.create(t, "Text only")
	res := call(t, pf.cs, "add_model", map[string]any{"project": id, "path": pf.stl, "name": "cube", "include_screenshot": true})
	if res.IsError || len(images(res)) != 0 {
		t.Errorf("add_model: error %v, %d image(s) with the environment switch", res.IsError, len(images(res)))
	}
	// The environment wins over the argument; get_view ignores it.
	view := call(t, pf.cs, "get_view", map[string]any{"project": id})
	if view.IsError || len(images(view)) != 1 {
		t.Errorf("get_view with the environment switch: error %v, %d image(s)", view.IsError, len(images(view)))
	}
}

type failingViews struct{ ProjectStore }

func (failingViews) View(string, projects.ViewRequest) (*projects.ViewResult, error) {
	return nil, errors.New("the renderer broke")
}

type hugeViews struct{ ProjectStore }

func (h hugeViews) View(ref string, req projects.ViewRequest) (*projects.ViewResult, error) {
	v, err := h.ProjectStore.View(ref, req)
	if err == nil {
		v.PNG = []byte("this is not a png")
	}
	return v, err
}

// A screenshot that cannot be drawn never fails the call.
func TestScreenshotFailuresDoNotFailTheCall(t *testing.T) {
	for name, wrap := range map[string]func(ProjectStore) ProjectStore{
		"render error": func(s ProjectStore) ProjectStore { return failingViews{s} },
		"bad image":    func(s ProjectStore) ProjectStore { return hugeViews{s} },
	} {
		t.Run(name, func(t *testing.T) {
			pf := newProjFixture(t)
			pf.wrap = wrap
			id := pf.create(t, "Broken")
			res := call(t, pf.cs, "add_model", map[string]any{"project": id, "path": pf.stl})
			if res.IsError || len(images(res)) != 0 || !strings.Contains(text(t, res), "Added 1 object") {
				t.Errorf("error %v, %d image(s):\n%s", res.IsError, len(images(res)), text(t, res))
			}
		})
	}
	// get_view itself reports the failure.
	pf := newProjFixture(t)
	pf.wrap = func(s ProjectStore) ProjectStore { return failingViews{s} }
	id := pf.create(t, "Broken view")
	if res := call(t, pf.cs, "get_view", map[string]any{"project": id}); !res.IsError {
		t.Error("get_view hid a render failure")
	}
}

func TestGetProjectListsParts(t *testing.T) {
	pf := newProjFixture(t)
	id := pf.withModel(t, "Parts")
	pf.ok(t, "add_modifier", map[string]any{"project": id, "object": "cube", "kind": "support_blocker", "shape": "box", "size": []float64{4, 6, 8}, "position": []float64{0, 0, 0}, "name": "hole", "include_screenshot": false})
	body := bodyOf(pf.ok(t, "get_project", map[string]any{"project": id}))
	contains(t, "get_project", body, "Parts (object | part id | kind | name | size mm | centre x,y,z", "support_blocker", "| hole | 4,6,8 |")
}

// show_ranges says whether there was anything to draw: bands are drawn
// whenever an object has ranges, and a picture without any says so.
func TestGetViewSaysWhenThereAreNoRangesToDraw(t *testing.T) {
	pf := newProjFixture(t)
	id := pf.withModel(t, "Bands")
	none := pf.ok(t, "get_view", map[string]any{"project": id, "show_ranges": true, "width": 300})
	if f := frontOf(t, none); f["ranges"] != 0 {
		t.Errorf("front = %v", f)
	}
	contains(t, "no ranges", bodyOf(none), "no object in the picture has height ranges")
	pf.ok(t, "set_height_ranges", map[string]any{"project": id, "object": "cube", "ranges": []map[string]any{
		{"from_z": 2, "to_z": 6, "values": map[string]any{"wall_loops": 5}}, {"from_z": 8, "to_z": 30, "values": map[string]any{"wall_loops": 4}}}, "include_screenshot": false})
	some := pf.ok(t, "get_view", map[string]any{"project": id, "show_ranges": true, "width": 300, "focus": []string{"cube"}, "view_name": "Front"})
	if f := frontOf(t, some); f["ranges"] != 2 {
		t.Errorf("front = %v", f)
	}
	contains(t, "ranges", bodyOf(some), "2 height range band(s)")
	// Without show_ranges there is no ranges field.
	if f := frontOf(t, pf.ok(t, "get_view", map[string]any{"project": id, "width": 300})); f["ranges"] != nil {
		t.Errorf("ranges reported without show_ranges: %v", f)
	}
}
