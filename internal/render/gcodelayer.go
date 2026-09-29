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
	layerBackground = color.NRGBA{R: 126, G: 130, B: 138, A: 255}
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

// stripBackground is the margin strip under a picture: the legend, the scale
// and the title sit there, so they never cover the drawing.
var stripBackground = color.NRGBA{R: 236, G: 238, B: 242, A: 255}

// speedKey is the speed scale of a picture coloured by speed.
type speedKey struct {
	lo, hi float64 // mm/s
}

// strip is what a picture's margin strip shows.
type strip struct {
	legend []legendEntry
	speed  *speedKey
	// scaleMM and scalePx draw a scale bar of scaleMM millimetres that is
	// scalePx pixels long; 0 draws none.
	scaleMM float64
	scalePx int
	title   string
}

// stripLayout is the rows of a strip for an image of the given width.
type stripLayout struct {
	scale, pad, rowH int
	legendRows       [][]legendEntry
	more             bool
	speedRow         bool
	height           int
}

const maxLegendRows = 4

func layoutStrip(width, textScale int, s strip) stripLayout {
	l := stripLayout{scale: textScale, pad: 6 * textScale, rowH: (glyphHeight + 3) * textScale}
	entryW := func(e legendEntry) int {
		return glyphHeight*textScale + 2*textScale + textWidth(e.label, textScale) + 3*l.pad
	}
	var row []legendEntry
	x := l.pad
	for _, e := range s.legend {
		w := entryW(e)
		if x+w > width-l.pad && len(row) > 0 {
			l.legendRows = append(l.legendRows, row)
			row, x = nil, l.pad
			if len(l.legendRows) == maxLegendRows {
				l.more = true
				break
			}
		}
		row = append(row, e)
		x += w
	}
	if len(row) > 0 && len(l.legendRows) < maxLegendRows {
		l.legendRows = append(l.legendRows, row)
	}
	rows := len(l.legendRows)
	if l.more {
		rows++ // a row that says there are more
	}
	if s.speed != nil {
		l.speedRow = true
		rows += 2 // the bar and its numbers
	}
	rows++ // scale and title
	l.height = l.pad + rows*l.rowH + l.pad/2
	return l
}

// composeStrip returns an image of content's width and height plus the strip
// under it, drawn from the layout.
func composeStrip(content *image.NRGBA, s strip, l stripLayout) *image.NRGBA {
	w, h := content.Bounds().Dx(), content.Bounds().Dy()
	img := image.NewNRGBA(image.Rect(0, 0, w, h+l.height))
	copy(img.Pix, content.Pix)
	fillRect(img, 0, h, w, l.height, stripBackground)
	y := h + l.pad/2
	ts, pad := l.scale, l.pad
	for _, row := range l.legendRows {
		x := pad
		for _, e := range row {
			swatch(img, x, y, glyphHeight*ts, max(1, ts/2), e.col)
			drawText(img, x+(glyphHeight+2)*ts, y, ts, e.label, layerText)
			x += glyphHeight*ts + 2*ts + textWidth(e.label, ts) + 3*pad
		}
		y += l.rowH
	}
	if l.more {
		drawText(img, pad, y, ts, "...", layerText)
		y += l.rowH
	}
	if s.speed != nil {
		barW := min(w-2*pad-textWidth("MM/S ", ts), 120*ts)
		x0 := pad + textWidth("MM/S ", ts)
		drawText(img, pad, y, ts, "MM/S", layerText)
		for i := 0; i < barW; i++ {
			fillRect(img, x0+i, y, 1, glyphHeight*ts, speedColour(float64(i)/float64(barW-1)))
		}
		y += l.rowH
		// Real ticks: the lowest, the middle and the highest speed drawn.
		for i, frac := range []float64{0, 0.5, 1} {
			v := s.speed.lo + (s.speed.hi-s.speed.lo)*frac
			tx := x0 + int(float64(barW-1)*frac)
			fillRect(img, tx, y-l.rowH+glyphHeight*ts, ts, ts+1, layerText)
			text := fmt.Sprintf("%.0f", v)
			lx := tx - textWidth(text, ts)/2
			if i == 0 {
				lx = tx
			} else if i == 2 {
				lx = tx - textWidth(text, ts)
			}
			drawText(img, lx, y, ts, text, layerText)
		}
		y += l.rowH
	}
	// Scale bar and title on the last row.
	if s.scaleMM > 0 && s.scalePx > 0 {
		bw := s.scalePx
		mid := y + glyphHeight*ts/2
		fillRect(img, pad, mid, bw, ts, layerText)
		fillRect(img, pad, mid-2*ts, ts, 5*ts, layerText)
		fillRect(img, pad+bw-ts, mid-2*ts, ts, 5*ts, layerText)
		drawText(img, pad+bw+2*ts, y, ts, fmt.Sprintf("%g MM", s.scaleMM), layerText)
	}
	if s.title != "" {
		drawText(img, w-pad-textWidth(s.title, ts), y, ts, s.title, layerText)
	}
	return img
}

