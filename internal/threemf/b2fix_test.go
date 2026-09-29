package threemf

import (
	"archive/zip"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sairaph/creality-slicer-mcp/internal/mesh"
)

// A Bambu Studio 2.x style package: every mesh is its own object inside
// 3D/3dmodel.model, the objects of the build are wrappers that reference the
// meshes as components of the same file (no p:path), and the root element has
// no production extension.
const inlineRoot = `<model unit="millimeter" xml:lang="en-US" xmlns="http://schemas.microsoft.com/3dmanufacturing/core/2015/02" xmlns:BambuStudio="http://schemas.bambulab.com/package/2021">`

const paintedTriangle = `<triangle v1="0" v2="2" v3="1" paint_color="4" paint_supports="8"/>`

func inlineMeshObject(id int, painted bool) string {
	tri := `<triangle v1="0" v2="2" v3="1"/>`
	if painted {
		tri = paintedTriangle
	}
	return `  <object id="` + strconvI(id) + `" type="model">
   <mesh>
    <vertices>
     <vertex x="-5" y="-5" z="-5"/>
     <vertex x="5" y="-5" z="-5"/>
     <vertex x="5" y="5" z="-5"/>
     <vertex x="-5" y="5" z="-5"/>
     <vertex x="-5" y="-5" z="5"/>
     <vertex x="5" y="-5" z="5"/>
     <vertex x="5" y="5" z="5"/>
     <vertex x="-5" y="5" z="5"/>
    </vertices>
    <triangles>
     ` + tri + `
     <triangle v1="0" v2="3" v3="2"/>
     <triangle v1="4" v2="5" v3="6"/>
     <triangle v1="4" v2="6" v3="7"/>
     <triangle v1="0" v2="1" v3="5"/>
     <triangle v1="0" v2="5" v3="4"/>
     <triangle v1="2" v2="3" v3="7"/>
     <triangle v1="2" v2="7" v3="6"/>
     <triangle v1="1" v2="2" v3="6"/>
     <triangle v1="1" v2="6" v3="5"/>
     <triangle v1="3" v2="0" v3="4"/>
     <triangle v1="3" v2="4" v3="7"/>
    </triangles>
   </mesh>
  </object>
`
}

func strconvI(n int) string { return itoa(n) }

func inlineWrapper(id, mesh int) string {
	return "  <object id=\"" + itoa(id) + "\" type=\"model\">\n   <components>\n    <component objectid=\"" + itoa(mesh) + "\" transform=\"1 0 0 0 1 0 0 0 1 0 0 0\"/>\n   </components>\n  </object>\n"
}

func itoa(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return itoa(n/10) + string(rune('0'+n%10))
}

func bambuInlineEntries() []entry {
	model := `<?xml version="1.0" encoding="UTF-8"?>
` + inlineRoot + `
 <metadata name="Application">BambuStudio-2.0.0</metadata>
 <metadata name="BambuStudio:3mfVersion">1</metadata>
 <metadata name="Title"></metadata>
 <resources>
` + inlineMeshObject(1, true) + inlineMeshObject(3, false) + inlineWrapper(2, 1) + inlineWrapper(4, 3) + inlineWrapper(5, 1) + ` </resources>
 <build>
  <item objectid="2" transform="1 0 0 0 1 0 0 0 1 10 10 5" printable="1"/>
  <item objectid="4" transform="1 0 0 0 1 0 0 0 1 40 10 5" printable="1"/>
  <item objectid="5" transform="1 0 0 0 1 0 0 0 1 70 10 5" printable="1"/>
 </build>
</model>
`
	settings := `<?xml version="1.0" encoding="UTF-8"?>
<config>
  <object id="2">
    <metadata key="name" value="First"/>
    <metadata key="extruder" value="1"/>
    <part id="1" subtype="normal_part">
      <metadata key="name" value="First"/>
      <metadata key="matrix" value="1 0 0 0 0 1 0 0 0 0 1 0 0 0 0 1"/>
      <mesh_stat edges_fixed="0" degenerate_facets="0" facets_removed="0" facets_reversed="0" backwards_edges="0"/>
    </part>
  </object>
  <object id="4">
    <metadata key="name" value="Second"/>
    <part id="3" subtype="normal_part">
      <metadata key="name" value="Second"/>
      <metadata key="matrix" value="1 0 0 0 0 1 0 0 0 0 1 0 0 0 0 1"/>
      <mesh_stat edges_fixed="0" degenerate_facets="0" facets_removed="0" facets_reversed="0" backwards_edges="0"/>
    </part>
  </object>
  <object id="5">
    <metadata key="name" value="Third"/>
    <part id="1" subtype="normal_part">
      <metadata key="name" value="Third"/>
      <metadata key="matrix" value="1 0 0 0 0 1 0 0 0 0 1 0 0 0 0 1"/>
      <mesh_stat edges_fixed="0" degenerate_facets="0" facets_removed="0" facets_reversed="0" backwards_edges="0"/>
    </part>
  </object>
  <plate>
    <metadata key="plater_id" value="1"/>
    <metadata key="plater_name" value=""/>
    <metadata key="locked" value="false"/>
    <model_instance>
      <metadata key="object_id" value="2"/>
      <metadata key="instance_id" value="0"/>
      <metadata key="identify_id" value="11"/>
    </model_instance>
    <model_instance>
      <metadata key="object_id" value="4"/>
      <metadata key="instance_id" value="0"/>
      <metadata key="identify_id" value="12"/>
    </model_instance>
    <model_instance>
      <metadata key="object_id" value="5"/>
      <metadata key="instance_id" value="0"/>
      <metadata key="identify_id" value="13"/>
    </model_instance>
  </plate>
  <assemble>
  </assemble>
</config>
`
	return []entry{
		{"[Content_Types].xml", []byte(contentTypes)},
		{"3D/3dmodel.model", []byte(model)},
		{"Metadata/model_settings.config", []byte(settings)},
		{"Metadata/project_settings.config", []byte(goldenProjectSettings)},
		{"Metadata/plate_1.png", pngBytes},
		{"_rels/.rels", []byte(rootRels)},
	}
}

