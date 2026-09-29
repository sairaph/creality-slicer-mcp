package threemf

import (
	"archive/zip"
	"bytes"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/sairaph/creality-slicer-mcp/internal/mesh"
)

func newProject(t *testing.T) *Project {
	t.Helper()
	return New(NewOptions{AppVersion: "7.2.2.5483", Now: fixedNow})
}

func saveReopen(t *testing.T, p *Project) *Project {
	t.Helper()
	path := filepath.Join(t.TempDir(), "p.3mf")
	if err := p.Save(path); err != nil {
		t.Fatal(err)
	}
	p.Close() // it is bound to the file just written; the caller continues with the reopened copy
	q, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	q.Now = fixedNow
	t.Cleanup(func() { q.Close() })
	return q
}

func TestNewProjectIsWrittenLikeTheApp(t *testing.T) {
	p := newProject(t)
	o, err := p.AddObject(ObjectSpec{Name: "Cube", Mesh: mesh.Box(10, 10, 10), Extruder: 2})
	if err != nil {
		t.Fatal(err)
	}
	if o.ID != 2 || o.Parts[0].ID != 1 {
		t.Fatalf("ids: object %d part %d (mesh objects come first, the wrapper is the next id)", o.ID, o.Parts[0].ID)
	}
	path := filepath.Join(t.TempDir(), "new.3mf")
	if err := p.Save(path); err != nil {
		t.Fatal(err)
	}
	names, got := readZip(t, path)
	wantNames := []string{
		"[Content_Types].xml", "3D/3dmodel.model", "3D/_rels/3dmodel.model.rels", "3D/Objects/object_1.model",
		"Metadata/custom_gcode_per_layer.xml", "Metadata/model_settings.config", "Metadata/slice_info.config",
		"Metadata/creality.config", "_rels/.rels",
	}
	// The default project has no project_settings until the layer above sets it.
	if strings.Join(names, "|") != strings.Join(wantNames, "|") {
		t.Errorf("members:\n%v\nwant\n%v", names, wantNames)
	}
	// The object file is the golden one (cube, ids, uuid pattern).
	if string(got["3D/Objects/object_1.model"]) != goldenObject1 {
		t.Errorf("object file:\n%s", got["3D/Objects/object_1.model"])
	}
	wantModel := `<?xml version="1.0" encoding="UTF-8"?>
` + goldenRoot + `
 <metadata name="Application">Creality_Print V7.2.2.5483 Release</metadata>
 <metadata name="BambuStudio:3mfVersion">1</metadata>
 <metadata name="Copyright"></metadata>
 <metadata name="CreationDate">2026-09-29</metadata>
 <metadata name="Description"></metadata>
 <metadata name="Designer"></metadata>
 <metadata name="DesignerCover"></metadata>
 <metadata name="DesignerUserId"></metadata>
 <metadata name="License"></metadata>
 <metadata name="ModificationDate">2026-09-29</metadata>
 <metadata name="Origin"></metadata>
 <metadata name="Title"></metadata>
 <resources>
  <object id="2" p:UUID="00000001-61cb-4c03-9d28-80fed5dfa1dc" type="model">
   <components>
    <component p:path="/3D/Objects/object_1.model" objectid="1" p:UUID="00010000-b206-40ff-9872-83e8017abed1" transform="1 0 0 0 1 0 0 0 1 0 0 0"/>
   </components>
  </object>
 </resources>
 <build p:UUID="2c7c17d8-22b5-4d84-8835-1976022ea369">
  <item objectid="2" p:UUID="00000002-b1ec-4553-aec9-835e5b724bb4" transform="1 0 0 0 1 0 0 0 1 0 0 0" printable="1"/>
 </build>
</model>
`
	if string(got["3D/3dmodel.model"]) != wantModel {
		t.Errorf("3dmodel.model:\n%s", got["3D/3dmodel.model"])
	}
	wantSettings := `<?xml version="1.0" encoding="UTF-8"?>
<config>
  <object id="2">
    <metadata key="name" value="Cube"/>
    <metadata key="extruder" value="2"/>
    <part id="1" subtype="normal_part">
      <metadata key="name" value="Cube"/>
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
      <metadata key="identify_id" value="1"/>
    </model_instance>
  </plate>
  <assemble>
  </assemble>
</config>
`
	if string(got["Metadata/model_settings.config"]) != wantSettings {
		t.Errorf("model_settings.config:\n%s", got["Metadata/model_settings.config"])
	}
	for name, want := range map[string]string{
		"[Content_Types].xml":                 contentTypes,
		"3D/_rels/3dmodel.model.rels":         `<?xml version="1.0" encoding="UTF-8"?>` + "\n" + `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` + "\n" + ` <Relationship Target="/3D/Objects/object_1.model" Id="rel-1" Type="http://schemas.microsoft.com/3dmanufacturing/2013/01/3dmodel"/>` + "\n</Relationships>",
		"_rels/.rels":                         rootRels,
		"Metadata/slice_info.config":          goldenSliceInfo,
		"Metadata/creality.config":            goldenCreality,
		"Metadata/custom_gcode_per_layer.xml": "<?xml version=\"1.0\" encoding=\"utf-8\"?>\n<custom_gcodes_per_layer>\n<plate>\n<plate_info id=\"1\"/>\n<mode value=\"SingleExtruder\"/>\n</plate>\n</custom_gcodes_per_layer>",
	} {
		if string(got[name]) != want {
			t.Errorf("%s:\n%s", name, got[name])
		}
	}
	// It opens again with everything intact, and the 7.2 CLI recognises it by
	// the Application prefix.
	q := saveReopen(t, p)
	if !strings.HasPrefix(q.Metadata.Value("Application"), "Creality_Print V") || len(q.Objects) != 1 || q.Objects[0].Extruder() != 2 || len(q.Warnings) != 0 {
		t.Errorf("%v %v", q.Metadata, q.Warnings)
	}
}

