package gcodeinfo

import (
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
)

// Measures of Analyze.
const (
	MeasureFirstLayers      = "first_layers"
	MeasureBounds           = "bounds"
	MeasureFlow             = "flow"
	MeasureRadius           = "radius"
	MeasureShortRuns        = "short_runs"
	MeasureUnsupportedStart = "unsupported_starts"
	MeasureSupportContacts  = "support_contacts"
	MeasureWallOrder        = "wall_order"
)

// AllMeasures lists the measures in the order a report shows them.
var AllMeasures = []string{MeasureFirstLayers, MeasureBounds, MeasureFlow, MeasureRadius, MeasureShortRuns, MeasureUnsupportedStart, MeasureSupportContacts, MeasureWallOrder}

// AnalyzeOptions selects what Analyze measures.
type AnalyzeOptions struct {
	// Objects are object names; they match the label prefix before "_id_" of
	// EXCLUDE_OBJECT_START, ignoring case and punctuation. Empty is every object.
	Objects []string
	// LayerFrom and LayerTo (1 based, 0 = open) or ZFrom and ZTo (mm, with UseZ)
	// limit the layers measured.
	LayerFrom, LayerTo int
	UseZ               bool
	ZFrom, ZTo         float64
	// Features are ";TYPE:" names, ignoring case; empty is every feature.
	Features []string
	// Measures are the measure names; empty means first_layers and bounds.
	Measures []string
	// Center [x, y] is needed for radius.
	Center *[2]float64
	// MinRun is the length in mm below which a run is short (default 1).
	MinRun float64
	// PerLayer keeps the flow figures per layer too.
	PerLayer bool
}

// ObjectRef names an object of the G-code.
type ObjectRef struct {
	Label string // NAME= of EXCLUDE_OBJECT_START
	Name  string // the label before "_id_"
	ID    int
	Copy  int
	// Display is Name, with "#id" (and ".copy") added when several objects share it.
	Display string
}

// FeatureLayers is the first and last layer a feature of an object prints.
type FeatureLayers struct {
	Feature     string
	First, Last int
}

// FirstLayersRow is the first_layers result of one object.
type FirstLayersRow struct {
	Object                string
	Features              []FeatureLayers
	FirstLayer, LastLayer int
	// Gaps are layers inside the object's range with no extrusion of it.
	Gaps []int
}

// BoundsRow is an XY box of extruded paths. Layer 0 is the whole layer range.
type BoundsRow struct {
	Object, Feature string
	Layer           int
	Z               float64
	MinX, MaxX      float64
	MinY, MaxY      float64
}

// FlowRow is the flow of one feature of an object. Layer 0 is the whole range.
type FlowRow struct {
	Object, Feature       string
	Layer                 int
	Segments              int
	LengthMM, EMM         float64
	EPerMM                float64
	Median, P01, P99, Max float64
}

// RadiusRow is the distance of the extruded paths from the centre.
type RadiusRow struct {
	Object, Feature string
	Layer           int
	Min, Max        float64
}

// ShortRunRow counts the runs of one object on one layer.
type ShortRunRow struct {
	Object      string
	Layer       int
	Z           float64
	Runs, Short int
	// Ends are the end positions of the first short runs (at most 20).
	Ends [][2]float64
}

// StartIsland is extrusion that starts where the layer below has none.
type StartIsland struct {
	Object           string
	Layer            int
	Z                float64
	Features         []string
	MinX, MaxX       float64
	MinY, MaxY       float64
	AreaMM2          float64
	SupportedPercent float64
}

// SupportCluster is a patch of support or support interface on a layer.
type SupportCluster struct {
	Object, Feature string
	Layer           int
	Z               float64
	MinX, MaxX      float64
	MinY, MaxY      float64
	AreaMM2         float64
	// Above lists the objects that print on the patch on the next layer.
	Above []string
}

// WallOrderRow says, per object, on how many layers the outer wall printed
// before the inner wall and on how many after it.
type WallOrderRow struct {
	Object                 string
	OuterFirst, InnerFirst int
	// InnerFirstLayers lists the layers (first 50) where the inner wall came first.
	InnerFirstLayers []int
}

