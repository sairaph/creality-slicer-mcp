package render

import (
	"image/color"
	"math"
	"path/filepath"
	"testing"

	"github.com/sairaph/creality-slicer-mcp/internal/gcodeinfo"
)

// square is the four extruding moves of a 40 mm square from 10,10.
func square(feature string, tool int, speed float64) []gcodeinfo.Move {
	pts := [][2]float64{{10, 10}, {50, 10}, {50, 50}, {10, 50}, {10, 10}}
	var out []gcodeinfo.Move
	for i := 0; i < 4; i++ {
		out = append(out, gcodeinfo.Move{
			X0: pts[i][0], Y0: pts[i][1], X1: pts[i+1][0], Y1: pts[i+1][1],
			Extruding: true, Feature: feature, Tool: tool, Speed: speed,
		})
	}
	return out
}

func countColour(img interface {
	At(x, y int) color.Color
}, w, h int, want color.NRGBA) int {
	n := 0
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r, g, b, a := img.At(x, y).RGBA()
			if a == 0xffff && uint8(r>>8) == want.R && uint8(g>>8) == want.G && uint8(b>>8) == want.B {
				n++
			}
		}
	}
	return n
}

func TestGCodeLayerColoursByFeature(t *testing.T) {
	moves := append(square("Outer wall", 0, 3000), square("Sparse infill", 0, 6000)...)
	data, err := GCodeLayer(moves, ByFeature, 400, LayerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	img := decode(t, data)
	if img.Bounds().Dx() != 400 || img.Bounds().Dy() != 400 {
		t.Fatalf("size %v", img.Bounds())
	}
	if p := img.NRGBAAt(399, 200); p != layerBackground {
		t.Errorf("background = %v, want %v", p, layerBackground)
	}
	// The second square is drawn over the first, so the outer wall colour
	// survives only in the legend swatch; the infill colour holds the square.
	outer := countColour(img, 400, 400, featurePalette["outer wall"])
	infill := countColour(img, 400, 400, featurePalette["sparse infill"])
	if outer == 0 {
		t.Error("the legend has no outer wall swatch")
	}
	if infill < 500 {
		t.Errorf("only %d sparse infill pixels: the square is missing", infill)
	}
}

func TestGCodeLayerFramesTheLayerAndKeepsAspect(t *testing.T) {
	data, _ := GCodeLayer(square("Outer wall", 0, 3000), ByFeature, 400, LayerOptions{})
	img := decode(t, data)
	col := featurePalette["outer wall"]
	minX, minY, maxX, maxY := 400, 400, -1, -1
	for y := 0; y < 400; y++ {
		for x := 0; x < 400; x++ {
			if x < 20 && y < 20 {
				continue // the legend swatch
			}
			if img.NRGBAAt(x, y) == col {
				minX, maxX, minY, maxY = min(minX, x), max(maxX, x), min(minY, y), max(maxY, y)
			}
		}
	}
	w, h := maxX-minX, maxY-minY
	if absInt(w-h) > 3 {
		t.Errorf("a square is drawn %d by %d px", w, h)
	}
	// 84 percent of 400 px, less the line width.
	if w < 300 || w > 340 {
		t.Errorf("the square spans %d px, want it to fill the frame with a margin", w)
	}
	// Y is up: the square's centre is the frame's centre.
	if cx, cy := (minX+maxX)/2, (minY+maxY)/2; absInt(cx-200) > 3 || absInt(cy-200) > 3 {
		t.Errorf("the square is centred at %d,%d", cx, cy)
	}
}

func TestGCodeLayerYIsUp(t *testing.T) {
	moves := []gcodeinfo.Move{
		{X0: 10, Y0: 10, X1: 10, Y1: 20, Extruding: true, Feature: "Outer wall"},               // short vertical piece
		{X0: 10, Y0: 10, X1: 110, Y1: 10, Extruding: true, Feature: "Sparse infill"},           // long horizontal piece along the bottom
		{X0: 110, Y0: 10, X1: 110, Y1: 110, Extruding: true, Feature: "Internal solid infill"}, // long vertical piece on the right
	}
	img := decode(t, mustLayer(t, moves, ByFeature, 400, LayerOptions{}))
	cx, cy, n := centroid(img, featurePalette["sparse infill"])
	_, vy, _ := centroid(img, featurePalette["internal solid infill"])
	if n == 0 || cy < vy {
		t.Errorf("the segment at Y=10 (centroid y %.0f) is not below the one spanning Y 10..110 (centroid y %.0f)", cy, vy)
	}
	if cx > 250 {
		t.Errorf("the segment along X 10..110 has centroid x %.0f: X runs right, so it should sit left of the vertical one", cx)
	}
}

func mustLayer(t *testing.T, moves []gcodeinfo.Move, by ColorBy, size int, opts LayerOptions) []byte {
	t.Helper()
	data, err := GCodeLayer(moves, by, size, opts)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestGCodeLayerColoursByFilament(t *testing.T) {
	moves := append(square("Outer wall", 0, 3000), func() []gcodeinfo.Move {
		m := square("Outer wall", 1, 3000)
		for i := range m {
			m[i].X0, m[i].X1 = m[i].X0+60, m[i].X1+60
		}
		return m
	}()...)
	c0, c1 := color.NRGBA{R: 200, G: 10, B: 10, A: 255}, color.NRGBA{R: 10, G: 10, B: 200, A: 255}
	img := decode(t, mustLayer(t, moves, ByFilament, 400, LayerOptions{ToolColours: []color.NRGBA{c0, c1}}))
	if n := countColour(img, 400, 400, c0); n < 300 {
		t.Errorf("tool 0 has %d pixels of its colour", n)
	}
	if n := countColour(img, 400, 400, c1); n < 300 {
		t.Errorf("tool 1 has %d pixels of its colour", n)
	}
	// A tool without a colour takes one from the palette.
	img = decode(t, mustLayer(t, square("Outer wall", 2, 3000), ByFilament, 400, LayerOptions{}))
	if n := countColour(img, 400, 400, toolPalette[2]); n < 300 {
		t.Errorf("an unlisted tool did not use the palette colour: %d pixels", n)
	}
}

func TestGCodeLayerColoursBySpeed(t *testing.T) {
	slow, fast := square("Outer wall", 0, 1800), square("Outer wall", 0, 12000)
	for i := range fast {
		fast[i].X0, fast[i].X1 = fast[i].X0+60, fast[i].X1+60
	}
	img := decode(t, mustLayer(t, append(slow, fast...), BySpeed, 400, LayerOptions{}))
	lo, hi := speedColour(0), speedColour(1)
	if n := countColour(img, 400, 400, lo); n < 300 {
		t.Errorf("%d pixels of the slowest colour", n)
	}
	if n := countColour(img, 400, 400, hi); n < 300 {
		t.Errorf("%d pixels of the fastest colour", n)
	}
	if lo.B <= lo.R || hi.R <= hi.B {
		t.Errorf("gradient ends %v %v: want blue for slow and red for fast", lo, hi)
	}
}

func TestGCodeLayerTravelsAreHiddenByDefault(t *testing.T) {
	moves := append(square("Outer wall", 0, 3000), gcodeinfo.Move{X0: 50, Y0: 50, X1: 100, Y1: 100, Feature: "Outer wall"})
	without := decode(t, mustLayer(t, moves, ByFeature, 300, LayerOptions{}))
	with := decode(t, mustLayer(t, moves, ByFeature, 300, LayerOptions{Travels: true}))
	trav := color.NRGBA{R: travelColour[0], G: travelColour[1], B: travelColour[2], A: 255}
	if n := countColour(without, 300, 300, trav); n != 0 {
		t.Errorf("travel pixels drawn by default: %d", n)
	}
	if n := countColour(with, 300, 300, trav); n == 0 {
		t.Error("Travels did not draw the travel move")
	}
	// Hidden travels also stay out of the framing.
	far := append(square("Outer wall", 0, 3000), gcodeinfo.Move{X0: 50, Y0: 50, X1: 900, Y1: 900})
	a := mustLayer(t, far, ByFeature, 300, LayerOptions{})
	b := mustLayer(t, square("Outer wall", 0, 3000), ByFeature, 300, LayerOptions{})
	if string(a) != string(b) {
		t.Error("a hidden travel changed the picture")
	}
}

func TestGCodeLayerArcsAreDrawnAsCurves(t *testing.T) {
	// A half circle of radius 20 from (30,20) to (-10,20)... counter clockwise
	// around (10,20).
	arc := gcodeinfo.Move{X0: 30, Y0: 20, X1: -10, Y1: 20, I: -20, J: 0, Arc: true, Extruding: true, Feature: "Outer wall"}
	segs := segments(arc)
	if len(segs) < 20 {
		t.Fatalf("an arc became %d segments", len(segs))
	}
	for _, s := range segs {
		for _, p := range [][2]float64{{s.x0, s.y0}, {s.x1, s.y1}} {
			if d := math.Hypot(p[0]-10, p[1]-20); math.Abs(d-20) > 1e-6 {
				t.Fatalf("point %v is %g from the centre, want 20", p, d)
			}
		}
	}
	if last := segs[len(segs)-1]; last.x1 != -10 || last.y1 != 20 {
		t.Errorf("the arc ends at %v,%v", last.x1, last.y1)
	}
	// Counter clockwise from angle 0 passes through the top (larger Y).
	if mid := segs[len(segs)/2]; mid.y0 < 30 {
		t.Errorf("a counter clockwise half circle from the right should pass over the top; mid y = %g", mid.y0)
	}
	cw := arc
	cw.Clockwise = true
	if mid := segments(cw)[len(segments(cw))/2]; mid.y0 > 10 {
		t.Errorf("a clockwise half circle from the right should pass under the centre; mid y = %g", mid.y0)
	}
	// A full circle: same start and end.
	full := gcodeinfo.Move{X0: 30, Y0: 20, X1: 30, Y1: 20, I: -20, J: 0, Arc: true, Extruding: true}
	if got := len(segments(full)); got < 60 {
		t.Errorf("a full circle became %d segments", got)
	}
	if _, err := GCodeLayer([]gcodeinfo.Move{arc}, ByFeature, 200, LayerOptions{}); err != nil {
		t.Error(err)
	}
}

func TestGCodeLayerScaleBarAndTitle(t *testing.T) {
	data := mustLayer(t, square("Outer wall", 0, 3000), ByFeature, 400, LayerOptions{Title: "LAYER 1"})
	img := decode(t, data)
	// A 40 mm square fills about 340 px, so a nice bar of 20 mm fits: the bar
	// is a dark horizontal run near the bottom left.
	run := 0
	for x := 6; x < 400; x++ {
		if img.NRGBAAt(x, 400-6-1) == layerText {
			run++
		}
	}
	if run < 100 {
		t.Errorf("scale bar is %d px wide, want about 20 mm worth", run)
	}
	// The title is drawn at the bottom right.
	dark := 0
	for y := 380; y < 395; y++ {
		for x := 320; x < 396; x++ {
			if img.NRGBAAt(x, y) == layerText {
				dark++
			}
		}
	}
	if dark < 30 {
		t.Errorf("only %d dark pixels where the title goes", dark)
	}
}

func TestGCodeLayerBedOutlineOnlyWithFitBed(t *testing.T) {
	moves := square("Outer wall", 0, 3000)
	plain := mustLayer(t, moves, ByFeature, 300, LayerOptions{Bed: DefaultBed})
	if string(plain) != string(mustLayer(t, moves, ByFeature, 300, LayerOptions{})) {
		t.Error("the bed changed the picture without FitBed")
	}
	fit := decode(t, mustLayer(t, moves, ByFeature, 300, LayerOptions{Bed: DefaultBed, FitBed: true}))
	if n := countColour(fit, 300, 300, bedOutline); n == 0 {
		t.Error("FitBed drew no bed outline")
	}
}

func TestGCodeLayerEdgeCasesAndErrors(t *testing.T) {
	if _, err := GCodeLayer(nil, "rainbow", 300, LayerOptions{}); err == nil {
		t.Error("an unknown colour mode was accepted")
	}
	if _, err := GCodeLayer(nil, ByFeature, 10, LayerOptions{}); err == nil {
		t.Error("a tiny size was accepted")
	}
	if img := decode(t, mustLayer(t, nil, ByFeature, 200, LayerOptions{})); img.Bounds().Dx() != 200 {
		t.Error("no moves gave the wrong size")
	}
	// A layer of travels only still renders (with them framed).
	if _, err := GCodeLayer([]gcodeinfo.Move{{X0: 0, Y0: 0, X1: 5, Y1: 5}}, BySpeed, 200, LayerOptions{}); err != nil {
		t.Error(err)
	}
	// Many features: the legend is cut to fit and says so, never overflows.
	var moves []gcodeinfo.Move
	for i := 0; i < 60; i++ {
		moves = append(moves, gcodeinfo.Move{X0: 0, Y0: float64(i), X1: 50, Y1: float64(i), Extruding: true, Feature: "feature number " + string(rune('a'+i%26)) + string(rune('a'+i/26))})
	}
	if _, err := GCodeLayer(moves, ByFeature, 200, LayerOptions{}); err != nil {
		t.Error(err)
	}
}

func TestGCodeLayerOfARealFile(t *testing.T) {
	path := filepath.Join("..", "gcodeinfo", "testdata", "cubes_2filaments.gcode")
	layers, err := gcodeinfo.Layers(path)
	if err != nil || len(layers) < 10 {
		t.Fatalf("layers: %d, %v", len(layers), err)
	}
	moves, err := gcodeinfo.LayerMoves(path, layers[len(layers)/2])
	if err != nil {
		t.Fatal(err)
	}
	for _, by := range []ColorBy{ByFeature, ByFilament, BySpeed} {
		img := decode(t, mustLayer(t, moves, by, 500, LayerOptions{Title: "LAYER"}))
		if img.Bounds().Dx() != 500 {
			t.Errorf("%s: size %v", by, img.Bounds())
		}
	}
	feat := decode(t, mustLayer(t, moves, ByFeature, 500, LayerOptions{}))
	for _, name := range []string{"outer wall", "inner wall", "sparse infill"} {
		if countColour(feat, 500, 500, featurePalette[name]) < 50 {
			t.Errorf("feature %q is not visible in a real layer", name)
		}
	}
}

func TestFeatureColourIsStable(t *testing.T) {
	if featureColour("Some new feature") != featureColour("some new feature ") {
		t.Error("the colour of an unknown feature depends on case or spacing")
	}
	if featureColour("Outer wall") != featurePalette["outer wall"] {
		t.Error("a known feature did not use the palette")
	}
}
