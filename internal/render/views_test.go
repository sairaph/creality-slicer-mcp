package render

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/sairaph/creality-slicer-mcp/internal/mesh"
)

// countNear counts the pixels within tol (per channel) of want.
func countNear(img *image.NRGBA, want color.NRGBA, tol int) int {
	n := 0
	abs := func(a int) int {
		if a < 0 {
			return -a
		}
		return a
	}
	for y := 0; y < img.Bounds().Dy(); y++ {
		for x := 0; x < img.Bounds().Dx(); x++ {
			c := img.NRGBAAt(x, y)
			if abs(int(c.R)-int(want.R)) <= tol && abs(int(c.G)-int(want.G)) <= tol && abs(int(c.B)-int(want.B)) <= tol {
				n++
			}
		}
	}
	return n
}

// countWhere counts the pixels a predicate accepts and returns their bounding
// box.
func countWhere(img *image.NRGBA, f func(c color.NRGBA) bool) (n int, box image.Rectangle) {
	box = image.Rectangle{Min: image.Point{X: 1 << 30, Y: 1 << 30}, Max: image.Point{X: -1, Y: -1}}
	for y := 0; y < img.Bounds().Dy(); y++ {
		for x := 0; x < img.Bounds().Dx(); x++ {
			if f(img.NRGBAAt(x, y)) {
				n++
				box.Min.X, box.Min.Y = min(box.Min.X, x), min(box.Min.Y, y)
				box.Max.X, box.Max.Y = max(box.Max.X, x+1), max(box.Max.Y, y+1)
			}
		}
	}
	return
}

func purplish(c color.NRGBA) bool {
	return int(c.R) > int(c.G)+70 && int(c.B) > int(c.G)+70 && c.A == 255
}

func bluish(c color.NRGBA) bool {
	return int(c.B) > int(c.R)+70 && int(c.B) > int(c.G)+40 && c.A == 255
}

func diffPixels(a, b *image.NRGBA) int {
	if a.Bounds() != b.Bounds() {
		return -1
	}
	n := 0
	for i := 0; i < len(a.Pix); i += 4 {
		if a.Pix[i] != b.Pix[i] || a.Pix[i+1] != b.Pix[i+1] || a.Pix[i+2] != b.Pix[i+2] {
			n++
		}
	}
	return n
}

// viewScene is a purple 20 mm cube at 130,130, a smaller blue box at 60,80, and
// on the cube a modifier that sticks out of its top.
func viewScene() *Scene {
	red := Object{Mesh: mesh.Box(20, 20, 20), Transform: mesh.Translate(130, 130, 10), Colour: color.NRGBA{R: 170, G: 40, B: 190, A: 255}, Name: "cube", Label: "2 cube"}
	red.Parts = []Part{{Mesh: mesh.Box(10, 10, 10), Transform: mesh.Translate(130, 130, 20), Kind: PartModifier, Name: "mod"}}
	red.Ranges = []Range{{From: 2, To: 8}}
	blue := Object{Mesh: mesh.Box(30, 10, 6), Transform: mesh.Translate(60, 80, 3), Colour: color.NRGBA{R: 40, G: 60, B: 210, A: 255}, Name: "bar", Label: "3 bar"}
	return &Scene{Objects: []Object{red, blue}}
}

func renderView(t *testing.T, s *Scene, o ViewOptions) (*image.NRGBA, ViewStats) {
	t.Helper()
	data, st, err := RenderView(s, o)
	if err != nil {
		t.Fatal(err)
	}
	return decode(t, data), st
}

func TestEveryNamedViewDiffers(t *testing.T) {
	s := viewScene()
	imgs := map[ViewName]*image.NRGBA{}
	for _, v := range ViewNames {
		img, st := renderView(t, s, ViewOptions{View: v, Width: 400, Height: 400})
		if st.Width != 400 || st.Height != 400 || st.Objects != 2 {
			t.Errorf("%s: stats %+v", v, st)
		}
		imgs[v] = img
	}
	for i, a := range ViewNames {
		for _, b := range ViewNames[i+1:] {
			if d := diffPixels(imgs[a], imgs[b]); d < 500 {
				t.Errorf("views %s and %s differ in only %d pixels", a, b, d)
			}
		}
	}
	if _, ok := ParseViewName(" front "); !ok {
		t.Error("view names are not case insensitive")
	}
	if _, _, err := RenderView(s, ViewOptions{View: "Sideways"}); err == nil {
		t.Error("an unknown view was accepted")
	}
}

