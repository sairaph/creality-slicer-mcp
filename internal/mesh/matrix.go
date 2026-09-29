package mesh

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Matrix is an affine transform in the 3MF order: twelve numbers
// m00 m01 m02 m10 m11 m12 m20 m21 m22 m30 m31 m32 exactly as the "transform"
// attribute of a build item or component lists them. Points are row vectors:
//
//	x' = x*m00 + y*m10 + z*m20 + m30
//	y' = x*m01 + y*m11 + z*m21 + m31
//	z' = x*m02 + y*m12 + z*m22 + m32
//
// so m[0..2], m[3..5] and m[6..8] are the images of the X, Y and Z axes and
// m[9..11] is the translation.
type Matrix [12]float64

// Identity is the transform that changes nothing.
func Identity() Matrix { return Matrix{1, 0, 0, 0, 1, 0, 0, 0, 1, 0, 0, 0} }

// Translate moves by (x, y, z).
func Translate(x, y, z float64) Matrix { return Matrix{1, 0, 0, 0, 1, 0, 0, 0, 1, x, y, z} }

// Scale scales about the origin.
func Scale(x, y, z float64) Matrix { return Matrix{x, 0, 0, 0, y, 0, 0, 0, z, 0, 0, 0} }

// RotateX rotates by angle radians about the X axis (right handed).
func RotateX(a float64) Matrix {
	s, c := math.Sincos(a)
	return Matrix{1, 0, 0, 0, c, s, 0, -s, c, 0, 0, 0}
}

// RotateY rotates by angle radians about the Y axis (right handed).
func RotateY(a float64) Matrix {
	s, c := math.Sincos(a)
	return Matrix{c, 0, -s, 0, 1, 0, s, 0, c, 0, 0, 0}
}

// RotateZ rotates by angle radians about the Z axis (right handed).
func RotateZ(a float64) Matrix {
	s, c := math.Sincos(a)
	return Matrix{c, s, 0, -s, c, 0, 0, 0, 1, 0, 0, 0}
}

// Apply transforms a point.
func (m Matrix) Apply(p [3]float64) [3]float64 {
	return [3]float64{
		p[0]*m[0] + p[1]*m[3] + p[2]*m[6] + m[9],
		p[0]*m[1] + p[1]*m[4] + p[2]*m[7] + m[10],
		p[0]*m[2] + p[1]*m[5] + p[2]*m[8] + m[11],
	}
}

// ApplyF32 transforms a float32 point.
func (m Matrix) ApplyF32(p [3]float32) [3]float32 {
	r := m.Apply([3]float64{float64(p[0]), float64(p[1]), float64(p[2])})
	return [3]float32{float32(r[0]), float32(r[1]), float32(r[2])}
}

// Then returns the transform that applies m first and next second.
func (m Matrix) Then(next Matrix) Matrix {
	var r Matrix
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			r[3*i+j] = m[3*i]*next[j] + m[3*i+1]*next[3+j] + m[3*i+2]*next[6+j]
		}
	}
	for j := 0; j < 3; j++ {
		r[9+j] = m[9]*next[j] + m[10]*next[3+j] + m[11]*next[6+j] + next[9+j]
	}
	return r
}

// Compose applies the transforms left to right: Compose(a, b, c) = a.Then(b).Then(c).
func Compose(ms ...Matrix) Matrix {
	r := Identity()
	for _, m := range ms {
		r = r.Then(m)
	}
	return r
}

// Determinant of the linear part.
func (m Matrix) Determinant() float64 {
	return m[0]*(m[4]*m[8]-m[5]*m[7]) - m[1]*(m[3]*m[8]-m[5]*m[6]) + m[2]*(m[3]*m[7]-m[4]*m[6])
}