// Analysis is the result of Analyze.
type Analysis struct {
	// Layers is the number of layers of the file; for a plate printed by object
	// the most layers of any object.
	Layers int
	// ByObject is true when the layers restart: with print_sequence by object the
	// G-code prints each object from its first layer to its last, and a layer
	// number is then the number of the layer within its object, counted from 1.
	ByObject bool
	// AllLabels lists every object label found in the G-code, requested or not.
	AllLabels         []string
	FilamentMM        []float64
	Objects           []ObjectRef
	Options           AnalyzeOptions
	FirstLayers       []FirstLayersRow
	Bounds            []BoundsRow
	Flow              []FlowRow
	Radius            []RadiusRow
	ShortRuns         []ShortRunRow
	UnsupportedStarts []StartIsland
	SupportContacts   []SupportCluster
	WallOrder         []WallOrderRow
}

type objAcc struct {
	ref      ObjectRef
	extruded map[int]bool // layers with extrusion in range
	feat     map[string]*FeatureLayers
	bounds   map[bkey]*BoundsRow
	flow     map[bkey]*flowAcc
	radius   map[bkey]*RadiusRow
	runs     map[int]*ShortRunRow
	order    map[int]*[2]int // per layer: sequence of the first outer and inner wall move
}

type bkey struct {
	feature string
	layer   int
}

type flowAcc struct {
	segs   int
	length float64
	e      float64
	max    float64
	hist   map[int32]uint32
}

const flowBin = 0.002

func (f *flowAcc) add(ratio, length, e float64) {
	f.segs++
	f.length += length
	f.e += e
	if ratio > f.max {
		f.max = ratio
	}
	f.hist[int32(ratio/flowBin)]++
}