func TestNewProjectWithSettingsAndSeveralObjects(t *testing.T) {
	p := newProject(t)
	cfg := NewConfig()
	cfg.SetString("name", "project_settings")
	cfg.SetString("from", "project")
	cfg.SetList("filament_colour", "#FFFFFF", "#000000")
	cfg.SetList("compatible_printers")
	cfg.SetString("version", "7.2.2.5483")
	p.SetSettings(cfg)

	tr := mesh.Translate(100, 50, 0)
	box := mesh.Box(10, 10, 10).Transformed(mesh.Translate(5, 5, 5)) // occupies 0..10
	a, err := p.AddObject(ObjectSpec{Name: "A", Mesh: box, Transform: &tr})
	if err != nil {
		t.Fatal(err)
	}
	b, err := p.AddObject(ObjectSpec{Name: "B", Mesh: mesh.Cylinder(4, 20), Extruder: 1, Config: KVs{{"wall_loops", "5"}, {"enable_support", "1"}}})
	if err != nil {
		t.Fatal(err)
	}
	if a.ID != 2 || b.ID != 4 || b.Parts[0].ID != 3 {
		t.Fatalf("ids %d %d %d", a.ID, b.ID, b.Parts[0].ID)
	}
	if a.UUID != "00000001-61cb-4c03-9d28-80fed5dfa1dc" || b.UUID != "00000002-61cb-4c03-9d28-80fed5dfa1dc" {
		t.Errorf("%q %q", a.UUID, b.UUID)
	}
	// Object config keys are kept sorted like the app's config keys.
	if b.Config[0].Key != "enable_support" || b.Config[1].Key != "extruder" || b.Config[2].Key != "wall_loops" {
		t.Errorf("%v", b.Config)
	}
	q := saveReopen(t, p)
	if len(q.Objects) != 2 || len(q.Items) != 2 || len(q.Plates[0].Instances) != 2 {
		t.Fatalf("%d %d %d", len(q.Objects), len(q.Items), len(q.Plates[0].Instances))
	}
	// The mesh is stored centred and the placement is in the item transform.
	if got := q.Items[0].Transform.Translation(); got != [3]float64{105, 55, 5} {
		t.Errorf("item %v", got)
	}
	qa := q.Object(2)
	if qa.Parts[0].Mesh.Min != [3]float32{-5, -5, -5} || qa.Parts[0].Mesh.Max != [3]float32{5, 5, 5} {
		t.Errorf("stored mesh bounds %v %v", qa.Parts[0].Mesh.Min, qa.Parts[0].Mesh.Max)
	}
	m, err := q.LoadMesh(q.Object(4).Parts[0])
	if err != nil || !m.Stats().Manifold || math.Abs(m.Volume()-mesh.Cylinder(4, 20).Volume()) > 1e-3 {
		t.Errorf("cylinder round trip: %v", err)
	}
	// Placed through item and component transforms, A is where it was asked to be.
	placed := m.Transformed(q.Items[1].Transform)
	_ = placed
	world, err := mesh.Read3MF(q.Path())
	if err != nil || len(world) != 2 {
		t.Fatalf("Read3MF: %v %d", err, len(world))
	}
	bb, _ := world[0].Mesh.BBox()
	if bb.Min != [3]float32{100, 50, 0} || bb.Max != [3]float32{110, 60, 10} {
		t.Errorf("object A in bed coordinates: %v", bb)
	}
	if q.Settings == nil || q.Settings.String("from") != "project" || len(q.Settings.List("filament_colour")) != 2 {
		t.Errorf("settings")
	}
	if v, _ := q.Settings.Get("compatible_printers"); !v.IsList || len(v.List) != 0 {
		t.Errorf("%+v", v)
	}
	ids := map[int]bool{}
	for _, in := range q.Plates[0].Instances {
		if in.IdentifyID <= 0 || ids[in.IdentifyID] {
			t.Errorf("identify ids must be unique and positive: %+v", q.Plates[0].Instances)
		}
		ids[in.IdentifyID] = true
	}
	if q.Plates[0].Instances[1].IdentifyID <= q.Plates[0].Instances[0].IdentifyID {
		t.Error("identify ids increase")
	}
}

func TestProjectSettingsAreOnlyRewrittenWhenChanged(t *testing.T) {
	p := openGolden(t)
	out := filepath.Join(t.TempDir(), "s.3mf")
	p.Settings.SetString("wipe_tower_rotation_angle", "0") // new key, sorted insert
	p.Settings.SetList("filament_colour", "#111111", "#222222", "#333333")
	if err := p.Save(out); err != nil {
		t.Fatal(err)
	}
	_, got := readZip(t, out)
	text := string(got["Metadata/project_settings.config"])
	if !strings.Contains(text, "\r\n    \"wipe_tower_rotation_angle\": \"0\"\r\n}\r\n") {
		t.Errorf("the new key belongs at its sorted place (last here):\n%s", text)
	}
	if !strings.Contains(text, "\"#333333\"\r\n    ],") || strings.Contains(text, "#F4E076") {
		t.Errorf("%s", text)
	}
	// Untouched members stay byte identical.
	_, want := readZip(t, p.Path())
	if !bytes.Equal(got["3D/3dmodel.model"], want["3D/3dmodel.model"]) {
		t.Error("3dmodel.model rewritten needlessly")
	}
}

