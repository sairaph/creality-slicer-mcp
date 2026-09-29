package render

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"strings"

	"github.com/sairaph/creality-slicer-mcp/internal/mesh"
)

// ViewName is one of the named cameras of RenderView. The names are the ones
// freecad-mcp uses. Z is up. Front looks along +Y (camera at -Y), Back along
// -Y, Left along +X (camera at -X), Right along -X, Top straight down and
// Bottom straight up. Isometric is azimuth 45 degrees from the front left at
// elevation 35.26, Dimetric azimuth 45 at 20.7, Trimetric azimuth 60 at 30. All
// are orthographic.
type ViewName string

// The named views.
const (
	Isometric ViewName = "Isometric"
	Front     ViewName = "Front"
	Top       ViewName = "Top"
	Right     ViewName = "Right"
	Back      ViewName = "Back"
	Left      ViewName = "Left"
	Bottom    ViewName = "Bottom"
	Dimetric  ViewName = "Dimetric"
	Trimetric ViewName = "Trimetric"
)

// ViewNames lists the named views, the default first.
var ViewNames = []ViewName{Isometric, Front, Top, Right, Back, Left, Bottom, Dimetric, Trimetric}

// ParseViewName finds a view by its name, ignoring case.
func ParseViewName(s string) (ViewName, bool) {
	for _, v := range ViewNames {
		if strings.EqualFold(string(v), strings.TrimSpace(s)) {
			return v, true
		}
	}
	return "", false
}

// orbit is the camera at an azimuth from the front (towards the left, -X) and
// an elevation, both in degrees.
func orbit(azDeg, elDeg float64) camera {
	az, el := azDeg*math.Pi/180, elDeg*math.Pi/180
	c := [3]float64{-math.Sin(az) * math.Cos(el), -math.Cos(az) * math.Cos(el), math.Sin(el)}
	look := [3]float64{-c[0], -c[1], -c[2]}
	r := norm(cross(look, [3]float64{0, 0, 1}))
	u := cross(r, look)
	return camera{r: r, u: u, c: c}
}

func cameraFor(v ViewName) (camera, error) {
	z := [3]float64{0, 0, 1}
	switch v {
	case "", Isometric:
		return orbit(45, 35.264389682754654), nil
	case Dimetric:
		return orbit(45, 20.7), nil
	case Trimetric:
		return orbit(60, 30), nil
	case Front:
		return camera{r: [3]float64{1, 0, 0}, u: z, c: [3]float64{0, -1, 0}}, nil
	case Back:
		return camera{r: [3]float64{-1, 0, 0}, u: z, c: [3]float64{0, 1, 0}}, nil
	case Left:
		return camera{r: [3]float64{0, -1, 0}, u: z, c: [3]float64{-1, 0, 0}}, nil
	case Right:
		return camera{r: [3]float64{0, 1, 0}, u: z, c: [3]float64{1, 0, 0}}, nil
	case Top:
		return camera{r: [3]float64{1, 0, 0}, u: [3]float64{0, 1, 0}, c: z}, nil
	case Bottom:
		return camera{r: [3]float64{1, 0, 0}, u: [3]float64{0, -1, 0}, c: [3]float64{0, 0, -1}}, nil
	}
	return camera{}, fmt.Errorf("render: unknown view %q", v)
}

// ViewOptions say what RenderView draws.
type ViewOptions struct {
	// View is the camera; empty means Isometric.
	View ViewName
	// Focus lists the indexes into Scene.Objects the picture is framed on
	// (their parts and ranges included when drawn); empty frames the whole bed.
	Focus []int
	// ShowParts draws the parts of the objects, ShowLabels their Label,
	// ShowRanges their height ranges as tinted bands.
	ShowParts, ShowLabels, ShowRanges bool
	// Width and Height are the image size in pixels; both zero gives a longest
	// edge of 1024 at the aspect of the framing; one given keeps that aspect.
	Width, Height int
	// LongEdge is the longest edge when Width and Height are both zero; 0 means
	// 1024.
	LongEdge int
}

