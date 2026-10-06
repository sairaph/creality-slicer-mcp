package gcodeinfo

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// gcodeBuilder writes synthetic plate G-code in the shape Creality Print 7.3
// writes it: ;LAYER_CHANGE with ;Z: and ;HEIGHT:, EXCLUDE_OBJECT blocks, ;TYPE:
// and ;WIDTH:, relative or absolute E, numbers without a leading zero.
type gcodeBuilder struct {
	sb       strings.Builder
	absolute bool
	e        float64
}

func newBuilder(absolute bool) *gcodeBuilder {
	b := &gcodeBuilder{absolute: absolute}
	b.sb.WriteString("; HEADER_BLOCK_START\n; filament_diameter: 1.75,2.85\n; HEADER_BLOCK_END\n")
	if absolute {
		b.sb.WriteString("M82\n")
	} else {
		b.sb.WriteString("M83\n")
	}
	return b
}

func (b *gcodeBuilder) layer(z float64) {
	fmt.Fprintf(&b.sb, ";LAYER_CHANGE\n;Z:%s\n;HEIGHT:0.2\n", trimNum(z))
}
func (b *gcodeBuilder) begin(label string) {
	fmt.Fprintf(&b.sb, "; OBJECT_ID: 1\nEXCLUDE_OBJECT_START NAME=%s\n", label)
}
func (b *gcodeBuilder) end(label string) {
	fmt.Fprintf(&b.sb, "EXCLUDE_OBJECT_END NAME=%s\n", label)
}
func (b *gcodeBuilder) typ(name string, width float64) {
	fmt.Fprintf(&b.sb, ";TYPE:%s\n;WIDTH:%s\n", name, trimNum(width))
}
func (b *gcodeBuilder) travel(x, y float64) {
	fmt.Fprintf(&b.sb, "G1 X%s Y%s F30000\n", trimNum(x), trimNum(y))
}
func (b *gcodeBuilder) eWord(e float64) string {
	if b.absolute {
		b.e += e
		return trimNum(b.e)
	}
	return trimNum(e)
}

// line extrudes to (x, y) with the E of flow ratio 1 for the width and 0.2 height.
func (b *gcodeBuilder) line(x0, y0, x, y, width float64) {
	l := math.Hypot(x-x0, y-y0)
	e := l * width * 0.2 / (math.Pi * 1.75 * 1.75 / 4)
	fmt.Fprintf(&b.sb, "G1 X%s Y%s E%s F3600\n", trimNum(x), trimNum(y), b.eWord(e))
}
func (b *gcodeBuilder) lineE(x, y, e float64) {
	fmt.Fprintf(&b.sb, "G1 X%s Y%s E%s\n", trimNum(x), trimNum(y), b.eWord(e))
}

// square prints the outline of a square of side s with its lower left corner at (x, y).
func (b *gcodeBuilder) square(x, y, s, width float64) {
	b.travel(x, y)
	b.line(x, y, x+s, y, width)
	b.line(x+s, y, x+s, y+s, width)
	b.line(x+s, y+s, x, y+s, width)
	b.line(x, y+s, x, y, width)
}