func TestPaintedMeshesAndUnknownMembersSurviveEdits(t *testing.T) {
	painted := strings.Replace(goldenObject1,
		`<triangle v1="0" v2="2" v3="1"/>`,
		`<triangle v1="0" v2="2" v3="1" paint_color="4" paint_supports="8" paint_seam="C" paint_fuzzy_skin="4"/>`, 1)
	painted = strings.Replace(painted, `<triangle v1="3" v2="4" v3="7"/>`, `<triangle v1="3" v2="4" v3="7" face_property="1"/>`, 1)
	entries := goldenEntries()
	for i := range entries {
		if entries[i].name == "3D/Objects/object_1.model" {
			entries[i].data = []byte(painted)
		}
	}
	entries = append(entries, entry{"Metadata/plate_2.png", pngBytes}, entry{"Metadata/vendor_extension.bin", []byte{0, 1, 2, 3, 255}})
	p, err := Open(writeZip(t, entries))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	p.Now = fixedNow
	o := p.Object(2)
	pt := o.Painted()
	if !pt.Supports || !pt.Seam || !pt.Color || !pt.FuzzySkin || !pt.FaceProperty || !o.Parts[0].Mesh.Painted.Any() {
		t.Errorf("painted flags %+v", pt)
	}
	if unknown := p.UnknownMembers(); len(unknown) != 2 {
		t.Errorf("unknown %v", unknown)
	}
	// Edit settings, transforms, plates: everything else is copied as it is.
	if err := p.SetObjectOverride(3, "wall_loops", "6"); err != nil {
		t.Fatal(err)
	}
	if err := p.SetTransform(3, 0, mesh.Translate(1, 1, 1)); err != nil {
		t.Fatal(err)
	}
	p.AddPlate("second")
	out := filepath.Join(t.TempDir(), "edited.3mf")
	if err := p.Save(out); err != nil {
		t.Fatal(err)
	}
	_, got := readZip(t, out)
	for _, e := range entries {
		switch e.name {
		case "3D/3dmodel.model", "3D/_rels/3dmodel.model.rels", "Metadata/model_settings.config", "Metadata/custom_gcode_per_layer.xml":
			continue // regenerated
		}
		if !bytes.Equal(got[e.name], e.data) {
			t.Errorf("member %s changed", e.name)
		}
	}
	if !bytes.Contains(got["3D/Objects/object_1.model"], []byte(`paint_color="4" paint_supports="8" paint_seam="C" paint_fuzzy_skin="4"`)) {
		t.Error("painted triangle data lost")
	}
	q := saveReopen(t, p)
	if !q.Object(3).Painted().Any() || len(q.Plates) != 2 || q.Object(3).Config.Value("wall_loops") != "6" {
		t.Errorf("after reopen: %+v", q.Object(3).Painted())
	}
}

func TestRemoveObjectPrunesFilesButKeepsSharedMeshes(t *testing.T) {
	p := openGolden(t)
	// Objects 2 and 3 share object_1.model: removing one keeps it.
	if err := p.RemoveObject(3); err != nil {
		t.Fatal(err)
	}
	q := saveReopen(t, p)
	names, _ := readZip(t, q.Path())
	joined := strings.Join(names, "|")
	if !strings.Contains(joined, "3D/Objects/object_1.model") {
		t.Errorf("the shared mesh file must stay: %v", names)
	}
	if len(q.Objects) != 1 || q.Objects[0].ID != 2 || len(q.Items) != 1 || len(q.Plates[0].Instances) != 1 || len(q.Assemble) != 1 || q.Assemble[0].ObjectID != 2 {
		t.Errorf("%d objects %d items %d instances %d assemble", len(q.Objects), len(q.Items), len(q.Plates[0].Instances), len(q.Assemble))
	}
	if len(q.Warnings) != 0 {
		t.Errorf("%v", q.Warnings)
	}
	// Removing the last user of the mesh removes the file and the stub.
	if err := q.RemoveObject(2); err != nil {
		t.Fatal(err)
	}
	r := saveReopen(t, q)
	names, _ = readZip(t, r.Path())
	for _, n := range names {
		if strings.HasPrefix(n, "3D/Objects/") {
			t.Errorf("object file %s should be gone", n)
		}
	}
	if len(r.Objects) != 0 || len(r.Items) != 0 || len(r.Plates) != 1 || len(r.Plates[0].Instances) != 0 {
		t.Errorf("%d %d", len(r.Objects), len(r.Items))
	}
	if err := r.RemoveObject(2); !errors.Is(err, ErrNotFound) {
		t.Errorf("%v", err)
	}
}

