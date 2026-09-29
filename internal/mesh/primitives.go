package mesh

import "math"

// CylinderSegments and the sphere resolution are fixed so modifier shapes are
// reproducible.
const (
	CylinderSegments = 64
	SphereSegments   = 32
	SphereRings      = 16
)

// Box returns an axis aligned box of the given size centred on the origin.
func Box(x, y, z float64) *Mesh {
	hx, hy, hz := float32(x/2), float32(y/2), float32(z/2)
	m := &Mesh{Vertices: [][3]float32{
		{-hx, -hy, -hz}, {hx, -hy, -hz}, {hx, hy, -hz}, {-hx, hy, -hz},
		{-hx, -hy, hz}, {hx, -hy, hz}, {hx, hy, hz}, {-hx, hy, hz},
	}}
	m.Triangles = [][3]uint32{
		{0, 2, 1}, {0, 3, 2}, // bottom (-z)
		{4, 5, 6}, {4, 6, 7}, // top (+z)
		{0, 1, 5}, {0, 5, 4}, // front (-y)
		{2, 3, 7}, {2, 7, 6}, // back (+y)
		{1, 2, 6}, {1, 6, 5}, // right (+x)
		{3, 0, 4}, {3, 4, 7}, // left (-x)
	}
	return m
}

// Cylinder returns a cylinder along Z with the given radius and height,
// centred on the origin, with CylinderSegments sides and flat caps.
func Cylinder(radius, height float64) *Mesh {
	n := CylinderSegments
	h := float32(height / 2)
	m := &Mesh{}
	// Ring vertices: bottom ring 0..n-1, top ring n..2n-1, then the two cap centres.
	for _, z := range []float32{-h, h} {
		for i := 0; i < n; i++ {
			a := 2 * math.Pi * float64(i) / float64(n)
			m.Vertices = append(m.Vertices, [3]float32{float32(radius * math.Cos(a)), float32(radius * math.Sin(a)), z})
		}
	}
	bottom, top := uint32(2*n), uint32(2*n+1)
	m.Vertices = append(m.Vertices, [3]float32{0, 0, -h}, [3]float32{0, 0, h})
	for i := 0; i < n; i++ {
		j := (i + 1) % n
		a, b, c, d := uint32(i), uint32(j), uint32(n+j), uint32(n+i)
		m.Triangles = append(m.Triangles,
			[3]uint32{a, b, c}, [3]uint32{a, c, d}, // side
			[3]uint32{bottom, b, a}, // bottom cap faces down
			[3]uint32{top, d, c},    // top cap faces up
		)
	}
	return m
}

// Sphere returns a UV sphere with SphereSegments around and SphereRings from
// pole to pole, centred on the origin.
func Sphere(radius float64) *Mesh {
	seg, rings := SphereSegments, SphereRings
	m := &Mesh{}
	m.Vertices = append(m.Vertices, [3]float32{0, 0, float32(radius)}) // north pole
	for r := 1; r < rings; r++ {
		phi := math.Pi * float64(r) / float64(rings)
		for s := 0; s < seg; s++ {
			th := 2 * math.Pi * float64(s) / float64(seg)
			m.Vertices = append(m.Vertices, [3]float32{
				float32(radius * math.Sin(phi) * math.Cos(th)),
				float32(radius * math.Sin(phi) * math.Sin(th)),
				float32(radius * math.Cos(phi)),
			})
		}
	}
	south := uint32(len(m.Vertices))
	m.Vertices = append(m.Vertices, [3]float32{0, 0, -float32(radius)})
	ring := func(r, s int) uint32 { return uint32(1 + (r-1)*seg + s%seg) }
	for s := 0; s < seg; s++ {
		m.Triangles = append(m.Triangles, [3]uint32{0, ring(1, s), ring(1, s+1)})
	}
	for r := 1; r < rings-1; r++ {
		for s := 0; s < seg; s++ {
			a, b, c, d := ring(r, s), ring(r+1, s), ring(r+1, s+1), ring(r, s+1)
			m.Triangles = append(m.Triangles, [3]uint32{a, b, c}, [3]uint32{a, c, d})
		}
	}
	for s := 0; s < seg; s++ {
		m.Triangles = append(m.Triangles, [3]uint32{south, ring(rings-1, s+1), ring(rings-1, s)})
	}
	return m
}
