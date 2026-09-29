package render

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"os"
	"testing"
	"time"

	"github.com/sairaph/creality-slicer-mcp/internal/mesh"
	"github.com/sairaph/creality-slicer-mcp/internal/userhome/testhome"
)

func TestMain(m *testing.M) { os.Exit(testhome.Run(m)) }

var (
	red   = color.NRGBA{R: 220, G: 30, B: 30, A: 255}
	green = color.NRGBA{R: 30, G: 200, B: 60, A: 255}
	blue  = color.NRGBA{R: 40, G: 60, B: 230, A: 255}
)

func decode(t *testing.T, data []byte) *image.NRGBA {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	// An opaque image may be stored as RGBA; compare all of them as NRGBA.
	n := image.NewNRGBA(img.Bounds())
	draw.Draw(n, n.Bounds(), img, img.Bounds().Min, draw.Src)
	return n
}

// box places a 20 mm cube with its bottom on the bed, centred on x, y.
func box(x, y float64, col color.NRGBA, id int) Object {
	return Object{
		Mesh:       mesh.Box(20, 20, 20),
		Transform:  mesh.Translate(x, y, 10),
		Colour:     col,
		Name:       "cube",
		InstanceID: id,
	}
}

func threeBoxes() *Scene {
	return &Scene{Plate: 2, Objects: []Object{box(40, 130, red, 1), box(130, 130, green, 2), box(220, 130, blue, 3)}}
}

// opaqueBounds returns the bounding box of the pixels with any alpha, and their count.
func opaqueBounds(img *image.NRGBA) (r image.Rectangle, n int) {
	minX, minY, maxX, maxY := 1<<30, 1<<30, -1, -1
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if img.NRGBAAt(x, y).A == 0 {
				continue
			}
			n++
			minX, minY = min(minX, x), min(minY, y)
			maxX, maxY = max(maxX, x+1), max(maxY, y+1)
		}
	}
	if n == 0 {
		return image.Rectangle{}, 0
	}
	return image.Rect(minX, minY, maxX, maxY), n
}

// centroid of the pixels that are exactly col.
func centroid(img *image.NRGBA, col color.NRGBA) (cx, cy float64, n int) {
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if img.NRGBAAt(x, y) == col {
				cx += float64(x)
				cy += float64(y)
				n++
			}
		}
	}
	if n > 0 {
		cx, cy = cx/float64(n), cy/float64(n)
	}
	return
}

func TestPlateImagesNamesSizesAndTransparency(t *testing.T) {
	imgs, err := PlateImages(threeBoxes())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int{
		"plate_2.png": 300, "plate_2_small.png": 96, "plate_no_light_2.png": 300, "top_2.png": 300, "pick_2.png": 300,
	}
	if len(imgs) != len(want) {
		t.Fatalf("got %d images, want %d", len(imgs), len(want))
	}
	for name, size := range want {
		data, ok := imgs[name]
		if !ok {
			t.Fatalf("no %s", name)
		}
		img := decode(t, data)
		if img.Bounds().Dx() != size || img.Bounds().Dy() != size {
			t.Errorf("%s is %v, want %dx%d", name, img.Bounds(), size, size)
		}
		if a := img.NRGBAAt(0, 0).A; a != 0 {
			t.Errorf("%s: the corner has alpha %d, want a transparent background", name, a)
		}
		if _, n := opaqueBounds(img); n == 0 {
			t.Errorf("%s is empty", name)
		}
	}
	// Plate 0 means plate 1.
	one, _ := PlateImages(&Scene{Objects: threeBoxes().Objects})
	if _, ok := one["plate_1.png"]; !ok {
		t.Error("a scene without a plate number does not use plate_1")
	}
}

