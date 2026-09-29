package render

import (
	"fmt"
	"hash/fnv"
	"image"
	"image/color"
	"math"
	"strings"

	"github.com/sairaph/creality-slicer-mcp/internal/gcodeinfo"
)

// ColorBy chooses what the colour of an extrusion means in GCodeLayer.
type ColorBy string

const (
	// ByFeature colours by the ";TYPE:" of the move (outer wall, infill, ...).
	ByFeature ColorBy = "feature"
	// ByFilament colours by the tool (T0, T1, ...).
	ByFilament ColorBy = "filament"
	// BySpeed colours by the feed rate, blue (slow) to red (fast).
	BySpeed ColorBy = "speed"
)

// LayerOptions are the optional inputs of GCodeLayer.
type LayerOptions struct {
	// Bed is the bed outline. With FitBed the picture frames the whole bed and
	// draws its outline; without it the picture frames the layer's moves and
	// the bed is not drawn.
	Bed    Rect
	FitBed bool
	// ToolColours gives the colour of tool i for ByFilament; a tool without
	// one takes a colour from a fixed palette.
	ToolColours []color.NRGBA
	// Travels also draws the non-extruding moves, thin and grey.
	Travels bool
	// ExtrusionWidth is the drawn width of an extrusion in mm; 0 means 0.45.
	ExtrusionWidth float64
	// Title is a short line drawn at the bottom right, such as "LAYER 12 Z 2.40".
	Title string
}

// featurePalette is the fixed colour of each ";TYPE:" value the slicer writes.
var featurePalette = map[string]color.NRGBA{
	"outer wall":               {255, 140, 0, 255},
	"inner wall":               {255, 215, 60, 255},
	"overhang wall":            {30, 120, 230, 255},
	"sparse infill":            {200, 40, 60, 255},
	"internal solid infill":    {150, 60, 190, 255},
	"top surface":              {220, 40, 40, 255},
	"bottom surface":           {60, 150, 150, 255},
	"bridge":                   {90, 130, 210, 255},
	"internal bridge":          {130, 170, 230, 255},
	"gap infill":               {255, 255, 255, 255},
	"support":                  {60, 180, 90, 255},
	"support interface":        {40, 130, 70, 255},
	"skirt":                    {0, 135, 110, 255},
	"brim":                     {0, 170, 135, 255},
	"prime tower":              {180, 110, 80, 255},
	"wipe tower":               {180, 110, 80, 255},
	"ironing":                  {240, 140, 190, 255},
	"custom":                   {140, 140, 150, 255},
	"floating vertical shell":  {200, 100, 100, 255},
	"internal bridge (sparse)": {130, 170, 230, 255},
}

// toolPalette is the fallback colour of tool i.
var toolPalette = []color.NRGBA{
	{220, 60, 50, 255}, {50, 110, 220, 255}, {50, 170, 80, 255}, {240, 180, 30, 255},
	{150, 70, 190, 255}, {30, 170, 190, 255}, {240, 120, 30, 255}, {110, 110, 120, 255},
}

func featureColour(name string) color.NRGBA {
	key := strings.ToLower(strings.TrimSpace(name))
	if c, ok := featurePalette[key]; ok {
		return c
	}
	// A stable colour derived from the name, so an unknown feature is always
	// drawn the same.
	h := fnv.New32a()
	h.Write([]byte(key))
	hue := float64(h.Sum32()%360) / 360
	return hsv(hue, 0.65, 0.85)
}

func hsv(h, s, v float64) color.NRGBA {
	i := math.Floor(h * 6)
	f := h*6 - i
	p, q, t := v*(1-s), v*(1-f*s), v*(1-(1-f)*s)
	var r, g, b float64
	switch int(i) % 6 {
	case 0:
		r, g, b = v, t, p
	case 1:
		r, g, b = q, v, p
	case 2:
		r, g, b = p, v, t
	case 3:
		r, g, b = p, q, v
	case 4:
		r, g, b = t, p, v
	default:
		r, g, b = v, p, q
	}
	return color.NRGBA{uint8(r*255 + 0.5), uint8(g*255 + 0.5), uint8(b*255 + 0.5), 255}
}

func toolColour(opts LayerOptions, tool int) color.NRGBA {
	if tool >= 0 && tool < len(opts.ToolColours) && opts.ToolColours[tool].A != 0 {
		c := opts.ToolColours[tool]
		c.A = 255
		return c
	}
	return toolPalette[((tool%len(toolPalette))+len(toolPalette))%len(toolPalette)]
}

// speedColour maps t in [0,1] from blue through green and yellow to red.
func speedColour(t float64) color.NRGBA {
	stops := []color.NRGBA{{50, 80, 220, 255}, {40, 190, 200, 255}, {60, 190, 90, 255}, {240, 210, 50, 255}, {220, 50, 40, 255}}
	t = math.Max(0, math.Min(1, t)) * float64(len(stops)-1)
	i := int(t)
	if i >= len(stops)-1 {
		return stops[len(stops)-1]
	}
	f := t - float64(i)
	a, b := stops[i], stops[i+1]
	mix := func(x, y uint8) uint8 { return uint8(float64(x)*(1-f) + float64(y)*f + 0.5) }
	return color.NRGBA{mix(a.R, b.R), mix(a.G, b.G), mix(a.B, b.B), 255}
}

