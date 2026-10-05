package gcodeinfo

import (
	"math"
	"math/bits"
	"sort"
)

// gridCell is the raster resolution of the toolpath analysis, in mm.
const gridCell = 0.1

// grid is a sparse raster of 0.1 mm cells kept in 64 by 64 cell tiles, so a
// plate G-code of any size needs memory for the cells that are marked only.
type grid struct {
	tiles map[[2]int32]*[64]uint64
}

func newGrid() *grid { return &grid{tiles: map[[2]int32]*[64]uint64{}} }

func floorDiv(a, b int) int {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}

// setSpan marks the cells ix0..ix1 of row iy.
func (g *grid) setSpan(iy, ix0, ix1 int) {
	if ix1 < ix0 {
		return
	}
	ty := floorDiv(iy, 64)
	row := iy - ty*64
	for tx := floorDiv(ix0, 64); tx <= floorDiv(ix1, 64); tx++ {
		lo, hi := ix0-tx*64, ix1-tx*64
		if lo < 0 {
			lo = 0
		}
		if hi > 63 {
			hi = 63
		}
		var mask uint64
		if hi-lo == 63 {
			mask = ^uint64(0)
		} else {
			mask = ((uint64(1) << uint(hi-lo+1)) - 1) << uint(lo)
		}
		key := [2]int32{int32(tx), int32(ty)}
		t := g.tiles[key]
		if t == nil {
			t = new([64]uint64)
			g.tiles[key] = t
		}
		t[row] |= mask
	}
}

func (g *grid) has(ix, iy int) bool {
	t := g.tiles[[2]int32{int32(floorDiv(ix, 64)), int32(floorDiv(iy, 64))}]
	if t == nil {
		return false
	}
	return t[iy-floorDiv(iy, 64)*64]>>uint(ix-floorDiv(ix, 64)*64)&1 == 1
}

func (g *grid) set(ix, iy int) { g.setSpan(iy, ix, ix) }

// stamp marks the cells of a disc of radius r mm around (x, y).
func (g *grid) stamp(x, y, r float64) {
	y0 := int(math.Floor((y - r) / gridCell))
	y1 := int(math.Floor((y + r) / gridCell))
	for iy := y0; iy <= y1; iy++ {
		cy := (float64(iy) + 0.5) * gridCell
		d := r*r - (cy-y)*(cy-y)
		if d < 0 {
			continue
		}
		hw := math.Sqrt(d)
		ix0 := int(math.Floor((x - hw) / gridCell))
		ix1 := int(math.Floor((x + hw) / gridCell))
		g.setSpan(iy, ix0, ix1)
	}
}

// path marks a polyline of segments, dilated by half the line width. A disc is
// stamped every 0.8 radius along the path, wherever the vertices of an
// arc fall: short chords do not multiply the work.
func (g *grid) path(pts [][2]float64, width float64) {
	r := math.Max(width/2, gridCell/2)
	step := r * 0.8 // discs overlap enough that the scallops are far below a cell
	g.stamp(pts[0][0], pts[0][1], r)
	carry := 0.0 // distance since the last stamp
	for i := 1; i < len(pts); i++ {
		a, b := pts[i-1], pts[i]
		l := math.Hypot(b[0]-a[0], b[1]-a[1])
		if l == 0 {
			continue
		}
		pos := step - carry
		for pos <= l {
			t := pos / l
			g.stamp(a[0]+(b[0]-a[0])*t, a[1]+(b[1]-a[1])*t, r)
			pos += step
		}
		carry = l - (pos - step)
	}
	last := pts[len(pts)-1]
	if carry > 1e-9 {
		g.stamp(last[0], last[1], r)
	}
}

// each calls fn for every marked cell.
func (g *grid) each(fn func(ix, iy int)) {
	for key, t := range g.tiles {
		for row := 0; row < 64; row++ {
			w := t[row]
			for w != 0 {
				bit := trailingZeros(w)
				w &= w - 1
				fn(int(key[0])*64+bit, int(key[1])*64+row)
			}
		}
	}
}

func trailingZeros(w uint64) int {
	n := 0
	for w&1 == 0 {
		w >>= 1
		n++
	}
	return n
}

func (g *grid) empty() bool { return len(g.tiles) == 0 }

// union adds the cells of o to g.
func (g *grid) union(o *grid) {
	for key, t := range o.tiles {
		d := g.tiles[key]
		if d == nil {
			d = new([64]uint64)
			g.tiles[key] = d
		}
		for i := range t {
			d[i] |= t[i]
		}
	}
}

// run is a horizontal stretch of marked cells on one row.
type run struct{ y, x0, x1 int }