func TestPlateImagesFillTheFrame(t *testing.T) {
	imgs, _ := PlateImages(threeBoxes())
	for _, name := range []string{"plate_2.png", "top_2.png", "plate_no_light_2.png"} {
		r, _ := opaqueBounds(decode(t, imgs[name]))
		longest := max(r.Dx(), r.Dy())
		if longest < 270 || longest > 296 {
			t.Errorf("%s: the models span %d px, want them to fill the frame with a small margin", name, longest)
		}
		// Centred on the longer axis.
		if left, right := r.Min.X, 300-r.Max.X; r.Dx() >= r.Dy() && absInt(left-right) > 3 {
			t.Errorf("%s: margins %d and %d are not equal", name, left, right)
		}
	}
	small, _ := PlateImages(threeBoxes())
	r, _ := opaqueBounds(decode(t, small["plate_2_small.png"]))
	if longest := max(r.Dx(), r.Dy()); longest < 84 || longest > 94 {
		t.Errorf("small: the models span %d px of 96", longest)
	}
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func TestFlatImageUsesExactlyTheObjectColours(t *testing.T) {
	imgs, _ := PlateImages(threeBoxes())
	img := decode(t, imgs["plate_no_light_2.png"])
	seen := map[color.NRGBA]int{}
	for y := 0; y < 300; y++ {
		for x := 0; x < 300; x++ {
			if p := img.NRGBAAt(x, y); p.A == 255 {
				seen[p]++
			}
		}
	}
	if len(seen) != 3 || seen[red] == 0 || seen[green] == 0 || seen[blue] == 0 {
		t.Errorf("fully opaque colours = %v, want exactly red, green and blue", seen)
	}
}

func TestLitImageShadesFacesDifferently(t *testing.T) {
	imgs, _ := PlateImages(&Scene{Objects: []Object{box(130, 130, red, 1)}})
	img := decode(t, imgs["plate_1.png"])
	shades := map[color.NRGBA]int{}
	for y := 0; y < 300; y++ {
		for x := 0; x < 300; x++ {
			if p := img.NRGBAAt(x, y); p.A == 255 {
				shades[p]++
			}
		}
	}
	if len(shades) < 3 {
		t.Fatalf("a lit cube has %d shades, want one per visible face at least", len(shades))
	}
	for p := range shades {
		if p.G != p.B || p.R < p.G {
			t.Errorf("shade %v is not a shade of red", p)
		}
	}
}

func TestPickImageHoldsOneFlatColourPerInstanceAndNoBlending(t *testing.T) {
	imgs, _ := PlateImages(threeBoxes())
	img := decode(t, imgs["pick_2.png"])
	seen := map[color.NRGBA]int{}
	for y := 0; y < 300; y++ {
		for x := 0; x < 300; x++ {
			p := img.NRGBAAt(x, y)
			if p.A != 0 && p.A != 255 {
				t.Fatalf("pixel %d,%d has alpha %d: pick images are not smoothed", x, y, p.A)
			}
			if p.A == 255 {
				seen[p]++
			}
		}
	}
	want := []color.NRGBA{{R: 1, A: 255}, {R: 2, A: 255}, {R: 3, A: 255}}
	if len(seen) != 3 {
		t.Fatalf("colours = %v, want exactly %v", seen, want)
	}
	for _, c := range want {
		if seen[c] == 0 {
			t.Errorf("no pixel of instance colour %v", c)
		}
	}
	// A big id uses all three bytes.
	imgs, _ = PlateImages(&Scene{Objects: []Object{box(130, 130, red, 0x010203)}})
	got := decode(t, imgs["pick_1.png"]).NRGBAAt(150, 150)
	if got != (color.NRGBA{R: 3, G: 2, B: 1, A: 255}) {
		t.Errorf("instance 0x010203 drew as %v", got)
	}
}

func TestViewsPlaceObjectsLikeTheApp(t *testing.T) {
	imgs, _ := PlateImages(threeBoxes())
	flat := decode(t, imgs["plate_no_light_2.png"])
	rx, ry, _ := centroid(flat, red)
	gx, gy, _ := centroid(flat, green)
	bx, by, _ := centroid(flat, blue)
	// Iso: +X runs right and up the picture (the app's default camera).
	if !(rx < gx && gx < bx) || !(ry > gy && gy > by) {
		t.Errorf("iso centroids red (%.0f,%.0f) green (%.0f,%.0f) blue (%.0f,%.0f): want x rising and y falling", rx, ry, gx, gy, bx, by)
	}
	// Top: +X right, all on one horizontal line through the middle.
	top := decode(t, imgs["top_2.png"])
	r, _ := opaqueBounds(top)
	if r.Dy() > 40 || absInt((r.Min.Y+r.Max.Y)/2-150) > 3 {
		t.Errorf("top view spans y %d..%d: the objects should sit on one line through the middle", r.Min.Y, r.Max.Y)
	}
	l, _ := opaqueBounds(top)
	if l.Dx() < 270 {
		t.Errorf("top view spans %d px in x", l.Dx())
	}
}

func TestRenderingIsDeterministic(t *testing.T) {
	a, _ := PlateImages(threeBoxes())
	b, _ := PlateImages(threeBoxes())
	for name := range a {
		if !bytes.Equal(a[name], b[name]) {
			t.Errorf("%s differs between two renders", name)
		}
	}
	p1, _ := Preview(threeBoxes(), ViewIso, 200)
	p2, _ := Preview(threeBoxes(), ViewIso, 200)
	if !bytes.Equal(p1, p2) {
		t.Error("Preview differs between two renders")
	}
}

func TestEmptySceneAndBadInputs(t *testing.T) {
	imgs, err := PlateImages(&Scene{})
	if err != nil {
		t.Fatal(err)
	}
	if _, n := opaqueBounds(decode(t, imgs["plate_1.png"])); n != 0 {
		t.Error("an empty scene drew something")
	}
	if _, err := Preview(&Scene{}, "side", 200); err == nil {
		t.Error("an unknown view was accepted")
	}
	if _, err := Preview(&Scene{}, ViewIso, 4); err == nil {
		t.Error("a tiny size was accepted")
	}
	// A nil mesh and a mesh without triangles are skipped.
	s := &Scene{Objects: []Object{{}, {Mesh: &mesh.Mesh{}}, box(100, 100, red, 1)}}
	if _, err := PlateImages(s); err != nil {
		t.Error(err)
	}
}

func TestPreviewShowsBedGridAndOutline(t *testing.T) {
	data, err := Preview(&Scene{}, ViewTop, 260)
	if err != nil {
		t.Fatal(err)
	}
	img := decode(t, data)
	if img.Bounds().Dx() != 260 {
		t.Fatalf("size %v", img.Bounds())
	}
	if p := img.NRGBAAt(0, 0); p != previewBackground {
		t.Errorf("corner = %v, want the opaque background %v", p, previewBackground)
	}
	// The whole preview is opaque.
	for y := 0; y < 260; y += 13 {
		for x := 0; x < 260; x += 13 {
			if img.NRGBAAt(x, y).A != 255 {
				t.Fatalf("pixel %d,%d is not opaque", x, y)
			}
		}
	}
	// The bed is drawn: the middle differs from the background, and grid lines
	// and the outline add further colours.
	seen := map[color.NRGBA]bool{}
	for y := 0; y < 260; y++ {
		for x := 0; x < 260; x++ {
			seen[img.NRGBAAt(x, y)] = true
		}
	}
	if !seen[bedFill] {
		t.Error("the bed fill is missing")
	}
	dark := 0
	for c := range seen {
		if c.R < 140 && c.G < 145 && c.B < 160 {
			dark++
		}
	}
	if dark == 0 {
		t.Error("the bed outline is missing")
	}
	// 10 mm grid: 26 cells across a 260 mm bed at 260 px is 10 px; find a
	// grid-coloured pixel in the interior row through the middle of a cell.
	grid := 0
	for x := 20; x < 240; x++ {
		if p := img.NRGBAAt(x, 135); p.R < bedFill.R-8 && p.R > 190 {
			grid++
		}
	}
	if grid < 10 {
		t.Errorf("found %d grid pixels on a scan line, want about one per 10 mm", grid)
	}
}

func TestObjectsOutsideThePrintableAreaAreTintedRed(t *testing.T) {
	obj := box(130, 130, color.NRGBA{R: 90, G: 130, B: 200, A: 255}, 1)
	inside := &Scene{Objects: []Object{obj}}
	outside := &Scene{Objects: []Object{obj}, Printable: Rect{0, 0, 100, 100}}
	if inside.Outside(obj) || !outside.Outside(obj) {
		t.Fatalf("Outside: inside=%v outside=%v", inside.Outside(obj), outside.Outside(obj))
	}
	a, _ := Preview(inside, ViewTop, 260)
	b, _ := Preview(outside, ViewTop, 260)
	ia, ib := decode(t, a), decode(t, b)
	redder := 0
	for y := 0; y < 260; y++ {
		for x := 0; x < 260; x++ {
			pa, pb := ia.NRGBAAt(x, y), ib.NRGBAAt(x, y)
			if int(pb.R)-int(pa.R) > 30 && int(pa.B)-int(pb.B) > 30 {
				redder++
			}
		}
	}
	if redder < 100 {
		t.Errorf("only %d pixels turned red for an object outside the printable area", redder)
	}
}

func TestOutsideByHeightAndEdges(t *testing.T) {
	s := &Scene{MaxHeight: 15}
	if !s.Outside(box(130, 130, red, 1)) {
		t.Error("a 20 mm object passed a 15 mm limit")
	}
	s = &Scene{}
	if s.Outside(box(10, 10, red, 1)) {
		t.Error("an object touching the corner is inside")
	}
	if !s.Outside(box(5, 130, red, 1)) {
		t.Error("an object crossing the bed edge is outside")
	}
	if s.Outside(Object{}) {
		t.Error("an object without a mesh is outside")
	}
}

func TestWipeTowerFootprintIsDrawn(t *testing.T) {
	without, _ := Preview(&Scene{}, ViewTop, 260)
	with, _ := Preview(&Scene{WipeTower: &Rect{20, 20, 80, 50}}, ViewTop, 260)
	a, b := decode(t, without), decode(t, with)
	diff := 0
	for y := 0; y < 260; y++ {
		for x := 0; x < 260; x++ {
			if a.NRGBAAt(x, y) != b.NRGBAAt(x, y) {
				diff++
			}
		}
	}
	// 60 by 30 mm at 1 px per mm is 1800 px, plus the outline.
	if diff < 1500 {
		t.Errorf("the wipe tower changed %d pixels, want its footprint", diff)
	}
	// Blue tinted, bottom left of the picture (Y up).
	p := b.NRGBAAt(50, 260-35)
	if !(p.B > p.R+15) {
		t.Errorf("tower centre pixel %v is not blue tinted", p)
	}
}

func TestPreviewIsoFramesTheBedAndObjects(t *testing.T) {
	data, err := Preview(threeBoxes(), ViewIso, 300)
	if err != nil {
		t.Fatal(err)
	}
	img := decode(t, data)
	// The bed outline reaches close to the picture's left and right edges.
	darkCols := map[int]bool{}
	for y := 0; y < 300; y++ {
		for x := 0; x < 300; x++ {
			p := img.NRGBAAt(x, y)
			if p.R < 150 && p.G < 155 && p.B < 170 && p.R > 100 {
				darkCols[x] = true
			}
		}
	}
	minX, maxX := 300, 0
	for x := range darkCols {
		minX, maxX = min(minX, x), max(maxX, x)
	}
	if minX > 20 || maxX < 280 {
		t.Errorf("bed outline spans x %d..%d of 300, want it framed with a small margin", minX, maxX)
	}
	// The red cube is in the picture in its own colour family.
	found := false
	for y := 0; y < 300 && !found; y++ {
		for x := 0; x < 300; x++ {
			if p := img.NRGBAAt(x, y); p.R > 150 && p.G < 60 && p.B < 60 {
				found = true
				break
			}
		}
	}
	if !found {
		t.Error("the red object is not visible")
	}
}

func TestPerformanceOnHalfAMillionTriangles(t *testing.T) {
	m := denseSphere(707, 354)
	if len(m.Triangles) < 490000 {
		t.Fatalf("test mesh has %d triangles", len(m.Triangles))
	}
	s := &Scene{Objects: []Object{{Mesh: m, Transform: mesh.Translate(130, 130, 50), Colour: red}}}
	timed := func(name string, f func() error) {
		start := time.Now()
		if err := f(); err != nil {
			t.Fatal(err)
		}
		d := time.Since(start)
		t.Logf("%s: %v for %d triangles", name, d, len(m.Triangles))
		if d.Seconds() > 5 {
			t.Errorf("%s took %v, far over the 2 s target", name, d)
		}
	}
	timed("Preview iso 300", func() error { _, err := Preview(s, ViewIso, 300); return err })
	timed("thumbnail iso 300", func() error {
		_, err := thumbnail(s, ViewIso, 300, shadeLit, objectColour, true)
		return err
	})
	timed("PlateImages (five images)", func() error { _, err := PlateImages(s); return err })
}

// denseSphere is a UV sphere of radius 50 with seg segments and rings rings.
func denseSphere(seg, rings int) *mesh.Mesh {
	m := &mesh.Mesh{}
	m.Vertices = append(m.Vertices, [3]float32{0, 0, 50})
	for r := 1; r < rings; r++ {
		phi := math.Pi * float64(r) / float64(rings)
		for s := 0; s < seg; s++ {
			th := 2 * math.Pi * float64(s) / float64(seg)
			m.Vertices = append(m.Vertices, [3]float32{
				float32(50 * math.Sin(phi) * math.Cos(th)), float32(50 * math.Sin(phi) * math.Sin(th)), float32(50 * math.Cos(phi)),
			})
		}
	}
	south := uint32(len(m.Vertices))
	m.Vertices = append(m.Vertices, [3]float32{0, 0, -50})
	ring := func(r, s int) uint32 { return uint32(1 + (r-1)*seg + s%seg) }
	for s := 0; s < seg; s++ {
		m.Triangles = append(m.Triangles, [3]uint32{0, ring(1, s), ring(1, s+1)})
	}
	for r := 1; r < rings-1; r++ {
		for s := 0; s < seg; s++ {
			a, b, c, d := ring(r, s), ring(r+1, s), ring(r+1, s+1), ring(r, s+1)
			m.Triangles = append(m.Triangles, [3]uint32{a, b, c}, [3]uint32{a, c, d})
		}
	}
	for s := 0; s < seg; s++ {
		m.Triangles = append(m.Triangles, [3]uint32{south, ring(rings-1, s+1), ring(rings-1, s)})
	}
	return m
}