// circle prints a full circle of radius r around (cx, cy) as one G3 with P1,
// starting at (cx+r, cy), then a half circle G2 back and forth would be too much:
// one is enough to test the arc.
func (b *gcodeBuilder) circle(cx, cy, r, width float64) {
	b.travel(cx+r, cy)
	e := 2 * math.Pi * r * width * 0.2 / (math.Pi * 1.75 * 1.75 / 4)
	fmt.Fprintf(&b.sb, "G3 X%s Y%s I%s J0 P1 E%s F3600\n", trimNum(cx+r), trimNum(cy), trimNum(-r), b.eWord(e))
}
func (b *gcodeBuilder) raw(s string) { b.sb.WriteString(s) }
func (b *gcodeBuilder) write(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "plate_1.gcode")
	if err := os.WriteFile(p, []byte(b.sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// trimNum writes a number the way the slicer does: no leading zero below 1.
func trimNum(v float64) string {
	s := fmt.Sprintf("%.5f", v)
	s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	if s == "" || s == "-" {
		return "0"
	}
	s = strings.Replace(s, "-0.", "-.", 1)
	if strings.HasPrefix(s, "0.") {
		s = s[1:]
	}
	return s
}

func TestAnalyzeFirstLayersGapsAndBounds(t *testing.T) {
	b := newBuilder(false)
	for i, z := range []float64{0.2, 0.4, 0.6, 0.8} {
		b.layer(z)
		b.begin("Cyl_id_0_copy_0")
		b.typ("Outer wall", 0.45)
		b.circle(50, 50, 5, 0.45)
		b.end("Cyl_id_0_copy_0")
		if i != 2 { // the square skips layer 3
			b.begin("Sq_id_1_copy_0")
			b.typ("Outer wall", 0.45)
			b.square(10.5, 10.5, 9.5, 0.45)
			if i == 0 {
				b.typ("Bottom surface", 0.5)
				b.line(11, 11, 19, 11, 0.5)
			}
			b.end("Sq_id_1_copy_0")
		}
	}
	p := b.write(t)
	res, err := Analyze(p, AnalyzeOptions{Measures: []string{MeasureFirstLayers, MeasureBounds, MeasureRadius}, Center: &[2]float64{50, 50}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Layers != 4 || len(res.Objects) != 2 {
		t.Fatalf("layers %d objects %+v", res.Layers, res.Objects)
	}
	var sq, cyl *FirstLayersRow
	for i := range res.FirstLayers {
		switch res.FirstLayers[i].Object {
		case "Sq":
			sq = &res.FirstLayers[i]
		case "Cyl":
			cyl = &res.FirstLayers[i]
		}
	}
	if sq == nil || cyl == nil || sq.FirstLayer != 1 || sq.LastLayer != 4 || len(sq.Gaps) != 1 || sq.Gaps[0] != 3 || len(cyl.Gaps) != 0 {
		t.Fatalf("first layers %+v", res.FirstLayers)
	}
	var bottom FeatureLayers
	for _, f := range sq.Features {
		if f.Feature == "Bottom surface" {
			bottom = f
		}
	}
	if bottom.First != 1 || bottom.Last != 1 {
		t.Fatalf("bottom surface %+v", bottom)
	}
	// The G3 with P1 is a full turn: the box is the circle's, to the chord sampling.
	for _, r := range res.Bounds {
		if r.Object == "Cyl" && r.Layer == 0 {
			if !near(r.MinX, 45, 0.01) || !near(r.MaxX, 55, 0.01) || !near(r.MinY, 45, 0.01) || !near(r.MaxY, 55, 0.01) {
				t.Errorf("circle bounds %+v", r)
			}
		}
		if r.Object == "Sq" && r.Feature == "Outer wall" && r.Layer == 0 {
			if r.MinX != 10.5 || r.MaxX != 20 || r.MinY != 10.5 || r.MaxY != 20 {
				t.Errorf("square bounds %+v", r)
			}
		}
	}
	for _, r := range res.Radius {
		if r.Object == "Cyl" && r.Layer == 0 && (!near(r.Min, 5, 0.01) || !near(r.Max, 5, 0.01)) {
			t.Errorf("radius %+v", r)
		}
	}
	// per layer rows exist too
	n := 0
	for _, r := range res.Bounds {
		if r.Object == "Sq" && r.Feature == "Outer wall" && r.Layer > 0 {
			n++
		}
	}
	if n != 3 {
		t.Errorf("%d per layer rows for the square", n)
	}
}

func TestAnalyzeArcsWithAndWithoutP(t *testing.T) {
	b := newBuilder(false)
	b.layer(0.2)
	b.begin("A_id_0_copy_0")
	b.typ("Outer wall", 0.45)
	b.travel(110, 100)
	// a half circle G2 (clockwise) from (110,100) to (90,100) around (100,100), then G3 back
	b.raw("G2 X90 Y100 I-10 J0 E1 F3600\n")
	b.raw("G3 X110 Y100 I10 J0 E1\n")
	// 1.5 turns counter clockwise with P2: a full extra turn
	b.raw("G3 X90 Y100 I-10 J0 P2 E1\n")
	b.end("A_id_0_copy_0")
	res, err := Analyze(b.write(t), AnalyzeOptions{Measures: []string{MeasureBounds}})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res.Bounds {
		if r.Layer != 0 {
			continue
		}
		// G2 from the right end goes through the top... clockwise from 0 degrees goes to -90 (y down).
		if !near(r.MinX, 90, 0.01) || !near(r.MaxX, 110, 0.01) || !near(r.MinY, 90, 0.01) || !near(r.MaxY, 110, 0.01) {
			t.Fatalf("bounds %+v", r)
		}
	}
	res, err = Analyze(b.write(t), AnalyzeOptions{Measures: []string{MeasureFlow}})
	if err != nil {
		t.Fatal(err)
	}
	// Lengths: pi*10 twice for the halves, then one and a half turns: 3*pi*10 for P2 from (110) to (90): half + one full turn.
	var length float64
	for _, f := range res.Flow {
		if f.Layer == 0 {
			length = f.LengthMM
		}
	}
	if want := math.Pi*10*2 + math.Pi*10*3; !near(length, want, 0.5) {
		t.Fatalf("arc length %v, want %v", length, want)
	}
}

func TestAnalyzeFlowRelativeAbsoluteWipeAndRetract(t *testing.T) {
	for _, absolute := range []bool{false, true} {
		b := newBuilder(absolute)
		b.layer(0.2)
		b.begin("F_id_0_copy_0")
		b.typ("Outer wall", 0.4)
		for i := 0; i < 20; i++ {
			x0 := 10.0 + float64(i)*11
			b.travel(x0, 10)
			b.line(x0, 10, x0+10, 10, 0.4) // ratio 1
		}
		b.travel(300, 10)
		b.lineE(310, 10, 2*10*0.4*0.2/(math.Pi*1.75*1.75/4)) // ratio 2
		// retract and prime without XY, and a wipe with E: none of them is path
		b.raw("G1 E-1 F2400\n;WIPE_START\nG1 X320 Y10 E.5\n;WIPE_END\nG1 E1 F2400\n")
		b.end("F_id_0_copy_0")
		res, err := Analyze(b.write(t), AnalyzeOptions{Measures: []string{MeasureFlow}})
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Flow) != 1 {
			t.Fatalf("absolute %v: flow %+v", absolute, res.Flow)
		}
		f := res.Flow[0]
		if f.Segments != 21 || !near(f.LengthMM, 210, 1e-6) || !near(f.Median, 1, 0.003) || !near(f.P01, 1, 0.003) || !near(f.Max, 2, 0.003) || !near(f.P99, 2, 0.003) {
			t.Fatalf("absolute %v: flow %+v", absolute, f)
		}
		wantE := 21 * 10 * 0.4 * 0.2 / (math.Pi * 1.75 * 1.75 / 4)
		if !near(f.EMM, wantE+10*0.4*0.2/(math.Pi*1.75*1.75/4), 0.01) {
			t.Errorf("E %v", f.EMM)
		}
	}
}

func TestAnalyzeShortRuns(t *testing.T) {
	b := newBuilder(false)
	b.layer(0.2)
	b.begin("R_id_0_copy_0")
	b.typ("Outer wall", 0.4)
	b.travel(10, 10)
	b.line(10, 10, 10.5, 10, 0.4) // short run, 0.5 mm
	b.travel(20, 10)
	b.line(20, 10, 25, 10, 0.4) // 5 mm
	b.raw("G1 E-.8 F2400\n")    // a retract ends the run
	b.line(25, 10, 25.2, 10, 0.4)
	b.typ("Inner wall", 0.4) // a feature change ends the run
	b.line(25.2, 10, 25.4, 10, 0.4)
	b.end("R_id_0_copy_0")
	res, err := Analyze(b.write(t), AnalyzeOptions{Measures: []string{MeasureShortRuns}, MinRun: 1})
	if err != nil {
		t.Fatal(err)
	}
	var layerRow, total *ShortRunRow
	for i := range res.ShortRuns {
		if res.ShortRuns[i].Layer == 1 {
			layerRow = &res.ShortRuns[i]
		} else {
			total = &res.ShortRuns[i]
		}
	}
	if layerRow == nil || total == nil || layerRow.Runs != 4 || layerRow.Short != 3 || len(layerRow.Ends) != 3 || layerRow.Ends[0] != [2]float64{10.5, 10} {
		t.Fatalf("short runs %+v", res.ShortRuns)
	}
	if total.Runs != 4 || total.Short != 3 {
		t.Fatalf("total %+v", total)
	}
}

func TestAnalyzeUnsupportedStarts(t *testing.T) {
	b := newBuilder(false)
	for i, z := range []float64{0.2, 0.4, 0.6} {
		b.layer(z)
		b.begin("C_id_0_copy_0")
		b.typ("Outer wall", 0.45)
		b.square(100, 100, 10, 0.45)
		if i == 2 {
			b.typ("Outer wall", 0.45)
			b.square(130, 130, 2, 0.45) // an island in mid-air
			b.typ("Bridge", 0.45)
			b.travel(140, 140)
			b.line(140, 140, 146, 140, 0.45) // a bridge is no finding
		}
		b.end("C_id_0_copy_0")
	}
	res, err := Analyze(b.write(t), AnalyzeOptions{Measures: []string{MeasureUnsupportedStart}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.UnsupportedStarts) != 1 {
		t.Fatalf("islands %+v", res.UnsupportedStarts)
	}
	s := res.UnsupportedStarts[0]
	if s.Object != "C" || s.Layer != 3 || !near(s.Z, 0.6, 1e-9) || !near(s.MinX, 129.7, 0.2) || !near(s.MaxX, 132.3, 0.2) || !near(s.MinY, 129.7, 0.2) || s.SupportedPercent != 0 || len(s.Features) != 1 || s.Features[0] != "Outer wall" || s.AreaMM2 < 1 {
		t.Fatalf("island %+v", s)
	}
	// a layer range: layers 1 to 2 see nothing, since layer 3 is outside it
	res, err = Analyze(b.write(t), AnalyzeOptions{Measures: []string{MeasureUnsupportedStart}, LayerFrom: 1, LayerTo: 2})
	if err != nil || len(res.UnsupportedStarts) != 0 {
		t.Fatalf("range 1-2: %v %+v", err, res.UnsupportedStarts)
	}
	// from layer 3 only: the layer below is still read for the comparison
	res, err = Analyze(b.write(t), AnalyzeOptions{Measures: []string{MeasureUnsupportedStart}, LayerFrom: 3})
	if err != nil || len(res.UnsupportedStarts) != 1 {
		t.Fatalf("from 3: %v %+v", err, res.UnsupportedStarts)
	}
}

func TestAnalyzeSupportContacts(t *testing.T) {
	b := newBuilder(false)
	b.layer(0.2)
	b.begin("D_id_0_copy_0")
	b.typ("Outer wall", 0.45)
	b.square(150, 150, 5, 0.45)
	b.end("D_id_0_copy_0")
	b.layer(0.4)
	b.begin("D_id_0_copy_0")
	b.typ("Support interface", 0.45)
	b.square(150, 150, 5, 0.45)
	b.line(150, 150, 155, 155, 0.45)
	b.end("D_id_0_copy_0")
	b.begin("E_id_1_copy_0")
	b.typ("Outer wall", 0.45) // earlier in the file than the next layer: not above yet
	b.square(200, 200, 3, 0.45)
	b.end("E_id_1_copy_0")
	b.layer(0.6)
	b.begin("E_id_1_copy_0")
	b.typ("Outer wall", 0.45)
	b.square(151, 151, 3, 0.45)
	b.end("E_id_1_copy_0")
	res, err := Analyze(b.write(t), AnalyzeOptions{Measures: []string{MeasureSupportContacts}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.SupportContacts) != 1 {
		t.Fatalf("clusters %+v", res.SupportContacts)
	}
	c := res.SupportContacts[0]
	if c.Object != "D" || c.Feature != "Support interface" || c.Layer != 2 || len(c.Above) != 1 || c.Above[0] != "E" || !near(c.MinX, 149.75, 0.2) || !near(c.MaxX, 155.25, 0.2) {
		t.Fatalf("cluster %+v", c)
	}
}

func TestAnalyzeWallOrder(t *testing.T) {
	b := newBuilder(false)
	for i, z := range []float64{0.2, 0.4, 0.6} {
		b.layer(z)
		b.begin("W_id_0_copy_0")
		names := []string{"Outer wall", "Inner wall"}
		if i == 1 {
			names = []string{"Inner wall", "Outer wall"}
		}
		for k, n := range names {
			b.typ(n, 0.45)
			b.square(10+float64(k), 10+float64(k), 20-2*float64(k), 0.45)
		}
		b.end("W_id_0_copy_0")
	}
	res, err := Analyze(b.write(t), AnalyzeOptions{Measures: []string{MeasureWallOrder}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.WallOrder) != 1 || res.WallOrder[0].OuterFirst != 2 || res.WallOrder[0].InnerFirst != 1 || len(res.WallOrder[0].InnerFirstLayers) != 1 || res.WallOrder[0].InnerFirstLayers[0] != 2 {
		t.Fatalf("wall order %+v", res.WallOrder)
	}
}

func TestAnalyzeFiltersToolChangeAndNames(t *testing.T) {
	b := newBuilder(false)
	for i, z := range []float64{0.2, 0.4, 0.6, 0.8} {
		b.layer(z)
		if i == 2 {
			b.raw("T1\n")
		}
		// two objects interleaved on every layer; the second has a space in its name
		b.begin("Left_id_0_copy_0")
		b.typ("Outer wall", 0.45)
		b.square(10, 10, 5, 0.45)
		b.end("Left_id_0_copy_0")
		b.begin("Right_Part_id_1_copy_0")
		b.typ("Inner wall", 0.45)
		b.square(50, 10, 5, 0.45)
		b.end("Right_Part_id_1_copy_0")
		// the prime tower and travel between objects belong to no object
		b.typ("Prime tower", 0.5)
		b.travel(250, 250)
		b.line(250, 250, 258, 250, 0.5)
	}
	p := b.write(t)
	all, err := Analyze(p, AnalyzeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Objects) != 2 || len(all.FirstLayers) != 2 {
		t.Fatalf("objects %+v rows %+v", all.Objects, all.FirstLayers)
	}
	for _, r := range all.Bounds {
		if r.MaxX > 100 {
			t.Fatalf("the prime tower is in the bounds: %+v", r)
		}
	}
	one, err := Analyze(p, AnalyzeOptions{Objects: []string{"right part"}, Features: []string{"inner WALL"}, LayerFrom: 2, LayerTo: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(one.FirstLayers) != 1 || one.FirstLayers[0].Object != "Right_Part" || one.FirstLayers[0].FirstLayer != 2 || one.FirstLayers[0].LastLayer != 3 {
		t.Fatalf("filtered %+v", one.FirstLayers)
	}
	// by z
	byZ, err := Analyze(p, AnalyzeOptions{UseZ: true, ZFrom: 0.6, ZTo: 0.8})
	if err != nil || byZ.FirstLayers[0].FirstLayer != 3 || byZ.FirstLayers[0].LastLayer != 4 {
		t.Fatalf("by z: %v %+v", err, byZ.FirstLayers)
	}
	// instances of one name are told apart
	b2 := newBuilder(false)
	b2.layer(0.2)
	for _, l := range []string{"Twin_id_0_copy_0", "Twin_id_0_copy_1", "Twin_id_5_copy_0"} {
		b2.begin(l)
		b2.typ("Outer wall", 0.45)
		b2.square(10, 10, 5, 0.45)
		b2.end(l)
	}
	twins, err := Analyze(b2.write(t), AnalyzeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, o := range twins.Objects {
		names = append(names, o.Display)
	}
	if strings.Join(names, ",") != "Twin#0,Twin#0.1,Twin#5" {
		t.Fatalf("names %v", names)
	}
}

func TestAnalyzeErrors(t *testing.T) {
	b := newBuilder(false)
	b.layer(0.2)
	p := b.write(t)
	if _, err := Analyze(p, AnalyzeOptions{Measures: []string{"nope"}}); err == nil {
		t.Error("unknown measure accepted")
	}
	if _, err := Analyze(p, AnalyzeOptions{Measures: []string{MeasureRadius}}); err == nil {
		t.Error("radius without a center accepted")
	}
	if _, err := Analyze(filepath.Join(t.TempDir(), "none.gcode"), AnalyzeOptions{}); err == nil {
		t.Error("missing file accepted")
	}
}

func TestGridIslands(t *testing.T) {
	g := newGrid()
	g.stamp(0, 0, 0.3)
	g.stamp(0.3, 0, 0.3) // touching: one island
	g.stamp(50, 50, 0.3)
	g.stamp(-70, -70, 0.3) // negative coordinates and another tile
	if n := len(g.islands()); n != 3 {
		t.Fatalf("%d islands", n)
	}
	if !g.has(int(math.Floor(50/gridCell)), int(math.Floor(50/gridCell))) || g.has(1000, 1000) {
		t.Error("has")
	}
}

// print_sequence by object: each object restarts its layers. Numbering is per
// object and the layer below is the object's own.
func TestAnalyzeByObjectSequences(t *testing.T) {
	b := newBuilder(false)
	for _, label := range []string{"First_id_0_copy_0", "Second_id_1_copy_0"} {
		for _, z := range []float64{0.2, 0.4, 0.6} {
			b.layer(z)
			b.begin(label)
			b.typ("Outer wall", 0.45)
			b.square(100, 100, 10, 0.45) // the same place: the second object must not see the first's layer
			b.end(label)
		}
	}
	res, err := Analyze(b.write(t), AnalyzeOptions{Measures: []string{MeasureFirstLayers, MeasureUnsupportedStart}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.ByObject || res.Layers != 3 || len(res.FirstLayers) != 2 {
		t.Fatalf("by object %v, layers %d, rows %+v", res.ByObject, res.Layers, res.FirstLayers)
	}
	for _, r := range res.FirstLayers {
		if r.FirstLayer != 1 || r.LastLayer != 3 || len(r.Gaps) != 0 {
			t.Errorf("%s: layers %d-%d gaps %v", r.Object, r.FirstLayer, r.LastLayer, r.Gaps)
		}
	}
	if len(res.UnsupportedStarts) != 0 {
		t.Fatalf("false islands: %+v", res.UnsupportedStarts)
	}
	// by layer files are not marked
	l := newBuilder(false)
	for _, z := range []float64{0.2, 0.4} {
		l.layer(z)
		l.begin("A_id_0_copy_0")
		l.typ("Outer wall", 0.45)
		l.square(10, 10, 5, 0.45)
		l.end("A_id_0_copy_0")
	}
	res, err = Analyze(l.write(t), AnalyzeOptions{})
	if err != nil || res.ByObject {
		t.Fatalf("by layer marked by object: %v %v", err, res.ByObject)
	}
	// a one layer object followed by another that starts at the same z
	one := newBuilder(false)
	for _, label := range []string{"A_id_0_copy_0", "B_id_1_copy_0"} {
		one.layer(0.2)
		one.begin(label)
		one.typ("Outer wall", 0.45)
		one.square(10, 10, 5, 0.45)
		one.end(label)
	}
	res, err = Analyze(one.write(t), AnalyzeOptions{})
	if err != nil || !res.ByObject || res.Layers != 1 {
		t.Fatalf("equal z restart: %v %+v", err, res)
	}
}

// The features filter picks what is reported, not what is rasterised: the layer
// below and the island detection see every feature.
func TestAnalyzeFeatureFilterKeepsTheRaster(t *testing.T) {
	b := newBuilder(false)
	for i, z := range []float64{0.2, 0.4, 0.6} {
		b.layer(z)
		b.begin("T_id_0_copy_0")
		b.typ("Sparse infill", 0.45)
		b.square(100, 100, 10, 0.45) // the layer below, a different feature
		b.typ("Top surface", 0.45)
		b.line(101, 105, 109, 105, 0.45)
		if i == 2 {
			b.typ("Outer wall", 0.45)
			b.square(130, 130, 2, 0.45) // a real island, not a top surface
		}
		b.end("T_id_0_copy_0")
	}
	p := b.write(t)
	all, err := Analyze(p, AnalyzeOptions{Measures: []string{MeasureUnsupportedStart}})
	if err != nil || len(all.UnsupportedStarts) != 1 {
		t.Fatalf("all features: %v %+v", err, all.UnsupportedStarts)
	}
	top, err := Analyze(p, AnalyzeOptions{Measures: []string{MeasureUnsupportedStart}, Features: []string{"Top surface"}})
	if err != nil || len(top.UnsupportedStarts) != 0 {
		t.Fatalf("Top surface only: %v %+v", err, top.UnsupportedStarts)
	}
	wall, err := Analyze(p, AnalyzeOptions{Measures: []string{MeasureUnsupportedStart}, Features: []string{"outer wall"}})
	if err != nil || len(wall.UnsupportedStarts) != 1 || wall.UnsupportedStarts[0].Layer != 3 {
		t.Fatalf("Outer wall only: %v %+v", err, wall.UnsupportedStarts)
	}
	if got := strings.Join(all.AllLabels, ","); got != "T_id_0_copy_0" {
		t.Errorf("labels %q", got)
	}
}

const supportCfg = "; CONFIG_BLOCK_START\n; support_interface_spacing = 0.5\n; support_base_pattern_spacing = 2.5\n; support_line_width = 0.42\n; support_top_z_distance = 0.2\n; support_object_xy_distance = 0.35\n; CONFIG_BLOCK_END\n"

// stripes prints n parallel interface lines 1.2 mm long, a line pitch apart.
func (b *gcodeBuilder) stripes(x, y float64, n int, pitch float64) {
	b.typ("Support interface", 0.42)
	for i := 0; i < n; i++ {
		yy := y + float64(i)*pitch
		b.travel(x, yy)
		b.line(x, yy, x+1.2, yy, 0.42)
	}
}

// The object prints a z gap above the support (support_top_z_distance), so the
// contact is found a few layers up, and the support in another object's block
// still names the object that sits on it.
func TestAnalyzeSupportContactsAcrossTheZGapAndBlocks(t *testing.T) {
	b := newBuilder(false)
	b.layer(0.2)
	b.begin("Owner_id_0_copy_0") // the support is printed in this block ...
	b.stripes(100, 100, 3, 0.9)
	b.end("Owner_id_0_copy_0")
	b.layer(0.4) // ... the gap layer: nothing above yet
	b.begin("Owner_id_0_copy_0")
	b.typ("Outer wall", 0.45)
	b.square(300, 300, 3, 0.45)
	b.end("Owner_id_0_copy_0")
	b.layer(0.6) // ... and Sitter prints on it
	b.begin("Sitter_id_1_copy_0")
	b.typ("Outer wall", 0.45)
	b.square(99.5, 99.5, 4, 0.45)
	b.end("Sitter_id_1_copy_0")
	b.layer(0.8)
	b.begin("Sitter_id_1_copy_0")
	b.typ("Outer wall", 0.45)
	b.square(99.5, 99.5, 4, 0.45)
	b.end("Sitter_id_1_copy_0")
	b.raw(supportCfg)
	res, err := Analyze(b.write(t), AnalyzeOptions{Measures: []string{MeasureSupportContacts}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.SupportContacts) != 1 {
		t.Fatalf("clusters %+v", res.SupportContacts)
	}
	c := res.SupportContacts[0]
	if c.Object != "Owner" || len(c.Above) != 1 || c.Above[0] != "Sitter" {
		t.Fatalf("contact %+v, want Sitter (the support is Owner's block, the object above is Sitter)", c)
	}
	// no object near: the patch names nobody
	far := newBuilder(false)
	far.layer(0.2)
	far.begin("Owner_id_0_copy_0")
	far.stripes(100, 100, 3, 0.9)
	far.end("Owner_id_0_copy_0")
	far.layer(0.4)
	far.begin("Owner_id_0_copy_0")
	far.typ("Outer wall", 0.45)
	far.square(300, 300, 3, 0.45)
	far.end("Owner_id_0_copy_0")
	far.raw(supportCfg)
	res, err = Analyze(far.write(t), AnalyzeOptions{Measures: []string{MeasureSupportContacts}})
	if err != nil || len(res.SupportContacts) != 1 || len(res.SupportContacts[0].Above) != 0 {
		t.Fatalf("far: %v %+v", err, res.SupportContacts)
	}
}

// Strips one line pitch apart are one patch; farther apart (a wider spacing in
// the config) they are separate patches.
func TestAnalyzeSupportInterfaceStripsMerge(t *testing.T) {
	build := func(cfg string, pitch float64) *Analysis {
		b := newBuilder(false)
		b.layer(0.2)
		b.begin("S_id_0_copy_0")
		b.stripes(100, 100, 15, pitch)
		b.end("S_id_0_copy_0")
		b.raw(cfg)
		res, err := Analyze(b.write(t), AnalyzeOptions{Measures: []string{MeasureSupportContacts}})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	one := build(supportCfg, 0.92) // the pitch is spacing 0.5 plus line width 0.42
	if len(one.SupportContacts) != 1 {
		t.Fatalf("%d patches, want one", len(one.SupportContacts))
	}
	c := one.SupportContacts[0]
	if !near(c.MinY, 99.8, 0.15) || !near(c.MaxY, 100+14*0.92+0.2, 0.2) || c.AreaMM2 < 9 {
		t.Fatalf("patch %+v", c)
	}
	wide := build(supportCfg, 3) // strips 2.6 mm apart: far beyond the line pitch
	if len(wide.SupportContacts) != 15 {
		t.Fatalf("strips far apart must not merge: %d patches", len(wide.SupportContacts))
	}
}

// 7.2.2 writes ";:<z>" where 7.3 writes ";Z:<z>": the analysis reads both.
func TestAnalyzeReadsThe72LayerMarker(t *testing.T) {
	res, err := Analyze(oneFilament, AnalyzeOptions{Measures: []string{MeasureFirstLayers, MeasureBounds}, UseZ: true, ZFrom: 0.2, ZTo: 0.6})
	if err != nil {
		t.Fatal(err)
	}
	if res.Layers != 100 || len(res.FirstLayers) != 1 {
		t.Fatalf("layers %d rows %+v", res.Layers, res.FirstLayers)
	}
	r := res.FirstLayers[0]
	if r.FirstLayer != 1 || r.LastLayer != 3 {
		t.Fatalf("z 0.2 to 0.6 is layers 1 to 3, got %d to %d", r.FirstLayer, r.LastLayer)
	}
	for _, b := range res.Bounds {
		if b.Layer > 0 && (b.Z < 0.19 || b.Z > 0.61) {
			t.Fatalf("bounds row at z %v", b.Z)
		}
	}
}

// With independent support layer heights the slicer writes layers that hold
// only support between object layers: a stem standing on the object layer
// before them is not in mid air.
func TestAnalyzeUnsupportedStartsWithSupportOnlyLayers(t *testing.T) {
	build := func(island bool) *gcodeBuilder {
		b := newBuilder(false)
		step := func(z float64, object bool) {
			b.layer(z)
			b.begin("C_id_0_copy_0")
			if object {
				b.typ("Outer wall", 0.45)
				b.square(100, 100, 10, 0.45)
			}
			b.typ("Support", 0.45)
			b.square(60, 60, 4, 0.45)
			b.end("C_id_0_copy_0")
		}
		step(0.2, true)
		step(0.3, false) // support only
		step(0.4, false)
		step(0.6, true) // the stem again, above two support-only layers
		if island {
			b.begin("C_id_0_copy_0")
			b.typ("Outer wall", 0.45)
			b.square(130, 130, 2, 0.45)
			b.end("C_id_0_copy_0")
		}
		return b
	}
	res, err := Analyze(build(false).write(t), AnalyzeOptions{Measures: []string{MeasureUnsupportedStart}})
	if err != nil || len(res.UnsupportedStarts) != 0 {
		t.Fatalf("false islands after support-only layers: %v %+v", err, res.UnsupportedStarts)
	}
	res, err = Analyze(build(true).write(t), AnalyzeOptions{Measures: []string{MeasureUnsupportedStart}})
	if err != nil || len(res.UnsupportedStarts) != 1 || !near(res.UnsupportedStarts[0].MinX, 129.7, 0.2) {
		t.Fatalf("the real mid-air island: %v %+v", err, res.UnsupportedStarts)
	}
}

// A lower support column that has more support printing on it says "support";
// the patch an object prints on names the object; one with nothing above is "-".
func TestAnalyzeSupportOnSupport(t *testing.T) {
	b := newBuilder(false)
	for _, z := range []float64{0.2, 0.4, 0.6, 0.8, 1.0, 1.2} {
		b.layer(z)
		b.begin("D_id_0_copy_0")
		b.typ("Support", 0.45)
		b.square(100, 100, 4, 0.45) // a column of three support layers
		b.end("D_id_0_copy_0")
	}
	b.layer(1.4)
	b.begin("E_id_1_copy_0")
	b.typ("Outer wall", 0.45)
	b.square(100.5, 100.5, 3, 0.45) // the model prints on the top of the column
	b.end("E_id_1_copy_0")
	res, err := Analyze(b.write(t), AnalyzeOptions{Measures: []string{MeasureSupportContacts}})
	if err != nil {
		t.Fatal(err)
	}
	byLayer := map[int][]string{}
	for _, c := range res.SupportContacts {
		byLayer[c.Layer] = c.Above
	}
	if len(byLayer) != 6 {
		t.Fatalf("clusters %+v", res.SupportContacts)
	}
	if len(byLayer[1]) != 1 || byLayer[1][0] != SupportAbove || len(byLayer[2]) != 1 || byLayer[2][0] != SupportAbove {
		t.Errorf("lower layers %v %v, want support", byLayer[1], byLayer[2])
	}
	if len(byLayer[6]) != 1 || byLayer[6][0] != "E" {
		t.Errorf("the top layer 6 %v, want E", byLayer[6])
	}
	// a lone support patch with nothing over it stays empty
	b2 := newBuilder(false)
	b2.layer(0.2)
	b2.begin("D_id_0_copy_0")
	b2.typ("Support", 0.45)
	b2.square(100, 100, 4, 0.45)
	b2.end("D_id_0_copy_0")
	res, err = Analyze(b2.write(t), AnalyzeOptions{Measures: []string{MeasureSupportContacts}})
	if err != nil || len(res.SupportContacts) != 1 || len(res.SupportContacts[0].Above) != 0 {
		t.Errorf("lone patch: %v %+v", err, res.SupportContacts)
	}
}

// A vase mode file closes with a loop at the height of the layer before it: one
// more ;LAYER_CHANGE, no new layer, and no restart of the numbering.
func TestAnalyzeVaseClosingLoopIsNotALayer(t *testing.T) {
	b := newBuilder(false)
	for _, z := range []float64{0.2, 0.4, 0.6, 0.6} {
		b.layer(z)
		b.begin("V_id_0_copy_0")
		b.typ("Outer wall", 0.45)
		b.square(100, 100, 10, 0.45)
		b.end("V_id_0_copy_0")
	}
	res, err := Analyze(b.write(t), AnalyzeOptions{})
	if err != nil || res.Layers != 3 || res.ByObject {
		t.Fatalf("vase: %v layers %d by object %v", err, res.Layers, res.ByObject)
	}
}
