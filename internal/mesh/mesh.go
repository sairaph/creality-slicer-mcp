// Package mesh reads and builds triangle meshes: STL (binary and ASCII), OBJ,
// the mesh objects of a 3MF, primitive shapes for modifiers, and the affine
// transforms of the 3MF format including the lay flat rotation.
package mesh

import (
	"errors"
	"fmt"
	"math"
)

// Mesh is an indexed triangle mesh. Triangles wind counter clockwise seen from
// outside, so the signed volume of a closed mesh is positive.
type Mesh struct {
	Vertices  [][3]float32
	Triangles [][3]uint32
}

// BBox is an axis aligned bounding box.
type BBox struct {
	Min, Max [3]float32
}

// Size returns the extent along each axis.
func (b BBox) Size() [3]float32 {
	return [3]float32{b.Max[0] - b.Min[0], b.Max[1] - b.Min[1], b.Max[2] - b.Min[2]}
}

// Center returns the middle of the box.
func (b BBox) Center() [3]float32 {
	return [3]float32{(b.Min[0] + b.Max[0]) / 2, (b.Min[1] + b.Max[1]) / 2, (b.Min[2] + b.Max[2]) / 2}
}

// Union returns the smallest box holding both.
func (b BBox) Union(o BBox) BBox {
	for i := 0; i < 3; i++ {
		if o.Min[i] < b.Min[i] {
			b.Min[i] = o.Min[i]
		}
		if o.Max[i] > b.Max[i] {
			b.Max[i] = o.Max[i]
		}
	}
	return b
}

// ErrEmpty is returned for a mesh without triangles.
var ErrEmpty = errors.New("mesh has no triangles")

// BBox returns the bounding box of the vertices. ok is false for a mesh
// without vertices.
func (m *Mesh) BBox() (BBox, bool) {
	if len(m.Vertices) == 0 {
		return BBox{}, false
	}
	b := BBox{Min: m.Vertices[0], Max: m.Vertices[0]}
	for _, v := range m.Vertices[1:] {
		for i := 0; i < 3; i++ {
			if v[i] < b.Min[i] {
				b.Min[i] = v[i]
			}
			if v[i] > b.Max[i] {
				b.Max[i] = v[i]
			}
		}
	}
	return b, true
}

// Validate checks that every triangle index is in range.
func (m *Mesh) Validate() error {
	n := uint32(len(m.Vertices))
	for i, t := range m.Triangles {
		for _, idx := range t {
			if idx >= n {
				return fmt.Errorf("triangle %d references vertex %d of %d", i, idx, n)
			}
		}
	}
	return nil
}

// SignedVolume is the volume enclosed by the triangles, negative when they
// wind inward. Meaningful for closed meshes.
func (m *Mesh) SignedVolume() float64 {
	var sum float64
	for _, t := range m.Triangles {
		a, b, c := vec(m.Vertices[t[0]]), vec(m.Vertices[t[1]]), vec(m.Vertices[t[2]])
		sum += dot(a, cross(b, c))
	}
	return sum / 6
}

// Volume is the absolute enclosed volume in mm3.
func (m *Mesh) Volume() float64 { return math.Abs(m.SignedVolume()) }

// SurfaceArea is the total area of all triangles.
func (m *Mesh) SurfaceArea() float64 {
	var sum float64
	for _, t := range m.Triangles {
		sum += triArea(vec(m.Vertices[t[0]]), vec(m.Vertices[t[1]]), vec(m.Vertices[t[2]]))
	}
	return sum
}

// Stats describes the health of a mesh.
type Stats struct {
	Vertices  int
	Triangles int
	// Degenerate counts triangles with a repeated vertex or (nearly) no area.
	Degenerate int
	// OpenEdges counts edges used by exactly one triangle (holes).
	OpenEdges int
	// NonManifoldEdges counts edges used by more than two triangles.
	NonManifoldEdges int
	// Flipped counts edges whose two triangles traverse it in the same
	// direction (inconsistent winding).
	Flipped int
	// Manifold is true when there are no open, non manifold or flipped edges.
	Manifold bool
}