func TestAddAndRemoveParts(t *testing.T) {
	p := newProject(t)
	o, _ := p.AddObject(ObjectSpec{Name: "Body", Mesh: mesh.Box(20, 20, 20)})
	tr := mesh.Translate(0, 0, 5)
	mod, err := p.AddPart(o.ID, PartSpec{Name: "Dense zone", Mesh: mesh.Box(8, 8, 8), Transform: &tr, Config: KVs{{"sparse_infill_density", "40%"}, {"wall_loops", "4"}}})
	if err != nil {
		t.Fatal(err)
	}
	if mod.Subtype != SubtypeModifier || mod.Mesh.Type != "other" || mod.ID != 3 {
		t.Errorf("%+v", mod)
	}
	blocker, err := p.AddPart(o.ID, PartSpec{Subtype: SubtypeSupportBlocker, Name: "No support", Mesh: mesh.Cylinder(2, 6)})
	if err != nil {
		t.Fatal(err)
	}
	q := saveReopen(t, p)
	qo := q.Object(o.ID)
	if len(qo.Parts) != 3 || len(q.Warnings) != 0 {
		t.Fatalf("%d parts, warnings %v", len(qo.Parts), q.Warnings)
	}
	qm := qo.Part(mod.ID)
	if qm == nil || qm.Subtype != SubtypeModifier || qm.Name != "Dense zone" || qm.Config.Value("sparse_infill_density") != "40%" || qm.Matrix[11] != 5 || qm.ComponentTransform[11] != 5 {
		t.Fatalf("%+v", qm)
	}
	if qo.Part(blocker.ID).Subtype != SubtypeSupportBlocker || qo.Part(1).Subtype != SubtypeNormal {
		t.Errorf("subtypes")
	}
	// Modifier meshes read back, in their own file.
	if qm.Mesh.Path == qo.Parts[0].Mesh.Path {
		t.Errorf("the added part must have its own model file")
	}
	m, err := q.LoadMesh(qm)
	if err != nil || len(m.Triangles) != 12 {
		t.Errorf("%v", err)
	}
	// Part overrides.
	if err := q.SetPartOverride(o.ID, mod.ID, "sparse_infill_density", "60%"); err != nil {
		t.Fatal(err)
	}
	if ok, err := q.DeletePartOverride(o.ID, mod.ID, "wall_loops"); err != nil || !ok {
		t.Fatalf("%v %v", ok, err)
	}
	if ok, _ := q.DeletePartOverride(o.ID, mod.ID, "nope"); ok {
		t.Error("deleting a missing key reports false")
	}
	r := saveReopen(t, q)
	rm := r.Object(o.ID).Part(mod.ID)
	if rm.Config.Value("sparse_infill_density") != "60%" || len(rm.Config) != 1 {
		t.Errorf("%v", rm.Config)
	}
	// Remove the modifier again: its file goes.
	if err := r.RemovePart(o.ID, mod.ID); err != nil {
		t.Fatal(err)
	}
	s := saveReopen(t, r)
	if len(s.Object(o.ID).Parts) != 2 {
		t.Errorf("%d parts", len(s.Object(o.ID).Parts))
	}
	names, _ := readZip(t, s.Path())
	if c := strings.Count(strings.Join(names, "|"), "3D/Objects/"); c != 2 {
		t.Errorf("%d object files, want 2: %v", c, names)
	}
	if err := s.RemovePart(o.ID, 999); !errors.Is(err, ErrNotFound) {
		t.Errorf("%v", err)
	}
	only, _ := newProject(t).AddObject(ObjectSpec{Mesh: mesh.Box(1, 1, 1)})
	_ = only
	solo := newProject(t)
	so, _ := solo.AddObject(ObjectSpec{Mesh: mesh.Box(1, 1, 1)})
	if err := solo.RemovePart(so.ID, so.Parts[0].ID); !errors.Is(err, ErrInvalid) {
		t.Errorf("the last part cannot be removed: %v", err)
	}
	if _, err := s.AddPart(o.ID, PartSpec{Subtype: "weird", Mesh: mesh.Box(1, 1, 1)}); !errors.Is(err, ErrInvalid) {
		t.Errorf("%v", err)
	}
	if _, err := s.AddPart(12345, PartSpec{Mesh: mesh.Box(1, 1, 1)}); !errors.Is(err, ErrNotFound) {
		t.Errorf("%v", err)
	}
}

func TestOverridesAndValidation(t *testing.T) {
	p := newProject(t)
	o, _ := p.AddObject(ObjectSpec{Name: "X", Mesh: mesh.Box(5, 5, 5)})
	if err := p.SetObjectOverride(o.ID, "wall_loops", "3"); err != nil {
		t.Fatal(err)
	}
	if err := p.SetObjectOverride(o.ID, "extruder", "2"); err != nil {
		t.Fatal(err)
	}
	if o.Extruder() != 2 || o.Config[0].Key != "extruder" {
		t.Errorf("%v", o.Config)
	}
	// A value with awkward characters survives XML.
	weird := "line1\nline2 \"q\" <&> 'x'\ttab"
	if err := p.SetObjectOverride(o.ID, "custom_note", weird); err != nil {
		t.Fatal(err)
	}
	if err := p.RenameObject(o.ID, `A "quoted" <name> & more`); err != nil {
		t.Fatal(err)
	}
	q := saveReopen(t, p)
	qo := q.Object(o.ID)
	if qo.Config.Value("custom_note") != weird || qo.Name != `A "quoted" <name> & more` || qo.Parts[0].Name != "X" {
		t.Errorf("%q %q", qo.Config.Value("custom_note"), qo.Name)
	}
	if ok, err := q.DeleteObjectOverride(o.ID, "wall_loops"); !ok || err != nil {
		t.Errorf("%v %v", ok, err)
	}
	for _, key := range []string{"", "name", "module", "matrix", "a b", "x\"y", "a<b"} {
		if err := q.SetObjectOverride(o.ID, key, "1"); !errors.Is(err, ErrInvalid) {
			t.Errorf("key %q: %v", key, err)
		}
	}
	if err := q.SetObjectOverride(99, "a", "1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("%v", err)
	}
	if err := q.SetPartOverride(o.ID, 99, "a", "1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("%v", err)
	}
	if _, err := q.AddObject(ObjectSpec{Mesh: &mesh.Mesh{}}); !errors.Is(err, ErrInvalid) {
		t.Errorf("empty mesh: %v", err)
	}
	bad := mesh.Box(1, 1, 1)
	bad.Vertices[0][0] = float32(math.NaN())
	if _, err := q.AddObject(ObjectSpec{Mesh: bad}); !errors.Is(err, ErrInvalid) {
		t.Errorf("NaN: %v", err)
	}
	if _, err := q.AddObject(ObjectSpec{Mesh: mesh.Box(1, 1, 1), Plate: 7}); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown plate: %v", err)
	}
	if _, err := q.AddObject(ObjectSpec{Mesh: mesh.Box(1, 1, 1), Config: KVs{{"name", "x"}}}); !errors.Is(err, ErrInvalid) {
		t.Errorf("reserved key: %v", err)
	}
	if err := q.SetMetadata("bad key<", "x"); !errors.Is(err, ErrInvalid) {
		t.Errorf("%v", err)
	}
	if err := q.SetMetadata("Title", "A & B"); err != nil {
		t.Fatal(err)
	}
	r := saveReopen(t, q)
	if r.Metadata.Value("Title") != "A & B" {
		t.Errorf("%q", r.Metadata.Value("Title"))
	}
}

