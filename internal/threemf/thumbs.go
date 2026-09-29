package threemf

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

// ThumbnailKind names one of the images the app stores per plate.
type ThumbnailKind string

// Plate image kinds and their members (N is the plate number):
//
//	plate    Metadata/plate_N.png           the plate picture
//	small    Metadata/plate_N_small.png     its small version
//	no_light Metadata/plate_no_light_N.png  the picture without lighting
//	top      Metadata/top_N.png             the top view
//	pick     Metadata/pick_N.png            the object pick map
const (
	ThumbPlate   ThumbnailKind = "plate"
	ThumbSmall   ThumbnailKind = "small"
	ThumbNoLight ThumbnailKind = "no_light"
	ThumbTop     ThumbnailKind = "top"
	ThumbPick    ThumbnailKind = "pick"
)

func (k ThumbnailKind) member(plate int) (string, bool) {
	switch k {
	case ThumbPlate:
		return plateFile("plate_", plate, ".png"), true
	case ThumbSmall:
		return plateFile("plate_", plate, "_small.png"), true
	case ThumbNoLight:
		return plateFile("plate_no_light_", plate, ".png"), true
	case ThumbTop:
		return plateFile("top_", plate, ".png"), true
	case ThumbPick:
		return plateFile("pick_", plate, ".png"), true
	}
	return "", false
}

// plateKey is the model_settings.config plate key that names the image (the
// small picture has none).
func (k ThumbnailKind) plateKey() string {
	switch k {
	case ThumbPlate:
		return "thumbnail_file"
	case ThumbNoLight:
		return "thumbnail_no_light_file"
	case ThumbTop:
		return "top_file"
	case ThumbPick:
		return "pick_file"
	}
	return ""
}

var pngSignature = []byte("\x89PNG\r\n\x1a\n")

// SetPlateThumbnails stores the images of a plate exactly as the app does:
// the members listed at the ThumbnailKind constants (stored, not compressed),
// the plate keys thumbnail_file, thumbnail_no_light_file, top_file and
// pick_file in model_settings.config, and, for plate 1, the Thumbnail_Middle
// and Thumbnail_Small metadata of 3dmodel.model and the thumbnail
// relationships of _rels/.rels. Kinds not in images are left as they are. The
// stale Metadata/plate_N.json (pick data of the previous picture) is deleted.
// Images must be PNG files. A new project stays valid without thumbnails; the
// slice result of the plate is not touched.
func (p *Project) SetPlateThumbnails(plate int, images map[ThumbnailKind][]byte) error {
	pl := p.Plate(plate)
	if pl == nil {
		return fmt.Errorf("%w: plate %d", ErrNotFound, plate)
	}
	if len(images) == 0 {
		return fmt.Errorf("%w: no thumbnails given", ErrInvalid)
	}
	for kind, data := range images {
		if _, ok := kind.member(plate); !ok {
			return fmt.Errorf("%w: thumbnail kind %q (use plate, small, no_light, top or pick)", ErrInvalid, kind)
		}
		if !bytes.HasPrefix(data, pngSignature) {
			return fmt.Errorf("%w: the %s thumbnail is not a PNG image", ErrInvalid, kind)
		}
	}
	if err := p.ensureSplit(); err != nil {
		return err
	}
	for _, kind := range []ThumbnailKind{ThumbPlate, ThumbSmall, ThumbNoLight, ThumbTop, ThumbPick} {
		data, ok := images[kind]
		if !ok {
			continue
		}
		name, _ := kind.member(plate)
		p.setMember(name, append([]byte(nil), data...))
		if key := kind.plateKey(); key != "" {
			pl.Config.Set(key, name)
		}
	}
	if p.byName[plateFile("plate_", plate, ".json")] != nil {
		p.removeMember(plateFile("plate_", plate, ".json"))
	}
	p.ensurePNGContentType()
	p.settingsDirty = true
	if plate == 1 {
		if _, ok := images[ThumbPlate]; ok {
			p.Metadata.Set("Thumbnail_Middle", "/"+plateFile("plate_", 1, ".png"))
		}
		if _, ok := images[ThumbSmall]; ok {
			p.Metadata.Set("Thumbnail_Small", "/"+plateFile("plate_", 1, "_small.png"))
		}
		p.setMember(memberRels, p.rootRelsWithThumbnails(p.byName[plateFile("plate_", 1, ".png")] != nil, p.byName[plateFile("plate_", 1, "_small.png")] != nil))
		p.modelDirty = true
	}
	return nil
}

