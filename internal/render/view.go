package render

import (
	"fmt"
	"image/color"
	"math"
	"sync"
)

// MaxSize is the largest image edge in pixels. A reply carries about 1 MiB of
// image, so nothing larger is ever useful, and a canvas of this size with
// supersampling is already about 130 MB.
const MaxSize = 2048

// View is a camera for a plate.
type View string

const (
	// ViewIso looks from the front left at azimuth 45 degrees and elevation 35
	// degrees, orthographically: +X runs right and up the picture, +Y away and
	// to the right.
	ViewIso View = "iso"
	// ViewTop looks straight down; +X runs right, +Y up the picture.
	ViewTop View = "top"
)

// camera is an orthographic camera. r is screen right, u screen up, c points
// from the scene to the camera.
type camera struct {
	r, u, c [3]float64
}

func newCamera(v View) (camera, error) {
	switch v {
	case ViewIso:
		const az, el = 45 * math.Pi / 180, 35 * math.Pi / 180
		c := [3]float64{-math.Sin(az) * math.Cos(el), -math.Cos(az) * math.Cos(el), math.Sin(el)}
		r := norm(cross([3]float64{-c[0], -c[1], -c[2]}, [3]float64{0, 0, 1}))
		u := cross(r, [3]float64{-c[0], -c[1], -c[2]})
		return camera{r: r, u: u, c: c}, nil
	case ViewTop:
		return camera{r: [3]float64{1, 0, 0}, u: [3]float64{0, 1, 0}, c: [3]float64{0, 0, 1}}, nil
	}
	return camera{}, fmt.Errorf("render: unknown view %q (want iso or top)", v)
}

func cross(a, b [3]float64) [3]float64 {
	return [3]float64{a[1]*b[2] - a[2]*b[1], a[2]*b[0] - a[0]*b[2], a[0]*b[1] - a[1]*b[0]}
}

func dot(a, b [3]float64) float64 { return a[0]*b[0] + a[1]*b[1] + a[2]*b[2] }

func norm(a [3]float64) [3]float64 {
	l := math.Sqrt(dot(a, a))
	if l == 0 {
		return a
	}
	return [3]float64{a[0] / l, a[1] / l, a[2] / l}
}

// project returns the screen coordinates (mm, y up) and the depth of a point.
func (cam camera) project(p [3]float64) (sx, sy, depth float64) {
	return dot(p, cam.r), dot(p, cam.u), dot(p, cam.c)
}

// fit maps screen millimetres to canvas pixels.
type fit struct {
	scale, cx, cy float64
	w, h          float64
}

func (f fit) px(sx, sy float64) (float32, float32) {
	return float32((sx-f.cx)*f.scale + f.w/2), float32(f.h/2 - (sy-f.cy)*f.scale)
}

// newFit centres the extents (min, max in screen mm) in a w by h canvas with
// margin (a fraction of the size) on every side, at one uniform scale.
func newFit(minX, minY, maxX, maxY float64, w, h int, margin float64) fit {
	dx, dy := maxX-minX, maxY-minY
	if dx <= 0 {
		dx = 1
	}
	if dy <= 0 {
		dy = 1
	}
	s := math.Min(float64(w)*(1-2*margin)/dx, float64(h)*(1-2*margin)/dy)
	return fit{scale: s, cx: (minX + maxX) / 2, cy: (minY + maxY) / 2, w: float64(w), h: float64(h)}
}

// placed is an object's mesh in world coordinates.
type placed struct {
	obj   *Object
	world [][3]float32
}

// place transforms every object's vertices to the world once, in parallel.
func place(objs []Object) []placed {
	out := make([]placed, len(objs))
	var wg sync.WaitGroup
	for i := range objs {
		o := &objs[i]
		if o.Mesh == nil {
			out[i] = placed{obj: o}
			continue
		}
		wg.Add(1)
		go func(i int, o *Object) {
			defer wg.Done()
			w := make([][3]float32, len(o.Mesh.Vertices))
			for j, v := range o.Mesh.Vertices {
				w[j] = o.Transform.ApplyF32(v)
			}
			out[i] = placed{obj: o, world: w}
		}(i, o)
	}
	wg.Wait()
	return out
}

// extents returns the screen-space extents of the objects' vertices.
func (cam camera) extents(ps []placed) (minX, minY, maxX, maxY float64, ok bool) {
	minX, minY = math.Inf(1), math.Inf(1)
	maxX, maxY = math.Inf(-1), math.Inf(-1)
	rx, ry, rz := float32(cam.r[0]), float32(cam.r[1]), float32(cam.r[2])
	ux, uy, uz := float32(cam.u[0]), float32(cam.u[1]), float32(cam.u[2])
	for _, p := range ps {
		for _, v := range p.world {
			sx := float64(v[0]*rx + v[1]*ry + v[2]*rz)
			sy := float64(v[0]*ux + v[1]*uy + v[2]*uz)
			minX, maxX = math.Min(minX, sx), math.Max(maxX, sx)
			minY, maxY = math.Min(minY, sy), math.Max(maxY, sy)
			ok = true
		}
	}
	return
}