func (f *flowAcc) quantile(q float64) float64 {
	if f.segs == 0 {
		return 0
	}
	keys := make([]int32, 0, len(f.hist))
	for k := range f.hist {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	target := q * float64(f.segs)
	var cum float64
	for _, k := range keys {
		cum += float64(f.hist[k])
		if cum >= target {
			return math.Min((float64(k)+0.5)*flowBin, f.max)
		}
	}
	return f.max
}

// analyzer is the state of one streaming pass.
type analyzer struct {
	opt      AnalyzeOptions
	res      *Analysis
	want     map[string]bool
	wantObj  map[string]bool
	wantFeat map[string]bool
	objs     map[string]*objAcc
	labels   []string
	diam     []float64
	minRun   float64

	cur           *objAcc
	feature       string
	width, height float64
	layer         int
	z, lastZ      float64
	newLayer      bool
	inWipe        bool
	seq           int

	run struct {
		obj     *objAcc
		feature string
		length  float64
		last    [2]float64
		active  bool
	}
	// rasters of the layer being read: object label -> feature -> grid
	rast map[string]map[string]*grid
	// hist holds the extrusion of every object and support of the layers just
	// below the current one (see belowGrid); older layers are dropped.
	hist    []histLayer
	maxH    float64
	pending []pendingCluster
	// equalZ: the layer just opened repeats the height of the one before; refObject
	// is the object that printed last then, lastStarted the last object started.
	equalZ                 bool
	refObject, lastStarted string
	cfg                    map[string]string
	startIslands           []StartIsland
	supportClusters        []SupportCluster
}

// NormObjectName is the form object names are compared in: lower case, every
// character that is not a letter or digit written as an underscore.
func NormObjectName(s string) string { return normName(s) }

func normName(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

func parseObjectLabel(label string) ObjectRef {
	ref := ObjectRef{Label: label, Name: label}
	if i := strings.LastIndex(label, "_id_"); i >= 0 {
		ref.Name = label[:i]
		rest := label[i+4:]
		if j := strings.Index(rest, "_copy_"); j >= 0 {
			ref.ID, _ = strconv.Atoi(rest[:j])
			ref.Copy, _ = strconv.Atoi(rest[j+6:])
		} else {
			ref.ID, _ = strconv.Atoi(rest)
		}
	}
	return ref
}

func (a *analyzer) on(m string) bool { return a.want[m] }

func (a *analyzer) layerIn(layer int, z float64) bool {
	if a.opt.UseZ {
		return z >= a.opt.ZFrom-1e-6 && (a.opt.ZTo <= 0 || z <= a.opt.ZTo+1e-6)
	}
	return layer >= max(a.opt.LayerFrom, 1) && (a.opt.LayerTo <= 0 || layer <= a.opt.LayerTo)
}

func (a *analyzer) object(label string) *objAcc {
	if o, ok := a.objs[label]; ok {
		return o
	}
	o := &objAcc{ref: parseObjectLabel(label), extruded: map[int]bool{}, feat: map[string]*FeatureLayers{},
		bounds: map[bkey]*BoundsRow{}, flow: map[bkey]*flowAcc{}, radius: map[bkey]*RadiusRow{}, runs: map[int]*ShortRunRow{}, order: map[int]*[2]int{}}
	a.objs[label] = o
	a.labels = append(a.labels, label)
	return o
}

func (a *analyzer) selected(o *objAcc) bool {
	if len(a.wantObj) == 0 {
		return true
	}
	return a.wantObj[normName(o.ref.Name)] || a.wantObj[normName(o.ref.Label)]
}

// Analyze reads a plate G-code once and measures what the options ask for.
func Analyze(path string, opt AnalyzeOptions) (*Analysis, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	a := &analyzer{opt: opt, res: &Analysis{Options: opt}, want: map[string]bool{}, wantObj: map[string]bool{}, wantFeat: map[string]bool{}, objs: map[string]*objAcc{},
		minRun: opt.MinRun, rast: map[string]map[string]*grid{}}
	if a.minRun <= 0 {
		a.minRun = 1
	}
	ms := opt.Measures
	if len(ms) == 0 {
		ms = []string{MeasureFirstLayers, MeasureBounds}
	}
	for _, m := range ms {
		ok := false
		for _, k := range AllMeasures {
			ok = ok || k == m
		}
		if !ok {
			return nil, fmt.Errorf("unknown measure %q", m)
		}
		a.want[m] = true
	}
	if a.on(MeasureRadius) && opt.Center == nil {
		return nil, fmt.Errorf("the radius measure needs a center")
	}
	for _, o := range opt.Objects {
		a.wantObj[normName(o)] = true
	}
	for _, ft := range opt.Features {
		a.wantFeat[strings.ToLower(strings.TrimSpace(ft))] = true
	}
	if a.on(MeasureSupportContacts) {
		a.cfg = configTail(path)
	}
	st := newMotionState()
	lr := newLineReader(f, 0)
	for {
		line, _, _, err := lr.next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if len(line) == 0 {
			continue
		}
		if line[0] == ';' {
			a.comment(string(line), &st)
			continue
		}
		text := strings.TrimSpace(string(stripComment(line)))
		switch {
		case strings.HasPrefix(text, "EXCLUDE_OBJECT_START"):
			a.endRun()
			if i := strings.Index(text, "NAME="); i >= 0 {
				if name := strings.Fields(text[i+5:]); len(name) > 0 {
					if a.equalZ {
						a.decideEqualZ(name[0])
					}
					a.cur = a.object(name[0])
					a.lastStarted = name[0]
				}
			}
			continue
		case strings.HasPrefix(text, "EXCLUDE_OBJECT_END"):
			a.endRun()
			a.cur = nil
			continue
		}
		mv, ok := st.apply(line)
		motion := len(text) > 1 && text[0] == 'G' && strings.Contains("0123", text[1:2])
		if !ok {
			if motion && breaksRun(text) {
				a.endRun() // a retract, a prime or a lift between runs
			}
			continue
		}
		a.move(mv)
	}
	a.endRun()
	a.finishLayer()
	a.flushPending()
	a.finish()
	return a.res, nil
}

// breaksRun reports whether a motion line that moved nothing in XY (a retract,
// a prime, a lift) ends a run; a feed rate alone does not.
func breaksRun(text string) bool {
	for _, w := range strings.Fields(text)[1:] {
		switch w[0] {
		case 'E', 'e', 'Z', 'z':
			return true
		}
	}
	return false
}

func (a *analyzer) comment(text string, st *MotionState) {
	switch {
	case text == ";LAYER_CHANGE":
		a.endRun()
		a.finishLayer()
		a.layer++
		a.newLayer = true
		a.inWipe = false
	case strings.HasPrefix(text, ";Z:") || (a.newLayer && strings.HasPrefix(text, ";:")):
		// 7.3 writes ";Z:<z>" after ;LAYER_CHANGE, 7.2.2 writes ";:<z>".
		a.z, _ = strconv.ParseFloat(strings.TrimSpace(text[strings.IndexByte(text, ':')+1:]), 64)
		if a.newLayer {
			a.newLayer = false
			// A layer that does not rise above the one before starts a new object
			// sequence (print_sequence by object): numbering and the layer below
			// begin again.
			// A strictly lower z (an equal one is a repeated height, such as the
			// closing loop of vase mode, and just another layer marker).
			if a.layer > 1 && a.z < a.lastZ-1e-9 {
				a.restartByObject()
			} else if a.layer > 1 && a.z <= a.lastZ+1e-9 {
				// The same height again: another object's first layer (by object) or
				// the same object's closing loop (vase mode). The first object of the
				// layer decides; until then the layer is not counted.
				a.equalZ, a.refObject = true, a.lastStarted
			}
			a.lastZ = a.z
			if !a.equalZ {
				a.res.Layers = max(a.res.Layers, a.layer)
			}
		}
	case strings.HasPrefix(text, ";TYPE:"):
		a.endRun()
		a.feature = strings.TrimSpace(text[6:])
		st.Feature = a.feature
	case strings.HasPrefix(text, ";WIDTH:"):
		a.width, _ = strconv.ParseFloat(strings.TrimSpace(text[7:]), 64)
	case strings.HasPrefix(text, ";HEIGHT:"):
		a.height, _ = strconv.ParseFloat(strings.TrimSpace(text[8:]), 64)
	case strings.HasPrefix(text, ";WIPE_START"):
		a.endRun()
		a.inWipe = true
	case strings.HasPrefix(text, ";WIPE_END"):
		a.inWipe = false
	case strings.HasPrefix(text, "; filament_diameter:") && a.diam == nil:
		for _, f := range strings.Split(strings.TrimSpace(text[len("; filament_diameter:"):]), ",") {
			v, _ := strconv.ParseFloat(strings.TrimSpace(f), 64)
			a.diam = append(a.diam, v)
		}
		a.res.FilamentMM = a.diam
	}
}

func (a *analyzer) diameter(tool int) float64 {
	if tool >= 0 && tool < len(a.diam) && a.diam[tool] > 0 {
		return a.diam[tool]
	}
	if len(a.diam) > 0 && a.diam[0] > 0 {
		return a.diam[0]
	}
	return 1.75
}

func (a *analyzer) featureOK() bool {
	return len(a.wantFeat) == 0 || a.wantFeat[strings.ToLower(a.feature)]
}

// points samples the path of a move: a line is its two ends, an arc is cut
// into chords of at most 0.05 mm, with the full turns of P.
func points(mv Move) (pts [][2]float64, length float64) {
	if !mv.Arc {
		return [][2]float64{{mv.X0, mv.Y0}, {mv.X1, mv.Y1}}, math.Hypot(mv.X1-mv.X0, mv.Y1-mv.Y0)
	}
	cx, cy := mv.X0+mv.I, mv.Y0+mv.J
	r := math.Hypot(mv.I, mv.J)
	if r < 1e-9 {
		return [][2]float64{{mv.X0, mv.Y0}, {mv.X1, mv.Y1}}, math.Hypot(mv.X1-mv.X0, mv.Y1-mv.Y0)
	}
	a0 := math.Atan2(mv.Y0-cy, mv.X0-cx)
	a1 := math.Atan2(mv.Y1-cy, mv.X1-cx)
	var sweep float64 // positive counter clockwise
	if mv.Clockwise {
		sweep = a0 - a1
	} else {
		sweep = a1 - a0
	}
	for sweep <= 1e-9 {
		sweep += 2 * math.Pi
	}
	if math.Hypot(mv.X1-mv.X0, mv.Y1-mv.Y0) < 1e-6 {
		sweep = 2 * math.Pi
	}
	if mv.P > 1 {
		sweep += 2 * math.Pi * (mv.P - 1)
	}
	if mv.Clockwise {
		sweep = -sweep
	}
	length = r * math.Abs(sweep)
	n := int(math.Ceil(length / 0.05))
	if n < 1 {
		n = 1
	}
	pts = make([][2]float64, 0, n+1)
	for k := 0; k < n; k++ {
		ang := a0 + sweep*float64(k)/float64(n)
		pts = append(pts, [2]float64{cx + r*math.Cos(ang), cy + r*math.Sin(ang)})
	}
	pts = append(pts, [2]float64{mv.X1, mv.Y1})
	return pts, length
}

func (a *analyzer) endRun() {
	r := &a.run
	if !r.active {
		return
	}
	r.active = false
	if r.obj == nil || !a.on(MeasureShortRuns) || !a.selected(r.obj) || !a.layerIn(a.layer, a.z) {
		return
	}
	if len(a.wantFeat) > 0 && !a.wantFeat[strings.ToLower(r.feature)] {
		return
	}
	row := r.obj.runs[a.layer]
	if row == nil {
		row = &ShortRunRow{Object: r.obj.ref.Label, Layer: a.layer, Z: a.z}
		r.obj.runs[a.layer] = row
	}
	row.Runs++
	if r.length < a.minRun {
		row.Short++
		if len(row.Ends) < 20 {
			row.Ends = append(row.Ends, r.last)
		}
	}
}

func (a *analyzer) move(mv Move) {
	if !mv.Extruding || mv.E <= 0 || a.inWipe {
		a.endRun()
		return
	}
	if a.layer < 1 || a.cur == nil || a.feature == "" {
		return // the prime tower and anything outside an object belong to no object
	}
	o := a.cur
	pts, length := points(mv)
	if a.run.active && (a.run.obj != o || a.run.feature != a.feature) {
		a.endRun()
	}
	if !a.run.active {
		a.run.active, a.run.obj, a.run.feature, a.run.length = true, o, a.feature, 0
	}
	a.run.length += length
	a.run.last = pts[len(pts)-1]
	if !a.selected(o) {
		return
	}
	inRange := a.layerIn(a.layer, a.z)
	// The raster also needs the layer below the first one asked for.
	if (a.on(MeasureUnsupportedStart) || a.on(MeasureSupportContacts)) && (inRange || a.layerIn(a.layer+1, a.z)) {
		byFeat := a.rast[o.ref.Label]
		if byFeat == nil {
			byFeat = map[string]*grid{}
			a.rast[o.ref.Label] = byFeat
		}
		g := byFeat[a.feature]
		if g == nil {
			g = newGrid()
			byFeat[a.feature] = g
		}
		g.path(pts, a.width)
	}
	if !inRange || !a.featureOK() {
		return
	}
	o.extruded[a.layer] = true
	fl := o.feat[a.feature]
	if fl == nil {
		fl = &FeatureLayers{Feature: a.feature, First: a.layer, Last: a.layer}
		o.feat[a.feature] = fl
	}
	fl.Last = a.layer
	if a.on(MeasureBounds) {
		for _, k := range []bkey{{a.feature, a.layer}, {a.feature, 0}} {
			row := o.bounds[k]
			if row == nil {
				row = &BoundsRow{Object: o.ref.Label, Feature: a.feature, Layer: k.layer, Z: a.z, MinX: math.Inf(1), MinY: math.Inf(1), MaxX: math.Inf(-1), MaxY: math.Inf(-1)}
				if k.layer == 0 {
					row.Z = 0
				}
				o.bounds[k] = row
			}
			for _, p := range pts {
				row.MinX, row.MaxX = math.Min(row.MinX, p[0]), math.Max(row.MaxX, p[0])
				row.MinY, row.MaxY = math.Min(row.MinY, p[1]), math.Max(row.MaxY, p[1])
			}
		}
	}
	if a.on(MeasureFlow) && a.width > 0 && a.height > 0 && length > 0 {
		d := a.diameter(mv.Tool)
		ratio := mv.E * math.Pi * d * d / 4 / (length * a.width * a.height)
		keys := []bkey{{a.feature, 0}}
		if a.opt.PerLayer {
			keys = append(keys, bkey{a.feature, a.layer})
		}
		for _, k := range keys {
			fa := o.flow[k]
			if fa == nil {
				fa = &flowAcc{hist: map[int32]uint32{}}
				o.flow[k] = fa
			}
			fa.add(ratio, length, mv.E)
		}
	}
	if a.on(MeasureRadius) {
		c := a.opt.Center
		for _, k := range []bkey{{a.feature, a.layer}, {a.feature, 0}} {
			row := o.radius[k]
			if row == nil {
				row = &RadiusRow{Object: o.ref.Label, Feature: a.feature, Layer: k.layer, Min: math.Inf(1), Max: math.Inf(-1)}
				o.radius[k] = row
			}
			for i, p := range pts {
				row.Max = math.Max(row.Max, math.Hypot(p[0]-c[0], p[1]-c[1]))
				if i > 0 {
					row.Min = math.Min(row.Min, segDist(pts[i-1], p, *c))
				}
			}
		}
	}
	if a.on(MeasureWallOrder) {
		ord := o.order[a.layer]
		if ord == nil {
			ord = &[2]int{}
			o.order[a.layer] = ord
		}
		a.seq++
		switch strings.ToLower(a.feature) {
		case "outer wall":
			if ord[0] == 0 {
				ord[0] = a.seq
			}
		case "inner wall":
			if ord[1] == 0 {
				ord[1] = a.seq
			}
		}
	}
}

func segDist(a, b, c [2]float64) float64 {
	dx, dy := b[0]-a[0], b[1]-a[1]
	l2 := dx*dx + dy*dy
	if l2 == 0 {
		return math.Hypot(c[0]-a[0], c[1]-a[1])
	}
	t := math.Max(0, math.Min(1, ((c[0]-a[0])*dx+(c[1]-a[1])*dy)/l2))
	return math.Hypot(c[0]-(a[0]+t*dx), c[1]-(a[1]+t*dy))
}

func isBridge(feature string) bool  { return strings.Contains(strings.ToLower(feature), "bridge") }
func isSupport(feature string) bool { return strings.Contains(strings.ToLower(feature), "support") }

func cellsToMM(c int) float64 { return float64(c) * gridCell }

// finishLayer closes the layer that was being read: the island checks use the
// rasters of this layer and of the one below, and only those two are kept.
func (a *analyzer) finishLayer() {
	defer func() { a.rast = map[string]map[string]*grid{} }()
	if a.layer < 1 || !(a.on(MeasureUnsupportedStart) || a.on(MeasureSupportContacts)) {
		return
	}
	inRange := a.layerIn(a.layer, a.z)
	labels := make([]string, 0, len(a.rast))
	for l := range a.rast {
		labels = append(labels, l)
	}
	sort.Strings(labels)
	// support patches below: the objects that print on them within the z gap
	if a.on(MeasureSupportContacts) && len(a.pending) > 0 {
		a.resolvePending(labels, false)
	}
	below := a.belowGrid()
	all := newGrid()
	for _, l := range labels {
		o := a.objs[l]
		byFeat := a.rast[l]
		union := newGrid()
		var feats []string
		for ft := range byFeat {
			feats = append(feats, ft)
		}
		sort.Strings(feats)
		for _, ft := range feats {
			all.union(byFeat[ft])
			if !isBridge(ft) && !isSupport(ft) {
				union.union(byFeat[ft])
			}
			if a.on(MeasureSupportContacts) && inRange && isSupport(ft) && (len(a.wantFeat) == 0 || a.wantFeat[strings.ToLower(ft)]) {
				for _, p := range byFeat[ft].mergedPatches(a.mergeCells(ft)) {
					a.pending = append(a.pending, pendingCluster{obj: o, feature: ft, layer: a.layer, z: a.z, patch: p,
						limit: a.z + cfgNum(a.cfg, "support_top_z_distance", 0.2) + 2*math.Max(a.height, 0.05), names: map[string]bool{}})
				}
			}
		}
		if !a.on(MeasureUnsupportedStart) || !inRange || a.layer <= 1 {
			continue
		}
		for _, isl := range union.islands() {
			covered := 0
			for _, r := range isl.runs {
				covered += below.countRun(r.y, r.x0, r.x1)
			}
			pct := 100 * float64(covered) / float64(isl.cells)
			if pct >= 10 {
				continue
			}
			if !a.islandWanted(isl, byFeat, feats) {
				continue
			}
			si := StartIsland{Object: o.ref.Label, Layer: a.layer, Z: a.z, MinX: cellsToMM(isl.minX), MaxX: cellsToMM(isl.maxX + 1), MinY: cellsToMM(isl.minY), MaxY: cellsToMM(isl.maxY + 1),
				AreaMM2: float64(isl.cells) * gridCell * gridCell, SupportedPercent: pct}
			for _, ft := range feats {
				if isBridge(ft) || isSupport(ft) {
					continue
				}
				if hitsFeature(isl, byFeat[ft]) {
					si.Features = append(si.Features, ft)
				}
			}
			a.startIslands = append(a.startIslands, si)
		}
	}
	a.hist = append(a.hist, histLayer{z: a.z, all: all})
}

// finish turns the accumulators into the result rows.
func (a *analyzer) finish() {
	res := a.res
	res.AllLabels = append([]string(nil), a.labels...)
	sort.Strings(res.AllLabels)
	// display names: the name, with the id and copy when several objects share it
	count := map[string]int{}
	for _, l := range a.labels {
		count[a.objs[l].ref.Name]++
	}
	display := map[string]string{}
	for _, l := range a.labels {
		o := a.objs[l]
		d := o.ref.Name
		if count[d] > 1 {
			d = fmt.Sprintf("%s#%d", d, o.ref.ID)
			if o.ref.Copy > 0 {
				d += fmt.Sprintf(".%d", o.ref.Copy)
			}
		}
		o.ref.Display = d
		display[l] = d
		if a.selected(o) {
			res.Objects = append(res.Objects, o.ref)
		}
	}
	sort.SliceStable(res.Objects, func(i, j int) bool { return res.Objects[i].Display < res.Objects[j].Display })
	byDisplay := func(l string) string { return display[l] }
	ordered := make([]*objAcc, 0, len(a.labels))
	for _, l := range a.labels {
		if o := a.objs[l]; a.selected(o) {
			ordered = append(ordered, o)
		}
	}
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].ref.Display < ordered[j].ref.Display })
	for _, o := range ordered {
		name := o.ref.Display
		if a.on(MeasureFirstLayers) && len(o.extruded) > 0 {
			row := FirstLayersRow{Object: name, FirstLayer: math.MaxInt32}
			for l := range o.extruded {
				row.FirstLayer, row.LastLayer = min(row.FirstLayer, l), max(row.LastLayer, l)
			}
			for l := row.FirstLayer; l <= row.LastLayer; l++ {
				if !o.extruded[l] {
					row.Gaps = append(row.Gaps, l)
				}
			}
			for _, fl := range o.feat {
				row.Features = append(row.Features, *fl)
			}
			sort.Slice(row.Features, func(i, j int) bool {
				if row.Features[i].First != row.Features[j].First {
					return row.Features[i].First < row.Features[j].First
				}
				return row.Features[i].Feature < row.Features[j].Feature
			})
			res.FirstLayers = append(res.FirstLayers, row)
		}
		for k, b := range o.bounds {
			_ = k
			r := *b
			r.Object = name
			res.Bounds = append(res.Bounds, r)
		}
		for k, f := range o.flow {
			res.Flow = append(res.Flow, FlowRow{Object: name, Feature: k.feature, Layer: k.layer, Segments: f.segs, LengthMM: f.length, EMM: f.e,
				EPerMM: f.e / math.Max(f.length, 1e-9), Median: f.quantile(0.5), P01: f.quantile(0.01), P99: f.quantile(0.99), Max: f.max})
		}
		for _, r := range o.radius {
			rr := *r
			rr.Object = name
			res.Radius = append(res.Radius, rr)
		}
		if a.on(MeasureShortRuns) {
			total := ShortRunRow{Object: name}
			for _, r := range o.runs {
				rr := *r
				rr.Object = name
				if rr.Short > 0 {
					res.ShortRuns = append(res.ShortRuns, rr)
				}
				total.Runs += r.Runs
				total.Short += r.Short
			}
			res.ShortRuns = append(res.ShortRuns, total)
		}
		if a.on(MeasureWallOrder) {
			row := WallOrderRow{Object: name}
			var layers []int
			for l := range o.order {
				layers = append(layers, l)
			}
			sort.Ints(layers)
			for _, l := range layers {
				ord := o.order[l]
				if ord[0] == 0 || ord[1] == 0 {
					continue
				}
				if ord[0] < ord[1] {
					row.OuterFirst++
				} else {
					row.InnerFirst++
					if len(row.InnerFirstLayers) < 50 {
						row.InnerFirstLayers = append(row.InnerFirstLayers, l)
					}
				}
			}
			res.WallOrder = append(res.WallOrder, row)
		}
	}
	for _, s := range a.startIslands {
		s.Object = byDisplay(s.Object)
		res.UnsupportedStarts = append(res.UnsupportedStarts, s)
	}
	for _, c := range a.supportClusters {
		c.Object = byDisplay(c.Object)
		for i, l := range c.Above {
			if l != SupportAbove || a.objs[l] != nil {
				c.Above[i] = byDisplay(l)
			}
		}
		sort.Strings(c.Above)
		res.SupportContacts = append(res.SupportContacts, c)
	}
	sort.SliceStable(res.Bounds, func(i, j int) bool {
		return lessRow(res.Bounds[i].Object, res.Bounds[i].Feature, res.Bounds[i].Layer, res.Bounds[j].Object, res.Bounds[j].Feature, res.Bounds[j].Layer)
	})
	sort.SliceStable(res.Flow, func(i, j int) bool {
		return lessRow(res.Flow[i].Object, res.Flow[i].Feature, res.Flow[i].Layer, res.Flow[j].Object, res.Flow[j].Feature, res.Flow[j].Layer)
	})
	sort.SliceStable(res.Radius, func(i, j int) bool {
		return lessRow(res.Radius[i].Object, res.Radius[i].Feature, res.Radius[i].Layer, res.Radius[j].Object, res.Radius[j].Feature, res.Radius[j].Layer)
	})
	sort.SliceStable(res.ShortRuns, func(i, j int) bool {
		return res.ShortRuns[i].Object < res.ShortRuns[j].Object || (res.ShortRuns[i].Object == res.ShortRuns[j].Object && res.ShortRuns[i].Layer < res.ShortRuns[j].Layer)
	})
	sort.SliceStable(res.UnsupportedStarts, func(i, j int) bool { return res.UnsupportedStarts[i].Layer < res.UnsupportedStarts[j].Layer })
	sort.SliceStable(res.SupportContacts, func(i, j int) bool { return res.SupportContacts[i].Layer < res.SupportContacts[j].Layer })
}

