package mesh

import (
	"math"
	"sort"
)

// LayFlatToleranceDegrees is the angle within which triangle normals count as
// one face group.
const LayFlatToleranceDegrees = 1.0

// LayFlat returns a rotation about the origin that puts the model on its
// largest flat face. Triangles are grouped by normal (within one degree), the
// areas of each group are summed, and among the groups the one with the
// largest area lying on the boundary of the convex hull (a plane with every
// vertex on one side) wins. The result maps that group's normal to -Z, so the
// face ends up down. Follow it with LayOnBed to put the model at z = 0.
//
// ok is false when the mesh has no usable face; the identity is returned then.
func LayFlat(m *Mesh) (rot Matrix, ok bool) {
	type tri struct {
		n    vec3
		area float64
	}
	tris := make([]tri, 0, len(m.Triangles))
	for _, t := range m.Triangles {
		a, b, c := vec(m.Vertices[t[0]]), vec(m.Vertices[t[1]]), vec(m.Vertices[t[2]])
		cr := cross(sub(b, a), sub(c, a))
		l := length(cr)
		if l < 1e-12 {
			continue
		}
		tris = append(tris, tri{vec3{cr[0] / l, cr[1] / l, cr[2] / l}, l / 2})
	}
	if len(tris) == 0 {
		return Identity(), false
	}

	// Bin normals into half degree cells first (so a million triangles cost a
	// million map hits, not a million group searches), then merge the cells.
	const cell = 0.5 * math.Pi / 180
	type binKey struct{ theta, phi int }
	type bin struct {
		sum  vec3 // area weighted normal sum
		area float64
	}
	bins := map[binKey]*bin{}
	for _, t := range tris {
		theta := math.Acos(clamp(t.n[2], -1, 1))
		phi := math.Atan2(t.n[1], t.n[0])
		k := binKey{int(theta / cell), int(math.Floor(phi / cell))}
		b := bins[k]
		if b == nil {
			b = &bin{}
			bins[k] = b
		}
		b.sum = vec3{b.sum[0] + t.n[0]*t.area, b.sum[1] + t.n[1]*t.area, b.sum[2] + t.n[2]*t.area}
		b.area += t.area
	}
	cells := make([]*bin, 0, len(bins))
	for _, b := range bins {
		cells = append(cells, b)
	}
	sort.Slice(cells, func(i, j int) bool { return cells[i].area > cells[j].area })

	type group struct {
		seed vec3
		sum  vec3
		area float64
	}
	var groups []*group
	cosTol := math.Cos(LayFlatToleranceDegrees * math.Pi / 180)
	for _, c := range cells {
		n := unit(c.sum)
		var g *group
		for _, cand := range groups {
			if dot(cand.seed, n) >= cosTol {
				g = cand
				break
			}
		}
		if g == nil {
			g = &group{seed: n}
			groups = append(groups, g)
		}
		g.sum = vec3{g.sum[0] + c.sum[0], g.sum[1] + c.sum[1], g.sum[2] + c.sum[2]}
		g.area += c.area
	}
	sort.SliceStable(groups, func(i, j int) bool { return groups[i].area > groups[j].area })

	// Diagonal of the model for the flatness tolerance.
	bb, _ := m.BBox()
	sz := bb.Size()
	eps := 1e-5*math.Sqrt(float64(sz[0]*sz[0]+sz[1]*sz[1]+sz[2]*sz[2])) + 1e-6

	const candidates = 50
	bestArea := 0.0
	var bestNormal vec3
	for gi, g := range groups {
		if gi >= candidates {
			break
		}
		n := unit(g.sum)
		// The supporting plane with this normal: everything is below it.
		d := math.Inf(-1)
		for _, v := range m.Vertices {
			if p := dot(n, vec(v)); p > d {
				d = p
			}
		}
		// Area of the triangles that lie on that plane.
		area := 0.0
		for _, t := range m.Triangles {
			a, b, c := vec(m.Vertices[t[0]]), vec(m.Vertices[t[1]]), vec(m.Vertices[t[2]])
			if dot(n, a) >= d-eps && dot(n, b) >= d-eps && dot(n, c) >= d-eps {
				area += triArea(a, b, c)
			}
		}
		if area > bestArea {
			bestArea, bestNormal = area, n
		}
	}
	if bestArea == 0 {
		return Identity(), false
	}
	return rotationTo(bestNormal, vec3{0, 0, -1}), true
}

func clamp(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }

func unit(v vec3) vec3 {
	l := length(v)
	if l == 0 {
		return v
	}
	return vec3{v[0] / l, v[1] / l, v[2] / l}
}

// rotationTo returns the rotation about the origin taking unit vector a to unit vector b.
func rotationTo(a, b vec3) Matrix {
	c := dot(a, b)
	if c > 1-1e-12 {
		return Identity()
	}
	if c < -1+1e-12 {
		// Opposite: turn half a circle about any axis perpendicular to a.
		axis := vec3{1, 0, 0}
		if math.Abs(a[0]) > 0.9 {
			axis = vec3{0, 1, 0}
		}
		axis = unit(cross(a, axis))
		return axisAngle(axis, math.Pi)
	}
	axis := unit(cross(a, b))
	return axisAngle(axis, math.Acos(clamp(c, -1, 1)))
}

// axisAngle is Rodrigues' rotation about a unit axis, in the 3MF row vector layout.
func axisAngle(k vec3, angle float64) Matrix {
	s, c := math.Sincos(angle)
	t := 1 - c
	// Column vector matrix L; the 3MF layout stores L(r,c) at element 3*c+r.
	L := [3][3]float64{
		{t*k[0]*k[0] + c, t*k[0]*k[1] - s*k[2], t*k[0]*k[2] + s*k[1]},
		{t*k[0]*k[1] + s*k[2], t*k[1]*k[1] + c, t*k[1]*k[2] - s*k[0]},
		{t*k[0]*k[2] - s*k[1], t*k[1]*k[2] + s*k[0], t*k[2]*k[2] + c},
	}
	var m Matrix
	for col := 0; col < 3; col++ {
		for row := 0; row < 3; row++ {
			m[3*col+row] = L[row][col]
		}
	}
	return m
}
