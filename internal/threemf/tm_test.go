package threemf

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sairaph/creality-slicer-mcp/internal/mesh"
)

// TM2: a package from another tool keeps the relationships it has.
func TestThumbnailsKeepForeignRootRelationships(t *testing.T) {
	entries := bambuInlineEntries()
	foreign := `<?xml version="1.0" encoding="UTF-8"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
 <Relationship Target="/3D/3dmodel.model" Id="rel-1" Type="http://schemas.microsoft.com/3dmanufacturing/2013/01/3dmodel"/>
 <Relationship Target="/docProps/cover.png" Id="rel-2" Type="http://schemas.openxmlformats.org/package/2006/relationships/metadata/thumbnail"/>
 <Relationship Target="/Other/extra.xml" Id="rel-9" Type="http://example.org/extra"/>
</Relationships>`
	for i := range entries {
		if entries[i].name == "_rels/.rels" {
			entries[i].data = []byte(foreign)
		}
	}
	entries = append(entries, entry{"docProps/cover.png", pngBytes}, entry{"Other/extra.xml", []byte("<x/>")})
	p, err := Open(writeZip(t, entries))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	p.Now = fixedNow
	if err := p.SetPlateThumbnails(1, map[ThumbnailKind][]byte{ThumbSmall: pngBytes}); err != nil { // only the small image
		t.Fatal(err)
	}
	if err := p.SetPlateThumbnails(1, map[ThumbnailKind][]byte{ThumbPlate: pngBytes}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "t.3mf")
	if err := p.Save(path); err != nil {
		t.Fatal(err)
	}
	_, got := readZip(t, path)
	rels := string(got["_rels/.rels"])
	for _, want := range []string{`Target="/docProps/cover.png" Id="rel-2"`, `Target="/Other/extra.xml" Id="rel-9"`, `Target="/3D/3dmodel.model" Id="rel-1"`,
		`Target="/Metadata/plate_1.png"`, `cover-thumbnail-middle`, `Target="/Metadata/plate_1_small.png"`} {
		if !strings.Contains(rels, want) {
			t.Errorf("rels lack %s:\n%s", want, rels)
		}
	}
	// The added plate relationship does not reuse the taken id rel-2.
	if strings.Count(rels, `Id="rel-2"`) != 1 {
		t.Errorf("duplicate id:\n%s", rels)
	}
	// Setting the same images again adds nothing.
	if err := p.SetPlateThumbnails(1, map[ThumbnailKind][]byte{ThumbPlate: pngBytes, ThumbSmall: pngBytes}); err != nil {
		t.Fatal(err)
	}
	p.Save(path)
	_, again := readZip(t, path)
	if strings.Count(string(again["_rels/.rels"]), "<Relationship ") != strings.Count(rels, "<Relationship ")+0 {
		t.Errorf("relationships grew:\n%s", again["_rels/.rels"])
	}
}

// A package that only holds the model and thumbnail relationships gets the app's text.
func TestThumbnailRelsOfATidyPackageAreTheAppsText(t *testing.T) {
	p := newProject(t)
	if _, err := p.AddObject(ObjectSpec{Name: "Cube", Mesh: mesh.Box(10, 10, 10)}); err != nil {
		t.Fatal(err)
	}
	if err := p.SetPlateThumbnails(1, allImages()); err != nil {
		t.Fatal(err)
	}
	q := saveReopen(t, p)
	if err := q.SetPlateThumbnails(1, map[ThumbnailKind][]byte{ThumbPlate: pngBytes}); err != nil {
		t.Fatal(err)
	}
	if want := string(genRootRels(true, true)); string(mustRead(t, q, "_rels/.rels")) != want {
		t.Errorf("rels\n%s\nwant\n%s", mustRead(t, q, "_rels/.rels"), want)
	}
}