// lessRow orders rows by object, then layer (the whole range, layer 0, first
// within a feature), then feature.
func lessRow(ao, af string, al int, bo, bf string, bl int) bool {
	if ao != bo {
		return ao < bo
	}
	if af != bf {
		return af < bf
	}
	return al < bl
}

// islandWanted tells whether an island is reported under the features filter: it
// must contain extrusion of a requested feature. The rasters hold every feature
// whatever the filter says, so the layer below and the island detection do not
// depend on it.
func (a *analyzer) islandWanted(isl island, byFeat map[string]*grid, feats []string) bool {
	if len(a.wantFeat) == 0 {
		return true
	}
	for _, ft := range feats {
		if isBridge(ft) || isSupport(ft) || !a.wantFeat[strings.ToLower(ft)] {
			continue
		}
		if hitsFeature(isl, byFeat[ft]) {
			return true
		}
	}
	return false
}

func hitsFeature(isl island, g *grid) bool {
	for _, r := range isl.runs {
		if g.anyRun(r.y, r.x0, r.x1) {
			return true
		}
	}
	return false
}

// histLayer is the extrusion of everything printed in one layer (objects and
// support), kept for the layers that can still lie under a later one.
type histLayer struct {
	z   float64
	all *grid
}

// belowGrid is what a layer at the current z stands on: the extrusion of every
// layer whose top lies at most the current layer's height plus the largest layer
// height seen below its bottom. With independent support layer heights the slicer
// writes layers that hold only support between object layers, so the layer just
// before the current one is not the one under it. Layers further down are dropped.
func (a *analyzer) belowGrid() *grid {
	a.maxH = math.Max(a.maxH, a.height)
	low := a.z - math.Max(a.height, 0.05) - math.Max(a.maxH, 0.05) - 1e-6
	kept := a.hist[:0]
	for _, h := range a.hist {
		if h.z >= low {
			kept = append(kept, h)
		}
	}
	a.hist = kept
	below := newGrid()
	for _, h := range a.hist {
		if h.z < a.z-1e-9 {
			below.union(h.all)
		}
	}
	return below
}

// restartByObject begins the layer numbering again: the next object of a plate
// printed by object.
func (a *analyzer) restartByObject() {
	a.layer = 1
	a.hist = nil
	a.flushPending()
	a.res.ByObject = true
	a.res.Layers = max(a.res.Layers, a.layer)
}

// decideEqualZ settles a layer that repeated the height of the one before, when
// its first object is known: the same object continues its layer (the closing
// loop of vase mode, counted with the layer before), another object starts its
// own sequence (print by object).
func (a *analyzer) decideEqualZ(first string) {
	a.equalZ = false
	if first == a.refObject || a.refObject == "" {
		a.layer--
		return
	}
	a.restartByObject()
}
