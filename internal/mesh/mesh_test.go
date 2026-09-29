package mesh

import (
	"archive/zip"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sairaph/creality-slicer-mcp/internal/userhome/testhome"
)

func TestMain(m *testing.M) { os.Exit(testhome.Run(m)) }

func near(a, b, eps float64) bool { return math.Abs(a-b) <= eps }

func TestMatrixOrderAndRotation(t *testing.T) {
	// The golden build item: a right handed +90 degree turn about X.
	m, err := ParseMatrix("1 0 0 0 2.22044605e-16 1 0 -1 2.22044605e-16 44.7294838 43.8494246 10.2412338")
	if err != nil {
		t.Fatal(err)
	}
	p := m.Apply([3]float64{1, 2, 3})
	// (x, y, z) -> (x, -z, y) then translated.
	if !near(p[0], 1+44.7294838, 1e-9) || !near(p[1], -3+43.8494246, 1e-9) || !near(p[2], 2+10.2412338, 1e-9) {
		t.Errorf("%v", p)
	}
	r := RotateX(math.Pi / 2)
	if !near(r[4], 0, 1e-12) || !near(r[5], 1, 1e-12) || !near(r[7], -1, 1e-12) || !near(r[8], 0, 1e-12) {
		t.Errorf("RotateX(90) must match the golden layout: %v", r)
	}
	if got := Identity().String(); got != "1 0 0 0 1 0 0 0 1 0 0 0" {
		t.Errorf("%q", got)
	}
	if got := m.String(); got != "1 0 0 0 2.22044605e-16 1 0 -1 2.22044605e-16 44.7294838 43.8494246 10.2412338" {
		t.Errorf("golden transform must round trip textually: %q", got)
	}
	if _, err := ParseMatrix("1 2 3"); err == nil {
		t.Error("12 numbers needed")
	}
}

func TestMatrixComposeInverse(t *testing.T) {
	a := Compose(Scale(2, 2, 2), RotateZ(math.Pi/2), Translate(10, 0, 0))
	// Scale, then rotate 90 about Z (x -> y), then move: (1,0,0) -> (2,0,0) -> (0,2,0) -> (10,2,0).
	p := a.Apply([3]float64{1, 0, 0})
	if !near(p[0], 10, 1e-12) || !near(p[1], 2, 1e-12) || !near(p[2], 0, 1e-12) {
		t.Errorf("%v", p)
	}
	inv, ok := a.Inverse()
	if !ok {
		t.Fatal("invertible")
	}
	back := inv.Apply(p)
	if !near(back[0], 1, 1e-12) || !near(back[1], 0, 1e-12) {
		t.Errorf("%v", back)
	}
	id := a.Then(inv)
	for i, v := range Identity() {
		if !near(id[i], v, 1e-12) {
			t.Errorf("a*inv = %v", id)
			break
		}
	}
	if _, ok := Scale(0, 1, 1).Inverse(); ok {
		t.Error("singular")
	}
	// The 4x4 layout of model_settings.config.
	m4 := Translate(1, 2, 3).To4()
	if m4[3] != 1 || m4[7] != 2 || m4[11] != 3 || m4[15] != 1 {
		t.Errorf("%v", m4)
	}
	if back := a.To4().To3MF(); back != a {
		t.Errorf("4x4 round trip: %v vs %v", back, a)
	}
	if s := (Matrix4{1, 0, 0, 0, 0, 1, 0, 4.7683715779687498e-07, 0, 0, 1, 0, 0, 0, 0, 1}).String(); s != "1 0 0 0 0 1 0 4.7683715779687498e-07 0 0 1 0 0 0 0 1" {
		t.Errorf("golden 4x4 formatting: %q", s)
	}
	if _, err := ParseMatrix4("1"); err == nil {
		t.Error("16 numbers needed")
	}
}

func TestFormatG9(t *testing.T) {
	for in, want := range map[float64]string{
		0: "0", 1: "1", 0.5: "0.5", -2.03125012e-16: "-2.03125012e-16", 44.7294838: "44.7294838", 205.17273512: "205.172735",
		100000: "100000", 1e9: "1e+09", 123456789: "123456789", 0.0001: "0.0001", 0.00001: "1e-05", 2.82314348: "2.82314348",
	} {
		if got := FormatG9(in); got != want {
			t.Errorf("FormatG9(%v) = %q, want %q", in, got, want)
		}
	}
	if got := FormatG17(29.900367736816406); got != "29.900367736816406" {
		t.Errorf("%q", got)
	}
}