func openBambuInline(t *testing.T) *Project {
	t.Helper()
	p, err := Open(writeZip(t, bambuInlineEntries()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	p.Now = fixedNow
	return p
}

func TestInlineSlicerProjectsListObjectsNotMeshResources(t *testing.T) {
	p := openBambuInline(t)
	if !p.IsSlicerProject || p.layout != layoutInline || len(p.Warnings) != 0 {
		t.Fatalf("%v %v %v", p.IsSlicerProject, p.layout, p.Warnings)
	}
	if len(p.Objects) != 3 || len(p.Items) != 3 {
		t.Fatalf("the mesh resources 1 and 3 are not objects: %d objects", len(p.Objects))
	}
	for _, id := range []int{2, 4, 5} {
		if p.Object(id) == nil || len(p.Object(id).Parts) != 1 {
			t.Errorf("object %d: %+v", id, p.Object(id))
		}
	}
	if !p.Object(2).Painted().Color || !p.Object(2).Painted().Supports || p.Object(4).Painted().Any() || !p.Object(5).Painted().Color {
		t.Errorf("painted flags: %+v %+v %+v", p.Object(2).Painted(), p.Object(4).Painted(), p.Object(5).Painted())
	}
	if !p.Object(2).Parts[0].Mesh.Shared || p.Object(4).Parts[0].Mesh.Shared {
		t.Error("objects 2 and 5 share mesh 1")
	}
	if m, err := p.LoadMesh(p.Object(4).Parts[0]); err != nil || len(m.Triangles) != 12 {
		t.Errorf("%v", err)
	}
	// Opened and saved without a change it stays exactly as it was.
	out := filepath.Join(t.TempDir(), "same.3mf")
	if err := p.Save(out); err != nil {
		t.Fatal(err)
	}
	_, got := readZip(t, out)
	for _, e := range bambuInlineEntries() {
		if !bytes.Equal(got[e.name], e.data) {
			t.Errorf("%s changed without a mutation", e.name)
		}
	}
}

func TestFirstMutationConvertsAnInlineProjectToTheSplitLayout(t *testing.T) {
	p := openBambuInline(t)
	rawMesh1 := extractObject(t, string(bambuInlineEntries()[1].data), 1)
	rawMesh3 := extractObject(t, string(bambuInlineEntries()[1].data), 3)
	if err := p.SetObjectOverride(4, "wall_loops", "4"); err != nil {
		t.Fatal(err)
	}
	q := saveReopen(t, p)
	names, got := readZip(t, q.Path())
	if q.layout != layoutSplit || len(q.Warnings) != 0 || len(q.Objects) != 3 {
		t.Fatalf("layout %v warnings %v objects %d", q.layout, q.Warnings, len(q.Objects))
	}
	// Two meshes, two sub model files, in the app's naming.
	var sub []string
	for _, n := range names {
		if strings.HasPrefix(n, "3D/Objects/") {
			sub = append(sub, n)
		}
	}
	if len(sub) != 2 || sub[0] != "3D/Objects/object_1.model" || sub[1] != "3D/Objects/object_2.model" {
		t.Fatalf("%v", sub)
	}
	// The meshes moved verbatim: the whole element, painted attributes included.
	if !bytes.Contains(got[sub[0]], []byte(rawMesh1)) || !bytes.Contains(got[sub[1]], []byte(rawMesh3)) {
		t.Errorf("the mesh objects must be moved byte for byte:\n%s", got[sub[0]])
	}
	if !bytes.Contains(got[sub[0]], []byte(`paint_color="4" paint_supports="8"`)) {
		t.Error("painted data lost")
	}
	// The root now declares the production extension; components carry p:path.
	model := string(got["3D/3dmodel.model"])
	if !strings.Contains(model, `xmlns:p="http://schemas.microsoft.com/3dmanufacturing/production/2015/06"`) || !strings.Contains(model, `requiredextensions="p"`) {
		t.Errorf("root:\n%.400s", model)
	}
	if c := strings.Count(model, `<component p:path="/3D/Objects/object_1.model" objectid="1"`); c != 2 {
		t.Errorf("two objects use mesh 1: %d\n%s", c, model)
	}
	if !strings.Contains(model, `<component p:path="/3D/Objects/object_2.model" objectid="3"`) || strings.Contains(model, "<mesh>") {
		t.Errorf("%s", model)
	}
	// Ids, names, settings and placement are unchanged.
	for _, id := range []int{2, 4, 5} {
		if q.Object(id) == nil {
			t.Fatalf("object %d lost", id)
		}
	}
	if q.Object(4).Config.Value("wall_loops") != "4" || q.Object(2).Name != "First" || q.Object(2).Extruder() != 1 {
		t.Errorf("%+v", q.Object(4).Config)
	}
	if q.Items[1].Transform.Translation() != [3]float64{40, 10, 5} || q.Plates[0].Instances[2].IdentifyID != 13 {
		t.Errorf("%+v", q.Items)
	}
	if !q.Object(2).Painted().Color || q.Object(4).Painted().Any() || !q.Object(2).Parts[0].Mesh.Shared {
		t.Error("painted flags and sharing must survive")
	}
	// The whole package reads back as geometry.
	placed, err := mesh.Read3MF(q.Path())
	if err != nil || len(placed) != 3 {
		t.Fatalf("%v %d", err, len(placed))
	}
	bb, _ := placed[1].Mesh.BBox()
	if bb.Min != [3]float32{35, 5, 0} || bb.Max != [3]float32{45, 15, 10} {
		t.Errorf("object 4 in bed coordinates: %v", bb)
	}
	// And it is editable like any other project from now on.
	if _, err := q.AddObject(ObjectSpec{Name: "New", Mesh: mesh.Box(3, 3, 3)}); err != nil {
		t.Fatal(err)
	}
}

// extractObject returns the <object id="n"> element of a model text as written.
func extractObject(t *testing.T, model string, id int) string {
	t.Helper()
	start := strings.Index(model, `<object id="`+itoa(id)+`"`)
	end := strings.Index(model[start:], "</object>")
	if start < 0 || end < 0 {
		t.Fatalf("object %d not found", id)
	}
	return model[start : start+end+len("</object>")]
}

func TestInlineMeshThatIsItselfTheBuildObjectGetsAWrapper(t *testing.T) {
	model := `<?xml version="1.0" encoding="UTF-8"?>
` + inlineRoot + `
 <metadata name="Application">BambuStudio-01.07.03.04</metadata>
 <resources>
` + inlineMeshObject(1, true) + ` </resources>
 <build>
  <item objectid="1" transform="1 0 0 0 1 0 0 0 1 20 20 5" printable="1"/>
 </build>
</model>
`
	settings := `<?xml version="1.0" encoding="UTF-8"?>
<config>
  <object id="1">
    <metadata key="name" value="Solo"/>
    <part id="1" subtype="normal_part">
      <metadata key="name" value="Solo"/>
      <metadata key="matrix" value="1 0 0 0 0 1 0 0 0 0 1 0 0 0 0 1"/>
    </part>
  </object>
  <plate>
    <metadata key="plater_id" value="1"/>
    <metadata key="plater_name" value=""/>
    <metadata key="locked" value="false"/>
    <model_instance>
      <metadata key="object_id" value="1"/>
      <metadata key="instance_id" value="0"/>
      <metadata key="identify_id" value="7"/>
    </model_instance>
  </plate>
  <assemble>
   <assemble_item object_id="1" instance_id="0" transform="1 0 0 0 1 0 0 0 1 0 0 0" offset="0 0 0" />
  </assemble>
</config>
`
	p, err := Open(writeZip(t, []entry{{"3D/3dmodel.model", []byte(model)}, {"Metadata/model_settings.config", []byte(settings)}}))
	if err != nil {
		t.Fatal(err)
	}
	p.Now = fixedNow
	if len(p.Objects) != 1 || p.Objects[0].ID != 1 || !p.IsSlicerProject {
		t.Fatalf("%+v", p.Objects)
	}
	if err := p.SetObjectOverride(1, "wall_loops", "3"); err != nil { // the old id still names the object
		t.Fatal(err)
	}
	q := saveReopen(t, p)
	o := q.Objects[0]
	if o.ID != 2 || o.Parts[0].ID != 1 || o.Name != "Solo" || o.Config.Value("wall_loops") != "3" {
		t.Fatalf("the mesh object 1 becomes the mesh of wrapper 2: object %d part %d %+v", o.ID, o.Parts[0].ID, o.Config)
	}
	if q.Items[0].ObjectID != 2 || q.Plates[0].Instances[0].ObjectID != 2 || q.Plates[0].Instances[0].IdentifyID != 7 || len(q.Assemble) != 1 || q.Assemble[0].ObjectID != 2 {
		t.Errorf("references must follow: %+v %+v %+v", q.Items[0], q.Plates[0].Instances, q.Assemble)
	}
	if q.Assemble[0].Attrs.Value("object_id") != "2" {
		t.Errorf("%v", q.Assemble[0].Attrs)
	}
	if !o.Painted().Color {
		t.Error("painted data")
	}
	placed, err := mesh.Read3MF(q.Path())
	if err != nil || len(placed) != 1 {
		t.Fatalf("%v", err)
	}
	if bb, _ := placed[0].Mesh.BBox(); bb.Min != [3]float32{15, 15, 0} {
		t.Errorf("%v", bb)
	}
}

// ---------------------------------------------------------------- slice results

const sliceInfoWithPlates = `<?xml version="1.0" encoding="UTF-8"?>
<config>
  <header>
    <header_item key="X-CX-Client-Type" value="creality_print"/>
    <header_item key="X-CX-Client-Version" value="07.02.02.5483"/>
  </header>
  <plate>
    <metadata key="index" value="1"/>
    <metadata key="prediction" value="1285"/>
    <filament id="1" type="PLA" color="#FFFFFF" used_m="1.2" used_g="3.58"/>
  </plate>
  <plate>
    <metadata key="index" value="2"/>
    <metadata key="prediction" value="900"/>
  </plate>
</config>
`

const gcodeRels = `<?xml version="1.0" encoding="UTF-8"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
 <Relationship Target="/Metadata/plate_1.gcode" Id="rel-1" Type="http://schemas.bambulab.com/package/2021/gcode"/>
 <Relationship Target="/Metadata/plate_2.gcode" Id="rel-2" Type="http://schemas.bambulab.com/package/2021/gcode"/>
</Relationships>`

// twoPlateSliced is the golden hand-made project with a second plate holding
// object 3, both plates sliced.
func twoPlateSliced(t *testing.T) *Project {
	t.Helper()
	settings := strings.Replace(goldenSettings, `    <metadata key="thumbnail_file" value="Metadata/plate_1.png"/>`, `    <metadata key="gcode_file" value="Metadata/plate_1.gcode"/>
    <metadata key="thumbnail_file" value="Metadata/plate_1.png"/>`, 1)
	settings = strings.Replace(settings, `    <model_instance>
      <metadata key="object_id" value="3"/>
      <metadata key="instance_id" value="0"/>
      <metadata key="identify_id" value="125"/>
    </model_instance>
  </plate>`, `  </plate>
  <plate>
    <metadata key="plater_id" value="2"/>
    <metadata key="plater_name" value="Second"/>
    <metadata key="locked" value="false"/>
    <metadata key="gcode_file" value="Metadata/plate_2.gcode"/>
    <model_instance>
      <metadata key="object_id" value="3"/>
      <metadata key="instance_id" value="0"/>
      <metadata key="identify_id" value="125"/>
    </model_instance>
  </plate>`, 1)
	entries := goldenEntries()
	for i := range entries {
		switch entries[i].name {
		case "Metadata/model_settings.config":
			entries[i].data = []byte(settings)
		case "Metadata/slice_info.config":
			entries[i].data = []byte(sliceInfoWithPlates)
		}
	}
	entries = append(entries,
		entry{"Metadata/plate_1.gcode", []byte("; plate 1\n")},
		entry{"Metadata/plate_1.gcode.md5", []byte("AAAA")},
		entry{"Metadata/plate_2.gcode", []byte("; plate 2\n")},
		entry{"Metadata/plate_2.gcode.md5", []byte("BBBB")},
		entry{"Metadata/_rels/model_settings.config.rels", []byte(gcodeRels)},
		entry{"Metadata/plate_2.png", pngBytes},
		entry{"Metadata/plate_2.json", []byte("{}")},
	)
	p, err := Open(writeZip(t, entries))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	p.Now = fixedNow
	if len(p.Plates) != 2 || p.Plate(1).Config.Value("gcode_file") != "Metadata/plate_1.gcode" {
		t.Fatalf("setup: %+v", p.Plates)
	}
	return p
}

func members2(t *testing.T, p *Project) map[string]bool {
	t.Helper()
	out := filepath.Join(t.TempDir(), "m.3mf")
	if err := p.Save(out); err != nil {
		t.Fatal(err)
	}
	names, _ := readZip(t, out)
	set := map[string]bool{}
	for _, n := range names {
		set[n] = true
	}
	return set
}

func TestChangingAPlateResetsItsSliceResult(t *testing.T) {
	p := twoPlateSliced(t)
	if got := p.SliceResults(); len(got) != 2 {
		t.Fatalf("%+v", got)
	}
	// Moving object 2 (plate 1) resets plate 1 only.
	if err := p.SetTransform(2, 0, mesh.Translate(5, 5, 0)); err != nil {
		t.Fatal(err)
	}
	if got := p.SliceResults(); len(got) != 1 || got[0].Plate != 2 {
		t.Fatalf("plate 2 keeps its result: %+v", got)
	}
	out := filepath.Join(t.TempDir(), "r.3mf")
	if err := p.Save(out); err != nil {
		t.Fatal(err)
	}
	names, got := readZip(t, out)
	set := map[string]bool{}
	for _, n := range names {
		set[n] = true
	}
	if set["Metadata/plate_1.gcode"] || set["Metadata/plate_1.gcode.md5"] || !set["Metadata/plate_2.gcode"] || !set["Metadata/plate_2.gcode.md5"] {
		t.Errorf("%v", names)
	}
	info := string(got["Metadata/slice_info.config"])
	if strings.Contains(info, `value="1285"`) || !strings.Contains(info, `value="900"`) || !strings.Contains(info, "X-CX-Client-Type") {
		t.Errorf("slice_info.config:\n%s", info)
	}
	rels := string(got["Metadata/_rels/model_settings.config.rels"])
	if strings.Contains(rels, "plate_1.gcode") || !strings.Contains(rels, "plate_2.gcode") {
		t.Errorf("rels:\n%s", rels)
	}
	if strings.Contains(string(got["Metadata/model_settings.config"]), `value="Metadata/plate_1.gcode"`) || !strings.Contains(string(got["Metadata/model_settings.config"]), `value="Metadata/plate_2.gcode"`) {
		t.Errorf("gcode_file keys:\n%s", got["Metadata/model_settings.config"])
	}
	// The images stay (the app regenerates them; SetPlateThumbnails replaces them).
	if !set["Metadata/plate_1.png"] || !set["Metadata/plate_2.png"] {
		t.Error("images are not touched by a slice reset")
	}

	// A setting of an object on plate 2 resets plate 2: nothing is left, the
	// slice info is header only and the relationship file goes.
	q, err := Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	q.Now = fixedNow
	if err := q.SetObjectOverride(3, "wall_loops", "5"); err != nil {
		t.Fatal(err)
	}
	out2 := filepath.Join(t.TempDir(), "r2.3mf")
	if err := q.Save(out2); err != nil {
		t.Fatal(err)
	}
	names2, got2 := readZip(t, out2)
	for _, n := range names2 {
		if strings.Contains(n, ".gcode") || strings.Contains(n, "model_settings.config.rels") {
			t.Errorf("%s must be gone", n)
		}
	}
	if string(got2["Metadata/slice_info.config"]) != goldenSliceInfo {
		t.Errorf("slice_info.config must be the header only:\n%s", got2["Metadata/slice_info.config"])
	}
}

func TestWhichChangesResetWhichPlates(t *testing.T) {
	has := func(p *Project, plate int) bool {
		for _, r := range p.SliceResults() {
			if r.Plate == plate {
				return true
			}
		}
		return false
	}
	steps := map[string]func(p *Project) error{
		"object override": func(p *Project) error { return p.SetObjectOverride(2, "wall_loops", "3") },
		"delete override": func(p *Project) error { _, err := p.DeleteObjectOverride(2, "extruder"); return err },
		"layer ranges": func(p *Project) error {
			return p.SetLayerRanges(2, []LayerRange{{MinZ: 0, MaxZ: 2, Options: KVs{{"layer_height", "0.1"}}}})
		},
		"add part": func(p *Project) error {
			_, err := p.AddPart(2, PartSpec{Mesh: mesh.Box(1, 1, 1)})
			return err
		},
		"custom gcode": func(p *Project) error {
			return p.SetCustomGCodes(1, ModeMultiExtruder, []GCodeItem{{TopZ: 1, Type: GCodePausePrint}})
		},
		"plate setting": func(p *Project) error { return p.SetPlateKey(1, "print_sequence", "by object") },
		"add object": func(p *Project) error {
			_, err := p.AddObject(ObjectSpec{Mesh: mesh.Box(2, 2, 2), Plate: 1})
			return err
		},
		"remove object": func(p *Project) error { return p.RemoveObject(2) },
	}
	for name, step := range steps {
		p := twoPlateSliced(t)
		if err := step(p); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if has(p, 1) || !has(p, 2) {
			t.Errorf("%s: plate 1 must be reset and plate 2 kept: %+v", name, p.SliceResults())
		}
	}
	// Moving an instance between plates resets both.
	p := twoPlateSliced(t)
	if err := p.MoveInstance(3, 0, 1); err != nil {
		t.Fatal(err)
	}
	if has(p, 1) || has(p, 2) {
		t.Errorf("%+v", p.SliceResults())
	}
	// Things that do not change the slice keep the results.
	p = twoPlateSliced(t)
	for name, step := range map[string]func() error{
		"rename plate":  func() error { return p.RenamePlate(1, "x") },
		"lock plate":    func() error { return p.LockPlate(1, true) },
		"rename object": func() error { return p.RenameObject(2, "y") },
		"metadata":      func() error { return p.SetMetadata("Title", "t") },
		"image key":     func() error { return p.SetPlateKey(1, "thumbnail_file", "Metadata/plate_1.png") },
		"thumbnails":    func() error { return p.SetPlateThumbnails(1, map[ThumbnailKind][]byte{ThumbPlate: pngBytes}) },
	} {
		if err := step(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !has(p, 1) || !has(p, 2) {
			t.Errorf("%s must not reset: %+v", name, p.SliceResults())
		}
	}
	p.AddPlate("third")
	if !has(p, 1) || !has(p, 2) {
		t.Error("adding a plate changes nothing")
	}
	// Changing the project settings resets every plate.
	q := twoPlateSliced(t)
	q.Settings.SetString("layer_height", "0.1")
	set := members2(t, q)
	if set["Metadata/plate_1.gcode"] || set["Metadata/plate_2.gcode"] {
		t.Error("a settings change resets every plate")
	}
	// Removing plate 1 drops its results and those of the plates that shift.
	r := twoPlateSliced(t)
	if err := r.MoveInstance(2, 0, 2); err != nil {
		t.Fatal(err)
	}
	if err := r.RemovePlate(1); err != nil {
		t.Fatal(err)
	}
	if len(r.SliceResults()) != 0 || len(r.Plates) != 1 || r.Plates[0].Index != 1 {
		t.Errorf("%+v", r.SliceResults())
	}
}

func TestRemovePlateRenamesTheImagesOfTheLaterPlates(t *testing.T) {
	p := twoPlateSliced(t)
	if err := p.MoveInstance(3, 0, 1); err != nil {
		t.Fatal(err)
	}
	// Plate 2 is now empty and has the picture plate_2.png; plate 3 has its own.
	p.AddPlate("third")
	if err := p.MoveInstance(3, 0, 3); err != nil {
		t.Fatal(err)
	}
	if err := p.SetPlateThumbnails(3, map[ThumbnailKind][]byte{ThumbPlate: append(append([]byte(nil), pngBytes...), '3')}); err != nil {
		t.Fatal(err)
	}
	if err := p.RemovePlate(2); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "r.3mf")
	if err := p.Save(out); err != nil {
		t.Fatal(err)
	}
	names, got := readZip(t, out)
	set := map[string]bool{}
	for _, n := range names {
		set[n] = true
	}
	if set["Metadata/plate_3.png"] || !set["Metadata/plate_2.png"] || !bytes.HasSuffix(got["Metadata/plate_2.png"], []byte("3")) {
		t.Errorf("the picture of old plate 3 becomes plate_2.png: %v", names)
	}
	if set["Metadata/plate_2.json"] {
		t.Error("the removed plate loses its pick data")
	}
	q, err := Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	if q.Plate(2) == nil || q.Plate(2).Config.Value("thumbnail_file") != "Metadata/plate_2.png" || q.Plate(3) != nil {
		t.Errorf("%+v", q.Plates)
	}
}

// ---------------------------------------------------------------- thumbnails

const goldenRootRels = `<?xml version="1.0" encoding="UTF-8"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
 <Relationship Target="/3D/3dmodel.model" Id="rel-1" Type="http://schemas.microsoft.com/3dmanufacturing/2013/01/3dmodel"/>
 <Relationship Target="/Metadata/plate_1.png" Id="rel-2" Type="http://schemas.openxmlformats.org/package/2006/relationships/metadata/thumbnail"/>
 <Relationship Target="/Metadata/plate_1.png" Id="rel-4" Type="http://schemas.bambulab.com/package/2021/cover-thumbnail-middle"/>
<Relationship Target="/Metadata/plate_1_small.png" Id="rel-5" Type="http://schemas.bambulab.com/package/2021/cover-thumbnail-small"/>
</Relationships>`

func allImages() map[ThumbnailKind][]byte {
	img := func(tag string) []byte { return append(append([]byte(nil), pngBytes...), tag...) }
	return map[ThumbnailKind][]byte{
		ThumbPlate: img("plate"), ThumbSmall: img("small"), ThumbNoLight: img("nolight"), ThumbTop: img("top"), ThumbPick: img("pick"),
	}
}

func TestThumbnailsAreWrittenLikeTheGolden(t *testing.T) {
	p := newProject(t)
	if _, err := p.AddObject(ObjectSpec{Name: "Cube", Mesh: mesh.Box(10, 10, 10)}); err != nil {
		t.Fatal(err)
	}
	if err := p.SetPlateThumbnails(1, allImages()); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "t.3mf")
	if err := p.Save(path); err != nil {
		t.Fatal(err)
	}
	names, got := readZip(t, path)
	for _, want := range []string{"Metadata/plate_1.png", "Metadata/plate_1_small.png", "Metadata/plate_no_light_1.png", "Metadata/top_1.png", "Metadata/pick_1.png"} {
		if _, ok := got[want]; !ok {
			t.Errorf("%s missing from %v", want, names)
		}
	}
	if !bytes.HasSuffix(got["Metadata/plate_1_small.png"], []byte("small")) || !bytes.HasSuffix(got["Metadata/pick_1.png"], []byte("pick")) {
		t.Error("image content")
	}
	if string(got["_rels/.rels"]) != goldenRootRels {
		t.Errorf("_rels/.rels:\n%s", got["_rels/.rels"])
	}
	model := string(got["3D/3dmodel.model"])
	if !strings.Contains(model, ` <metadata name="Origin"></metadata>
 <metadata name="Thumbnail_Middle">/Metadata/plate_1.png</metadata>
 <metadata name="Thumbnail_Small">/Metadata/plate_1_small.png</metadata>
 <metadata name="Title"></metadata>
`) {
		t.Errorf("metadata:\n%.900s", model)
	}
	settings := string(got["Metadata/model_settings.config"])
	if !strings.Contains(settings, `    <metadata key="locked" value="false"/>
    <metadata key="thumbnail_file" value="Metadata/plate_1.png"/>
    <metadata key="thumbnail_no_light_file" value="Metadata/plate_no_light_1.png"/>
    <metadata key="top_file" value="Metadata/top_1.png"/>
    <metadata key="pick_file" value="Metadata/pick_1.png"/>
    <model_instance>`) {
		t.Errorf("plate keys:\n%s", settings)
	}
	// Images are stored, not compressed, like the app does.
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	for _, f := range zr.File {
		if strings.HasSuffix(f.Name, ".png") && f.Method != zip.Store {
			t.Errorf("%s is compressed", f.Name)
		}
	}
	q := saveReopen(t, p)
	if img, err := q.Thumbnail(1, ThumbTop); err != nil || !bytes.HasSuffix(img, []byte("top")) {
		t.Errorf("%v", err)
	}
	if _, err := q.Thumbnail(2, ThumbTop); !errors.Is(err, ErrNotFound) {
		t.Errorf("%v", err)
	}
}

func TestThumbnailsOfOtherPlatesAndPartialSets(t *testing.T) {
	p := newProject(t)
	p.AddObject(ObjectSpec{Name: "Cube", Mesh: mesh.Box(10, 10, 10)})
	p.AddPlate("two")
	if err := p.SetPlateThumbnails(2, allImages()); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "t.3mf")
	if err := p.Save(path); err != nil {
		t.Fatal(err)
	}
	_, got := readZip(t, path)
	if _, ok := got["Metadata/plate_2.png"]; !ok {
		t.Error("plate 2 image")
	}
	// Plate 2 images touch neither the root relationships nor the cover metadata.
	if string(got["_rels/.rels"]) != rootRels || strings.Contains(string(got["3D/3dmodel.model"]), "Thumbnail_") {
		t.Errorf("only plate 1 is the cover")
	}
	q := saveReopen(t, p)
	// A partial set for plate 1 writes only those images and their relationships.
	if err := q.SetPlateThumbnails(1, map[ThumbnailKind][]byte{ThumbPlate: allImages()[ThumbPlate]}); err != nil {
		t.Fatal(err)
	}
	r := saveReopen(t, q)
	_, got = readZip(t, r.Path())
	wantRels := strings.Replace(goldenRootRels, "<Relationship Target=\"/Metadata/plate_1_small.png\" Id=\"rel-5\" Type=\"http://schemas.bambulab.com/package/2021/cover-thumbnail-small\"/>\n", "", 1)
	if string(got["_rels/.rels"]) != wantRels || !strings.Contains(string(got["3D/3dmodel.model"]), "Thumbnail_Middle") || strings.Contains(string(got["3D/3dmodel.model"]), "Thumbnail_Small") {
		t.Errorf("%s", got["_rels/.rels"])
	}
	if _, ok := got["Metadata/top_1.png"]; ok {
		t.Error("kinds that were not given are not written")
	}
	// Replacing the images again works.
	if err := r.SetPlateThumbnails(1, allImages()); err != nil {
		t.Fatal(err)
	}
	s := saveReopen(t, r)
	_, got = readZip(t, s.Path())
	if string(got["_rels/.rels"]) != goldenRootRels {
		t.Errorf("%s", got["_rels/.rels"])
	}
}