// Thumbnail returns the stored image of a plate, or ErrNotFound.
func (p *Project) Thumbnail(plate int, kind ThumbnailKind) ([]byte, error) {
	name, ok := kind.member(plate)
	if !ok {
		return nil, fmt.Errorf("%w: thumbnail kind %q", ErrInvalid, kind)
	}
	return p.Read(name)
}

// genRootRels renders _rels/.rels the way the app does, with the thumbnail
// relationships of plate 1 when its images exist. The last line has no
// leading space: that is how the app writes it.
func genRootRels(plate, small bool) []byte {
	var b strings.Builder
	b.WriteString(xmlDecl)
	b.WriteString("<Relationships xmlns=\"http://schemas.openxmlformats.org/package/2006/relationships\">\n")
	b.WriteString(" <Relationship Target=\"/3D/3dmodel.model\" Id=\"rel-1\" Type=\"http://schemas.microsoft.com/3dmanufacturing/2013/01/3dmodel\"/>\n")
	if plate {
		b.WriteString(" <Relationship Target=\"/Metadata/plate_1.png\" Id=\"rel-2\" Type=\"http://schemas.openxmlformats.org/package/2006/relationships/metadata/thumbnail\"/>\n")
		b.WriteString(" <Relationship Target=\"/Metadata/plate_1.png\" Id=\"rel-4\" Type=\"http://schemas.bambulab.com/package/2021/cover-thumbnail-middle\"/>\n")
	}
	if small {
		b.WriteString("<Relationship Target=\"/Metadata/plate_1_small.png\" Id=\"rel-5\" Type=\"http://schemas.bambulab.com/package/2021/cover-thumbnail-small\"/>\n")
	}
	b.WriteString("</Relationships>")
	return []byte(b.String())
}

// ensurePNGContentType adds the png default to [Content_Types].xml when the
// package does not declare it.
func (p *Project) ensurePNGContentType() {
	m := p.byName[memberContentTypes]
	if m == nil {
		return
	}
	data, err := m.content()
	if err != nil || bytes.Contains(data, []byte(`Extension="png"`)) {
		return
	}
	text := string(data)
	line := " <Default Extension=\"png\" ContentType=\"image/png\"/>\n"
	if i := strings.LastIndex(text, "</Types>"); i >= 0 {
		text = text[:i] + line + text[i:]
		p.setMember(memberContentTypes, []byte(text))
	}
}

// renamePlateMembers moves a plate's members (images, pick data, slice
// result) to another plate number.
func (p *Project) renamePlateMembers(from, to int) {
	pairs := [][2]string{
		{plateFile("plate_", from, ".png"), plateFile("plate_", to, ".png")},
		{plateFile("plate_", from, "_small.png"), plateFile("plate_", to, "_small.png")},
		{plateFile("plate_no_light_", from, ".png"), plateFile("plate_no_light_", to, ".png")},
		{plateFile("top_", from, ".png"), plateFile("top_", to, ".png")},
		{plateFile("pick_", from, ".png"), plateFile("pick_", to, ".png")},
		{plateFile("plate_", from, ".json"), plateFile("plate_", to, ".json")},
	}
	for _, pr := range pairs {
		m := p.byName[pr[0]]
		if m == nil {
			continue
		}
		data, err := m.content()
		if err != nil {
			continue
		}
		p.removeMember(pr[0])
		p.setMember(pr[1], data)
	}
}