// shade is how a mesh is coloured.
type shade int

const (
	shadeLit  shade = iota // directional light plus ambient
	shadeFlat              // the flat colour, no shading
	shadeView              // like shadeLit with a floor, so black stays visible
)

// drawObjects rasterises the objects into c.
func (c *canvas) drawObjects(cam camera, f fit, ps []placed, mode shade, colour func(*Object) color.NRGBA) {
	// The light comes from above and a little left of the camera.
	light := norm([3]float64{
		cam.c[0] + cam.u[0]*0.6 - cam.r[0]*0.5,
		cam.c[1] + cam.u[1]*0.6 - cam.r[1]*0.5,
		cam.c[2] + cam.u[2]*0.6 - cam.r[2]*0.5,
	})
	for _, p := range ps {
		if p.obj.Mesh == nil {
			continue
		}
		base := colour(p.obj)
		sx := make([]float32, len(p.world))
		sy := make([]float32, len(p.world))
		sz := make([]float32, len(p.world))
		for i, v := range p.world {
			x, y, z := cam.project([3]float64{float64(v[0]), float64(v[1]), float64(v[2])})
			sx[i], sy[i] = f.px(x, y)
			sz[i] = float32(z)
		}
		for _, t := range p.obj.Mesh.Triangles {
			a, b, d := t[0], t[1], t[2]
			col := [4]uint8{base.R, base.G, base.B, 255}
			switch mode {
			case shadeLit:
				col = litColour(base, p.world[a], p.world[b], p.world[d], light)
			case shadeView:
				col = viewColour(base, p.world[a], p.world[b], p.world[d], light)
			}
			c.tri(sx[a], sy[a], sz[a], sx[b], sy[b], sz[b], sx[d], sy[d], sz[d], col, true)
		}
	}
}

// litColour shades base by the angle between the triangle's normal and the
// light. Both sides of a triangle are lit alike, so a mesh with flipped
// triangles still looks right.
func litColour(base color.NRGBA, a, b, d [3]float32, light [3]float64) [4]uint8 {
	e1 := [3]float64{float64(b[0] - a[0]), float64(b[1] - a[1]), float64(b[2] - a[2])}
	e2 := [3]float64{float64(d[0] - a[0]), float64(d[1] - a[1]), float64(d[2] - a[2])}
	n := cross(e1, e2)
	l := math.Sqrt(dot(n, n))
	k := 0.35
	if l > 0 {
		k += 0.65 * math.Abs(dot(n, light)) / l
	}
	return [4]uint8{shadeByte(base.R, k), shadeByte(base.G, k), shadeByte(base.B, k), 255}
}

func shadeByte(v uint8, k float64) uint8 {
	x := float64(v) * k
	if x > 255 {
		return 255
	}
	return uint8(x + 0.5)
}

// viewColour shades base like litColour, with a higher lowest light and a lift
// of the darks: a black filament keeps a visible shape and a white one never
// blends into a mid grey background.
func viewColour(base color.NRGBA, a, b, d [3]float32, light [3]float64) [4]uint8 {
	e1 := [3]float64{float64(b[0] - a[0]), float64(b[1] - a[1]), float64(b[2] - a[2])}
	e2 := [3]float64{float64(d[0] - a[0]), float64(d[1] - a[1]), float64(d[2] - a[2])}
	n := cross(e1, e2)
	l := math.Sqrt(dot(n, n))
	k := 0.5
	if l > 0 {
		k += 0.5 * math.Abs(dot(n, light)) / l
	}
	lum := (float64(base.R) + float64(base.G) + float64(base.B)) / 765
	lift := 34 * k * (1 - lum)
	ch := func(v uint8) uint8 { return uint8(math.Min(255, float64(v)*k+lift+0.5)) }
	return [4]uint8{ch(base.R), ch(base.G), ch(base.B), 255}
}

// meshDefault is the colour of an object without one.
var meshDefault = color.NRGBA{R: 170, G: 170, B: 176, A: 255}

// objectColour is the filament colour of o, or a neutral grey.
func objectColour(o *Object) color.NRGBA {
	if o.Colour.A == 0 {
		return meshDefault
	}
	c := o.Colour
	c.A = 255
	return c
}