func mustRead(t *testing.T, p *Project, name string) []byte {
	t.Helper()
	b, err := p.Read(name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TM3: an inline mesh that references a 3MF material cannot be moved to its own file.
func TestInlineMeshWithMaterialReferenceIsRefused(t *testing.T) {
	entries := bambuInlineEntries()
	for i := range entries {
		if entries[i].name == "3D/3dmodel.model" {
			entries[i].data = []byte(strings.Replace(string(entries[i].data), `<triangle v1="0" v2="3" v3="2"/>`, `<triangle v1="0" v2="3" v3="2" pid="1" p1="0"/>`, 1))
		}
	}
	p, err := Open(writeZip(t, entries))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	err = p.SetMetadata("Title", "x")
	if err == nil {
		err = p.SetPlateThumbnails(1, map[ThumbnailKind][]byte{ThumbPlate: pngBytes})
	}
	if !errors.Is(err, ErrUnsupportedLayout) || !strings.Contains(err.Error(), "3MF materials") {
		t.Fatalf("got %v", err)
	}
	// Untouched, the file still opens and saves as it is.
	if _, err := p.Read("3D/3dmodel.model"); err != nil {
		t.Fatal(err)
	}
}

// TM1 (final review): text XML cannot hold is refused at every boundary and
// never reaches the files, so exported projects are always well formed.
func TestControlCharactersNeverReachTheFiles(t *testing.T) {
	bad := []string{"a\x00b", "a\x0bb", "a￾b", "a\x1fb"}
	p := newProject(t)
	defer p.Close()
	o, err := p.AddObject(ObjectSpec{Name: "Cube", Mesh: mesh.Box(10, 10, 10)})
	if err != nil {
		t.Fatal(err)
	}
	part, _ := p.AddPart(o.ID, PartSpec{Subtype: SubtypeModifier, Name: "mod", Mesh: mesh.Box(2, 2, 2)})
	for _, s := range bad {
		for name, err := range map[string]error{
			"rename object":  p.RenameObject(o.ID, s),
			"rename plate":   p.RenamePlate(1, s),
			"metadata":       p.SetMetadata("Title", s),
			"object setting": p.SetObjectOverride(o.ID, "wall_loops", s),
			"part setting":   p.SetPartOverride(o.ID, part.ID, "wall_loops", s),
			"plate key":      p.SetPlateKey(1, "bed_type", s),
			"gcode":          p.SetCustomGCodes(1, ModeSingleExtruder, []GCodeItem{{TopZ: 1, Type: GCodeCustom, Extra: s}}),
			"ranges":         p.SetLayerRanges(o.ID, []LayerRange{{MinZ: 0, MaxZ: 1, Options: KVs{{"layer_height", s}}}}),
		} {
			if !errors.Is(err, ErrInvalid) {
				t.Errorf("%s with %q: %v", name, s, err)
			}
		}
		if _, err := p.AddObject(ObjectSpec{Name: s, Mesh: mesh.Box(5, 5, 5)}); !errors.Is(err, ErrInvalid) {
			t.Errorf("add object %q: %v", s, err)
		}
		if _, err := p.AddPart(o.ID, PartSpec{Name: s, Mesh: mesh.Box(1, 1, 1)}); !errors.Is(err, ErrInvalid) {
			t.Errorf("add part %q: %v", s, err)
		}
	}
	// AddPlate has no error to return: the characters are dropped.
	pl := p.AddPlate("pla\x0bte")
	if pl.Name != "plate" {
		t.Errorf("plate name %q", pl.Name)
	}
	// Tab, line feed and carriage return stay legal.
	if err := p.RenameObject(o.ID, "a\tb"); err != nil {
		t.Errorf("tab refused: %v", err)
	}
	// Whatever a file brought in is stripped on write: force a name in.
	o.Name = "in\x0bput"
	p.MarkModified()
	path := filepath.Join(t.TempDir(), "x.3mf")
	if err := p.Save(path); err != nil {
		t.Fatal(err)
	}
	_, files := readZip(t, path)
	for name, data := range files {
		if strings.HasSuffix(name, ".config") || strings.HasSuffix(name, ".model") || strings.HasSuffix(name, ".xml") {
			dec := xml.NewDecoder(bytes.NewReader(data))
			dec.Strict = true
			for {
				if _, err := dec.Token(); err != nil {
					if err != io.EOF {
						t.Errorf("%s is not well formed XML: %v", name, err)
					}
					break
				}
			}
		}
	}
	if !strings.Contains(string(files["Metadata/model_settings.config"]), "input") {
		t.Errorf("the stripped name is missing from model_settings.config")
	}
}

// V3: a multi-line custom G-code keeps its line breaks in custom_gcode_per_layer.xml
// (written as character references, which every XML parser reads back as line breaks).
func TestMultiLineCustomGCodeSurvives(t *testing.T) {
	p := newProject(t)
	defer p.Close()
	if _, err := p.AddObject(ObjectSpec{Name: "Cube", Mesh: mesh.Box(10, 10, 10)}); err != nil {
		t.Fatal(err)
	}
	text := "M117 first\nM117 second\r\nM117 third\twith tab"
	if err := p.SetCustomGCodes(1, ModeSingleExtruder, []GCodeItem{{TopZ: 2, Type: GCodeCustom, Extra: text}}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "m.3mf")
	if err := p.Save(path); err != nil {
		t.Fatal(err)
	}
	_, files := readZip(t, path)
	raw := string(files["Metadata/custom_gcode_per_layer.xml"])
	if strings.Contains(raw, "first\nM117") || !strings.Contains(raw, "first&#10;M117 second&#13;&#10;M117 third&#9;with") {
		t.Fatalf("attribute not escaped:\n%s", raw)
	}
	// A strict XML parser (attribute value normalisation included) returns the text unchanged.
	dec := xml.NewDecoder(strings.NewReader(raw))
	found := false
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		if se, ok := tok.(xml.StartElement); ok && se.Name.Local == "layer" {
			for _, a := range se.Attr {
				if a.Name.Local == "extra" && a.Value == text {
					found = true
				}
			}
		}
	}
	if !found {
		t.Fatal("the text did not survive a strict XML parse")
	}
	q := saveReopen(t, p)
	defer q.Close()
	if got := q.CustomGCodes(1).Items[0].Extra; got != text {
		t.Fatalf("reopened extra %q, want %q", got, text)
	}
}