// Inverse returns the inverse transform, ok false when the linear part is singular.
func (m Matrix) Inverse() (Matrix, bool) {
	det := m.Determinant()
	if math.Abs(det) < 1e-300 {
		return Matrix{}, false
	}
	inv := 1 / det
	var r Matrix
	r[0] = (m[4]*m[8] - m[5]*m[7]) * inv
	r[1] = (m[2]*m[7] - m[1]*m[8]) * inv
	r[2] = (m[1]*m[5] - m[2]*m[4]) * inv
	r[3] = (m[5]*m[6] - m[3]*m[8]) * inv
	r[4] = (m[0]*m[8] - m[2]*m[6]) * inv
	r[5] = (m[2]*m[3] - m[0]*m[5]) * inv
	r[6] = (m[3]*m[7] - m[4]*m[6]) * inv
	r[7] = (m[1]*m[6] - m[0]*m[7]) * inv
	r[8] = (m[0]*m[4] - m[1]*m[3]) * inv
	t := Matrix{r[0], r[1], r[2], r[3], r[4], r[5], r[6], r[7], r[8], 0, 0, 0}.Apply([3]float64{-m[9], -m[10], -m[11]})
	r[9], r[10], r[11] = t[0], t[1], t[2]
	return r, true
}

// Translation returns the translation part.
func (m Matrix) Translation() [3]float64 { return [3]float64{m[9], m[10], m[11]} }

// String formats like the 3MF transform attribute: nine significant digits,
// shortest form ("1 0 0 0 1 0 0 0 1 12.5 0 3").
func (m Matrix) String() string {
	parts := make([]string, 12)
	for i, v := range m {
		parts[i] = FormatG9(v)
	}
	return strings.Join(parts, " ")
}

// ParseMatrix reads a 3MF transform attribute (twelve numbers).
func ParseMatrix(s string) (Matrix, error) {
	f := strings.Fields(s)
	if len(f) != 12 {
		return Matrix{}, fmt.Errorf("transform needs 12 numbers, got %d", len(f))
	}
	var m Matrix
	for i, x := range f {
		v, err := strconv.ParseFloat(x, 64)
		if err != nil {
			return Matrix{}, fmt.Errorf("transform: %w", err)
		}
		m[i] = v
	}
	return m, nil
}

// FormatG9 formats like C's %.9g (what the slicer writes for coordinates and
// transforms).
func FormatG9(v float64) string { return formatG(v, 9) }

// FormatG17 formats like C's %.17g (what the slicer writes for matrices in
// model_settings.config).
func FormatG17(v float64) string { return formatG(v, 17) }

func formatG(v float64, prec int) string {
	if v == 0 {
		return "0"
	}
	return strconv.FormatFloat(v, 'g', prec, 64)
}

// Matrix4 is a 4x4 matrix in the row-major, column-vector layout of the
// "matrix" metadata of model_settings.config: sixteen numbers, row 0 first,
// the translation in elements 3, 7 and 11.
type Matrix4 [16]float64

// Identity4 is the 4x4 identity.
func Identity4() Matrix4 {
	return Matrix4{1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1}
}

// To4 converts a 3MF transform to the 4x4 layout.
func (m Matrix) To4() Matrix4 {
	return Matrix4{
		m[0], m[3], m[6], m[9],
		m[1], m[4], m[7], m[10],
		m[2], m[5], m[8], m[11],
		0, 0, 0, 1,
	}
}

// To3MF converts back, dropping the (0 0 0 1) row.
func (m Matrix4) To3MF() Matrix {
	return Matrix{
		m[0], m[4], m[8],
		m[1], m[5], m[9],
		m[2], m[6], m[10],
		m[3], m[7], m[11],
	}
}

// String formats the sixteen numbers with seventeen significant digits.
func (m Matrix4) String() string {
	parts := make([]string, 16)
	for i, v := range m {
		parts[i] = FormatG17(v)
	}
	return strings.Join(parts, " ")
}

// ParseMatrix4 reads sixteen numbers.
func ParseMatrix4(s string) (Matrix4, error) {
	f := strings.Fields(s)
	if len(f) != 16 {
		return Matrix4{}, fmt.Errorf("matrix needs 16 numbers, got %d", len(f))
	}
	var m Matrix4
	for i, x := range f {
		v, err := strconv.ParseFloat(x, 64)
		if err != nil {
			return Matrix4{}, fmt.Errorf("matrix: %w", err)
		}
		m[i] = v
	}
	return m, nil
}