func TestPlates(t *testing.T) {
	p := newProject(t)
	a, _ := p.AddObject(ObjectSpec{Name: "A", Mesh: mesh.Box(5, 5, 5)})
	b, _ := p.AddObject(ObjectSpec{Name: "B", Mesh: mesh.Box(5, 5, 5)})
	p2 := p.AddPlate("Second")
	p3 := p.AddPlate("")
	if p2.Index != 2 || p3.Index != 3 {
		t.Fatalf("%d %d", p2.Index, p3.Index)
	}
	if err := p.MoveInstance(b.ID, 0, 2); err != nil {
		t.Fatal(err)
	}
	if err := p.MoveInstance(b.ID, 0, 2); err != nil {
		t.Errorf("moving to the same plate is a no-op: %v", err)
	}
	if p.PlateOf(b.ID, 0) != p2 || p.PlateOf(a.ID, 0) != p.Plates[0] {
		t.Error("PlateOf")
	}
	if err := p.LockPlate(2, true); err != nil {
		t.Fatal(err)
	}
	if err := p.RenamePlate(3, "Spare <1>"); err != nil {
		t.Fatal(err)
	}
	if err := p.SetPlateKey(2, "print_sequence", "by object"); err != nil {
		t.Fatal(err)
	}
	if err := p.SetPlateKey(2, "bed_type", "Textured PEI Plate"); err != nil {
		t.Fatal(err)
	}
	if err := p.SetPlateKey(2, "plater_id", "9"); !errors.Is(err, ErrInvalid) {
		t.Errorf("%v", err)
	}
	if err := p.SetCustomGCodes(2, ModeMultiExtruder, []GCodeItem{
		{TopZ: 4.2, Type: GCodeColorChange, Extruder: 2, Color: "#FF0000"},
		{TopZ: 0.6, Type: GCodePausePrint, Extruder: 1},
		{TopZ: 8, Type: GCodeCustom, Extruder: 1, Extra: "M117 halfway"},
		{TopZ: 9, Type: GCodeToolChange, Extruder: 2},
	}); err != nil {
		t.Fatal(err)
	}
	q := saveReopen(t, p)
	if len(q.Plates) != 3 {
		t.Fatalf("%d plates", len(q.Plates))
	}
	pl := q.Plate(2)
	if pl.Name != "Second" || !pl.Locked || len(pl.Instances) != 1 || pl.Instances[0].ObjectID != b.ID || pl.Config.Value("print_sequence") != "by object" {
		t.Errorf("%+v", pl)
	}
	// bed_type is written before print_sequence, the app's order.
	if pl.Config[0].Key != "bed_type" || pl.Config[1].Key != "print_sequence" {
		t.Errorf("%v", pl.Config)
	}
	if q.Plate(3).Name != "Spare <1>" || len(q.Plate(3).Instances) != 0 {
		t.Errorf("%+v", q.Plate(3))
	}
	g := q.CustomGCodes(2)
	if g.Mode != ModeMultiExtruder || len(g.Items) != 4 || g.Items[0].TopZ != 0.6 || g.Items[0].GCode != "M601" || g.Items[1].Color != "#FF0000" || g.Items[2].GCode != "M117 halfway" || g.Items[3].GCode != "tool_change" {
		t.Errorf("%+v", g)
	}
	if q.CustomGCodes(1).Mode != ModeSingleExtruder || q.CustomGCodes(3).Mode != ModeSingleExtruder {
		t.Errorf("default modes: %v %v", q.CustomGCodes(1).Mode, q.CustomGCodes(3).Mode)
	}

	// Removing a plate renumbers the later ones and their G-code state.
	if err := q.RemovePlate(2); !errors.Is(err, ErrInvalid) {
		t.Errorf("a plate with objects cannot be removed: %v", err)
	}
	if err := q.MoveInstance(b.ID, 0, 1); err != nil {
		t.Fatal(err)
	}
	if err := q.SetCustomGCodes(3, ModeMultiExtruder, []GCodeItem{{TopZ: 2, Type: GCodeColorChange, Extruder: 2, Color: "#00FF00"}}); err != nil {
		t.Fatal(err)
	}
	if err := q.RemovePlate(2); err != nil {
		t.Fatal(err)
	}
	r := saveReopen(t, q)
	if len(r.Plates) != 2 || r.Plates[1].Index != 2 || r.Plates[1].Name != "Spare <1>" {
		t.Errorf("%+v", r.Plates)
	}
	if got := r.CustomGCodes(2); len(got.Items) != 1 || got.Items[0].Color != "#00FF00" {
		t.Errorf("G-code state must follow its plate: %+v", got)
	}
	if err := r.RemovePlate(9); !errors.Is(err, ErrNotFound) {
		t.Errorf("%v", err)
	}
	solo := newProject(t)
	if err := solo.RemovePlate(1); !errors.Is(err, ErrInvalid) {
		t.Errorf("the last plate stays: %v", err)
	}
	if ok, err := r.DeletePlateKey(1, "nothing"); ok || err != nil {
		t.Errorf("%v %v", ok, err)
	}
	if err := r.SetCustomGCodes(1, "bogus", nil); !errors.Is(err, ErrInvalid) {
		t.Errorf("%v", err)
	}
	if err := r.SetCustomGCodes(1, ModeSingleExtruder, []GCodeItem{{TopZ: -1, Type: 0}}); !errors.Is(err, ErrInvalid) {
		t.Errorf("%v", err)
	}
	if err := r.SetCustomGCodes(1, ModeSingleExtruder, []GCodeItem{{TopZ: 1, Type: 9}}); !errors.Is(err, ErrInvalid) {
		t.Errorf("%v", err)
	}
	if err := r.SetCustomGCodes(7, ModeSingleExtruder, nil); !errors.Is(err, ErrNotFound) {
		t.Errorf("%v", err)
	}
}

