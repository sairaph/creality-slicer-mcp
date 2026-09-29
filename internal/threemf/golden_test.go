package threemf

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sairaph/creality-slicer-mcp/internal/mesh"
)

// This file holds a hand made equivalent of the golden project of
// dev_docs/10-cli.md 20.1 (saved by Creality Print 7.2.2.5483): a cube instead
// of the owner's mesh, two objects sharing it (production extension, empty
// stub file, 61cb and 71cb object UUIDs), the same member formatting. The
// tests prove the reader understands it and that regenerating every member
// reproduces the text byte for byte.

const goldenDate = "2026-09-29"

const goldenRoot = `<model unit="millimeter" xml:lang="en-US" xmlns="http://schemas.microsoft.com/3dmanufacturing/core/2015/02" xmlns:BambuStudio="http://schemas.bambulab.com/package/2021" xmlns:p="http://schemas.microsoft.com/3dmanufacturing/production/2015/06" requiredextensions="p">`

const goldenModel = `<?xml version="1.0" encoding="UTF-8"?>
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
 <metadata name="Thumbnail_Middle">/Metadata/plate_1.png</metadata>
 <metadata name="Thumbnail_Small">/Metadata/plate_1_small.png</metadata>
 <metadata name="Title"></metadata>
 <resources>
  <object id="2" p:UUID="00000001-61cb-4c03-9d28-80fed5dfa1dc" type="model">
   <components>
    <component p:path="/3D/Objects/object_1.model" objectid="1" p:UUID="00010000-b206-40ff-9872-83e8017abed1" transform="1 0 0 0 1 0 0 0 1 0 -2.03125012e-16 0"/>
   </components>
  </object>
  <object id="3" p:UUID="00000002-71cb-4c03-9d28-80fed5dfa1dc" type="model">
   <components>
    <component p:path="/3D/Objects/object_1.model" objectid="1" p:UUID="00020000-b206-40ff-9872-83e8017abed1" transform="1 0 0 0 1 0 0 0 1 0 -2.03125012e-16 0"/>
   </components>
  </object>
 </resources>
 <build p:UUID="2c7c17d8-22b5-4d84-8835-1976022ea369">
  <item objectid="2" p:UUID="00000002-b1ec-4553-aec9-835e5b724bb4" transform="1 0 0 0 2.22044605e-16 1 0 -1 2.22044605e-16 44.7294838 43.8494246 10.2412338" printable="1"/>
  <item objectid="3" p:UUID="00000003-b1ec-4553-aec9-835e5b724bb4" transform="1 0 0 0 1 0 0 0 1 100 50 5" printable="1"/>
 </build>
</model>
`

const goldenObject1 = `<?xml version="1.0" encoding="UTF-8"?>
` + goldenRoot + `
 <metadata name="BambuStudio:3mfVersion">1</metadata>
 <resources>
  <object id="1" p:UUID="00010000-81cb-4c03-9d28-80fed5dfa1dc" type="model">
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
     <triangle v1="0" v2="2" v3="1"/>
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
 </resources>
 <build/>
</model>
`

const goldenStub = `<?xml version="1.0" encoding="UTF-8"?>
` + goldenRoot + `
 <metadata name="BambuStudio:3mfVersion">1</metadata>
 <resources>
 </resources>
 <build/>
</model>
`

const goldenRels = `<?xml version="1.0" encoding="UTF-8"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
 <Relationship Target="/3D/Objects/object_1.model" Id="rel-1" Type="http://schemas.microsoft.com/3dmanufacturing/2013/01/3dmodel"/>
 <Relationship Target="/3D/Objects/object_2.model" Id="rel-2" Type="http://schemas.microsoft.com/3dmanufacturing/2013/01/3dmodel"/>
</Relationships>`