// removePlateMembers deletes a plate's images, pick data and slice result.
func (p *Project) removePlateMembers(plate int) {
	for _, name := range []string{
		plateFile("plate_", plate, ".png"), plateFile("plate_", plate, "_small.png"),
		plateFile("plate_no_light_", plate, ".png"), plateFile("top_", plate, ".png"),
		plateFile("pick_", plate, ".png"), plateFile("plate_", plate, ".json"),
	} {
		if p.byName[name] != nil {
			p.removeMember(name)
		}
	}
	p.ResetSliceResult(plate)
}

// relSpec is one thumbnail relationship of _rels/.rels.
type relSpec struct{ target, typ, id string }

var (
	relPlate  = relSpec{"/Metadata/plate_1.png", "http://schemas.openxmlformats.org/package/2006/relationships/metadata/thumbnail", "rel-2"}
	relMiddle = relSpec{"/Metadata/plate_1.png", "http://schemas.bambulab.com/package/2021/cover-thumbnail-middle", "rel-4"}
	relSmall  = relSpec{"/Metadata/plate_1_small.png", "http://schemas.bambulab.com/package/2021/cover-thumbnail-small", "rel-5"}
)

// rootRelsWithThumbnails returns _rels/.rels with the thumbnail relationships
// of plate 1. A package the tools made, or one that only holds the model and
// thumbnail relationships, gets the app's exact text. A package from another
// tool keeps every relationship it has (a cover image of its own, for
// example); only the missing thumbnail relationships are added.
func (p *Project) rootRelsWithThumbnails(plate, small bool) []byte {
	m := p.byName[memberRels]
	if m == nil {
		return genRootRels(plate, small)
	}
	data, err := m.content()
	if err != nil {
		return genRootRels(plate, small)
	}
	root, err := parseTree(data)
	if err != nil || root.Name != "Relationships" {
		return genRootRels(plate, small)
	}
	ours := func(c *node) (relSpec, bool) {
		for _, s := range []relSpec{relPlate, relMiddle, relSmall} {
			if c.attrValue("Target") == s.target && c.attrValue("Type") == s.typ {
				return s, true
			}
		}
		return relSpec{}, false
	}
	have := map[relSpec]bool{}
	used := map[string]bool{}
	foreign := false
	for _, c := range root.Children {
		if c.Name != "Relationship" {
			foreign = true
			continue
		}
		used[c.attrValue("Id")] = true
		if s, ok := ours(c); ok {
			have[s] = true
			continue
		}
		if c.attrValue("Target") == "/3D/3dmodel.model" && strings.HasSuffix(c.attrValue("Type"), "/3dmodel") {
			continue
		}
		foreign = true
	}
	if !foreign {
		return genRootRels(plate || have[relPlate], small || have[relSmall])
	}
	add := func(s relSpec) {
		if have[s] {
			return
		}
		id := s.id
		for n := 1; used[id]; n++ {
			id = "rel-" + strconv.Itoa(100+n)
		}
		used[id] = true
		root.Children = append(root.Children, &node{Name: "Relationship", Attrs: KVs{{"Target", s.target}, {"Id", id}, {"Type", s.typ}}})
	}
	if plate {
		add(relPlate)
		add(relMiddle)
	}
	if small {
		add(relSmall)
	}
	var b strings.Builder
	b.WriteString(xmlDecl + "<Relationships xmlns=\"http://schemas.openxmlformats.org/package/2006/relationships\">\n")
	for _, c := range root.Children {
		b.WriteString(" <" + c.Name)
		for _, a := range c.Attrs {
			if a.Key == "xmlns" {
				continue
			}
			b.WriteString(" " + a.Key + "=\"" + attrEscape(a.Value) + "\"")
		}
		b.WriteString("/>\n")
	}
	b.WriteString("</Relationships>")
	return []byte(b.String())
}