func TestPrimitivesAreClosedOutwardMeshes(t *testing.T) {
	for name, tc := range map[string]struct {
		m    *Mesh
		vol  float64
		tol  float64
		tris int
	}{
		"box":      {Box(10, 20, 30), 6000, 1e-3, 12},
		"cylinder": {Cylinder(5, 10), math.Pi * 25 * 10, 0.01 * math.Pi * 250, 4 * CylinderSegments},
		"sphere":   {Sphere(10), 4.0 / 3 * math.Pi * 1000, 0.02 * 4.0 / 3 * math.Pi * 1000, 2*SphereSegments + 2*SphereSegments*(SphereRings-2)},
	} {
		st := tc.m.Stats()
		if !st.Manifold || st.Degenerate != 0 || st.Triangles != tc.tris {
			t.Errorf("%s: %+v", name, st)
		}
		if v := tc.m.SignedVolume(); v <= 0 || !near(v, tc.vol, tc.vol*0.05) {
			t.Errorf("%s: signed volume %v, want about %v (positive: outward winding)", name, v, tc.vol)
		}
		bb, ok := tc.m.BBox()
		c := bb.Center()
		if !ok || !near(float64(c[0]), 0, 1e-4) || !near(float64(c[1]), 0, 1e-4) || !near(float64(c[2]), 0, 1e-4) {
			t.Errorf("%s: not centred: %v", name, bb)
		}
		if err := tc.m.Validate(); err != nil {
			t.Error(err)
		}
	}
	bb, _ := Box(10, 20, 30).BBox()
	if s := bb.Size(); s != [3]float32{10, 20, 30} {
		t.Errorf("%v", s)
	}
}

func TestStats(t *testing.T) {
	open := Box(1, 1, 1)
	open.Triangles = open.Triangles[:10] // remove one face
	st := open.Stats()
	if st.Manifold || st.OpenEdges != 4 || st.NonManifoldEdges != 0 {
		t.Errorf("%+v", st)
	}
	flipped := Box(1, 1, 1)
	flipped.Triangles[0] = [3]uint32{flipped.Triangles[0][0], flipped.Triangles[0][2], flipped.Triangles[0][1]}
	if st := flipped.Stats(); st.Manifold || st.Flipped == 0 {
		t.Errorf("%+v", st)
	}
	degenerate := Box(1, 1, 1)
	degenerate.Triangles = append(degenerate.Triangles, [3]uint32{0, 0, 1})
	if st := degenerate.Stats(); st.Degenerate != 1 || !st.Manifold {
		t.Errorf("%+v", st)
	}
	thin := &Mesh{Vertices: [][3]float32{{0, 0, 0}, {1, 0, 0}, {2, 0, 0}}, Triangles: [][3]uint32{{0, 1, 2}}}
	if st := thin.Stats(); st.Degenerate != 1 {
		t.Errorf("collinear points: %+v", st)
	}
	bad := &Mesh{Vertices: make([][3]float32, 2), Triangles: [][3]uint32{{0, 1, 2}}}
	if bad.Validate() == nil {
		t.Error("out of range index")
	}
}

func TestTransformedAndLayOnBed(t *testing.T) {
	b := Box(10, 10, 10)
	moved := b.Transformed(Translate(5, 5, 100))
	bb, _ := moved.BBox()
	if bb.Min != [3]float32{0, 0, 95} || bb.Max != [3]float32{10, 10, 105} {
		t.Errorf("%v", bb)
	}
	mirror := b.Transformed(Scale(-1, 1, 1))
	if mirror.SignedVolume() <= 0 || !mirror.Stats().Manifold {
		t.Errorf("a mirrored mesh must keep outward winding: %v", mirror.SignedVolume())
	}
	tr := LayOnBed(b, RotateX(0.3))
	got := b.Transformed(tr)
	gb, _ := got.BBox()
	if !near(float64(gb.Min[2]), 0, 1e-5) {
		t.Errorf("min z %v", gb.Min[2])
	}
	c := b.Clone()
	c.Append(Box(1, 1, 1))
	if len(c.Vertices) != 16 || len(c.Triangles) != 24 || len(b.Vertices) != 8 || c.Validate() != nil {
		t.Errorf("clone/append: %d %d", len(c.Vertices), len(c.Triangles))
	}
}