// Objects are listed as 3 then 2: the app writes them in memory order.
const goldenSettings = `<?xml version="1.0" encoding="UTF-8"?>
<config>
  <object id="3">
    <metadata key="name" value="Cube"/>
    <metadata key="enable_support" value="0"/>
    <metadata key="extruder" value="2"/>
    <metadata key="flush_into_infill" value="1"/>
    <part id="1" subtype="normal_part">
      <metadata key="name" value="Cube"/>
      <metadata key="matrix" value="1 0 0 0 0 1 0 4.7683715779687498e-07 0 0 1 0 0 0 0 1"/>
      <metadata key="source_file" value="cube.stl"/>
      <metadata key="source_object_id" value="0"/>
      <metadata key="source_volume_id" value="0"/>
      <metadata key="source_offset_x" value="0"/>
      <metadata key="source_offset_y" value="7.7411683797836304"/>
      <metadata key="source_offset_z" value="0"/>
      <mesh_stat edges_fixed="0" degenerate_facets="0" facets_removed="0" facets_reversed="0" backwards_edges="0"/>
    </part>
  </object>
  <object id="2">
    <metadata key="name" value="Cube"/>
    <metadata key="extruder" value="1"/>
    <part id="1" subtype="normal_part">
      <metadata key="name" value="Cube"/>
      <metadata key="matrix" value="1 0 0 0 0 1 0 4.7683715779687498e-07 0 0 1 0 0 0 0 1"/>
      <metadata key="source_file" value="cube.stl"/>
      <metadata key="source_object_id" value="0"/>
      <metadata key="source_volume_id" value="0"/>
      <metadata key="source_offset_x" value="0"/>
      <metadata key="source_offset_y" value="7.7411683797836304"/>
      <metadata key="source_offset_z" value="0"/>
      <mesh_stat edges_fixed="0" degenerate_facets="0" facets_removed="0" facets_reversed="0" backwards_edges="0"/>
    </part>
  </object>
  <plate>
    <metadata key="plater_id" value="1"/>
    <metadata key="plater_name" value=""/>
    <metadata key="locked" value="false"/>
    <metadata key="thumbnail_file" value="Metadata/plate_1.png"/>
    <metadata key="thumbnail_no_light_file" value="Metadata/plate_no_light_1.png"/>
    <metadata key="top_file" value="Metadata/top_1.png"/>
    <metadata key="pick_file" value="Metadata/pick_1.png"/>
    <model_instance>
      <metadata key="object_id" value="2"/>
      <metadata key="instance_id" value="0"/>
      <metadata key="identify_id" value="84"/>
    </model_instance>
    <model_instance>
      <metadata key="object_id" value="3"/>
      <metadata key="instance_id" value="0"/>
      <metadata key="identify_id" value="125"/>
    </model_instance>
  </plate>
  <assemble>
   <assemble_item object_id="3" instance_id="0" transform="1 0 0 0 2.2204460500000001e-16 1 0 -1 2.2204460500000001e-16 29.900367736816406 28.898197174072266 10.241233825683594" offset="0 0 0" />
   <assemble_item object_id="2" instance_id="0" transform="1 0 0 0 1 0 0 0 1 0 0 4.5" offset="0 0 0" />
  </assemble>
</config>
`

// The project settings are CRLF, four space indent, vectors one element per line.
var goldenProjectSettings = strings.ReplaceAll(`{
    "accel_to_decel_factor": "100%",
    "change_filament_gcode": "{if a == \"b\"}\nG1 X0 Y140 F30000\n{endif}",
    "compatible_printers": [],
    "filament_colour": [
        "#000000",
        "#F4E076"
    ],
    "flush_volumes_matrix": [
        "0",
        "677",
        "670",
        "0"
    ],
    "from": "project",
    "name": "project_settings",
    "thumbnails": "96x96/PNG, 300x300/PNG",
    "version": "7.2.2.5483"
}
`, "\n", "\r\n")

const goldenGCodes = `<?xml version="1.0" encoding="utf-8"?>
<custom_gcodes_per_layer>
<plate>
<plate_info id="1"/>
<layer top_z="0.45000000000000001" type="1" extruder="1" color="" extra="" gcode="M601"/>
<layer top_z="4.2000000000000002" type="0" extruder="2" color="#FF0000" extra="" gcode=""/>
<mode value="MultiExtruder"/>
</plate>
</custom_gcodes_per_layer>`