var (
	layerBackground = color.NRGBA{R: 250, G: 250, B: 251, A: 255}
	layerText       = color.NRGBA{R: 45, G: 48, B: 58, A: 255}
	travelColour    = [4]uint8{170, 175, 185, 255}
)

// segment is a straight piece of a move, in mm.
type segment struct{ x0, y0, x1, y1 float64 }

// segments returns the straight pieces a move is drawn as: itself, or for an
// arc a run of chords.
func segments(m gcodeinfo.Move) []segment {
	if !m.Arc {
		return []segment{{m.X0, m.Y0, m.X1, m.Y1}}
	}
	cx, cy := m.X0+m.I, m.Y0+m.J
	r := math.Hypot(m.I, m.J)
	if r == 0 {
		return []segment{{m.X0, m.Y0, m.X1, m.Y1}}
	}
	a0 := math.Atan2(m.Y0-cy, m.X0-cx)
	a1 := math.Atan2(m.Y1-cy, m.X1-cx)
	if m.Clockwise {
		for a1 > a0 {
			a1 -= 2 * math.Pi
		}
	} else {
		for a1 < a0 {
			a1 += 2 * math.Pi
		}
	}
	if a1 == a0 {
		// Same start and end: a full circle.
		if m.Clockwise {
			a1 -= 2 * math.Pi
		} else {
			a1 += 2 * math.Pi
		}
	}
	n := int(math.Ceil(math.Abs(a1-a0) / (5 * math.Pi / 180)))
	n = max(n, 2)
	segs := make([]segment, 0, n)
	px, py := m.X0, m.Y0
	for i := 1; i <= n; i++ {
		a := a0 + (a1-a0)*float64(i)/float64(n)
		x, y := cx+r*math.Cos(a), cy+r*math.Sin(a)
		if i == n {
			x, y = m.X1, m.Y1
		}
		segs = append(segs, segment{px, py, x, y})
		px, py = x, y
	}
	return segs
}

type legendEntry struct {
	label string
	col   color.NRGBA
}

