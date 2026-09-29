package mesh

import (
	"bufio"
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

// ReadOBJ reads a Wavefront OBJ file.
func ReadOBJ(path string) (*Mesh, error) {
	data, err := readFileLimited(path)
	if err != nil {
		return nil, err
	}
	return ParseOBJ(data)
}

// ParseOBJ decodes OBJ data: v and f lines only. Polygons become triangle
// fans, negative indices count from the end, materials, normals, texture
// coordinates and vertex colours are ignored.
func ParseOBJ(data []byte) (*Mesh, error) {
	m := &Mesh{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	line := 0
	for sc.Scan() {
		line++
		text := strings.TrimSpace(sc.Text())
		if text == "" || text[0] == '#' {
			continue
		}
		f := strings.Fields(text)
		switch f[0] {
		case "v":
			if len(f) < 4 {
				return nil, fmt.Errorf("line %d: a vertex needs x y z", line)
			}
			var v [3]float32
			for i := 0; i < 3; i++ {
				x, err := strconv.ParseFloat(f[1+i], 32)
				if err != nil {
					return nil, fmt.Errorf("line %d: bad coordinate %q", line, f[1+i])
				}
				v[i] = float32(x)
			}
			m.Vertices = append(m.Vertices, v)
		case "f":
			if len(f) < 4 {
				return nil, fmt.Errorf("line %d: a face needs at least 3 vertices", line)
			}
			idx := make([]uint32, 0, len(f)-1)
			for _, ref := range f[1:] {
				if s := strings.IndexByte(ref, '/'); s >= 0 {
					ref = ref[:s]
				}
				n, err := strconv.Atoi(ref)
				if err != nil || n == 0 {
					return nil, fmt.Errorf("line %d: bad vertex reference %q", line, ref)
				}
				if n < 0 {
					n = len(m.Vertices) + n + 1
				}
				if n < 1 || n > len(m.Vertices) {
					return nil, fmt.Errorf("line %d: vertex %s is out of range (%d vertices so far)", line, ref, len(m.Vertices))
				}
				idx = append(idx, uint32(n-1))
			}
			for i := 1; i+1 < len(idx); i++ {
				m.Triangles = append(m.Triangles, [3]uint32{idx[0], idx[i], idx[i+1]})
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(m.Triangles) == 0 {
		return nil, ErrEmpty
	}
	return m, nil
}