// ViewStats says what RenderView drew.
type ViewStats struct {
	Objects, Parts, Ranges int
	Width, Height          int
}

// The colours of the parts, as a fill seen in front of the model, the same
// fill seen through it (faint), and the solid outline.
type volumeColours struct{ fill, through, outline [4]uint8 }

var partColours = map[PartKind]volumeColours{
	PartModifier: {[4]uint8{255, 200, 0, 120}, [4]uint8{255, 200, 0, 45}, [4]uint8{214, 160, 0, 255}},
	PartNegative: {[4]uint8{230, 60, 50, 120}, [4]uint8{230, 60, 50, 45}, [4]uint8{190, 30, 30, 255}},
	PartEnforcer: {[4]uint8{60, 190, 90, 120}, [4]uint8{60, 190, 90, 45}, [4]uint8{30, 140, 60, 255}},
	PartBlocker:  {[4]uint8{100, 120, 150, 120}, [4]uint8{100, 120, 150, 45}, [4]uint8{70, 85, 115, 255}},
}

// rangeColours cycle over the bands of height ranges.
var rangeColours = []volumeColours{
	{[4]uint8{255, 140, 0, 100}, [4]uint8{255, 140, 0, 40}, [4]uint8{220, 110, 0, 255}},
	{[4]uint8{0, 170, 170, 100}, [4]uint8{0, 170, 170, 40}, [4]uint8{0, 130, 130, 255}},
	{[4]uint8{150, 90, 200, 100}, [4]uint8{150, 90, 200, 40}, [4]uint8{110, 60, 160, 255}},
}

var (
	axisX      = color.NRGBA{R: 220, G: 50, B: 50, A: 255}
	axisY      = color.NRGBA{R: 40, G: 170, B: 70, A: 255}
	labelText  = color.NRGBA{R: 25, G: 30, B: 45, A: 255}
	labelPanel = color.NRGBA{R: 255, G: 255, B: 255, A: 235}
)

// axisLength is the length of the axis arrows in mm.
const axisLength = 40.0

// viewMargin is the space kept round the framed set, as a fraction of the
// image size.
const viewMargin = 0.06