// GCodeLayer draws one layer's moves from above as a square PNG of size
// pixels: extrusions coloured by feature, filament (tool) or speed; travels
// hidden unless opts.Travels; a legend at the top left and a scale bar at the
// bottom left. The background is opaque. moves are what gcodeinfo.LayerMoves
// returns for the layer.
func GCodeLayer(moves []gcodeinfo.Move, by ColorBy, size int, opts LayerOptions) ([]byte, error) {
	if size < 64 || size > MaxSize {
		return nil, fmt.Errorf("render: size %d is out of range (64 to %d)", size, MaxSize)
	}
	switch by {
	case ByFeature, ByFilament, BySpeed:
	default:
		return nil, fmt.Errorf("render: unknown colour mode %q (want feature, filament or speed)", by)
	}
	ew := opts.ExtrusionWidth
	if ew <= 0 {
		ew = 0.45
	}

	// Extents of what is drawn.
	minX, minY, maxX, maxY := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	var speedLo, speedHi = math.Inf(1), math.Inf(-1)
	anyExtrusion := false
	for _, m := range moves {
		if m.Extruding {
			anyExtrusion = true
		}
	}
	for _, m := range moves {
		if anyExtrusion && !m.Extruding && !opts.Travels {
			continue
		}
		for _, s := range segments(m) {
			minX, maxX = math.Min(minX, math.Min(s.x0, s.x1)), math.Max(maxX, math.Max(s.x0, s.x1))
			minY, maxY = math.Min(minY, math.Min(s.y0, s.y1)), math.Max(maxY, math.Max(s.y0, s.y1))
		}
		if m.Extruding && m.Speed > 0 {
			sp := m.Speed / 60
			speedLo, speedHi = math.Min(speedLo, sp), math.Max(speedHi, sp)
		}
	}
	if opts.FitBed && !opts.Bed.Empty() || math.IsInf(minX, 1) {
		if !opts.Bed.Empty() {
			minX, minY, maxX, maxY = opts.Bed.X0, opts.Bed.Y0, opts.Bed.X1, opts.Bed.Y1
		} else {
			minX, minY, maxX, maxY = 0, 0, 1, 1
		}
	}

	ss := ssFactor(size)
	c := newCanvas(size*ss, size*ss, layerBackground)
	f := newFit(minX, minY, maxX, maxY, c.w, c.h, 0.08)
	// The same square scale on both axes: newFit keeps the aspect ratio.

	if opts.FitBed && !opts.Bed.Empty() {
		b := opts.Bed
		corners := [5][2]float64{{b.X0, b.Y0}, {b.X1, b.Y0}, {b.X1, b.Y1}, {b.X0, b.Y1}, {b.X0, b.Y0}}
		for i := 0; i < 4; i++ {
			x0, y0 := f.px(corners[i][0], corners[i][1])
			x1, y1 := f.px(corners[i+1][0], corners[i+1][1])
			c.line(x0, y0, x1, y1, float32(ss)*1.5, rgba(bedOutline))
		}
	}

	width := float32(math.Max(ew*f.scale, float64(ss)))
	draw := func(s segment, w float32, col [4]uint8) {
		x0, y0 := f.px(s.x0, s.y0)
		x1, y1 := f.px(s.x1, s.y1)
		c.line(x0, y0, x1, y1, w, col)
	}
	if opts.Travels {
		for _, m := range moves {
			if !m.Extruding {
				for _, s := range segments(m) {
					draw(s, float32(ss)*0.8, travelColour)
				}
			}
		}
	}

	var legend []legendEntry
	seen := map[string]bool{}
	note := func(key, label string, col color.NRGBA) {
		if !seen[key] {
			seen[key] = true
			legend = append(legend, legendEntry{label, col})
		}
	}
	for _, m := range moves {
		if !m.Extruding {
			continue
		}
		var col color.NRGBA
		switch by {
		case ByFeature:
			name := strings.TrimSpace(m.Feature)
			if name == "" {
				name = "unknown"
			}
			col = featureColour(name)
			note(strings.ToLower(name), name, col)
		case ByFilament:
			col = toolColour(opts, m.Tool)
			note(fmt.Sprint(m.Tool), fmt.Sprintf("T%d", m.Tool), col)
		case BySpeed:
			t := 0.5
			if speedHi > speedLo {
				t = (m.Speed/60 - speedLo) / (speedHi - speedLo)
			}
			col = speedColour(t)
		}
		for _, s := range segments(m) {
			draw(s, width, rgba(col))
		}
	}

	img := c.downsample(ss)
	textScale := max(1, size/320)
	pad := 6 * textScale

	// Legend, top left.
	y := pad
	panelW := 0
	switch by {
	case BySpeed:
		if !math.IsInf(speedLo, 1) {
			barW, barH := 60*textScale, 6*textScale
			lo, hi := fmt.Sprintf("%.0f MM/S", speedLo), fmt.Sprintf("%.0f MM/S", speedHi)
			panelW = max(barW, textWidth(hi, textScale)+textWidth(lo, textScale)+3*pad)
			fillRect(img, pad, y, panelW+2*pad, barH+glyphHeight*textScale+3*pad, layerBackground)
			for i := 0; i < barW; i++ {
				fillRect(img, pad+pad+i, y+pad, 1, barH, speedColour(float64(i)/float64(barW-1)))
			}
			ty := y + pad + barH + pad/2
			drawText(img, 2*pad, ty, textScale, lo, layerText)
			drawText(img, 2*pad+panelW-textWidth(hi, textScale), ty, textScale, hi, layerText)
		}
	default:
		rowH := (glyphHeight + 3) * textScale
		limit := size - 3*pad - (glyphHeight+8)*textScale // keep the scale bar's room
		maxRows := max((limit-y)/rowH, 0)
		show := legend
		more := false
		if len(show) > maxRows {
			show, more = show[:max(maxRows-1, 0)], true
		}
		for _, e := range show {
			panelW = max(panelW, textWidth(e.label, textScale)+glyphHeight*textScale+pad)
		}
		rows := len(show)
		if more {
			rows++
			panelW = max(panelW, textWidth("...", textScale))
		}
		if rows > 0 {
			fillRect(img, pad/2, y-pad/2, panelW+2*pad, rows*rowH+pad, layerBackground)
		}
		for _, e := range show {
			fillRect(img, pad, y, glyphHeight*textScale, glyphHeight*textScale, e.col)
			drawText(img, pad+(glyphHeight+2)*textScale, y, textScale, e.label, layerText)
			y += rowH
		}
		if more {
			drawText(img, pad, y, textScale, "...", layerText)
		}
	}

	// Scale bar, bottom left.
	pxPerMM := f.scale / float64(ss)
	barMM := 0.0
	for _, l := range []float64{1, 2, 5, 10, 20, 50, 100, 200} {
		if l*pxPerMM <= 0.25*float64(size) {
			barMM = l
		}
	}
	if barMM > 0 {
		bw := int(math.Round(barMM * pxPerMM))
		barY := size - pad - 2*textScale
		fillRect(img, pad, barY, bw, 2*textScale, layerText)
		fillRect(img, pad, barY-2*textScale, textScale, 4*textScale, layerText)
		fillRect(img, pad+bw-textScale, barY-2*textScale, textScale, 4*textScale, layerText)
		drawText(img, pad, barY-glyphHeight*textScale-3*textScale, textScale, fmt.Sprintf("%g MM", barMM), layerText)
	}
	if opts.Title != "" {
		drawText(img, size-pad-textWidth(opts.Title, textScale), size-pad-glyphHeight*textScale, textScale, opts.Title, layerText)
	}
	return encodePNG(image.Image(img))
}