// tint mixes a colour towards red for an object outside the printable area.
func tint(c color.NRGBA) color.NRGBA {
	const k = 0.55
	return color.NRGBA{
		R: uint8(float64(c.R)*(1-k) + 225*k),
		G: uint8(float64(c.G)*(1-k) + 40*k),
		B: uint8(float64(c.B)*(1-k) + 40*k),
		A: 255,
	}
}

// bedPoint is a point on the bed plane (z = 0) in canvas pixels.
func bedPoint(cam camera, f fit, x, y float64) (float32, float32) {
	sx, sy, _ := cam.project([3]float64{x, y, 0})
	return f.px(sx, sy)
}

var (
	previewBackground = color.NRGBA{R: 246, G: 247, B: 249, A: 255}
	bedOutline        = color.NRGBA{R: 120, G: 126, B: 140, A: 255}
)

// bedColours are the colours of the bed plane, its grid, its outline and the
// wipe tower footprint.
type bedColours struct {
	fill, grid, outline color.NRGBA
	tower               [4]uint8
	towerOutline        color.NRGBA
}

// lightBed is the bed of Preview: light on a near white background.
var lightBed = bedColours{
	fill: color.NRGBA{R: 226, G: 229, B: 235, A: 255}, grid: color.NRGBA{R: 205, G: 209, B: 217, A: 255},
	outline: bedOutline, tower: [4]uint8{120, 160, 220, 110}, towerOutline: color.NRGBA{R: 70, G: 110, B: 190, A: 255},
}

// viewBackground and viewBed are the neutral mid grey of RenderView: white and
// black filaments both read against it.
var (
	viewBackground = color.NRGBA{R: 122, G: 126, B: 134, A: 255}
	viewBed        = bedColours{
		fill: color.NRGBA{R: 158, G: 162, B: 170, A: 255}, grid: color.NRGBA{R: 140, G: 144, B: 153, A: 255},
		outline: color.NRGBA{R: 52, G: 56, B: 66, A: 255}, tower: [4]uint8{110, 160, 235, 130}, towerOutline: color.NRGBA{R: 40, G: 84, B: 170, A: 255},
	}
)

// drawBed paints the bed plane with its outline, the 10 mm grid and the wipe
// tower footprint, before the objects.
func (c *canvas) drawBed(cam camera, f fit, s *Scene, ss int, pal bedColours) {
	bed := s.bed()
	quad := func(r Rect, col [4]uint8) {
		var p [4][2]float32
		for i, pt := range [4][2]float64{{r.X0, r.Y0}, {r.X1, r.Y0}, {r.X1, r.Y1}, {r.X0, r.Y1}} {
			p[i][0], p[i][1] = bedPoint(cam, f, pt[0], pt[1])
		}
		c.quad(p, col)
	}
	quad(bed, rgba(pal.fill))
	gridW := float32(ss) * 0.8
	for x := math.Ceil(bed.X0/10) * 10; x <= bed.X1; x += 10 {
		x0, y0 := bedPoint(cam, f, x, bed.Y0)
		x1, y1 := bedPoint(cam, f, x, bed.Y1)
		c.line(x0, y0, x1, y1, gridW, rgba(pal.grid))
	}
	for y := math.Ceil(bed.Y0/10) * 10; y <= bed.Y1; y += 10 {
		x0, y0 := bedPoint(cam, f, bed.X0, y)
		x1, y1 := bedPoint(cam, f, bed.X1, y)
		c.line(x0, y0, x1, y1, gridW, rgba(pal.grid))
	}
	outline := func(r Rect, col color.NRGBA, w float32) {
		pts := [5][2]float64{{r.X0, r.Y0}, {r.X1, r.Y0}, {r.X1, r.Y1}, {r.X0, r.Y1}, {r.X0, r.Y0}}
		for i := 0; i < 4; i++ {
			x0, y0 := bedPoint(cam, f, pts[i][0], pts[i][1])
			x1, y1 := bedPoint(cam, f, pts[i+1][0], pts[i+1][1])
			c.line(x0, y0, x1, y1, w, rgba(col))
		}
	}
	if s.WipeTower != nil && !s.WipeTower.Empty() {
		quad(*s.WipeTower, pal.tower)
		outline(*s.WipeTower, pal.towerOutline, float32(ss)*1.4)
	}
	outline(bed, pal.outline, float32(ss)*1.8)
}

