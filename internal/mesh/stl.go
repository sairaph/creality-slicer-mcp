package mesh

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"strconv"
)

// ReadSTL reads a binary or ASCII STL file.
func ReadSTL(path string) (*Mesh, error) {
	data, err := readFileLimited(path)
	if err != nil {
		return nil, err
	}
	return ParseSTL(data)
}

// ParseSTL decodes STL data. Binary versus ASCII is decided by the size
// formula (84 + 50 bytes per triangle), never by a "solid" prefix, because
// many binary files start their 80 byte header with "solid".
func ParseSTL(data []byte) (*Mesh, error) {
	if len(data) >= 84 {
		n := binary.LittleEndian.Uint32(data[80:84])
		if uint64(len(data)) == 84+50*uint64(n) {
			return parseBinarySTL(data, int(n))
		}
	}
	m, err := parseASCIISTL(data)
	if err != nil {
		return nil, fmt.Errorf("not a valid STL file (neither binary by size nor ASCII): %w", err)
	}
	return m, nil
}

// welder merges vertices with identical coordinates.
type welder struct {
	index map[[3]float32]uint32
	m     *Mesh
}

func newWelder(triangles int) *welder {
	return &welder{index: make(map[[3]float32]uint32, triangles/2+1), m: &Mesh{Triangles: make([][3]uint32, 0, triangles)}}
}

func (w *welder) vertex(v [3]float32) uint32 {
	// -0 and 0 must weld together.
	for i := range v {
		if v[i] == 0 {
			v[i] = 0
		}
	}
	if i, ok := w.index[v]; ok {
		return i
	}
	i := uint32(len(w.m.Vertices))
	w.m.Vertices = append(w.m.Vertices, v)
	w.index[v] = i
	return i
}

func parseBinarySTL(data []byte, n int) (*Mesh, error) {
	w := newWelder(n)
	off := 84
	for i := 0; i < n; i++ {
		rec := data[off : off+50]
		var idx [3]uint32
		for k := 0; k < 3; k++ {
			var v [3]float32
			for c := 0; c < 3; c++ {
				bits := binary.LittleEndian.Uint32(rec[12+12*k+4*c:])
				v[c] = math.Float32frombits(bits)
				if math.IsNaN(float64(v[c])) || math.IsInf(float64(v[c]), 0) {
					return nil, fmt.Errorf("facet %d has a non finite coordinate", i)
				}
			}
			idx[k] = w.vertex(v)
		}
		w.m.Triangles = append(w.m.Triangles, idx)
		off += 50
	}
	if n == 0 {
		return nil, ErrEmpty
	}
	return w.m, nil
}

func parseASCIISTL(data []byte) (*Mesh, error) {
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	sc.Split(bufio.ScanWords)
	w := newWelder(1024)
	var tri [3]uint32
	corner := 0
	sawSolid := false
	for sc.Scan() {
		switch string(bytes.ToLower(sc.Bytes())) {
		case "solid":
			sawSolid = true
		case "vertex":
			var v [3]float32
			for c := 0; c < 3; c++ {
				if !sc.Scan() {
					return nil, io.ErrUnexpectedEOF
				}
				f, err := strconv.ParseFloat(sc.Text(), 32)
				if err != nil {
					return nil, fmt.Errorf("bad vertex coordinate %q", sc.Text())
				}
				v[c] = float32(f)
			}
			tri[corner] = w.vertex(v)
			corner++
			if corner == 3 {
				w.m.Triangles = append(w.m.Triangles, tri)
				corner = 0
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if !sawSolid {
		return nil, fmt.Errorf("no \"solid\" keyword")
	}
	if corner != 0 {
		return nil, fmt.Errorf("a facet has %d vertices", corner)
	}
	if len(w.m.Triangles) == 0 {
		return nil, ErrEmpty
	}
	return w.m, nil
}

// WriteBinarySTL encodes the mesh as binary STL (normals computed).
func WriteBinarySTL(m *Mesh) []byte {
	buf := make([]byte, 84+50*len(m.Triangles))
	copy(buf, "binary STL written by creality-slicer-mcp")
	binary.LittleEndian.PutUint32(buf[80:], uint32(len(m.Triangles)))
	off := 84
	for _, t := range m.Triangles {
		a, b, c := vec(m.Vertices[t[0]]), vec(m.Vertices[t[1]]), vec(m.Vertices[t[2]])
		n := cross(sub(b, a), sub(c, a))
		if l := length(n); l > 0 {
			n = vec3{n[0] / l, n[1] / l, n[2] / l}
		}
		for i := 0; i < 3; i++ {
			binary.LittleEndian.PutUint32(buf[off+4*i:], math.Float32bits(float32(n[i])))
		}
		for k, idx := range t {
			for c := 0; c < 3; c++ {
				binary.LittleEndian.PutUint32(buf[off+12+12*k+4*c:], math.Float32bits(m.Vertices[idx][c]))
			}
		}
		off += 50
	}
	return buf
}