func TestViewDirections(t *testing.T) {
	// The blue bar is at x 60 (left of the cube at 130) and y 80 (nearer to
	// the front than the cube at 130).
	s := viewScene()
	centre := func(img *image.NRGBA, f func(color.NRGBA) bool) (float64, float64) {
		n, box := countWhere(img, f)
		if n == 0 {
			t.Fatal("colour not found")
		}
		return float64(box.Min.X+box.Max.X) / 2, float64(box.Min.Y+box.Max.Y) / 2
	}
	front, _ := renderView(t, s, ViewOptions{View: Front, Width: 600, Height: 300, ShowParts: false})
	bx, _ := centre(front, bluish)
	rx, _ := centre(front, purplish)
	if bx >= rx {
		t.Errorf("Front: the bar (x 60) is at %.0f, right of the cube (x 130) at %.0f", bx, rx)
	}
	back, _ := renderView(t, s, ViewOptions{View: Back, Width: 600, Height: 300})
	bx, _ = centre(back, bluish)
	rx, _ = centre(back, purplish)
	if bx <= rx {
		t.Errorf("Back: the bar should be right of the cube: %.0f, %.0f", bx, rx)
	}
	top, _ := renderView(t, s, ViewOptions{View: Top, Width: 500, Height: 500})
	_, by := centre(top, bluish)
	_, ry := centre(top, purplish)
	if by <= ry { // +Y is up the picture, so the bar (y 80) is lower than the cube (y 130)
		t.Errorf("Top: the bar (y 80) is at row %.0f, above the cube (y 130) at row %.0f", by, ry)
	}
	left, _ := renderView(t, s, ViewOptions{View: Left, Width: 600, Height: 300})
	bx, _ = centre(left, bluish)
	rx, _ = centre(left, purplish)
	// Left: the camera is at -X looking +X, +Y runs to the left of the picture.
	if bx <= rx {
		t.Errorf("Left: the bar (y 80) should be right of the cube (y 130): %.0f, %.0f", bx, rx)
	}
	right, _ := renderView(t, s, ViewOptions{View: Right, Width: 600, Height: 300})
	bx, _ = centre(right, bluish)
	rx, _ = centre(right, purplish)
	if bx >= rx {
		t.Errorf("Right: the bar (y 80) should be left of the cube (y 130): %.0f, %.0f", bx, rx)
	}
}

func TestFocusFillsTheFrame(t *testing.T) {
	s := viewScene()
	for _, v := range []ViewName{Front, Right, Top, Isometric} {
		img, _ := renderView(t, s, ViewOptions{View: v, Focus: []int{0}, ShowParts: false, Width: 500, Height: 500})
		n, box := countWhere(img, purplish)
		if n == 0 {
			t.Fatalf("%s: the focused object is not drawn", v)
		}
		if w, h := box.Dx(), box.Dy(); w < 500*6/10 && h < 500*6/10 {
			t.Errorf("%s: the focused object spans %dx%d of 500x500", v, w, h)
		}
		if nb, _ := countWhere(img, bluish); nb != 0 {
			t.Errorf("%s: the other object is in a picture framed on the first", v)
		}
	}
	// Without focus the bed frames the picture and the cube is small in it.
	img, _ := renderView(t, s, ViewOptions{View: Top, ShowParts: false, Width: 500, Height: 500})
	if _, box := countWhere(img, purplish); box.Dx() > 250 {
		t.Errorf("the whole-bed view shows the cube %d px wide", box.Dx())
	}
}