// Preview draws the plate from a view as a square PNG of size pixels: the bed
// with its outline and 10 mm grid, the wipe tower footprint, and the objects
// lit and in their filament colours, tinted red when outside the printable
// area. The background is opaque.
func Preview(s *Scene, view View, size int) ([]byte, error) {
	if size < 16 || size > MaxSize {
		return nil, fmt.Errorf("render: size %d is out of range (16 to %d)", size, MaxSize)
	}
	cam, err := newCamera(view)
	if err != nil {
		return nil, err
	}
	ss := ssFactor(size)
	c := newCanvas(size*ss, size*ss, previewBackground)
	ps := place(s.Objects)

	// Frame the bed and the objects together.
	minX, minY, maxX, maxY := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	bed := s.bed()
	for _, pt := range [4][2]float64{{bed.X0, bed.Y0}, {bed.X1, bed.Y0}, {bed.X1, bed.Y1}, {bed.X0, bed.Y1}} {
		sx, sy, _ := cam.project([3]float64{pt[0], pt[1], 0})
		minX, maxX, minY, maxY = math.Min(minX, sx), math.Max(maxX, sx), math.Min(minY, sy), math.Max(maxY, sy)
	}
	if ox0, oy0, ox1, oy1, ok := cam.extents(ps); ok {
		minX, maxX, minY, maxY = math.Min(minX, ox0), math.Max(maxX, ox1), math.Min(minY, oy0), math.Max(maxY, oy1)
	}
	f := newFit(minX, minY, maxX, maxY, c.w, c.h, 0.04)

	c.drawBed(cam, f, s, ss, lightBed)
	c.drawObjects(cam, f, ps, shadeLit, func(o *Object) color.NRGBA {
		col := objectColour(o)
		if s.Outside(*o) {
			return tint(col)
		}
		return col
	})
	return encodePNG(c.downsample(ss))
}

// thumbnailMargin is the space kept around the models in a thumbnail, as a
// fraction of the image size.
const thumbnailMargin = 0.04

// thumbnail renders the models only (no bed, transparent background) framed
// to fill the image, like the app's own plate thumbnails.
func thumbnail(s *Scene, view View, size int, mode shade, colour func(*Object) color.NRGBA, aa bool) ([]byte, error) {
	if size < 1 || size > MaxSize {
		return nil, fmt.Errorf("render: size %d is out of range (1 to %d)", size, MaxSize)
	}
	cam, err := newCamera(view)
	if err != nil {
		return nil, err
	}
	ss := 1
	if aa {
		ss = ssFactor(size)
	}
	c := newCanvas(size*ss, size*ss, color.NRGBA{})
	ps := place(s.Objects)
	if minX, minY, maxX, maxY, ok := cam.extents(ps); ok {
		f := newFit(minX, minY, maxX, maxY, c.w, c.h, thumbnailMargin)
		c.drawObjects(cam, f, ps, mode, colour)
	}
	return encodePNG(c.downsample(ss))
}

// The five project thumbnails, keyed by the file names the app writes into
// Metadata/ (plate_N.png and so on, N the scene's plate number).
const (
	thumbSize      = 300
	thumbSmallSize = 96
)

// PlateImages renders the five thumbnails of a project plate, all with a
// transparent background and no bed, framed on the models:
//
//	plate_N.png           300 px, iso view, lit, filament colours
//	plate_N_small.png     96 px, the same
//	plate_no_light_N.png  300 px, iso view, flat filament colours
//	top_N.png             300 px, top view, lit
//	pick_N.png            300 px, iso view, one flat colour per instance
//	                      (its id as red, green, blue bytes), no shading and no
//	                      edge smoothing, so a pixel identifies its instance
//
// The map is keyed by those file names.
func PlateImages(s *Scene) (map[string][]byte, error) {
	n := s.plate()
	out := map[string][]byte{}
	type job struct {
		name string
		size int
		view View
		mode shade
		col  func(*Object) color.NRGBA
		aa   bool
	}
	pick := func(o *Object) color.NRGBA {
		id := o.InstanceID
		return color.NRGBA{R: uint8(id), G: uint8(id >> 8), B: uint8(id >> 16), A: 255}
	}
	jobs := []job{
		{fmt.Sprintf("plate_%d.png", n), thumbSize, ViewIso, shadeLit, objectColour, true},
		{fmt.Sprintf("plate_%d_small.png", n), thumbSmallSize, ViewIso, shadeLit, objectColour, true},
		{fmt.Sprintf("plate_no_light_%d.png", n), thumbSize, ViewIso, shadeFlat, objectColour, true},
		{fmt.Sprintf("top_%d.png", n), thumbSize, ViewTop, shadeLit, objectColour, true},
		{fmt.Sprintf("pick_%d.png", n), thumbSize, ViewIso, shadeFlat, pick, false},
	}
	for _, j := range jobs {
		data, err := thumbnail(s, j.view, j.size, j.mode, j.col, j.aa)
		if err != nil {
			return nil, err
		}
		out[j.name] = data
	}
	return out, nil
}