// scaleBarFor picks a round length for a scale bar that is at most a quarter
// of the picture's width.
func scaleBarFor(pxPerMM float64, width int) (mm float64, px int) {
	for _, l := range []float64{1, 2, 5, 10, 20, 50, 100, 200} {
		if l*pxPerMM <= 0.25*float64(width) {
			mm = l
		}
	}
	return mm, int(math.Round(mm * pxPerMM))
}

// GCodeLayer draws the moves of one layer from above as a square PNG of size
// pixels: extrusions coloured by feature, filament (tool) or speed, travels
// hidden unless opts.Travels. The drawing has the picture to itself: the legend,
// the speed scale with its real lowest and highest values, the scale bar and the
// title are in a strip at the bottom, which is part of the size. moves are what
// gcodeinfo.LayerMoves returns for the layer; for a plate printed by object
// the caller passes the moves of every object at that height.
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

	// Extents of what is drawn, the speed range and the legend.
	minX, minY, maxX, maxY := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	speedLo, speedHi := math.Inf(1), math.Inf(-1)
	anyExtrusion := false
	for _, m := range moves {
		if m.Extruding {
			anyExtrusion = true
		}
	}
	var st strip
	seen := map[string]bool{}
	note := func(key, label string, col color.NRGBA) {
		if !seen[key] {
			seen[key] = true
			st.legend = append(st.legend, legendEntry{label, col})
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
		if !m.Extruding {
			continue
		}
		if m.Speed > 0 {
			sp := m.Speed / 60
			speedLo, speedHi = math.Min(speedLo, sp), math.Max(speedHi, sp)
		}
		switch by {
		case ByFeature:
			name := strings.TrimSpace(m.Feature)
			if name == "" {
				name = "unknown"
			}
			note(strings.ToLower(name), name, featureColour(name))
		case ByFilament:
			note(fmt.Sprint(m.Tool), fmt.Sprintf("T%d", m.Tool), toolColour(opts, m.Tool))
		}
	}
	uniformSpeed := false
	if by == BySpeed && !math.IsInf(speedLo, 1) {
		if speedHi-speedLo < 1 {
			uniformSpeed = true
			st.legend = append(st.legend, legendEntry{fmt.Sprintf("ALL EXTRUSIONS %.0f MM/S", speedLo), speedColour(0.5)})
		} else {
			st.speed = &speedKey{speedLo, speedHi}
		}
	}
	if opts.FitBed && !opts.Bed.Empty() || math.IsInf(minX, 1) {
		if !opts.Bed.Empty() {
			minX, minY, maxX, maxY = opts.Bed.X0, opts.Bed.Y0, opts.Bed.X1, opts.Bed.Y1
		} else {
			minX, minY, maxX, maxY = 0, 0, 1, 1
		}
	}

	textScale := max(1, size/320)
	st.title = opts.Title
	// The strip's height depends on its rows, which do not depend on the drawing.
	layout := layoutStrip(size, textScale, st)
	contentH := max(size-layout.height, size/2)

	ss := ssFactor(size)
	c := newCanvas(size*ss, contentH*ss, layerBackground)
	f := newFit(minX, minY, maxX, maxY, c.w, c.h, 0.08)

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
		case ByFilament:
			col = toolColour(opts, m.Tool)
		case BySpeed:
			t := 0.5
			if !uniformSpeed && speedHi > speedLo {
				t = (m.Speed/60 - speedLo) / (speedHi - speedLo)
			}
			col = speedColour(t)
		}
		for _, s := range segments(m) {
			draw(s, width, rgba(col))
		}
	}

	content := c.downsample(ss)
	st.scaleMM, st.scalePx = scaleBarFor(f.scale/float64(ss), size)
	return encodePNG(image.Image(composeStrip(content, st, layout)))
}

// swatch paints a legend swatch with a thin dark border, so a white or a black
// one is seen against the strip.
func swatch(img *image.NRGBA, x, y, size, border int, fill color.NRGBA) {
	fillRect(img, x-border, y-border, size+2*border, size+2*border, layerText)
	fillRect(img, x, y, size, size, fill)
}