func TestThumbnailsReplaceStalePickDataAndPatchContentTypes(t *testing.T) {
	entries := goldenEntries()
	for i := range entries {
		if entries[i].name == "[Content_Types].xml" {
			entries[i].data = []byte(strings.Replace(string(entries[i].data), ` <Default Extension="png" ContentType="image/png"/>`+"\n", "", 1))
		}
	}
	entries = append(entries, entry{"Metadata/plate_1.json", []byte(`{"bbox_all":[1,2,3,4]}`)})
	p, err := Open(writeZip(t, entries))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	p.Now = fixedNow
	if err := p.SetPlateThumbnails(1, map[ThumbnailKind][]byte{ThumbPlate: allImages()[ThumbPlate]}); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "o.3mf")
	if err := p.Save(out); err != nil {
		t.Fatal(err)
	}
	names, got := readZip(t, out)
	for _, n := range names {
		if n == "Metadata/plate_1.json" {
			t.Error("plate_1.json describes the old picture and must be deleted")
		}
	}
	if !strings.Contains(string(got["[Content_Types].xml"]), `Extension="png"`) {
		t.Errorf("png content type:\n%s", got["[Content_Types].xml"])
	}
	// The image members of the golden project, the object files, everything else is untouched.
	_, want := readZip(t, p.Path())
	_ = want
	if !bytes.Equal(got["3D/Objects/object_1.model"], []byte(goldenObject1)) {
		t.Error("object files must not change")
	}
}

