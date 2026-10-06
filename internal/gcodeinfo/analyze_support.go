package gcodeinfo

import (
	"bytes"
	"io"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
)

// configTail reads the key = value lines of the config block from the end of a
// plate G-code (the block follows the toolpath, so it is read from the tail
// instead of with an extra pass). Missing keys give an empty map.
func configTail(path string) map[string]string {
	out := map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return out
	}
	const tail = 2 << 20
	start := st.Size() - tail
	if start < 0 {
		start = 0
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return out
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return out
	}
	i := bytes.LastIndex(data, []byte("; CONFIG_BLOCK_START"))
	if i < 0 {
		return out
	}
	for _, line := range strings.Split(string(data[i:]), "\n") {
		line = strings.TrimRight(line, "\r")
		if !strings.HasPrefix(line, "; ") {
			continue
		}
		if k, v, ok := strings.Cut(line[2:], " = "); ok {
			out[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return out
}

func cfgNum(cfg map[string]string, key string, def float64) float64 {
	if v, ok := cfg[key]; ok {
		first, _, _ := strings.Cut(v, ",")
		if f, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(first), "%"), 64); err == nil {
			return f
		}
	}
	return def
}

// dilated returns a copy of g with every cell grown by d cells in each
// direction (a square structuring element).
func (g *grid) dilated(d int) *grid {
	out := newGrid()
	for _, r := range g.runs() {
		for dy := -d; dy <= d; dy++ {
			out.setSpan(r.y+dy, r.x0-d, r.x1+d)
		}
	}
	return out
}

// patch is a group of marked cells whose runs were merged by dilation.
type patch struct {
	runs                   []run
	cells                  int
	minX, maxX, minY, maxY int
}

// mergedPatches groups the cells of g into patches: cells that lie within
// d cells of each other (the dilation by d joins them) form one patch. The
// patch keeps the cells of g, not the dilated ones, so area and bounds are the
// extrusion's own.
func (g *grid) mergedPatches(d int) []patch {
	if g.empty() {
		return nil
	}
	if d <= 0 {
		var out []patch
		for _, isl := range g.islands() {
			out = append(out, patch{runs: isl.runs, cells: isl.cells, minX: isl.minX, maxX: isl.maxX, minY: isl.minY, maxY: isl.maxY})
		}
		return out
	}
	big := g.dilated(d).islands()
	// rows of the dilated islands, to find the island of a cell
	type seg struct{ x0, x1, k int }
	rows := map[int][]seg{}
	for k, isl := range big {
		for _, r := range isl.runs {
			rows[r.y] = append(rows[r.y], seg{r.x0, r.x1, k})
		}
	}
	for y := range rows {
		s := rows[y]
		sort.Slice(s, func(i, j int) bool { return s[i].x0 < s[j].x0 })
	}
	find := func(x, y int) int {
		s := rows[y]
		i := sort.Search(len(s), func(i int) bool { return s[i].x1 >= x })
		if i < len(s) && s[i].x0 <= x {
			return s[i].k
		}
		return -1
	}
	out := make([]patch, len(big))
	for i := range out {
		out[i] = patch{minX: math.MaxInt32, minY: math.MaxInt32, maxX: math.MinInt32, maxY: math.MinInt32}
	}
	for _, r := range g.runs() {
		k := find(r.x0, r.y)
		if k < 0 {
			continue
		}
		p := &out[k]
		p.runs = append(p.runs, r)
		p.cells += r.x1 - r.x0 + 1
		p.minX, p.maxX = min(p.minX, r.x0), max(p.maxX, r.x1)
		p.minY, p.maxY = min(p.minY, r.y), max(p.maxY, r.y)
	}
	res := out[:0]
	for _, p := range out {
		if p.cells > 0 {
			res = append(res, p)
		}
	}
	return res
}

// touches reports whether the patch, grown by d cells, meets a marked cell of g.
func (p patch) touches(g *grid, d int) bool {
	for _, r := range p.runs {
		for dy := -d; dy <= d; dy++ {
			if g.anyRun(r.y+dy, r.x0-d, r.x1+d) {
				return true
			}
		}
	}
	return false
}

// pendingCluster is a support patch waiting for the layers above it: the object
// is separated from the support by a gap in z (support_top_z_distance), so the
// objects that print on it show up a few layers later, not on the next one.
type pendingCluster struct {
	obj     *objAcc
	feature string
	layer   int
	z       float64
	patch   patch
	limit   float64 // the z up to which an object layer counts as printed on it
	names   map[string]bool
	// supportAbove: more support of the same object prints over the patch.
	supportAbove bool
}

// mergeCells is how far (in cells) the strips of a support feature are grown to
// join into one patch: half the line pitch, which is the spacing of that
// feature plus the line width.
func (a *analyzer) mergeCells(feature string) int {
	width := cfgNum(a.cfg, "support_line_width", 0.42)
	spacing := cfgNum(a.cfg, "support_base_pattern_spacing", 2.5)
	if strings.Contains(strings.ToLower(feature), "interface") {
		spacing = cfgNum(a.cfg, "support_interface_spacing", 0.5)
	}
	return int(math.Ceil((spacing + width) / 2 / gridCell))
}

// resolvePending checks the layer just finished against the waiting patches:
// an object of that layer whose model extrusion lies within the XY distance of a
// patch prints on it. A patch whose z window is over is recorded; with flush all
// patches are recorded.
func (a *analyzer) resolvePending(labels []string, flush bool) {
	xy := int(math.Ceil(cfgNum(a.cfg, "support_object_xy_distance", 0.35)/gridCell)) + 1
	var above map[string]*grid
	var keep []pendingCluster
	for _, pc := range a.pending {
		if !flush && a.z <= pc.limit+1e-9 {
			if above == nil {
				above = map[string]*grid{}
				for _, l := range labels {
					g := newGrid()
					for ft, fg := range a.rast[l] {
						if !isSupport(ft) {
							g.union(fg)
						}
					}
					above[l] = g
				}
			}
			for _, l := range labels {
				if !pc.names[l] && pc.patch.touches(above[l], xy) {
					pc.names[l] = true
				}
			}
			// support of the same object printed above it: a lower support column
			if !pc.supportAbove {
				for ft, fg := range a.rast[pc.obj.ref.Label] {
					if isSupport(ft) && pc.patch.touches(fg, 1) {
						pc.supportAbove = true
						break
					}
				}
			}
			keep = append(keep, pc)
			continue
		}
		a.recordSupport(pc)
	}
	a.pending = keep
}

func (a *analyzer) flushPending() {
	a.resolvePending(nil, true)
}

func (a *analyzer) recordSupport(pc pendingCluster) {
	var names []string
	for l := range pc.names {
		names = append(names, l)
	}
	sort.Strings(names)
	if len(names) == 0 && pc.supportAbove {
		names = []string{SupportAbove}
	}
	a.supportClusters = append(a.supportClusters, SupportCluster{
		Object: pc.obj.ref.Label, Feature: pc.feature, Layer: pc.layer, Z: pc.z,
		MinX: cellsToMM(pc.patch.minX), MaxX: cellsToMM(pc.patch.maxX + 1), MinY: cellsToMM(pc.patch.minY), MaxY: cellsToMM(pc.patch.maxY + 1),
		AreaMM2: float64(pc.patch.cells) * gridCell * gridCell, Above: names,
	})
}

// SupportAbove is the "printed on it" entry of a support patch that has more
// support, and no object, printing over it.
const SupportAbove = "support"