// wordMask returns the bits of cells x0..x1 inside the tile column tx.
func wordMask(tx, x0, x1 int) (uint64, bool) {
	lo, hi := x0-tx*64, x1-tx*64
	if hi < 0 || lo > 63 {
		return 0, false
	}
	if lo < 0 {
		lo = 0
	}
	if hi > 63 {
		hi = 63
	}
	if hi-lo == 63 {
		return ^uint64(0), true
	}
	return ((uint64(1) << uint(hi-lo+1)) - 1) << uint(lo), true
}

// countRun is the number of marked cells among x0..x1 of row y.
func (g *grid) countRun(y, x0, x1 int) int {
	ty := floorDiv(y, 64)
	row := y - ty*64
	n := 0
	for tx := floorDiv(x0, 64); tx <= floorDiv(x1, 64); tx++ {
		t := g.tiles[[2]int32{int32(tx), int32(ty)}]
		if t == nil {
			continue
		}
		if m, ok := wordMask(tx, x0, x1); ok {
			n += bits.OnesCount64(t[row] & m)
		}
	}
	return n
}

// anyRun reports whether a cell among x0..x1 of row y is marked.
func (g *grid) anyRun(y, x0, x1 int) bool { return g.countRun(y, x0, x1) > 0 }

// runs lists the marked cells as horizontal runs, sorted by row then column.
func (g *grid) runs() []run {
	var out []run
	for key, t := range g.tiles {
		for row := 0; row < 64; row++ {
			w := t[row]
			y := int(key[1])*64 + row
			base := int(key[0]) * 64
			for w != 0 {
				lo := bits.TrailingZeros64(w)
				shifted := w >> uint(lo)
				n := bits.TrailingZeros64(^shifted)
				if n >= 64-lo {
					n = 64 - lo
				}
				out = append(out, run{y, base + lo, base + lo + n - 1})
				if lo+n >= 64 {
					break
				}
				w &^= ((uint64(1) << uint(n)) - 1) << uint(lo)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].y != out[j].y {
			return out[i].y < out[j].y
		}
		return out[i].x0 < out[j].x0
	})
	// merge runs that touch across tile borders
	m := out[:0]
	for _, r := range out {
		if k := len(m); k > 0 && m[k-1].y == r.y && m[k-1].x1+1 >= r.x0 {
			if r.x1 > m[k-1].x1 {
				m[k-1].x1 = r.x1
			}
			continue
		}
		m = append(m, r)
	}
	return m
}

// island is a group of 8-connected cells, kept as runs.
type island struct {
	runs                   []run
	cells                  int
	minX, maxX, minY, maxY int
}

// islands splits the marked cells of g into 8-connected groups. It joins runs of
// neighbouring rows with a union-find, so the cost follows the number of runs,
// not the number of cells.
func (g *grid) islands() []island {
	rs := g.runs()
	if len(rs) == 0 {
		return nil
	}
	parent := make([]int, len(rs))
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(i int) int {
		for parent[i] != i {
			parent[i] = parent[parent[i]]
			i = parent[i]
		}
		return i
	}
	union := func(a, b int) {
		ra, rb := find(a), find(b)
		if ra != rb {
			parent[rb] = ra
		}
	}
	// rows are consecutive blocks in rs
	start := 0
	for start < len(rs) {
		end := start
		for end < len(rs) && rs[end].y == rs[start].y {
			end++
		}
		next := end
		for next < len(rs) && rs[next].y == rs[start].y+1 {
			next++
		}
		// runs of row y at [start,end), runs of row y+1 at [end,next)
		i, j := start, end
		for i < end && j < next {
			if rs[i].x0 <= rs[j].x1+1 && rs[j].x0 <= rs[i].x1+1 {
				union(i, j)
			}
			if rs[i].x1 < rs[j].x1 {
				i++
			} else {
				j++
			}
		}
		start = end
	}
	byRoot := map[int]*island{}
	var order []int
	for i, r := range rs {
		root := find(i)
		isl := byRoot[root]
		if isl == nil {
			isl = &island{minX: r.x0, maxX: r.x1, minY: r.y, maxY: r.y}
			byRoot[root] = isl
			order = append(order, root)
		}
		isl.runs = append(isl.runs, r)
		isl.cells += r.x1 - r.x0 + 1
		isl.minX, isl.maxX = min(isl.minX, r.x0), max(isl.maxX, r.x1)
		isl.minY, isl.maxY = min(isl.minY, r.y), max(isl.maxY, r.y)
	}
	out := make([]island, 0, len(order))
	for _, root := range order {
		out = append(out, *byRoot[root])
	}
	return out
}