func TestThumbnailErrors(t *testing.T) {
	p := newProject(t)
	if err := p.SetPlateThumbnails(9, map[ThumbnailKind][]byte{ThumbPlate: pngBytes}); !errors.Is(err, ErrNotFound) {
		t.Errorf("%v", err)
	}
	if err := p.SetPlateThumbnails(1, nil); !errors.Is(err, ErrInvalid) {
		t.Errorf("%v", err)
	}
	if err := p.SetPlateThumbnails(1, map[ThumbnailKind][]byte{"huge": pngBytes}); !errors.Is(err, ErrInvalid) {
		t.Errorf("%v", err)
	}
	if err := p.SetPlateThumbnails(1, map[ThumbnailKind][]byte{ThumbPlate: []byte("GIF89a")}); !errors.Is(err, ErrInvalid) {
		t.Errorf("%v", err)
	}
	if err := p.SetPlateThumbnails(1, map[ThumbnailKind][]byte{ThumbPlate: pngBytes, ThumbTop: []byte("nope")}); !errors.Is(err, ErrInvalid) {
		t.Errorf("one bad image rejects the call: %v", err)
	}
	if _, ok := p.byName["Metadata/plate_1.png"]; ok {
		t.Error("nothing may be written for a rejected call")
	}
	if _, err := p.Thumbnail(1, "huge"); !errors.Is(err, ErrInvalid) {
		t.Errorf("%v", err)
	}
}

func TestNewProjectWithoutThumbnailsIsValidAndThumbnailsCanComeLater(t *testing.T) {
	p := newProject(t)
	p.AddObject(ObjectSpec{Name: "A", Mesh: mesh.Box(4, 4, 4)})
	q := saveReopen(t, p)
	if len(q.Warnings) != 0 || q.Plate(1).Config.Value("thumbnail_file") != "" {
		t.Errorf("%v %v", q.Warnings, q.Plate(1).Config)
	}
	if err := q.SetPlateThumbnails(1, allImages()); err != nil {
		t.Fatal(err)
	}
	r := saveReopen(t, q)
	if img, err := r.Thumbnail(1, ThumbPlate); err != nil || len(img) == 0 {
		t.Errorf("%v", err)
	}
	if _, err := os.Stat(r.Path()); err != nil {
		t.Error(err)
	}
}