func TestLayFlatOnTheBiggestFace(t *testing.T) {
	// A 10 x 20 x 30 block turned in an arbitrary way: it must end up lying on
	// its 20 x 30 face, so the height is 10 and the footprint 20 x 30.
	block := Box(10, 20, 30)
	orient := Compose(RotateX(0.7), RotateY(1.1), RotateZ(-0.4))
	tilted := block.Transformed(orient)
	rot, ok := LayFlat(tilted)
	if !ok {
		t.Fatal("no flat face found")
	}
	flat := tilted.Transformed(LayOnBed(tilted, rot))
	bb, _ := flat.BBox()
	s := bb.Size()
	if !near(float64(bb.Min[2]), 0, 1e-4) || !near(float64(s[2]), 10, 1e-3) {
		t.Errorf("bounds %v size %v", bb, s)
	}
	foot := []float64{float64(s[0]), float64(s[1])}
	if !((near(foot[0], 20, 1e-3) && near(foot[1], 30, 1e-3)) || (near(foot[0], 30, 1e-3) && near(foot[1], 20, 1e-3))) {
		// Rotated about Z in general, so only the bounding footprint must cover 20 x 30.
		if foot[0]*foot[1] < 20*30-1e-3 {
			t.Errorf("footprint %v", foot)
		}
	}
	if !near(flat.Volume(), 6000, 0.5) {
		t.Errorf("volume %v", flat.Volume())
	}
	// Already flat: identity.
	rot, ok = LayFlat(Box(10, 20, 30).Transformed(Translate(0, 0, 15)))
	if !ok {
		t.Fatal("flat box")
	}
	// The big face is +-x/y-normal? No: 20 x 30 faces are the +-Z ones (10 is Z? Box(10,20,30) has z = 30),
	// so the largest face (20 x 30 is x by z... ) is checked through the resulting height instead.
	h, _ := Box(10, 20, 30).Transformed(rot).BBox()
	if !near(float64(h.Size()[2]), 10, 1e-4) {
		t.Errorf("height after lay flat %v", h.Size()[2])
	}
}

func TestLayFlatPrefersTheHullPlane(t *testing.T) {
	// A cube (small faces) with a big flat plate (area 400) standing on its
	// edge: the plate is the largest group but so is not a base? A plate is a
	// convex hull face, so it wins. A separate concave pocket face (large,
	// not on the hull) must not.
	plate := Box(20, 20, 1)
	upright := plate.Transformed(RotateX(math.Pi / 2)) // 20 wide, 1 deep, 20 high
	rot, ok := LayFlat(upright)
	if !ok {
		t.Fatal("no face")
	}
	bb, _ := upright.Transformed(LayOnBed(upright, rot)).BBox()
	if !near(float64(bb.Size()[2]), 1, 1e-4) {
		t.Errorf("plate must lie flat, height %v", bb.Size()[2])
	}

	// An L shaped extrusion: the inner faces are large but face each other and
	// are not hull planes; the outer faces are.
	l := lShape(10, 10, 2, 40)
	rot, ok = LayFlat(l)
	if !ok {
		t.Fatal("L shape")
	}
	lb, _ := l.Transformed(LayOnBed(l, rot)).BBox()
	if lb.Size()[2] > 10+1e-3 {
		t.Errorf("L shape height %v", lb.Size()[2])
	}
}

// lShape builds an L profile (arm lengths a and b, thickness th) extruded by
// depth along Z, by joining two boxes (it overlaps at the corner, which is
// fine for the hull test).
func lShape(a, b, th, depth float64) *Mesh {
	m := Box(a, th, depth).Transformed(Translate(a/2, th/2, 0))
	m.Append(Box(th, b, depth).Transformed(Translate(th/2, b/2, 0)))
	return m
}

