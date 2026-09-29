package render

import (
	"fmt"
	"image"
	"math"

	"github.com/sairaph/creality-slicer-mcp/internal/gcodeinfo"
)

// maxIsoMoves is the most extrusions GCodeIso draws; a print with more is drawn
// with every few layers left out, so the picture stays quick.
const maxIsoMoves = 2500000

// GCodeIso draws the extrusions of a whole print, layer upon layer, from the
// Isometric view: what the slicer will print, in the colour of the filament of
// each tool (opts.ToolColours), on the bed. moves are the moves of every layer
// of the plate in any order (each carries its Z). The picture is width by
// height pixels, its margin strip holds the tools and the title.
func GCodeIso(moves []gcodeinfo.Move, width, height int, opts LayerOptions) ([]byte, error) {
	if width < 64 || height < 64 || width > MaxSize || height > MaxSize {
		return nil, fmt.Errorf("render: size %dx%d is out of range (64 to %d)", width, height, MaxSize)
	}
	ew := opts.ExtrusionWidth
	if ew <= 0 {
		ew = 0.45
	}
	cam := orbit(45, 35.264389682754654)

	// Which layers to draw, the extents, the tools and the top.
	extruding := 0
	for _, m := range moves {
		if m.Extruding {
			extruding++
		}
	}
	step := 1 + extruding/maxIsoMoves
	drawn := func(m gcodeinfo.Move) bool {
		return m.Extruding && int(math.Round(m.Z/0.05))%step == 0
	}
	minX, minY, maxX, maxY := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	zTop := 0.0
	var st strip
	seen := map[int]bool{}
	for _, m := range moves {
		if !drawn(m) {
			continue
		}
		zTop = math.Max(zTop, m.Z)
		for _, s := range segments(m) {
			for _, p := range [2][2]float64{{s.x0, s.y0}, {s.x1, s.y1}} {
				sx, sy, _ := cam.project([3]float64{p[0], p[1], m.Z})
				minX, maxX = math.Min(minX, sx), math.Max(maxX, sx)
				minY, maxY = math.Min(minY, sy), math.Max(maxY, sy)
			}
		}
		if !seen[m.Tool] {
			seen[m.Tool] = true
			st.legend = append(st.legend, legendEntry{fmt.Sprintf("T%d", m.Tool), toolColour(opts, m.Tool)})
		}
	}
	bed := opts.Bed
	if bed.Empty() {
		bed = DefaultBed
	}
	if math.IsInf(minX, 1) {
		// Nothing to draw: the bed alone.
		for _, pt := range [4][2]float64{{bed.X0, bed.Y0}, {bed.X1, bed.Y0}, {bed.X1, bed.Y1}, {bed.X0, bed.Y1}} {
			sx, sy, _ := cam.project([3]float64{pt[0], pt[1], 0})
			minX, maxX, minY, maxY = math.Min(minX, sx), math.Max(maxX, sx), math.Min(minY, sy), math.Max(maxY, sy)
		}
	}
	// The picture is framed on the print, with the bed around it.
	if maxX-minX < 1 {
		maxX = minX + 1
	}
	if maxY-minY < 1 {
		maxY = minY + 1
	}

	textScale := max(1, max(width, height)/512)
	st.title = opts.Title
	layout := layoutStrip(width, textScale, st)
	contentH := max(height-layout.height, height/2)
	ss := ssFactor(max(width, height))
	c := newCanvas(width*ss, contentH*ss, viewBackground)
	f := newFit(minX, minY, maxX, maxY, c.w, c.h, 0.07)

	c.drawBed(cam, f, &Scene{Bed: bed}, ss, viewBed)
	lineW := float32(math.Max(ew*f.scale, float64(ss)*1.1))
	for _, m := range moves {
		if !drawn(m) {
			continue
		}
		base := toolColour(opts, m.Tool)
		// Higher layers are a little lighter, which reads as height.
		k := 0.7
		if zTop > 0 {
			k += 0.3 * m.Z / zTop
		}
		lum := (float64(base.R) + float64(base.G) + float64(base.B)) / 765
		lift := 40 * (1 - lum)
		ch := func(v uint8) uint8 { return uint8(math.Min(255, float64(v)*k+lift*k+0.5)) }
		col := [4]uint8{ch(base.R), ch(base.G), ch(base.B), 255}
		for _, s := range segments(m) {
			x0, y0, z0 := cam.project([3]float64{s.x0, s.y0, m.Z})
			x1, y1, z1 := cam.project([3]float64{s.x1, s.y1, m.Z})
			px0, py0 := f.px(x0, y0)
			px1, py1 := f.px(x1, y1)
			c.lineDepth(px0, py0, float32(z0), px1, py1, float32(z1), lineW, col)
		}
	}
	content := c.downsample(ss)
	return encodePNG(image.Image(composeStrip(content, st, layout)))
}
