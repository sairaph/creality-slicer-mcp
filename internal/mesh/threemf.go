package mesh

import (
	"archive/zip"
	"fmt"
	"github.com/sairaph/creality-slicer-mcp/internal/mesh/xmlscan"
	"io"
	"strconv"
	"strings"
)

// ModelComponent is a component of a 3MF object: another object, possibly in
// another model file of the package (production extension p:path).
type ModelComponent struct {
	Path      string // "" = the same file
	ObjectID  int
	Transform Matrix
}

// ModelObject is one <object> of a 3MF model file.
type ModelObject struct {
	ID         int
	Name       string
	Type       string
	Mesh       *Mesh // nil for an object made of components
	Components []ModelComponent
}

// ModelItem is one <item> of the <build> section.
type ModelItem struct {
	ObjectID  int
	Path      string
	Transform Matrix
	Printable bool
}

// Model is a parsed 3MF model file.
type Model struct {
	Unit    string
	Objects map[int]*ModelObject
	Order   []int // object ids in file order
	Items   []ModelItem
}

// ParseModel decodes one 3MF model file (3D/3dmodel.model or a
// 3D/Objects/*.model of the production extension).
func ParseModel(r io.Reader) (*Model, error) {
	data, err := readAllLimited(r)
	if err != nil {
		return nil, err
	}
	sc := xmlscan.New(data)
	m := &Model{Unit: "millimeter", Objects: map[int]*ModelObject{}}
	var cur *ModelObject
	inVertices, inTriangles := false, false
	num := func(t *xmlscan.Token, key string) (float64, bool) {
		b, ok := t.AttrBytes(key)
		if !ok {
			return 0, false
		}
		f, err := strconv.ParseFloat(string(b), 32)
		return f, err == nil
	}
	index := func(t *xmlscan.Token, key string) (uint32, bool) {
		b, ok := t.AttrBytes(key)
		if !ok {
			return 0, false
		}
		n, err := strconv.ParseUint(string(b), 10, 32)
		return uint32(n), err == nil
	}
	for {
		t, ok, err := sc.Next()
		if err != nil {
			return nil, fmt.Errorf("3MF model: %w", err)
		}
		if !ok {
			break
		}
		switch t.Kind {
		case xmlscan.Start:
			switch string(t.Name) {
			case "model":
				if u := t.AttrString("unit"); u != "" {
					m.Unit = u
				}
			case "object":
				id, err := strconv.Atoi(t.AttrString("id"))
				if err != nil {
					return nil, fmt.Errorf("3MF model: object without a numeric id")
				}
				cur = &ModelObject{ID: id, Name: t.AttrString("name"), Type: t.AttrString("type")}
				m.Objects[id] = cur
				m.Order = append(m.Order, id)
			case "mesh":
				if cur != nil {
					cur.Mesh = &Mesh{}
				}
			case "vertices":
				inVertices = true
			case "triangles":
				inTriangles = true
			case "vertex":
				if cur != nil && cur.Mesh != nil && inVertices {
					x, ok1 := num(t, "x")
					y, ok2 := num(t, "y")
					z, ok3 := num(t, "z")
					if !ok1 || !ok2 || !ok3 {
						return nil, fmt.Errorf("3MF model: object %d has a bad vertex coordinate", cur.ID)
					}
					cur.Mesh.Vertices = append(cur.Mesh.Vertices, [3]float32{float32(x), float32(y), float32(z)})
				}
			case "triangle":
				if cur != nil && cur.Mesh != nil && inTriangles {
					a, ok1 := index(t, "v1")
					b, ok2 := index(t, "v2")
					c, ok3 := index(t, "v3")
					if !ok1 || !ok2 || !ok3 {
						return nil, fmt.Errorf("3MF model: object %d has a bad triangle index", cur.ID)
					}
					cur.Mesh.Triangles = append(cur.Mesh.Triangles, [3]uint32{a, b, c})
				}
			case "component":
				if cur != nil {
					id, err := strconv.Atoi(t.AttrString("objectid"))
					if err != nil {
						return nil, fmt.Errorf("3MF model: component without a numeric objectid")
					}
					c := ModelComponent{Path: t.AttrString("path"), ObjectID: id, Transform: Identity()}
					if s := t.AttrString("transform"); s != "" {
						if c.Transform, err = ParseMatrix(s); err != nil {
							return nil, fmt.Errorf("3MF model: component transform: %w", err)
						}
					}
					cur.Components = append(cur.Components, c)
				}
			case "item":
				id, err := strconv.Atoi(t.AttrString("objectid"))
				if err != nil {
					return nil, fmt.Errorf("3MF model: build item without a numeric objectid")
				}
				item := ModelItem{ObjectID: id, Path: t.AttrString("path"), Transform: Identity(), Printable: t.AttrString("printable") != "0"}
				if s := t.AttrString("transform"); s != "" {
					if item.Transform, err = ParseMatrix(s); err != nil {
						return nil, fmt.Errorf("3MF model: item transform: %w", err)
					}
				}
				m.Items = append(m.Items, item)
			}
		case xmlscan.End:
			switch string(t.Name) {
			case "object":
				cur = nil
			case "vertices":
				inVertices = false
			case "triangles":
				inTriangles = false
			}
		}
	}
	return m, nil
}