// RenderView draws a plate from a named camera: the bed with its outline, grid
// and wipe tower, the X (red) and Y (green) arrows at the plate origin, the
// objects lit in their filament colours (red tinted outside the printable
// area), then the parts as translucent volumes with outlines, the height ranges
// as tinted bands, and the labels. The picture is framed on the focus objects,
// or on the bed. Parts are blended after the opaque pass and tested against
// its depth: in front of a surface they are drawn at full strength, behind it
// only as a faint tint, and their outline is always drawn, so a modifier inside
// a model can still be seen.
func RenderView(s *Scene, o ViewOptions) ([]byte, ViewStats, error) {
	var st ViewStats
	cam, err := cameraFor(o.View)
	if err != nil {
		return nil, st, err
	}
	if o.Width < 0 || o.Height < 0 || o.Width > MaxSize || o.Height > MaxSize {
		return nil, st, fmt.Errorf("render: size %dx%d is out of range (1 to %d)", o.Width, o.Height, MaxSize)
	}
	ps := place(s.Objects)

	type volume struct {
		owner int // index of the object the volume belongs to
		world [][3]float32
		m     *mesh.Mesh
		col   volumeColours
	}
	var volumes []volume
	for i, p := range ps {
		if p.obj.Mesh == nil {
			continue
		}
		if o.ShowParts {
			for _, part := range p.obj.Parts {
				if part.Mesh == nil || len(part.Mesh.Vertices) == 0 {
					continue
				}
				col, ok := partColours[part.Kind]
				if !ok {
					col = partColours[PartModifier]
				}
				w := make([][3]float32, len(part.Mesh.Vertices))
				for j, v := range part.Mesh.Vertices {
					w[j] = part.Transform.ApplyF32(v)
				}
				volumes = append(volumes, volume{i, w, part.Mesh, col})
				st.Parts++
			}
		}
		if o.ShowRanges && len(p.obj.Ranges) > 0 {
			if b, ok := p.obj.bounds(); ok {
				const pad = 0.4
				for k, r := range p.obj.Ranges {
					from, to := math.Max(r.From, 0), r.To
					if to <= from {
						continue
					}
					box := mesh.Box(float64(b.Max[0]-b.Min[0])+2*pad, float64(b.Max[1]-b.Min[1])+2*pad, to-from)
					cx, cy := float64(b.Min[0]+b.Max[0])/2, float64(b.Min[1]+b.Max[1])/2
					w := box.Transformed(mesh.Translate(cx, cy, (from+to)/2))
					volumes = append(volumes, volume{i, w.Vertices, w, rangeColours[k%len(rangeColours)]})
					st.Ranges++
				}
			}
		}
	}
	st.Objects = len(s.Objects)

	// Frame the focus set, or the bed with everything on it.
	focus := map[int]bool{}
	for _, i := range o.Focus {
		if i >= 0 && i < len(ps) {
			focus[i] = true
		}
	}
	minX, minY, maxX, maxY := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	grow := func(p [3]float64) {
		sx, sy, _ := cam.project(p)
		minX, maxX, minY, maxY = math.Min(minX, sx), math.Max(maxX, sx), math.Min(minY, sy), math.Max(maxY, sy)
	}
	growWorld := func(w [][3]float32) {
		for _, v := range w {
			grow([3]float64{float64(v[0]), float64(v[1]), float64(v[2])})
		}
	}
	if len(focus) == 0 {
		bed := s.bed()
		for _, pt := range [4][2]float64{{bed.X0, bed.Y0}, {bed.X1, bed.Y0}, {bed.X1, bed.Y1}, {bed.X0, bed.Y1}} {
			grow([3]float64{pt[0], pt[1], 0})
		}
		for _, p := range ps {
			growWorld(p.world)
		}
		for _, v := range volumes {
			growWorld(v.world)
		}
	} else {
		for i := range ps {
			if focus[i] {
				growWorld(ps[i].world)
			}
		}
		// Their parts and height range bands, when drawn.
		for _, v := range volumes {
			if focus[v.owner] {
				growWorld(v.world)
			}
		}
	}
	if math.IsInf(minX, 1) {
		return nil, st, fmt.Errorf("render: nothing to frame")
	}
	// Keep the framing box from being extremely thin or wide.
	dx, dy := maxX-minX, maxY-minY
	if dx < 1 {
		dx = 1
	}
	if dy < 1 {
		dy = 1
	}
	if dy < dx/4 {
		dy = dx / 4
	} else if dx < dy/4 {
		dx = dy / 4
	}
	cx, cy := (minX+maxX)/2, (minY+maxY)/2
	minX, maxX, minY, maxY = cx-dx/2, cx+dx/2, cy-dy/2, cy+dy/2

	w, h := o.Width, o.Height
	aspect := dx / dy
	switch {
	case w == 0 && h == 0:
		long := o.LongEdge
		if long <= 0 {
			long = 1024
		}
		if aspect >= 1 {
			w, h = long, int(math.Round(float64(long)/aspect))
		} else {
			w, h = int(math.Round(float64(long)*aspect)), long
		}
	case w == 0:
		w = int(math.Round(float64(h) * aspect))
	case h == 0:
		h = int(math.Round(float64(w) / aspect))
	}
	w, h = min(max(w, 64), MaxSize), min(max(h, 64), MaxSize)
	st.Width, st.Height = w, h

	ss := ssFactor(max(w, h))
	c := newCanvas(w*ss, h*ss, previewBackground)
	f := newFit(minX, minY, maxX, maxY, c.w, c.h, viewMargin)

	c.drawBed(cam, f, s, ss)
	bed := s.bed()
	type tip struct {
		text string
		x, y float32
		col  color.NRGBA
	}
	var tips []tip
	for _, ax := range []struct {
		dir  [3]float64
		col  color.NRGBA
		text string
	}{{[3]float64{1, 0, 0}, axisX, "X"}, {[3]float64{0, 1, 0}, axisY, "Y"}} {
		x0, y0 := bedPoint(cam, f, bed.X0, bed.Y0)
		sx, sy, _ := cam.project([3]float64{bed.X0 + ax.dir[0]*axisLength, bed.Y0 + ax.dir[1]*axisLength, 0})
		x1, y1 := f.px(sx, sy)
		length := math.Hypot(float64(x1-x0), float64(y1-y0))
		if length < float64(6*ss) {
			continue // the axis points at or away from the camera
		}
		c.line(x0, y0, x1, y1, float32(ss)*2.4, rgba(ax.col))
		// Arrow head: a triangle pointing along the axis.
		ux, uy := float32(float64(x1-x0)/length), float32(float64(y1-y0)/length)
		hl, hw := float32(ss)*9, float32(ss)*4.5
		c.tri(x1+ux*hl, y1+uy*hl, 0, x1-uy*hw, y1+ux*hw, 0, x1+uy*hw, y1-ux*hw, 0, rgba(ax.col), false)
		tips = append(tips, tip{ax.text, x1 + ux*hl*1.6, y1 + uy*hl*1.6, ax.col})
	}

	c.drawObjects(cam, f, ps, shadeLit, func(ob *Object) color.NRGBA {
		col := objectColour(ob)
		if s.Outside(*ob) {
			return tint(col)
		}
		return col
	})
	for _, v := range volumes {
		c.drawVolume(cam, f, v.world, v.m, v.col, ss)
	}

	img := c.downsample(ss)
	scale := max(1, (max(w, h)+256)/512)
	for _, t := range tips {
		x, y := int(t.x)/ss-glyphWidth*scale/2, int(t.y)/ss-glyphHeight*scale/2
		drawText(img, x, y, scale, t.text, t.col)
	}
	if o.ShowLabels {
		for _, p := range ps {
			if p.obj.Label == "" || p.obj.Mesh == nil {
				continue
			}
			b, ok := p.obj.bounds()
			if !ok {
				continue
			}
			sx, sy, _ := cam.project([3]float64{float64(b.Min[0]+b.Max[0]) / 2, float64(b.Min[1]+b.Max[1]) / 2, float64(b.Max[2])})
			px, py := f.px(sx, sy)
			if px < 0 || py < 0 || int(px) >= c.w || int(py) >= c.h {
				continue // out of the frame (not in the focus): no label
			}
			drawLabel(img, int(px)/ss, int(py)/ss, scale, p.obj.Label)
		}
	}
	data, err := encodePNG(img)
	return data, st, err
}