func TestPartsAreDrawnWhereTheModifierIs(t *testing.T) {
	s := viewScene()
	on, st := renderView(t, s, ViewOptions{View: Front, Focus: []int{0}, ShowParts: true, Width: 500, Height: 500})
	off, _ := renderView(t, s, ViewOptions{View: Front, Focus: []int{0}, ShowParts: false, Width: 500, Height: 500})
	if st.Parts != 1 {
		t.Errorf("parts drawn: %d", st.Parts)
	}
	outline := color.NRGBA{R: 214, G: 160, B: 0, A: 255}
	if a, b := countNear(on, outline, 30), countNear(off, outline, 30); a < 60 || b != 0 {
		t.Errorf("modifier outline pixels: %d with parts, %d without", a, b)
	}
	// The part sticks out of the top of the cube: yellow fill over the
	// background above the cube's top edge.
	yellowish := func(c color.NRGBA) bool {
		return int(c.R) > 165 && int(c.G) > 130 && int(c.B) < 110 && int(c.R) > int(c.B)+90
	}
	if n, box := countWhere(on, yellowish); n < 200 {
		t.Errorf("only %d yellow fill pixels", n)
	} else if _, cube := countWhere(on, purplish); box.Min.Y >= cube.Min.Y {
		t.Errorf("the yellow fill (top row %d) is not above the cube (top row %d)", box.Min.Y, cube.Min.Y)
	}
	// Seen through the model the part is a faint tint: the cube's pixels under the
	// modifier differ from the picture without parts.
	if d := diffPixels(on, off); d < 2000 {
		t.Errorf("parts changed only %d pixels", d)
	}
	// Each part kind has its own colour.
	kinds := map[PartKind]color.NRGBA{
		PartNegative: {R: 190, G: 30, B: 30, A: 255}, PartEnforcer: {R: 30, G: 140, B: 60, A: 255}, PartBlocker: {R: 70, G: 85, B: 115, A: 255},
	}
	for kind, outline := range kinds {
		sc := viewScene()
		sc.Objects[0].Parts[0].Kind = kind
		img, _ := renderView(t, sc, ViewOptions{View: Front, Focus: []int{0}, ShowParts: true, Width: 500, Height: 500})
		if n := countNear(img, outline, 30); n < 60 {
			t.Errorf("%s: %d outline pixels", kind, n)
		}
	}
}

func TestLabelsAreDrawn(t *testing.T) {
	s := viewScene()
	with, _ := renderView(t, s, ViewOptions{View: Isometric, ShowLabels: true, Width: 600, Height: 600})
	without, _ := renderView(t, s, ViewOptions{View: Isometric, ShowLabels: false, Width: 600, Height: 600})
	white := color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	if a, b := countNear(with, white, 2), countNear(without, white, 2); a < 300 || b != 0 {
		t.Errorf("label panel pixels: %d with labels, %d without", a, b)
	}
	dark := color.NRGBA{R: 25, G: 30, B: 45, A: 255}
	if a := countNear(with, dark, 12); a < 30 {
		t.Errorf("label text pixels: %d", a)
	}
	// An object without a label gets none.
	s.Objects[0].Label, s.Objects[1].Label = "", ""
	none, _ := renderView(t, s, ViewOptions{View: Isometric, ShowLabels: true, Width: 600, Height: 600})
	if countNear(none, white, 2) != 0 {
		t.Error("a label was drawn for an object without one")
	}
}

func TestAxesAndRanges(t *testing.T) {
	s := &Scene{Objects: []Object{{Mesh: mesh.Box(20, 20, 20), Transform: mesh.Translate(130, 130, 10), Colour: color.NRGBA{R: 120, G: 120, B: 125, A: 255},
		Ranges: []Range{{From: 2, To: 8}, {From: 12, To: 16}}}}}
	img, _ := renderView(t, s, ViewOptions{View: Top, Width: 500, Height: 500})
	if n := countNear(img, axisX, 40); n < 40 {
		t.Errorf("X axis pixels: %d", n)
	}
	if n := countNear(img, axisY, 40); n < 40 {
		t.Errorf("Y axis pixels: %d", n)
	}
	// Bands only when asked.
	front := ViewOptions{View: Front, Focus: []int{0}, Width: 500, Height: 500}
	off, st := renderView(t, s, front)
	front.ShowRanges = true
	on, st2 := renderView(t, s, front)
	if st.Ranges != 0 || st2.Ranges != 2 {
		t.Errorf("ranges drawn: %d without, %d with", st.Ranges, st2.Ranges)
	}
	orange := color.NRGBA{R: 220, G: 110, B: 0, A: 255}
	if a, b := countNear(on, orange, 30), countNear(off, orange, 30); a < 100 || b != 0 {
		t.Errorf("range outline pixels: %d with bands, %d without", a, b)
	}
}

