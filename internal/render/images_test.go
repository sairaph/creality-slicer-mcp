package render

import (
	"image/color"
	"testing"

	"github.com/sairaph/creality-slicer-mcp/internal/gcodeinfo"
	"github.com/sairaph/creality-slicer-mcp/internal/mesh"
)

// Images: the neutral grey background, the darkest filament staying visible,
// edges, highlights, 4:3, labels that do not cover each other.

func absInt2(a int) int {
	if a < 0 {
		return -a
	}
	return a
}

func TestViewBackgroundIsNeutralGreyAndBlackAndWhiteAreVisible(t *testing.T) {
	mk := func(col color.NRGBA, x float64) Object {
		return Object{Mesh: mesh.Box(20, 20, 20), Transform: mesh.Translate(x, 130, 10), Colour: col}
	}
	s := &Scene{Objects: []Object{mk(color.NRGBA{R: 255, G: 255, B: 255, A: 255}, 100), mk(color.NRGBA{A: 255}, 160)}}
	img, _ := renderView(t, s, ViewOptions{View: Front, Width: 800, Height: 600, Focus: []int{0, 1}})
	if p := img.NRGBAAt(2, 2); p != viewBackground {
		t.Errorf("corner = %v, want the grey background %v", p, viewBackground)
	}
	if viewBackground.R < 100 || viewBackground.R > 150 || absInt2(int(viewBackground.R)-int(viewBackground.B)) > 20 {
		t.Errorf("the background %v is not a neutral mid grey", viewBackground)
	}
	// White is lighter than the background and black is not pure black.
	light, _ := countWhere(img, func(c color.NRGBA) bool { return c.R > 200 && c.G > 200 && c.B > 200 })
	if light < 2000 {
		t.Errorf("only %d light pixels for the white object", light)
	}
	dark, _ := countWhere(img, func(c color.NRGBA) bool { return c.R > 6 && c.R < 80 && c.G < 80 && c.B < 80 })
	if dark < 2000 {
		t.Errorf("only %d dark pixels that are not black for the black object", dark)
	}
	pure, _ := countWhere(img, func(c color.NRGBA) bool { return c.R == 0 && c.G == 0 && c.B == 0 })
	if pure > 50 {
		t.Errorf("%d pure black pixels: the black object has no shading floor", pure)
	}
	// Edges: the white object has a darker outline against its light faces.
	edge, _ := countWhere(img, func(c color.NRGBA) bool {
		return c.R > 40 && c.R < 110 && c.G < 110 && c.B < 120 && absInt2(int(c.R)-int(c.B)) < 12
	})
	if edge < 200 {
		t.Errorf("only %d edge pixels", edge)
	}
}

func TestHighlightOutlinesTheChangedObject(t *testing.T) {
	s := viewScene()
	opts := ViewOptions{View: Isometric, Width: 600, Height: 450}
	plain, _ := renderView(t, s, opts)
	opts.Highlight = []int{0}
	lit, _ := renderView(t, s, opts)
	pink := func(c color.NRGBA) bool { return c.R > 230 && c.G < 90 && c.B > 100 && c.B < 190 }
	a, _ := countWhere(lit, pink)
	b, _ := countWhere(plain, pink)
	if a < 80 || b != 0 {
		t.Errorf("highlight pixels: %d with, %d without", a, b)
	}
}

func TestViewFrameIsFourByThree(t *testing.T) {
	s := viewScene()
	for _, c := range []struct {
		o    ViewOptions
		w, h int
	}{
		{ViewOptions{View: Top}, 1024, 768},
		{ViewOptions{View: Right, Width: 600}, 600, 450},
		{ViewOptions{View: Right, Height: 300}, 400, 300},
		{ViewOptions{View: Front, Width: 500, Height: 500}, 500, 500},
		{ViewOptions{View: Isometric, LongEdge: 512}, 512, 384},
	} {
		_, st := renderView(t, s, c.o)
		if st.Width != c.w || st.Height != c.h {
			t.Errorf("%+v gave %dx%d, want %dx%d", c.o, st.Width, st.Height, c.w, c.h)
		}
	}
}

func TestLabelsThatCollideAreMovedOrLeftOut(t *testing.T) {
	// Objects behind each other in the Front view: their labels want the same spot.
	var objs []Object
	for i := 0; i < 8; i++ {
		objs = append(objs, Object{Mesh: mesh.Box(20, 20, 20), Transform: mesh.Translate(130, 30+30*float64(i), 10),
			Colour: color.NRGBA{R: 200, G: 200, B: 200, A: 255}, Label: string(rune('1'+i)) + " part"})
	}
	opts := ViewOptions{View: Front, ShowLabels: true, Width: 600, Height: 450}
	img, st := renderView(t, &Scene{Objects: objs[:2]}, opts)
	if len(st.SkippedLabels) != 0 {
		t.Errorf("two colliding labels: skipped %v (one should have been moved)", st.SkippedLabels)
	}
	one, _ := renderView(t, &Scene{Objects: objs[:1]}, opts)
	white := color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	if a, b := countNear(img, white, 2), countNear(one, white, 2); a < b*17/10 {
		t.Errorf("two labels show %d panel pixels, one shows %d: they overlap", a, b)
	}
	_, st = renderView(t, &Scene{Objects: objs}, opts)
	if len(st.SkippedLabels) == 0 {
		t.Error("eight labels on one spot were all drawn")
	}
	if len(st.SkippedLabels) > 5 {
		t.Errorf("skipped %d labels of 8: %v", len(st.SkippedLabels), st.SkippedLabels)
	}
}

