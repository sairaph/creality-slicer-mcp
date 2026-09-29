package threemf

import (
	"bytes"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/sairaph/creality-slicer-mcp/internal/mesh"
	"github.com/sairaph/creality-slicer-mcp/internal/mesh/xmlscan"
)

// meshInfo is what one scan of a <mesh> element learns.
type meshInfo struct {
	Vertices, Triangles int
	Painted             Painted
	Min, Max            [3]float32
}

type componentXML struct {
	Path      string
	ObjectID  int
	UUID      string
	Transform mesh.Matrix
}

type fileObject struct {
	ID   int
	UUID string
	Type string
	Name string
	Mesh *meshInfo
	// Raw is the whole <object>...</object> element as written, kept for objects
	// with a mesh so a conversion to the split layout can move it verbatim.
	Raw        []byte
	Components []componentXML
}

type itemXML struct {
	ObjectID  int
	UUID      string
	Path      string
	Transform mesh.Matrix
	Printable bool
}

// modelFile is a scanned 3MF model file.
type modelFile struct {
	Root      string // the <model ...> start tag as written
	Metadata  KVs
	Objects   []*fileObject
	Items     []itemXML
	BuildUUID string
	HasBuild  bool
	Unit      string
}

func (m *modelFile) object(id int) *fileObject {
	for _, o := range m.Objects {
		if o.ID == id {
			return o
		}
	}
	return nil
}

// scanModel scans one model file. It counts vertices and triangles, notes
// painted triangle attributes and bounds per mesh, and collects the metadata,
// objects, components and build items, without keeping any geometry.
func scanModel(data []byte) (*modelFile, error) {
	data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf}) // offsets below index this slice
	sc := xmlscan.New(data)
	objStart := 0
	mf := &modelFile{Unit: "millimeter"}
	var cur *fileObject
	var metaName string
	var metaText strings.Builder
	inMeta := false
	for {
		t, ok, err := sc.Next()
		if err != nil {
			return nil, fmt.Errorf("model file: %w", err)
		}
		if !ok {
			break
		}
		switch t.Kind {
		case xmlscan.Start:
			switch string(t.Name) {
			case "model":
				if mf.Root == "" {
					mf.Root = string(t.Raw)
					if u := t.AttrString("unit"); u != "" {
						mf.Unit = u
					}
				}
			case "metadata":
				inMeta = true
				metaName = t.AttrString("name")
				metaText.Reset()
			case "object":
				id, err := strconv.Atoi(t.AttrString("id"))
				if err != nil {
					return nil, fmt.Errorf("model file: object without a numeric id")
				}
				uuid, _ := t.AttrFold("uuid")
				cur = &fileObject{ID: id, UUID: uuid, Type: t.AttrString("type"), Name: t.AttrString("name")}
				objStart = sc.Offset() - len(t.Raw)
				mf.Objects = append(mf.Objects, cur)
			case "mesh":
				if cur != nil {
					cur.Mesh = &meshInfo{Min: [3]float32{math.MaxFloat32, math.MaxFloat32, math.MaxFloat32}, Max: [3]float32{-math.MaxFloat32, -math.MaxFloat32, -math.MaxFloat32}}
				}
			case "vertex":
				if cur != nil && cur.Mesh != nil {
					cur.Mesh.Vertices++
					for i, k := range []string{"x", "y", "z"} {
						b, _ := t.AttrBytes(k)
						f, err := strconv.ParseFloat(string(b), 32)
						if err != nil {
							return nil, fmt.Errorf("model file: object %d has a bad vertex", cur.ID)
						}
						v := float32(f)
						if v < cur.Mesh.Min[i] {
							cur.Mesh.Min[i] = v
						}
						if v > cur.Mesh.Max[i] {
							cur.Mesh.Max[i] = v
						}
					}
				}
			case "triangle":
				if cur != nil && cur.Mesh != nil {
					cur.Mesh.Triangles++
					if b, ok := t.AttrBytes("paint_supports"); ok && len(b) > 0 {
						cur.Mesh.Painted.Supports = true
					}
					if b, ok := t.AttrBytes("paint_seam"); ok && len(b) > 0 {
						cur.Mesh.Painted.Seam = true
					}
					if b, ok := t.AttrBytes("paint_color"); ok && len(b) > 0 {
						cur.Mesh.Painted.Color = true
					}
					if b, ok := t.AttrBytes("paint_fuzzy_skin"); ok && len(b) > 0 {
						cur.Mesh.Painted.FuzzySkin = true
					}
					if b, ok := t.AttrBytes("face_property"); ok && len(b) > 0 {
						cur.Mesh.Painted.FaceProperty = true
					}
				}
			case "component":
				if cur != nil {
					id, err := strconv.Atoi(t.AttrString("objectid"))
					if err != nil {
						return nil, fmt.Errorf("model file: component without a numeric objectid")
					}
					uuid, _ := t.AttrFold("uuid")
					c := componentXML{Path: t.AttrString("path"), ObjectID: id, UUID: uuid, Transform: mesh.Identity()}
					if s := t.AttrString("transform"); s != "" {
						if c.Transform, err = mesh.ParseMatrix(s); err != nil {
							return nil, fmt.Errorf("model file: component transform: %w", err)
						}
					}
					cur.Components = append(cur.Components, c)
				}
			case "build":
				mf.HasBuild = true
				mf.BuildUUID, _ = t.AttrFold("uuid")
			case "item":
				id, err := strconv.Atoi(t.AttrString("objectid"))
				if err != nil {
					return nil, fmt.Errorf("model file: build item without a numeric objectid")
				}
				uuid, _ := t.AttrFold("uuid")
				it := itemXML{ObjectID: id, UUID: uuid, Path: t.AttrString("path"), Transform: mesh.Identity(), Printable: t.AttrString("printable") != "0"}
				if s := t.AttrString("transform"); s != "" {
					if it.Transform, err = mesh.ParseMatrix(s); err != nil {
						return nil, fmt.Errorf("model file: item transform: %w", err)
					}
				}
				mf.Items = append(mf.Items, it)
			}
		case xmlscan.Text:
			if inMeta {
				metaText.WriteString(xmlscan.Unescape(t.Data))
			}
		case xmlscan.End:
			switch string(t.Name) {
			case "metadata":
				if inMeta && metaName != "" {
					mf.Metadata = append(mf.Metadata, KV{metaName, metaText.String()})
				}
				inMeta = false
			case "object":
				if cur != nil && cur.Mesh != nil {
					cur.Raw = data[objStart:sc.Offset()]
				}
				cur = nil
			}
		}
	}
	if mf.Root == "" {
		return nil, fmt.Errorf("model file: no <model> element")
	}
	return mf, nil
}