func TestViewSizes(t *testing.T) {
	s := viewScene()
	data, st, err := RenderView(s, ViewOptions{View: Isometric})
	if err != nil {
		t.Fatal(err)
	}
	if max(st.Width, st.Height) != 1024 {
		t.Errorf("default longest edge %dx%d", st.Width, st.Height)
	}
	cfg, _ := png.DecodeConfig(bytes.NewReader(data))
	if cfg.Width != st.Width || cfg.Height != st.Height {
		t.Errorf("stats %dx%d but the image is %dx%d", st.Width, st.Height, cfg.Width, cfg.Height)
	}
	_, both, _ := RenderView(s, ViewOptions{View: Top})
	_, wide, _ := RenderView(s, ViewOptions{View: Top, Width: 300})
	if wide.Width != 300 || wide.Height != 300*both.Height/both.Width && abs(wide.Height-300*both.Height/both.Width) > 1 {
		t.Errorf("one given size keeps the aspect: %dx%d against %dx%d", wide.Width, wide.Height, both.Width, both.Height)
	}
	_, small, _ := RenderView(s, ViewOptions{View: Top, LongEdge: 256})
	if max(small.Width, small.Height) != 256 {
		t.Errorf("long edge 256 gave %dx%d", small.Width, small.Height)
	}
	if _, _, err := RenderView(s, ViewOptions{Width: MaxSize + 1}); err == nil {
		t.Error("a width above MaxSize was accepted")
	}
}

func abs(a int) int {
	if a < 0 {
		return -a
	}
	return a
}

// A scene without objects still draws the bed; a thumbnail is unchanged by the
// fields only views use.
func TestViewsDoNotChangeThumbnails(t *testing.T) {
	s := viewScene()
	before, err := PlateImages(s)
	if err != nil {
		t.Fatal(err)
	}
	s2 := viewScene()
	s2.Objects[0].Parts, s2.Objects[0].Ranges, s2.Objects[0].Label = nil, nil, ""
	s2.Objects[1].Label = ""
	after, err := PlateImages(s2)
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range before {
		if !bytes.Equal(data, after[name]) {
			t.Errorf("%s changed with parts, ranges and labels", name)
		}
	}
	empty, st, err := RenderView(&Scene{}, ViewOptions{View: Top, Width: 200, Height: 200})
	if err != nil || len(empty) == 0 || st.Objects != 0 {
		t.Errorf("empty scene: %v, %+v", err, st)
	}
}

// D2: a part face coplanar with a model face takes the faint "hidden" tint
// everywhere, not a solid colour.
func TestCoplanarPartFacesAreNotSolid(t *testing.T) {
	obj := Object{Mesh: mesh.Box(20, 20, 20), Transform: mesh.Translate(130, 130, 10), Colour: color.NRGBA{R: 170, G: 40, B: 190, A: 255}}
	// The part is as deep as the model: its front face lies in the model's.
	obj.Parts = []Part{{Mesh: mesh.Box(10, 20, 10), Transform: mesh.Translate(130, 130, 10), Kind: PartModifier}}
	opts := ViewOptions{View: Front, Focus: []int{0}, ShowParts: true, Width: 500, Height: 500}
	with, _ := renderView(t, &Scene{Objects: []Object{obj}}, opts)
	obj.Parts = nil
	opts.ShowParts = false
	without, _ := renderView(t, &Scene{Objects: []Object{obj}}, opts)
	// The middle of the part (250, 250) is inside its outline.
	a, b := with.NRGBAAt(250, 250), without.NRGBAAt(250, 250)
	if int(a.G)-int(b.G) > 75 { // two faint layers add about 60; a solid front face about 90
		t.Errorf("the coplanar part is drawn solid: green %d against %d without it", a.G, b.G)
	}
}

// D3: the focus frame includes the height range bands of the focused objects.
func TestFocusFramingIncludesRangeBands(t *testing.T) {
	obj := Object{Mesh: mesh.Box(20, 20, 20), Transform: mesh.Translate(130, 130, 10), Colour: color.NRGBA{R: 170, G: 40, B: 190, A: 255},
		Ranges: []Range{{From: 5, To: 60}}}
	img, st := renderView(t, &Scene{Objects: []Object{obj}}, ViewOptions{View: Front, Focus: []int{0}, ShowRanges: true, Width: 400, Height: 500})
	if st.Ranges != 1 {
		t.Fatalf("ranges drawn: %d", st.Ranges)
	}
	orange := color.NRGBA{R: 220, G: 110, B: 0, A: 255}
	n, box := countWhere(img, func(c color.NRGBA) bool {
		return int(c.R) > 190 && int(c.G) > 90 && int(c.G) < 140 && c.B < 60 && c.A == 255
	})
	_ = orange
	if n == 0 || box.Min.Y < 5 || box.Max.Y > 495 {
		t.Errorf("the band is cut by the frame: %d px, box %v", n, box)
	}
	if box.Dy() < 500*6/10 {
		t.Errorf("the band spans %d of 500 rows: the frame did not grow to it", box.Dy())
	}
}