func rowMoves(z float64, tool int, speed float64, n int) []gcodeinfo.Move {
	var out []gcodeinfo.Move
	for i := 0; i < n; i++ {
		y := 20 + float64(i)*2
		out = append(out, gcodeinfo.Move{X0: 20, Y0: y, X1: 60, Y1: y, Z: z, Extruding: true, Feature: "Sparse infill", Tool: tool, Speed: speed})
	}
	return out
}

func stripCount(img interface{ NRGBAAt(x, y int) color.NRGBA }, top, h, w int, c color.NRGBA) int {
	n := 0
	for y := top; y < h; y++ {
		for x := 0; x < w; x++ {
			if img.NRGBAAt(x, y) == c {
				n++
			}
		}
	}
	return n
}

func TestGCodeLayerSpeedScaleHasRealTicksAndUniformIsOneEntry(t *testing.T) {
	moves := append(rowMoves(0.2, 0, 600, 5), rowMoves(0.2, 0, 6000, 5)...)
	img := decode(t, mustLayer(t, moves, BySpeed, 400, LayerOptions{}))
	top := stripTop(img)
	if a, b := stripCount(img, top, 400, 400, speedColour(0)), stripCount(img, top, 400, 400, speedColour(1)); a < 4 || b < 4 {
		t.Errorf("the speed bar is missing from the strip: %d %d", a, b)
	}
	if dark := stripCount(img, top, 400, 400, layerText); dark < 150 {
		t.Errorf("only %d text pixels for the ticks, scale and title", dark)
	}
	// Uniform speed: one entry, not a bar with the same number twice.
	uni := decode(t, mustLayer(t, rowMoves(0.2, 0, 4000, 6), BySpeed, 400, LayerOptions{}))
	if n := stripCount(uni, stripTop(uni), 400, 400, speedColour(0.5)); n < 20 || n > 200 {
		t.Errorf("uniform speed: %d swatch pixels, want a single swatch", n)
	}
}

func TestGCodeIsoDrawsToolpathsByFilamentWithAStrip(t *testing.T) {
	var moves []gcodeinfo.Move
	for i := 0; i < 20; i++ {
		moves = append(moves, rowMoves(0.2+0.2*float64(i), i%2, 3000, 6)...)
	}
	opts := LayerOptions{ToolColours: []color.NRGBA{{R: 230, G: 40, B: 40, A: 255}, {R: 250, G: 250, B: 250, A: 255}}, Title: "PLATE 1 TOOLPATHS"}
	data, err := GCodeIso(moves, 600, 450, opts)
	if err != nil {
		t.Fatal(err)
	}
	img := decode(t, data)
	if b := img.Bounds(); b.Dx() != 600 || b.Dy() != 450 {
		t.Fatalf("size %v", b)
	}
	redish := func(c color.NRGBA) bool { return c.R > 150 && c.G < 90 && c.B < 90 }
	whiteish := func(c color.NRGBA) bool { return c.R > 200 && c.G > 200 && c.B > 200 }
	top := stripTop(img)
	region := func(f func(color.NRGBA) bool, y0, y1 int) int {
		n := 0
		for y := y0; y < y1; y++ {
			for x := 0; x < 600; x++ {
				if f(img.NRGBAAt(x, y)) {
					n++
				}
			}
		}
		return n
	}
	if region(redish, 0, top) < 300 || region(whiteish, 0, top) < 300 {
		t.Errorf("tool colours in the picture: red %d, white %d", region(redish, 0, top), region(whiteish, 0, top))
	}
	if region(redish, top, 450) < 20 || region(whiteish, top, 450) < 20 {
		t.Error("the strip has no swatch for each tool")
	}
	if _, err := GCodeIso(moves, 10, 10, opts); err == nil {
		t.Error("a tiny size was accepted")
	}
	if _, err := GCodeIso(nil, 300, 200, opts); err != nil {
		t.Errorf("no moves: %v", err)
	}
}

// The wipe tower footprint is named in the picture, and MinExtent keeps a small
// focused object from filling the frame.
func TestPrimeTowerIsLabelledAndMinExtentGivesContext(t *testing.T) {
	s := viewScene()
	s.WipeTower = &Rect{X0: 200, Y0: 200, X1: 240, Y1: 240}
	white := color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	with, _ := renderView(t, s, ViewOptions{View: Top, Width: 800, Height: 600})
	s.WipeTower = nil
	without, _ := renderView(t, s, ViewOptions{View: Top, Width: 800, Height: 600})
	if a, b := countNear(with, white, 2), countNear(without, white, 2); a < 150 || b != 0 {
		t.Errorf("tower label panel pixels: %d with a tower, %d without", a, b)
	}
	tight, _ := renderView(t, s, ViewOptions{View: Front, Focus: []int{0}, Width: 600, Height: 450})
	ctx, _ := renderView(t, s, ViewOptions{View: Front, Focus: []int{0}, Width: 600, Height: 450, MinExtent: 80})
	_, tb := countWhere(tight, purplish)
	_, cb := countWhere(ctx, purplish)
	if cb.Dx() >= tb.Dx()/2 {
		t.Errorf("with 80 mm of context the 20 mm object spans %d px, tight it spans %d", cb.Dx(), tb.Dx())
	}
	if cb.Dx() < 600/8 {
		t.Errorf("the object spans only %d px", cb.Dx())
	}
}