func TestLayFlatEmpty(t *testing.T) {
	if _, ok := LayFlat(&Mesh{}); ok {
		t.Error("empty mesh")
	}
	line := &Mesh{Vertices: [][3]float32{{0, 0, 0}, {1, 0, 0}, {2, 0, 0}}, Triangles: [][3]uint32{{0, 1, 2}}}
	if _, ok := LayFlat(line); ok {
		t.Error("degenerate mesh")
	}
}

func TestGuessUnits(t *testing.T) {
	if g := GuessUnits(BBox{Max: [3]float32{0.05, 0.02, 0.01}}); !g.Suspicious || g.SuggestedScale != 1000 || g.Reason == "" {
		t.Errorf("%+v", g)
	}
	if g := GuessUnits(BBox{Max: [3]float32{5000, 10, 10}}); !g.Suspicious || g.SuggestedScale != 0.001 {
		t.Errorf("%+v", g)
	}
	if g := GuessUnits(BBox{Max: [3]float32{100, 100, 100}}); g.Suspicious || g.SuggestedScale != 0 {
		t.Errorf("%+v", g)
	}
	if g := GuessUnits(BBox{}); g.Suspicious {
		t.Errorf("a point is not suspicious: %+v", g)
	}
}

func TestBinarySTLWithSolidHeader(t *testing.T) {
	// Many binary files start with "solid": the size formula decides.
	data := WriteBinarySTL(Box(2, 3, 4))
	copy(data, "solid looks like ascii but is binary")
	m, err := ParseSTL(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Triangles) != 12 || len(m.Vertices) != 8 || !m.Stats().Manifold {
		t.Errorf("%d triangles %d vertices (welded)", len(m.Triangles), len(m.Vertices))
	}
	if !near(m.Volume(), 24, 1e-4) {
		t.Errorf("volume %v", m.Volume())
	}
	// Truncated binary data is neither binary nor ASCII.
	if _, err := ParseSTL(data[:len(data)-3]); err == nil {
		t.Error("truncated")
	}
	// A header claiming zero triangles with a matching size.
	empty := make([]byte, 84)
	if _, err := ParseSTL(empty); err == nil {
		t.Error("no triangles")
	}
	// NaN coordinates are rejected.
	bad := WriteBinarySTL(Box(1, 1, 1))
	binary.LittleEndian.PutUint32(bad[84+12:], math.Float32bits(float32(math.NaN())))
	if _, err := ParseSTL(bad); err == nil {
		t.Error("NaN")
	}
}