// drawLabel draws text centred above the point x, y on a light panel, kept
// inside the image.
func drawLabel(img *image.NRGBA, x, y, scale int, text string) {
	pad := scale
	tw, th := textWidth(text, scale), glyphHeight*scale
	b := img.Bounds()
	left := min(max(x-tw/2-pad, b.Min.X), b.Max.X-tw-2*pad)
	top := min(max(y-th-3*pad, b.Min.Y), b.Max.Y-th-2*pad)
	fillRect(img, left, top, tw+2*pad, th+2*pad, labelPanel)
	drawText(img, left+pad, top+pad, scale, text, labelText)
}

// drawVolume draws a translucent volume: each triangle blended over what is
// there, strongly where it is in front of the opaque surface and faintly where
// the surface hides it, then its outline (feature and silhouette edges) solid.
func (c *canvas) drawVolume(cam camera, f fit, world [][3]float32, m *mesh.Mesh, col volumeColours, ss int) {
	sx := make([]float32, len(world))
	sy := make([]float32, len(world))
	sz := make([]float32, len(world))
	for i, v := range world {
		x, y, z := cam.project([3]float64{float64(v[0]), float64(v[1]), float64(v[2])})
		sx[i], sy[i] = f.px(x, y)
		sz[i] = float32(z)
	}
	for _, t := range m.Triangles {
		a, b, d := t[0], t[1], t[2]
		c.triThrough(sx[a], sy[a], sz[a], sx[b], sy[b], sz[b], sx[d], sy[d], sz[d], col.fill, col.through)
	}
	for _, e := range outlineEdges(world, m.Triangles, cam) {
		c.line(sx[e[0]], sy[e[0]], sx[e[1]], sy[e[1]], float32(ss)*1.6, col.outline)
	}
}