// unitScale converts a 3MF unit to millimetres.
func unitScale(unit string) float64 {
	switch strings.ToLower(unit) {
	case "micron":
		return 0.001
	case "centimeter":
		return 10
	case "inch":
		return 25.4
	case "foot":
		return 304.8
	case "meter":
		return 1000
	}
	return 1
}

// Placed is one build item of a 3MF as a single mesh in bed coordinates.
type Placed struct {
	Name string
	Mesh *Mesh
	// Unnamed is true when the file gives the object no name (Name is then
	// "object <id>"); callers that know the file name can do better.
	Unnamed bool
	// Origin is the XY offset to subtract from the mesh's file coordinates to get
	// plate relative ones: the origin of the plate the item sits on in a slicer
	// project (zero for other files).
	Origin [2]float64
	// Skipped names the parts of a slicer project object that were not taken:
	// modifiers, negative parts, support blockers and enforcers (see add_model).
	Skipped []string
}

// Read3MF reads every build item of a 3MF as one flattened mesh: components
// (also across model files of the production extension) resolved, component
// and build transforms applied, the model unit converted to millimetres. A
// package without a build section yields one entry per object. It reads the
// geometry only; use the threemf package for projects.
func Read3MF(path string) ([]Placed, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	files := map[string]*zip.File{}
	for _, f := range zr.File {
		files[strings.TrimPrefix(f.Name, "/")] = f
	}
	cache := map[string]*Model{}
	load := func(name string) (*Model, error) {
		name = strings.TrimPrefix(name, "/")
		if m, ok := cache[name]; ok {
			return m, nil
		}
		f := files[name]
		if f != nil && f.UncompressedSize64 > uint64(maxFileBytes) {
			return nil, fmt.Errorf("%w: %s inflates to %d bytes", ErrTooLarge, name, f.UncompressedSize64)
		}
		if f == nil {
			return nil, fmt.Errorf("3MF: model file %q is not in the package", name)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		m, err := ParseModel(rc)
		if err != nil {
			return nil, err
		}
		cache[name] = m
		return m, nil
	}
	const mainModel = "3D/3dmodel.model"
	main, err := load(mainModel)
	if err != nil {
		return nil, err
	}
	scale := unitScale(main.Unit)

	var flatten func(file string, id int, depth int) (*Mesh, string, error)
	flatten = func(file string, id, depth int) (*Mesh, string, error) {
		if depth > 16 {
			return nil, "", fmt.Errorf("3MF: components nest too deeply")
		}
		model, err := load(file)
		if err != nil {
			return nil, "", err
		}
		obj := model.Objects[id]
		if obj == nil {
			return nil, "", fmt.Errorf("3MF: object %d is not in %s", id, file)
		}
		if obj.Mesh != nil {
			if err := obj.Mesh.Validate(); err != nil {
				return nil, "", fmt.Errorf("3MF: object %d: %w", id, err)
			}
			return obj.Mesh, obj.Name, nil
		}
		out := &Mesh{}
		for _, c := range obj.Components {
			target := file
			if c.Path != "" {
				target = c.Path
			}
			sub, _, err := flatten(target, c.ObjectID, depth+1)
			if err != nil {
				return nil, "", err
			}
			out.Append(sub.Transformed(c.Transform))
		}
		return out, obj.Name, nil
	}

	var placed []Placed
	build := func(objFile string, id int, tr Matrix) error {
		m, name, err := flatten(objFile, id, 0)
		if err != nil {
			return err
		}
		if len(m.Triangles) == 0 {
			return nil
		}
		unnamed := name == ""
		if unnamed {
			name = fmt.Sprintf("object %d", id)
		}
		placed = append(placed, Placed{Name: name, Mesh: m.Transformed(tr.Then(Scale(scale, scale, scale))), Unnamed: unnamed})
		return nil
	}
	if len(main.Items) == 0 {
		for _, id := range main.Order {
			if err := build(mainModel, id, Identity()); err != nil {
				return nil, err
			}
		}
	}
	for _, it := range main.Items {
		if !it.Printable {
			continue // an item the project marks as not printable is not part of the model
		}
		file := mainModel
		if it.Path != "" {
			file = it.Path
		}
		if err := build(file, it.ObjectID, it.Transform); err != nil {
			return nil, err
		}
	}
	if len(placed) == 0 {
		return nil, ErrEmpty
	}
	return placed, nil
}