func TestLayerRanges(t *testing.T) {
	p := newProject(t)
	a, _ := p.AddObject(ObjectSpec{Name: "A", Mesh: mesh.Box(5, 5, 20)})
	b, _ := p.AddObject(ObjectSpec{Name: "B", Mesh: mesh.Box(5, 5, 20)})
	if err := p.SetLayerRanges(b.ID, []LayerRange{
		{MinZ: 5, MaxZ: 10, Options: KVs{{"layer_height", "0.1"}, {"sparse_infill_density", "50%"}}},
		{MinZ: 0, MaxZ: 2.5, Options: KVs{{"layer_height", "0.3"}}},
	}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "r.3mf")
	if err := p.Save(path); err != nil {
		t.Fatal(err)
	}
	_, got := readZip(t, path)
	// Object B is the second object: the file names it by position, 2.
	want := "<?xml version=\"1.0\" encoding=\"utf-8\"?>\n<objects>\n <object id=\"2\">\n  <range min_z=\"0\" max_z=\"2.5\">\n   <option opt_key=\"layer_height\">0.3</option>\n  </range>\n" +
		"  <range min_z=\"5\" max_z=\"10\">\n   <option opt_key=\"layer_height\">0.1</option>\n   <option opt_key=\"sparse_infill_density\">50%</option>\n  </range>\n </object>\n</objects>"
	if string(got["Metadata/layer_config_ranges.xml"]) != want {
		t.Errorf("layer_config_ranges.xml:\n%s\nwant\n%s", got["Metadata/layer_config_ranges.xml"], want)
	}
	q := saveReopen(t, p)
	if len(q.Object(a.ID).LayerRanges) != 0 || len(q.Object(b.ID).LayerRanges) != 2 {
		t.Fatalf("%+v", q.Object(b.ID).LayerRanges)
	}
	r := q.Object(b.ID).LayerRanges[1]
	if r.MinZ != 5 || r.MaxZ != 10 || r.Options.Value("sparse_infill_density") != "50%" {
		t.Errorf("%+v", r)
	}
	// Removing the first object shifts positions: B becomes object 1.
	if err := q.RemoveObject(a.ID); err != nil {
		t.Fatal(err)
	}
	s := saveReopen(t, q)
	_, got = readZip(t, s.Path())
	if !strings.Contains(string(got["Metadata/layer_config_ranges.xml"]), `<object id="1">`) || len(s.Objects[0].LayerRanges) != 2 {
		t.Errorf("%s", got["Metadata/layer_config_ranges.xml"])
	}
	// Clearing all ranges removes the member.
	if err := s.SetLayerRanges(s.Objects[0].ID, nil); err != nil {
		t.Fatal(err)
	}
	u := saveReopen(t, s)
	names, _ := readZip(t, u.Path())
	if strings.Contains(strings.Join(names, "|"), "layer_config_ranges.xml") {
		t.Errorf("empty ranges must remove the member: %v", names)
	}
	for name, ranges := range map[string][]LayerRange{
		"reversed": {{MinZ: 5, MaxZ: 2, Options: KVs{{"a", "1"}}}},
		"negative": {{MinZ: -1, MaxZ: 2, Options: KVs{{"a", "1"}}}},
		"overlap":  {{MinZ: 0, MaxZ: 5, Options: KVs{{"a", "1"}}}, {MinZ: 4, MaxZ: 8, Options: KVs{{"a", "1"}}}},
		"empty":    {{MinZ: 0, MaxZ: 5}},
		"bad key":  {{MinZ: 0, MaxZ: 5, Options: KVs{{"", "1"}}}},
	} {
		if err := u.SetLayerRanges(u.Objects[0].ID, ranges); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := u.SetLayerRanges(99, nil); !errors.Is(err, ErrNotFound) {
		t.Errorf("%v", err)
	}
}

func TestPlainThreeMFIsNotASlicerProject(t *testing.T) {
	inline := `<?xml version="1.0"?><model unit="millimeter" xmlns="http://schemas.microsoft.com/3dmanufacturing/core/2015/02"><metadata name="Application">FreeCAD</metadata>
<resources><object id="1" type="model"><mesh><vertices><vertex x="0" y="0" z="0" /><vertex x="1" y="0" z="0" /><vertex x="0" y="1" z="0" /><vertex x="0" y="0" z="1" /></vertices>
<triangles><triangle v1="0" v2="2" v3="1" paint_color="4"/><triangle v1="0" v2="1" v3="3"/><triangle v1="1" v2="2" v3="3"/><triangle v1="0" v2="3" v3="2"/></triangles></mesh></object></resources>
<build><item objectid="1"/></build></model>`
	p, err := Open(writeZip(t, []entry{{"3D/3dmodel.model", []byte(inline)}, {"Metadata/thumbnail.png", pngBytes}}))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if p.IsSlicerProject {
		t.Error("a package without model_settings.config is not a slicer project")
	}
	if len(p.Objects) != 1 || p.Objects[0].Parts[0].Mesh.Triangles != 4 || !p.Objects[0].Painted().Color || len(p.Plates) != 1 || len(p.Plates[0].Instances) != 1 {
		t.Errorf("%+v", p.Objects)
	}
	m, err := p.LoadMesh(p.Objects[0].Parts[0])
	if err != nil || len(m.Triangles) != 4 {
		t.Errorf("%v", err)
	}
	if _, err := p.AddObject(ObjectSpec{Mesh: mesh.Box(1, 1, 1)}); !errors.Is(err, ErrNotSlicerProject) {
		t.Errorf("%v", err)
	}
	if err := p.SetObjectOverride(1, "a", "1"); !errors.Is(err, ErrNotSlicerProject) {
		t.Errorf("%v", err)
	}
	if err := p.RemoveObject(1); !errors.Is(err, ErrNotSlicerProject) {
		t.Errorf("%v", err)
	}
	// Saving it unchanged still works and is byte identical.
	out := filepath.Join(t.TempDir(), "o.3mf")
	if err := p.Save(out); err != nil {
		t.Fatal(err)
	}
	_, got := readZip(t, out)
	if string(got["3D/3dmodel.model"]) != inline || !bytes.Equal(got["Metadata/thumbnail.png"], pngBytes) {
		t.Error("an unchanged inline project must be copied as it is")
	}
	p.MarkModified()
	if err := p.Save(out); !errors.Is(err, ErrNotSlicerProject) {
		t.Errorf("regenerating a plain 3MF must be refused: %v", err)
	}
}

func TestOpenErrorsAndOddPackages(t *testing.T) {
	if _, err := Open(filepath.Join(t.TempDir(), "missing.3mf")); err == nil {
		t.Error("missing file")
	}
	junk := filepath.Join(t.TempDir(), "junk.3mf")
	os.WriteFile(junk, []byte("not a zip"), 0o644)
	if _, err := Open(junk); err == nil {
		t.Error("not a zip")
	}
	if _, err := Open(writeZip(t, []entry{{"readme.txt", []byte("x")}})); err == nil {
		t.Error("no model")
	}
	if _, err := Open(writeZip(t, []entry{{"3D/3dmodel.model", []byte("<model><resources><object type=\"model\"/></resources></model>")}})); err == nil {
		t.Error("object without id")
	}
	// Broken optional members are kept as they are, with a warning.
	entries := goldenEntries()
	for i := range entries {
		switch entries[i].name {
		case "Metadata/project_settings.config":
			entries[i].data = []byte("{ nope")
		case "Metadata/model_settings.config":
			entries[i].data = []byte("<config><object")
		case "Metadata/custom_gcode_per_layer.xml":
			entries[i].data = []byte("<a><plate><layer top_z=\"x\"/></plate></a>")
		}
	}
	p, err := Open(writeZip(t, entries))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if len(p.Warnings) != 3 || p.Settings != nil {
		t.Errorf("warnings %v", p.Warnings)
	}
	out := filepath.Join(t.TempDir(), "w.3mf")
	if err := p.Save(out); err != nil {
		t.Fatal(err)
	}
	_, got := readZip(t, out)
	if string(got["Metadata/project_settings.config"]) != "{ nope" {
		t.Error("an unreadable member must be preserved")
	}
}

func TestMembersReport(t *testing.T) {
	p := openGolden(t)
	var total int
	for _, m := range p.Members() {
		total++
		if m.Size == 0 && m.Name != "Metadata/plate_1.png" && !strings.HasSuffix(m.Name, "/") {
			t.Errorf("%s has no size", m.Name)
		}
	}
	if total != len(goldenEntries()) {
		t.Errorf("%d members", total)
	}
	data, err := p.Read("Auxiliaries/readme.txt")
	if err != nil || string(data) != "an attachment the MCP does not understand" {
		t.Errorf("%v", err)
	}
	if _, err := p.Read("nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("%v", err)
	}
	if !isKnownMember("Metadata/plate_no_light_3.png") || !isKnownMember("Metadata/filament_settings_2.config") || isKnownMember("Metadata/other.bin") || isKnownMember("Auxiliaries/x") {
		t.Error("known members")
	}
}

func TestNextIdsAndIdentifyIDs(t *testing.T) {
	p := openGolden(t)
	if p.NextIdentifyID() != 126 {
		t.Errorf("%d", p.NextIdentifyID())
	}
	o, err := p.AddObject(ObjectSpec{Name: "New", Mesh: mesh.Box(3, 3, 3)})
	if err != nil {
		t.Fatal(err)
	}
	// Ids continue after the largest in the package (object 3, mesh 1).
	if o.Parts[0].ID != 4 || o.ID != 5 {
		t.Errorf("part %d object %d", o.Parts[0].ID, o.ID)
	}
	if !strings.HasSuffix(o.Parts[0].Mesh.Path, "object_3.model") {
		t.Errorf("new files continue the numbering after the stub: %s", o.Parts[0].Mesh.Path)
	}
	q := saveReopen(t, p)
	if len(q.Objects) != 3 || q.Plates[0].Instances[2].IdentifyID != 126 {
		t.Errorf("%+v", q.Plates[0].Instances)
	}
	// Existing objects keep their UUIDs, files and formatting.
	names, got := readZip(t, q.Path())
	if !strings.Contains(strings.Join(names, "|"), "3D/Objects/object_3.model") || !bytes.Equal(got["3D/Objects/object_1.model"], []byte(goldenObject1)) {
		t.Errorf("%v", names)
	}
	if !strings.Contains(string(got["3D/_rels/3dmodel.model.rels"]), "object_3.model") {
		t.Errorf("rels must list the new file:\n%s", got["3D/_rels/3dmodel.model.rels"])
	}
	if q.Object(2).UUID != "00000001-61cb-4c03-9d28-80fed5dfa1dc" || q.Object(3).UUID != "00000002-71cb-4c03-9d28-80fed5dfa1dc" {
		t.Error("existing uuids changed")
	}
	if strconv.Itoa(q.Object(5).backupID) != "3" {
		t.Errorf("backup id %d", q.Object(5).backupID)
	}
}

func TestSaveFailsCleanly(t *testing.T) {
	p := openGolden(t)
	dir := filepath.Join(t.TempDir(), "no", "such", "dir")
	if err := p.Save(filepath.Join(dir, "x.3mf")); err == nil {
		t.Error("saving into a missing folder must fail")
	}
	// The project is still usable afterwards.
	out := filepath.Join(t.TempDir(), "ok.3mf")
	if err := p.Save(out); err != nil {
		t.Fatalf("%v", err)
	}
	entries, _ := os.ReadDir(filepath.Dir(out))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("temporary file left behind: %s", e.Name())
		}
	}
	r, err := zip.OpenReader(out)
	if err != nil {
		t.Fatal(err)
	}
	r.Close()
}