func TestASCIISTL(t *testing.T) {
	src := `solid cube
 facet normal 0 0 -1
  outer loop
   vertex 0 0 0
   vertex 1 1 0
   vertex 1 0 0
  endloop
 endfacet
 facet normal 0 0 1
  outer loop
   vertex 0 0 1
   vertex 1 0 1
   vertex 1e0 1.0E0 1
  endloop
 endfacet
endsolid cube
`
	m, err := ParseSTL([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Triangles) != 2 || len(m.Vertices) != 6 {
		t.Errorf("%d %d", len(m.Triangles), len(m.Vertices))
	}
	// Uppercase keywords and CRLF.
	m, err = ParseSTL([]byte(strings.ReplaceAll(strings.ToUpper(src), "\n", "\r\n")))
	if err != nil || len(m.Triangles) != 2 {
		t.Errorf("%v", err)
	}
	for name, bad := range map[string]string{
		"no solid":   "facet vertex 1 2 3",
		"empty":      "solid x\nendsolid x",
		"partial":    "solid x\nvertex 0 0 0\nvertex 1 0 0\nendsolid",
		"bad number": "solid x\nvertex 0 0 abc",
		"cut off":    "solid x\nvertex 0 0",
		"not a mesh": "hello world",
	} {
		if _, err := ParseSTL([]byte(bad)); err == nil {
			t.Errorf("%s must fail", name)
		}
	}
}

func TestBinaryAndASCIIAgree(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "b.stl")
	if err := os.WriteFile(path, WriteBinarySTL(Sphere(5)), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := ReadSTL(path)
	if err != nil {
		t.Fatal(err)
	}
	if !m.Stats().Manifold || !near(m.Volume(), Sphere(5).Volume(), 1e-3) {
		t.Errorf("%+v", m.Stats())
	}
	if _, err := ReadSTL(filepath.Join(dir, "missing.stl")); err == nil {
		t.Error("missing file")
	}
}

func TestOBJ(t *testing.T) {
	src := "# cube\nmtllib x.mtl\no cube\nv 0 0 0\nv 1 0 0\nv 1 1 0\nv 0 1 0\nv 0 0 1 0.5 0.5 0.5\nvt 0 0\nvn 0 0 1\nusemtl m\ng a\n" +
		"f 1 2 3 4\nf 1/1/1 2/1/1 5/1/1\nf -5 -4 -1\ns off\n"
	m, err := ParseOBJ([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Vertices) != 5 || len(m.Triangles) != 4 { // quad -> 2, then 1, then 1
		t.Fatalf("%d vertices %d triangles", len(m.Vertices), len(m.Triangles))
	}
	if m.Triangles[1] != [3]uint32{0, 2, 3} || m.Triangles[3] != [3]uint32{0, 1, 4} {
		t.Errorf("%v", m.Triangles)
	}
	for name, bad := range map[string]string{
		"short vertex":  "v 1 2\nf 1 1 1",
		"bad coord":     "v a b c",
		"short face":    "v 0 0 0\nv 1 0 0\nf 1 2",
		"out of range":  "v 0 0 0\nv 1 0 0\nv 0 1 0\nf 1 2 4",
		"zero index":    "v 0 0 0\nv 1 0 0\nv 0 1 0\nf 0 1 2",
		"bad reference": "v 0 0 0\nv 1 0 0\nv 0 1 0\nf a b c",
		"no faces":      "v 0 0 0",
	} {
		if _, err := ParseOBJ([]byte(bad)); err == nil {
			t.Errorf("%s must fail", name)
		}
	}
	path := filepath.Join(t.TempDir(), "x.obj")
	os.WriteFile(path, []byte(src), 0o644)
	if _, err := ReadOBJ(path); err != nil {
		t.Error(err)
	}
}

func write3MF(t *testing.T, files map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "x.3mf")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for name, body := range files {
		w, _ := zw.Create(name)
		w.Write([]byte(body))
	}
	zw.Close()
	f.Close()
	return path
}

func meshXML(id int, extra string) string {
	// A unit cube with outward triangles.
	return fmt.Sprintf(`<object id="%d" type="model" %s><mesh><vertices>
<vertex x="0" y="0" z="0"/><vertex x="1" y="0" z="0"/><vertex x="1" y="1" z="0"/><vertex x="0" y="1" z="0"/>
<vertex x="0" y="0" z="1"/><vertex x="1" y="0" z="1"/><vertex x="1" y="1" z="1"/><vertex x="0" y="1" z="1"/>
</vertices><triangles>
<triangle v1="0" v2="2" v3="1"/><triangle v1="0" v2="3" v3="2"/><triangle v1="4" v2="5" v3="6"/><triangle v1="4" v2="6" v3="7"/>
<triangle v1="0" v2="1" v3="5"/><triangle v1="0" v2="5" v3="4"/><triangle v1="2" v2="3" v3="7"/><triangle v1="2" v2="7" v3="6"/>
<triangle v1="1" v2="2" v3="6"/><triangle v1="1" v2="6" v3="5"/><triangle v1="3" v2="0" v3="4"/><triangle v1="3" v2="4" v3="7"/>
</triangles></mesh></object>`, id, extra)
}

func TestRead3MFInlineAndUnits(t *testing.T) {
	model := `<?xml version="1.0"?><model unit="inch" xmlns="http://schemas.microsoft.com/3dmanufacturing/core/2015/02">
<resources>` + meshXML(1, `name="Cube"`) + `</resources>
<build><item objectid="1" transform="1 0 0 0 1 0 0 0 1 10 0 0"/></build></model>`
	placed, err := Read3MF(write3MF(t, map[string]string{"3D/3dmodel.model": model}))
	if err != nil {
		t.Fatal(err)
	}
	if len(placed) != 1 || placed[0].Name != "Cube" {
		t.Fatalf("%+v", placed)
	}
	bb, _ := placed[0].Mesh.BBox()
	// The item translation of 10 is in inches too: (10 .. 11) * 25.4.
	if !near(float64(bb.Min[0]), 254, 1e-3) || !near(float64(bb.Size()[0]), 25.4, 1e-3) {
		t.Errorf("%v", bb)
	}
	if !near(placed[0].Mesh.Volume(), 25.4*25.4*25.4, 0.1) {
		t.Errorf("volume %v", placed[0].Mesh.Volume())
	}
}

func TestRead3MFProductionComponents(t *testing.T) {
	main := `<?xml version="1.0"?><model unit="millimeter" xmlns="http://schemas.microsoft.com/3dmanufacturing/core/2015/02" xmlns:p="http://schemas.microsoft.com/3dmanufacturing/production/2015/06" requiredextensions="p">
<resources>
<object id="2" p:UUID="a" type="model"><components>
<component p:path="/3D/Objects/object_1.model" objectid="1" transform="2 0 0 0 2 0 0 0 2 0 0 0"/>
<component p:path="/3D/Objects/object_1.model" objectid="1" transform="1 0 0 0 1 0 0 0 1 5 0 0"/>
</components></object>
<object id="3" p:UUID="b" type="model"><components>
<component p:path="/3D/Objects/object_1.model" objectid="1"/>
</components></object>
</resources>
<build p:UUID="x"><item objectid="2" transform="1 0 0 0 1 0 0 0 1 100 0 0" printable="1"/><item objectid="3" printable="0"/></build></model>`
	sub := `<?xml version="1.0"?><model unit="millimeter" xmlns="http://schemas.microsoft.com/3dmanufacturing/core/2015/02"><resources>` + meshXML(1, `p:UUID="u" xmlns:p="urn:p"`) + `</resources><build/></model>`
	stub := `<model unit="millimeter"><resources></resources><build/></model>`
	placed, err := Read3MF(write3MF(t, map[string]string{
		"3D/3dmodel.model":          main,
		"3D/Objects/object_1.model": sub,
		"3D/Objects/object_2.model": stub,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(placed) != 1 {
		t.Fatalf("%d items", len(placed))
	}
	// Item 1: a 2 mm cube and a 1 mm cube at x 5, all moved by 100.
	if len(placed[0].Mesh.Triangles) != 24 {
		t.Errorf("%d triangles", len(placed[0].Mesh.Triangles))
	}
	bb, _ := placed[0].Mesh.BBox()
	if bb.Min[0] != 100 || bb.Max[0] != 106 {
		t.Errorf("%v", bb)
	}
	if placed[0].Name != "object 2" {
		t.Errorf("name %q", placed[0].Name)
	}
	// Item 2 is marked printable="0": it is not part of the model (MS1).
}

func TestRead3MFErrors(t *testing.T) {
	if _, err := Read3MF(filepath.Join(t.TempDir(), "none.3mf")); err == nil {
		t.Error("missing file")
	}
	if _, err := Read3MF(write3MF(t, map[string]string{"x.txt": "a"})); err == nil {
		t.Error("no model")
	}
	missing := `<model><resources><object id="1"><components><component p:path="/3D/Objects/gone.model" objectid="1" xmlns:p="urn:p"/></components></object></resources><build><item objectid="1"/></build></model>`
	if _, err := Read3MF(write3MF(t, map[string]string{"3D/3dmodel.model": missing})); err == nil {
		t.Error("missing component file")
	}
	loop := `<model><resources><object id="1"><components><component objectid="1"/></components></object></resources><build><item objectid="1"/></build></model>`
	if _, err := Read3MF(write3MF(t, map[string]string{"3D/3dmodel.model": loop})); err == nil {
		t.Error("recursive component")
	}
	badIndex := `<model><resources><object id="1"><mesh><vertices><vertex x="0" y="0" z="0"/></vertices><triangles><triangle v1="0" v2="1" v3="2"/></triangles></mesh></object></resources><build><item objectid="1"/></build></model>`
	if _, err := Read3MF(write3MF(t, map[string]string{"3D/3dmodel.model": badIndex})); err == nil {
		t.Error("index out of range")
	}
	empty := `<model><resources></resources><build/></model>`
	if _, err := Read3MF(write3MF(t, map[string]string{"3D/3dmodel.model": empty})); err == nil {
		t.Error("nothing to read")
	}
}

func TestParseModelDetails(t *testing.T) {
	xml := `<model unit="micron"><resources><object id="7" name="A &amp; B" type="other"><components><component objectid="8"/></components></object></resources><build><item objectid="7" printable="0"/></build></model>`
	m, err := ParseModel(strings.NewReader(xml))
	if err != nil {
		t.Fatal(err)
	}
	o := m.Objects[7]
	if m.Unit != "micron" || o == nil || o.Name != "A & B" || o.Type != "other" || len(o.Components) != 1 || o.Components[0].Transform != Identity() {
		t.Errorf("%+v", o)
	}
	if len(m.Items) != 1 || m.Items[0].Printable || m.Items[0].Transform != Identity() {
		t.Errorf("%+v", m.Items)
	}
	for _, bad := range []string{
		`<model><resources><object type="model"/></resources></model>`,
		`<model><resources><object id="1"><mesh><vertices><vertex x="a" y="0" z="0"/></vertices></mesh></object></resources></model>`,
		`<model><resources><object id="1"><components><component/></components></object></resources></model>`,
		`<model><build><item/></build></model>`,
		`<model><build><item objectid="1" transform="1 2"/></build></model>`,
		`<model><`,
	} {
		if _, err := ParseModel(strings.NewReader(bad)); err == nil {
			t.Errorf("must fail: %s", bad)
		}
	}
}

// MS1: a build item marked printable="0" is not part of the model.
func TestRead3MFSkipsUnprintableItems(t *testing.T) {
	model := `<?xml version="1.0"?><model unit="millimeter" xmlns="http://schemas.microsoft.com/3dmanufacturing/core/2015/02">
<resources>` + meshXML(1, `name="A"`) + meshXML(2, `name="B"`) + `</resources>
<build><item objectid="1"/><item objectid="2" printable="0"/></build></model>`
	placed, err := Read3MF(write3MF(t, map[string]string{"3D/3dmodel.model": model}))
	if err != nil || len(placed) != 1 || placed[0].Name != "A" {
		t.Fatalf("%+v %v", placed, err)
	}
	only := strings.Replace(model, `<item objectid="1"/>`, `<item objectid="1" printable="0"/>`, 1)
	if _, err := Read3MF(write3MF(t, map[string]string{"3D/3dmodel.model": only})); !errors.Is(err, ErrEmpty) {
		t.Fatalf("all items unprintable: %v", err)
	}
}

// MS1 (final review): a file over the limit is refused with ErrTooLarge instead
// of being read into memory, for every reader.
func TestReadersRefuseOversizedFiles(t *testing.T) {
	old := maxFileBytes
	maxFileBytes = 200
	defer func() { maxFileBytes = old }()
	dir := t.TempDir()
	big := filepath.Join(dir, "big.stl")
	if err := os.WriteFile(big, make([]byte, 1000), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSTL(big); !errors.Is(err, ErrTooLarge) {
		t.Errorf("stl: %v", err)
	}
	bigObj := filepath.Join(dir, "big.obj")
	os.WriteFile(bigObj, []byte(strings.Repeat("v 0 0 0\n", 100)), 0o644)
	if _, err := ReadOBJ(bigObj); !errors.Is(err, ErrTooLarge) {
		t.Errorf("obj: %v", err)
	}
	// A zip member that inflates past the limit (declared size and real size).
	model := `<?xml version="1.0"?><model unit="millimeter" xmlns="http://schemas.microsoft.com/3dmanufacturing/core/2015/02"><resources/><build/>` + strings.Repeat(" ", 5000) + `</model>`
	if _, err := Read3MF(write3MF(t, map[string]string{"3D/3dmodel.model": model})); !errors.Is(err, ErrTooLarge) {
		t.Errorf("3mf: %v", err)
	}
	if _, err := ParseModel(strings.NewReader(model)); !errors.Is(err, ErrTooLarge) {
		t.Errorf("model reader: %v", err)
	}
	// Under the limit everything reads as before.
	maxFileBytes = old
	if _, err := Read3MF(write3MF(t, map[string]string{"3D/3dmodel.model": `<?xml version="1.0"?><model unit="millimeter" xmlns="http://schemas.microsoft.com/3dmanufacturing/core/2015/02"><resources>` + meshXML(1, "") + `</resources><build><item objectid="1"/></build></model>`})); err != nil {
		t.Errorf("normal file: %v", err)
	}
}