const goldenSliceInfo = `<?xml version="1.0" encoding="UTF-8"?>
<config>
  <header>
    <header_item key="X-CX-Client-Type" value="creality_print"/>
    <header_item key="X-CX-Client-Version" value="07.02.02.5483"/>
  </header>
</config>
`

const goldenCreality = `<?xml version="1.0" encoding="UTF-8"?>
<config>
    <metadata key="Company" value="Creality"/>
    <metadata key="Application" value="Creality_Print"/>
    <metadata key="AppVersion" value="7.2.2.5483"/>
    <metadata key="AppStage" value="Release"/>
    <metadata key="FileVersion" value="1.0"/>
    <metadata key="FileType" value="Undefined"/>
    <metadata key="CreationDate" value="2026-09-29"/>
</config>
`

var pngBytes = []byte("\x89PNG\r\n\x1a\nfake plate thumbnail \x00\x01\x02")

type entry struct {
	name string
	data []byte
}

// goldenEntries lists the members in the order the app writes them.
func goldenEntries() []entry {
	return []entry{
		{"[Content_Types].xml", []byte(contentTypes)},
		{"Metadata/plate_1.png", pngBytes},
		{"3D/3dmodel.model", []byte(goldenModel)},
		{"3D/_rels/3dmodel.model.rels", []byte(goldenRels)},
		{"3D/Objects/object_1.model", []byte(goldenObject1)},
		{"3D/Objects/object_2.model", []byte(goldenStub)},
		{"Metadata/custom_gcode_per_layer.xml", []byte(goldenGCodes)},
		{"Metadata/project_settings.config", []byte(goldenProjectSettings)},
		{"Metadata/model_settings.config", []byte(goldenSettings)},
		{"Metadata/slice_info.config", []byte(goldenSliceInfo)},
		{"Metadata/creality.config", []byte(goldenCreality)},
		{"Auxiliaries/readme.txt", []byte("an attachment the MCP does not understand")},
		{"_rels/.rels", []byte(rootRels)},
	}
}

func writeZip(t *testing.T, entries []entry) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "project.3mf")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for _, e := range entries {
		method := zip.Deflate
		if strings.HasSuffix(e.name, ".png") {
			method = zip.Store
		}
		w, err := zw.CreateHeader(&zip.FileHeader{Name: e.name, Method: method})
		if err != nil {
			t.Fatal(err)
		}
		w.Write(e.data)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()
	return path
}

func readZip(t *testing.T, path string) (names []string, data map[string][]byte) {
	t.Helper()
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	data = map[string][]byte{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(rc)
		rc.Close()
		names = append(names, f.Name)
		data[f.Name] = b
	}
	return names, data
}

func fixedNow() time.Time {
	d, _ := time.Parse("2006-01-02", goldenDate)
	return d
}