// Stats computes the mesh statistics.
func (m *Mesh) Stats() Stats {
	s := Stats{Vertices: len(m.Vertices), Triangles: len(m.Triangles)}
	type edge struct{ a, b uint32 }
	type use struct{ fwd, rev int }
	edges := make(map[edge]*use, len(m.Triangles)*3/2)
	for _, t := range m.Triangles {
		if t[0] == t[1] || t[1] == t[2] || t[0] == t[2] {
			s.Degenerate++
			continue
		}
		a, b, c := vec(m.Vertices[t[0]]), vec(m.Vertices[t[1]]), vec(m.Vertices[t[2]])
		if triArea(a, b, c) < 1e-12 {
			s.Degenerate++
			continue
		}
		for i := 0; i < 3; i++ {
			from, to := t[i], t[(i+1)%3]
			key, forward := edge{from, to}, true
			if from > to {
				key, forward = edge{to, from}, false
			}
			u := edges[key]
			if u == nil {
				u = &use{}
				edges[key] = u
			}
			if forward {
				u.fwd++
			} else {
				u.rev++
			}
		}
	}
	for _, u := range edges {
		switch n := u.fwd + u.rev; {
		case n == 1:
			s.OpenEdges++
		case n > 2:
			s.NonManifoldEdges++
		case u.fwd == 2 || u.rev == 2:
			s.Flipped++
		}
	}
	s.Manifold = s.OpenEdges == 0 && s.NonManifoldEdges == 0 && s.Flipped == 0 && s.Triangles > 0
	return s
}

// Transformed returns a copy with every vertex transformed. A transform that
// mirrors (negative determinant) also flips the winding so the mesh stays
// outward facing.
func (m *Mesh) Transformed(t Matrix) *Mesh {
	out := &Mesh{Vertices: make([][3]float32, len(m.Vertices)), Triangles: make([][3]uint32, len(m.Triangles))}
	for i, v := range m.Vertices {
		out.Vertices[i] = t.ApplyF32(v)
	}
	copy(out.Triangles, m.Triangles)
	if t.Determinant() < 0 {
		for i, tri := range out.Triangles {
			out.Triangles[i] = [3]uint32{tri[0], tri[2], tri[1]}
		}
	}
	return out
}

// Clone copies the mesh.
func (m *Mesh) Clone() *Mesh {
	out := &Mesh{Vertices: make([][3]float32, len(m.Vertices)), Triangles: make([][3]uint32, len(m.Triangles))}
	copy(out.Vertices, m.Vertices)
	copy(out.Triangles, m.Triangles)
	return out
}

// Append adds the triangles of o to m.
func (m *Mesh) Append(o *Mesh) {
	base := uint32(len(m.Vertices))
	m.Vertices = append(m.Vertices, o.Vertices...)
	for _, t := range o.Triangles {
		m.Triangles = append(m.Triangles, [3]uint32{t[0] + base, t[1] + base, t[2] + base})
	}
}

// LayOnBed returns t followed by the translation that puts the lowest point
// of the transformed mesh at z = 0.
func LayOnBed(m *Mesh, t Matrix) Matrix {
	minZ := math.Inf(1)
	for _, v := range m.Vertices {
		if z := t.Apply([3]float64{float64(v[0]), float64(v[1]), float64(v[2])})[2]; z < minZ {
			minZ = z
		}
	}
	if math.IsInf(minZ, 1) {
		return t
	}
	return t.Then(Translate(0, 0, -minZ))
}

// UnitGuess is the result of GuessUnits.
type UnitGuess struct {
	// Suspicious is true when the size looks like the wrong unit.
	Suspicious bool
	Reason     string
	// SuggestedScale multiplies the mesh to get millimetres (0 when not suspicious).
	SuggestedScale float64
}

// GuessUnits reports a bounding box that is implausible for a print in
// millimetres: larger side under 1 mm or over 2000 mm. It never scales.
func GuessUnits(b BBox) UnitGuess {
	s := b.Size()
	largest := float64(max32(s[0], max32(s[1], s[2])))
	switch {
	case largest > 0 && largest < 1:
		return UnitGuess{true, fmt.Sprintf("the largest side is %.4g mm, so the model is probably in metres (scale 1000) or another large unit", largest), 1000}
	case largest > 2000:
		return UnitGuess{true, fmt.Sprintf("the largest side is %.5g mm, so the model is probably in micrometres (scale 0.001)", largest), 0.001}
	}
	return UnitGuess{}
}

func max32(a, b float32) float32 {
	if a > b {
		return a
	}
	return b
}

type vec3 = [3]float64

func vec(v [3]float32) vec3 { return vec3{float64(v[0]), float64(v[1]), float64(v[2])} }

func sub(a, b vec3) vec3 { return vec3{a[0] - b[0], a[1] - b[1], a[2] - b[2]} }

func dot(a, b vec3) float64 { return a[0]*b[0] + a[1]*b[1] + a[2]*b[2] }

func cross(a, b vec3) vec3 {
	return vec3{a[1]*b[2] - a[2]*b[1], a[2]*b[0] - a[0]*b[2], a[0]*b[1] - a[1]*b[0]}
}

func length(a vec3) float64 { return math.Sqrt(dot(a, a)) }

func triArea(a, b, c vec3) float64 { return length(cross(sub(b, a), sub(c, a))) / 2 }