func TestCustomGCodeTextComesFromThePrinterSettings(t *testing.T) {
	p := newProject(t)
	cfg := NewConfig()
	cfg.SetString("machine_pause_gcode", "M0 ; pause here")
	cfg.SetString("template_custom_gcode", "M117 template")
	p.SetSettings(cfg)
	if err := p.SetCustomGCodes(1, ModeMultiExtruder, []GCodeItem{
		{TopZ: 1, Type: GCodePausePrint, Extruder: 1},
		{TopZ: 2, Type: GCodeTemplate, Extruder: 1},
		{TopZ: 3, Type: GCodeColorChange, Extruder: 2, Color: "#00FF00"},
	}); err != nil {
		t.Fatal(err)
	}
	q := saveReopen(t, p)
	g := q.CustomGCodes(1)
	if g.Items[0].GCode != "M0 ; pause here" || g.Items[1].GCode != "M117 template" || g.Items[2].GCode != "" {
		t.Errorf("%+v", g.Items)
	}
}

func TestUnsavedPartsAreReadable(t *testing.T) {
	p := newProject(t)
	o, _ := p.AddObject(ObjectSpec{Name: "A", Mesh: mesh.Box(2, 3, 4)})
	m, err := p.LoadMesh(o.Parts[0])
	if err != nil || len(m.Triangles) != 12 || m.Volume() != 24 {
		t.Errorf("%v", err)
	}
	m.Vertices[0][0] = 99 // a copy: the stored mesh is not affected
	m2, _ := p.LoadMesh(o.Parts[0])
	if m2.Vertices[0][0] == 99 {
		t.Error("LoadMesh must hand out a copy")
	}
	if err := p.SetTransform(o.ID, 5, mesh.Identity()); !errors.Is(err, ErrNotFound) {
		t.Errorf("%v", err)
	}
	if err := p.RenameObject(77, "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("%v", err)
	}
	if ok, err := p.DeleteObjectOverride(77, "x"); ok || !errors.Is(err, ErrNotFound) {
		t.Errorf("%v", err)
	}
	if _, err := p.DeletePlateKey(9, "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("%v", err)
	}
	if err := p.RenamePlate(9, "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("%v", err)
	}
	if err := p.LockPlate(9, true); !errors.Is(err, ErrNotFound) {
		t.Errorf("%v", err)
	}
	if err := p.MoveInstance(o.ID, 3, 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("%v", err)
	}
	if err := p.MoveInstance(o.ID, 0, 9); !errors.Is(err, ErrNotFound) {
		t.Errorf("%v", err)
	}
	part := &Part{Mesh: MeshRef{Path: "/3D/Objects/none.model", ObjectID: 1}}
	if _, err := p.LoadMesh(part); !errors.Is(err, ErrNotFound) {
		t.Errorf("%v", err)
	}
	if !p.Plates[0].Config.Delete("x") == false {
		t.Error("KVs delete")
	}
}