func openGolden(t *testing.T) *Project {
	t.Helper()
	p, err := Open(writeZip(t, goldenEntries()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	p.Now = fixedNow
	return p
}

func TestGoldenIsUnderstood(t *testing.T) {
	p := openGolden(t)
	if len(p.Warnings) != 0 {
		t.Fatalf("warnings: %v", p.Warnings)
	}
	if p.Metadata.Value("Application") != "Creality_Print V7.2.2.5483 Release" || p.Metadata.Value("BambuStudio:3mfVersion") != "1" {
		t.Errorf("%v", p.Metadata)
	}
	if len(p.Objects) != 2 {
		t.Fatalf("%d objects", len(p.Objects))
	}
	o2, o3 := p.Object(2), p.Object(3)
	if o2 == nil || o3 == nil || o2.Name != "Cube" || o2.Extruder() != 1 || o3.Extruder() != 2 {
		t.Fatalf("%+v %+v", o2, o3)
	}
	if o3.Config.Value("enable_support") != "0" || o3.Config.Value("flush_into_infill") != "1" || len(o2.Config) != 1 {
		t.Errorf("%v %v", o3.Config, o2.Config)
	}
	if o2.UUID != "00000001-61cb-4c03-9d28-80fed5dfa1dc" || o3.UUID != "00000002-71cb-4c03-9d28-80fed5dfa1dc" {
		t.Errorf("%q %q", o2.UUID, o3.UUID)
	}
	part := o3.Parts[0]
	if len(o3.Parts) != 1 || part.ID != 1 || part.Subtype != SubtypeNormal || part.Name != "Cube" || !part.HasMatrix {
		t.Fatalf("%+v", part)
	}
	if part.Matrix[7] != 4.7683715779687498e-07 || part.Source == nil || part.Source.File != "cube.stl" || part.Source.OffsetY != "7.7411683797836304" {
		t.Errorf("%+v %+v", part.Matrix, part.Source)
	}
	if part.Mesh.Path != "/3D/Objects/object_1.model" || part.Mesh.Vertices != 8 || part.Mesh.Triangles != 12 || part.Mesh.Painted.Any() || !part.Mesh.Shared {
		t.Errorf("%+v", part.Mesh)
	}
	if part.Mesh.Min != [3]float32{-5, -5, -5} || part.Mesh.Max != [3]float32{5, 5, 5} {
		t.Errorf("bounds %v %v", part.Mesh.Min, part.Mesh.Max)
	}
	if part.ComponentTransform[10] != -2.03125012e-16 {
		t.Errorf("%v", part.ComponentTransform)
	}
	if len(p.Items) != 2 || p.Items[0].ObjectID != 2 || !p.Items[0].Printable || p.Items[1].Transform.Translation() != [3]float64{100, 50, 5} {
		t.Errorf("%+v", p.Items)
	}
	if len(p.Plates) != 1 {
		t.Fatalf("plates %d", len(p.Plates))
	}
	pl := p.Plates[0]
	if pl.Index != 1 || pl.Name != "" || pl.Locked || len(pl.Instances) != 2 || pl.Instances[0] != (Instance{2, 0, 84}) || pl.Instances[1].IdentifyID != 125 {
		t.Errorf("%+v", pl)
	}
	if pl.Config.Value("pick_file") != "Metadata/pick_1.png" || len(pl.Config) != 4 {
		t.Errorf("%v", pl.Config)
	}
	if len(p.Assemble) != 2 || p.Assemble[0].ObjectID != 3 || p.Assemble[1].Attrs.Value("offset") != "0 0 0" {
		t.Errorf("%+v", p.Assemble)
	}
	if p.Settings == nil || p.Settings.String("from") != "project" || len(p.Settings.List("filament_colour")) != 2 || len(p.Settings.List("compatible_printers")) != 0 {
		t.Errorf("settings")
	}
	if v, ok := p.Settings.Get("compatible_printers"); !ok || !v.IsList {
		t.Errorf("an empty vector stays a vector: %+v", v)
	}
	g := p.CustomGCodes(1)
	if g.Mode != ModeMultiExtruder || len(g.Items) != 2 || g.Items[0].TopZ != 0.45 || g.Items[0].Type != GCodePausePrint || g.Items[1].Color != "#FF0000" || g.Items[1].Extruder != 2 {
		t.Errorf("%+v", g)
	}
	if p.Creality.Value("AppVersion") != "7.2.2.5483" {
		t.Errorf("%v", p.Creality)
	}
	if unknown := p.UnknownMembers(); len(unknown) != 1 || unknown[0] != "Auxiliaries/readme.txt" {
		t.Errorf("unknown members %v", unknown)
	}
	if got := p.ItemsOf(2); len(got) != 1 || p.PlateOf(2, 0) != pl || p.PlateOf(9, 0) != nil {
		t.Error("ItemsOf/PlateOf")
	}
	m, err := p.LoadMesh(part)
	if err != nil || len(m.Triangles) != 12 || !m.Stats().Manifold || m.Volume() != 1000 {
		t.Errorf("mesh: %v %v", err, m)
	}
	// The mesh of the shared file also opens for the other object.
	if m2, err := p.LoadMesh(o2.Parts[0]); err != nil || len(m2.Triangles) != 12 {
		t.Errorf("%v", err)
	}
}

func TestSaveUnchangedIsByteIdentical(t *testing.T) {
	p := openGolden(t)
	out := filepath.Join(t.TempDir(), "out.3mf")
	if err := p.Save(out); err != nil {
		t.Fatal(err)
	}
	wantNames, want := readZip(t, p.Path())
	gotNames, got := readZip(t, out)
	if strings.Join(gotNames, "|") != strings.Join(wantNames, "|") {
		t.Errorf("member order changed:\n%v\n%v", gotNames, wantNames)
	}
	for name, b := range want {
		if !bytes.Equal(got[name], b) {
			t.Errorf("member %s differs", name)
		}
	}
	// The project stays usable and saves again to the same file.
	if err := p.Save(p.Path()); err != nil {
		t.Fatal(err)
	}
}

func TestRegeneratedMembersMatchTheGoldenFormatting(t *testing.T) {
	p := openGolden(t)
	p.MarkModified()
	out := filepath.Join(t.TempDir(), "regen.3mf")
	if err := p.Save(out); err != nil {
		t.Fatal(err)
	}
	_, want := readZip(t, p.Path())
	_, got := readZip(t, out)
	for _, name := range []string{
		"3D/3dmodel.model", "3D/_rels/3dmodel.model.rels", "Metadata/model_settings.config",
		"Metadata/custom_gcode_per_layer.xml",
	} {
		if !bytes.Equal(got[name], want[name]) {
			t.Errorf("%s differs:\n--- want\n%s\n--- got\n%s", name, want[name], got[name])
		}
	}
	// project_settings.config is only rewritten when a key changed; the
	// serialisation itself must equal the app's text.
	if !bytes.Equal(p.Settings.Marshal(), want["Metadata/project_settings.config"]) {
		t.Errorf("project settings serialisation differs:\n%s", p.Settings.Marshal())
	}
	// Everything not regenerated is untouched.
	for _, name := range []string{"3D/Objects/object_1.model", "3D/Objects/object_2.model", "Metadata/plate_1.png", "Auxiliaries/readme.txt", "Metadata/creality.config", "Metadata/slice_info.config"} {
		if !bytes.Equal(got[name], want[name]) {
			t.Errorf("%s must be untouched", name)
		}
	}
}

func TestModificationDateUpdatesOnlyWhenTheModelChanges(t *testing.T) {
	p := openGolden(t)
	later := func() time.Time { d, _ := time.Parse("2006-01-02", "2027-01-05"); return d }
	p.Now = later
	if err := p.SetObjectOverride(2, "wall_loops", "4"); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "o.3mf")
	if err := p.Save(out); err != nil {
		t.Fatal(err)
	}
	_, got := readZip(t, out)
	// Only model_settings.config changed: 3dmodel.model keeps its date.
	if !bytes.Equal(got["3D/3dmodel.model"], []byte(goldenModel)) {
		t.Error("3dmodel.model must not be rewritten for a settings change")
	}
	if !strings.Contains(string(got["Metadata/model_settings.config"]), `<metadata key="wall_loops" value="4"/>`) {
		t.Errorf("%s", got["Metadata/model_settings.config"])
	}
	p.Now = later
	if err := p.SetTransform(2, 0, mesh.Translate(1, 2, 3)); err != nil {
		t.Fatal(err)
	}
	if err := p.Save(out); err != nil {
		t.Fatal(err)
	}
	_, got = readZip(t, out)
	if !strings.Contains(string(got["3D/3dmodel.model"]), `<metadata name="ModificationDate">2027-01-05</metadata>`) ||
		!strings.Contains(string(got["3D/3dmodel.model"]), `transform="1 0 0 0 1 0 0 0 1 1 2 3" printable="1"`) {
		t.Errorf("%s", got["3D/3dmodel.model"])
	}
}
