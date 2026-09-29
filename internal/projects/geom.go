package projects

import (
	"math"

	"github.com/sairaph/creality-slicer-mcp/internal/mesh"
)

const deg = math.Pi / 180

// eulerMatrix builds a rotation from angles in degrees: about X first, then
// Y, then Z (fixed world axes).
func eulerMatrix(x, y, z float64) mesh.Matrix {
	return mesh.Compose(mesh.RotateX(x*deg), mesh.RotateY(y*deg), mesh.RotateZ(z*deg))
}

// decompose splits the linear part of a transform into per axis scale
// factors and the rotation (degrees, in the order eulerMatrix applies them).
// Shear is ignored.
func decompose(m mesh.Matrix) (scale [3]float64, rot [3]float64) {
	// The three rows of the 3MF layout are the images of the axes.
	for i := 0; i < 3; i++ {
		scale[i] = math.Sqrt(m[3*i]*m[3*i] + m[3*i+1]*m[3*i+1] + m[3*i+2]*m[3*i+2])
		if scale[i] == 0 {
			scale[i] = 1
		}
	}
	// Column vector rotation matrix L(r,c) = m[3c+r], divided by the scale.
	var L [3][3]float64
	for c := 0; c < 3; c++ {
		for r := 0; r < 3; r++ {
			L[r][c] = m[3*c+r] / scale[c]
		}
	}
	// L = Rz * Ry * Rx.
	sy := -L[2][0]
	if sy > 1 {
		sy = 1
	}
	if sy < -1 {
		sy = -1
	}
	y := math.Asin(sy)
	var x, z float64
	if math.Abs(sy) < 1-1e-9 {
		x = math.Atan2(L[2][1], L[2][2])
		z = math.Atan2(L[1][0], L[0][0])
	} else {
		x = math.Atan2(-L[1][2], L[1][1])
		z = 0
	}
	rot = [3]float64{round6(x / deg), round6(y / deg), round6(z / deg)}
	return scale, rot
}

func round6(v float64) float64 {
	r := math.Round(v*1e6) / 1e6
	if r == 0 {
		return 0
	}
	return r
}

// linear returns the transform with its translation removed.
func linear(m mesh.Matrix) mesh.Matrix {
	m[9], m[10], m[11] = 0, 0, 0
	return m
}

// bboxOf returns the bounding box of a mesh after a transform.
func bboxOf(m *mesh.Mesh, t mesh.Matrix) (mesh.BBox, bool) {
	return m.Transformed(t).BBox()
}

func size3(b mesh.BBox) [3]float64 {
	s := b.Size()
	return [3]float64{float64(s[0]), float64(s[1]), float64(s[2])}
}

func center3(b mesh.BBox) [3]float64 {
	c := b.Center()
	return [3]float64{float64(c[0]), float64(c[1]), float64(c[2])}
}

// rect is an axis aligned rectangle on the bed.
type rect struct{ x0, y0, x1, y1 float64 }

func (r rect) inflate(d float64) rect { return rect{r.x0 - d, r.y0 - d, r.x1 + d, r.y1 + d} }

func (r rect) overlaps(o rect) bool {
	return r.x0 < o.x1 && o.x0 < r.x1 && r.y0 < o.y1 && o.y0 < r.y1
}

func (r rect) contains(o rect) bool {
	return o.x0 >= r.x0-1e-6 && o.x1 <= r.x1+1e-6 && o.y0 >= r.y0-1e-6 && o.y1 <= r.y1+1e-6
}

func footprint(b mesh.BBox) rect {
	return rect{float64(b.Min[0]), float64(b.Min[1]), float64(b.Max[0]), float64(b.Max[1])}
}