// triThrough blends a triangle over the canvas: front where it is nearer than
// the depth buffer, through where the buffer is nearer. It never writes depth.
func (c *canvas) triThrough(x0, y0, z0, x1, y1, z1, x2, y2, z2 float32, front, through [4]uint8) {
	area := (x1-x0)*(y2-y0) - (x2-x0)*(y1-y0)
	if area == 0 || area != area {
		return
	}
	minX := int(math.Floor(float64(min(x0, x1, x2) - 0.5)))
	maxX := int(math.Ceil(float64(max(x0, x1, x2) - 0.5)))
	minY := int(math.Floor(float64(min(y0, y1, y2) - 0.5)))
	maxY := int(math.Ceil(float64(max(y0, y1, y2) - 0.5)))
	if maxX < 0 || maxY < 0 || minX >= c.w || minY >= c.h {
		return
	}
	minX, minY = max(minX, 0), max(minY, 0)
	maxX, maxY = min(maxX, c.w-1), min(maxY, c.h-1)
	inv := 1 / area
	dw0x, dw0y := (y1-y2)*inv, (x2-x1)*inv
	dw1x, dw1y := (y2-y0)*inv, (x0-x2)*inv
	px, py := float32(minX)+0.5, float32(minY)+0.5
	rowW0 := ((x1-px)*(y2-py) - (x2-px)*(y1-py)) * inv
	rowW1 := ((x2-px)*(y0-py) - (x0-px)*(y2-py)) * inv
	const eps = -1e-6
	// A part face counts as in front of the opaque surface only when it is more
	// than depthBias (mm) nearer the camera: a face coplanar with a surface
	// (a cylinder ending at the top of a box) takes the hidden tint everywhere.
	const depthBias = 0.05
	for y := minY; y <= maxY; y++ {
		w0, w1 := rowW0, rowW1
		idx := y*c.w + minX
		for x := minX; x <= maxX; x++ {
			w2 := 1 - w0 - w1
			if w0 >= eps && w1 >= eps && w2 >= eps {
				z := w0*z0 + w1*z1 + w2*z2
				if z > c.depth[idx]+depthBias {
					c.blend(idx*4, front)
				} else {
					c.blend(idx*4, through)
				}
			}
			w0 += dw0x
			w1 += dw1x
			idx++
		}
		rowW0 += dw0y
		rowW1 += dw1y
	}
}

// outlineEdges returns the edges to outline: boundary edges, edges where the
// surface bends by more than 30 degrees, and silhouette edges (one adjacent
// face towards the camera, one away).
func outlineEdges(world [][3]float32, tris [][3]uint32, cam camera) [][2]uint32 {
	type adj struct {
		n     [2][3]float64
		count int
	}
	edges := map[[2]uint32]*adj{}
	var order [][2]uint32
	for _, t := range tris {
		a, b, d := world[t[0]], world[t[1]], world[t[2]]
		e1 := [3]float64{float64(b[0] - a[0]), float64(b[1] - a[1]), float64(b[2] - a[2])}
		e2 := [3]float64{float64(d[0] - a[0]), float64(d[1] - a[1]), float64(d[2] - a[2])}
		n := norm(cross(e1, e2))
		for k := 0; k < 3; k++ {
			p, q := t[k], t[(k+1)%3]
			if p > q {
				p, q = q, p
			}
			key := [2]uint32{p, q}
			e := edges[key]
			if e == nil {
				e = &adj{}
				edges[key] = e
				order = append(order, key)
			}
			if e.count < 2 {
				e.n[e.count] = n
			}
			e.count++
		}
	}
	const cos30 = 0.8660254
	var out [][2]uint32
	for _, key := range order {
		e := edges[key]
		switch {
		case e.count != 2:
			out = append(out, key)
		case dot(e.n[0], e.n[1]) < cos30:
			out = append(out, key)
		case (dot(e.n[0], cam.c) > 0) != (dot(e.n[1], cam.c) > 0):
			out = append(out, key)
		}
	}
	return out
}
