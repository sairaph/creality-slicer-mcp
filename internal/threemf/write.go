package threemf

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/sairaph/creality-slicer-mcp/internal/mesh"
)

// UUID suffixes and constants of the app's writer (bbs_3mf.cpp:263-268).
const (
	objectUUIDSuffix    = "-61cb-4c03-9d28-80fed5dfa1dc"
	objectUUIDSuffix2   = "-71cb-4c03-9d28-80fed5dfa1dc"
	subObjectUUIDSuffix = "-81cb-4c03-9d28-80fed5dfa1dc"
	componentUUIDSuffix = "-b206-40ff-9872-83e8017abed1"
	buildUUID           = "2c7c17d8-22b5-4d84-8835-1976022ea369"
	buildUUIDSuffix     = "-b1ec-4553-aec9-835e5b724bb4"

	defaultRootTag = `<model unit="millimeter" xml:lang="en-US" xmlns="http://schemas.microsoft.com/3dmanufacturing/core/2015/02" xmlns:BambuStudio="http://schemas.bambulab.com/package/2021" xmlns:p="http://schemas.microsoft.com/3dmanufacturing/production/2015/06" requiredextensions="p">`
	xmlDecl        = "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n"
)

func hex8(n int) string { return fmt.Sprintf("%08x", uint32(n)) }

// genModel renders 3D/3dmodel.model.
func (p *Project) genModel() []byte {
	var b strings.Builder
	b.WriteString(xmlDecl)
	root := p.rootTag
	if root == "" {
		root = defaultRootTag
	}
	b.WriteString(root + "\n")
	for _, kv := range p.Metadata {
		b.WriteString(" <metadata name=\"" + kv.Key + "\">" + xmlEscape(kv.Value) + "</metadata>\n")
	}
	b.WriteString(" <resources>\n")
	for _, o := range p.Objects {
		typ := o.Type
		if typ == "" {
			typ = "model"
		}
		b.WriteString("  <object id=\"" + strconv.Itoa(o.ID))
		if o.UUID != "" {
			b.WriteString("\" p:UUID=\"" + o.UUID)
		}
		b.WriteString("\" type=\"" + typ + "\">\n")
		b.WriteString("   <components>\n")
		for i, part := range o.Parts {
			b.WriteString("    <component p:path=\"" + xmlEscape(part.Mesh.Path) + "\" objectid=\"" + strconv.Itoa(part.ID))
			uuid := part.componentUUID
			if uuid == "" {
				uuid = hex8(i+(o.backupID<<16)) + componentUUIDSuffix
			}
			b.WriteString("\" p:UUID=\"" + uuid + "\" transform=\"" + part.ComponentTransform.String() + "\"/>\n")
		}
		b.WriteString("   </components>\n")
		b.WriteString("  </object>\n")
	}
	b.WriteString(" </resources>\n")
	if len(p.Items) == 0 {
		b.WriteString(" <build/>\n")
	} else {
		bu := p.buildUUID
		if bu == "" {
			bu = buildUUID
		}
		b.WriteString(" <build p:UUID=\"" + bu + "\">\n")
		for _, it := range p.Items {
			b.WriteString("  <item objectid=\"" + strconv.Itoa(it.ObjectID))
			uuid := it.UUID
			if uuid == "" {
				uuid = hex8(it.ObjectID) + buildUUIDSuffix
			}
			b.WriteString("\" p:UUID=\"" + uuid)
			if it.Path != "" {
				b.WriteString("\" p:path=\"" + xmlEscape(it.Path))
			}
			printable := "1"
			if !it.Printable {
				printable = "0"
			}
			b.WriteString("\" transform=\"" + it.Transform.String() + "\" printable=\"" + printable + "\"/>\n")
		}
		b.WriteString(" </build>\n")
	}
	b.WriteString("</model>\n")
	return []byte(b.String())
}

// genObjectModel renders a 3D/Objects/object_N.model holding the meshes of
// the given parts.
func (p *Project) genObjectModel(o *Object, parts []*Part) []byte {
	var b strings.Builder
	b.WriteString(xmlDecl)
	root := p.rootTag
	if root == "" {
		root = defaultRootTag
	}
	b.WriteString(root + "\n")
	b.WriteString(" <metadata name=\"BambuStudio:3mfVersion\">1</metadata>\n")
	b.WriteString(" <resources>\n")
	for _, part := range parts {
		typ := "model"
		if part.Subtype != SubtypeNormal {
			typ = "other"
		}
		idx := 0
		for i, q := range o.Parts {
			if q == part {
				idx = i
			}
		}
		b.WriteString("  <object id=\"" + strconv.Itoa(part.ID) + "\" p:UUID=\"" + hex8(idx+(o.backupID<<16)) + subObjectUUIDSuffix + "\" type=\"" + typ + "\">\n")
		b.WriteString("   <mesh>\n    <vertices>\n")
		m := part.fresh
		for _, v := range m.Vertices {
			b.WriteString("     <vertex x=\"" + mesh.FormatG9(float64(v[0])) + "\" y=\"" + mesh.FormatG9(float64(v[1])) + "\" z=\"" + mesh.FormatG9(float64(v[2])) + "\"/>\n")
		}
		b.WriteString("    </vertices>\n    <triangles>\n")
		for _, t := range m.Triangles {
			b.WriteString("     <triangle v1=\"" + strconv.FormatUint(uint64(t[0]), 10) + "\" v2=\"" + strconv.FormatUint(uint64(t[1]), 10) + "\" v3=\"" + strconv.FormatUint(uint64(t[2]), 10) + "\"/>\n")
		}
		b.WriteString("    </triangles>\n   </mesh>\n  </object>\n")
	}
	b.WriteString(" </resources>\n <build/>\n</model>\n")
	return []byte(b.String())
}

// genModelRels renders 3D/_rels/3dmodel.model.rels for the object files.
func genModelRels(files []string) []byte {
	var b strings.Builder
	b.WriteString(xmlDecl)
	b.WriteString("<Relationships xmlns=\"http://schemas.openxmlformats.org/package/2006/relationships\">\n")
	for i, f := range files {
		b.WriteString(" <Relationship Target=\"/" + xmlEscape(f) + "\" Id=\"rel-" + strconv.Itoa(i+1) + "\" Type=\"http://schemas.microsoft.com/3dmanufacturing/2013/01/3dmodel\"/>\n")
	}
	b.WriteString("</Relationships>")
	return []byte(b.String())
}

// objectFileNames lists the 3D/Objects/*.model members in numeric order.
func (p *Project) objectFileNames() []string {
	var files []string
	for _, m := range p.members {
		if strings.HasPrefix(m.name, "3D/Objects/") && strings.HasSuffix(m.name, ".model") {
			files = append(files, m.name)
		}
	}
	num := func(s string) int {
		s = strings.TrimSuffix(strings.TrimPrefix(s, "3D/Objects/object_"), ".model")
		n, err := strconv.Atoi(s)
		if err != nil {
			return 1 << 30
		}
		return n
	}
	sort.SliceStable(files, func(i, j int) bool { return num(files[i]) < num(files[j]) })
	return files
}

const contentTypes = xmlDecl + "<Types xmlns=\"http://schemas.openxmlformats.org/package/2006/content-types\">\n" +
	" <Default Extension=\"rels\" ContentType=\"application/vnd.openxmlformats-package.relationships+xml\"/>\n" +
	" <Default Extension=\"model\" ContentType=\"application/vnd.ms-package.3dmanufacturing-3dmodel+xml\"/>\n" +
	" <Default Extension=\"png\" ContentType=\"image/png\"/>\n" +
	" <Default Extension=\"gcode\" ContentType=\"text/x.gcode\"/>\n" +
	"</Types>"

const rootRels = xmlDecl + "<Relationships xmlns=\"http://schemas.openxmlformats.org/package/2006/relationships\">\n" +
	" <Relationship Target=\"/3D/3dmodel.model\" Id=\"rel-1\" Type=\"http://schemas.microsoft.com/3dmanufacturing/2013/01/3dmodel\"/>\n" +
	"</Relationships>"

// genCreality renders Metadata/creality.config.
func genCreality(kvs KVs) []byte {
	var b strings.Builder
	b.WriteString(xmlDecl + "<config>\n")
	for _, kv := range kvs {
		b.WriteString("    <metadata key=\"" + xmlEscape(kv.Key) + "\" value=\"" + xmlEscape(kv.Value) + "\"/>\n")
	}
	b.WriteString("</config>\n")
	return []byte(b.String())
}

// genSliceInfo renders the header only slice_info.config of an unsliced project.
func genSliceInfo(version string) []byte {
	return []byte(xmlDecl + "<config>\n  <header>\n    <header_item key=\"X-CX-Client-Type\" value=\"creality_print\"/>\n" +
		"    <header_item key=\"X-CX-Client-Version\" value=\"" + xmlEscape(sliceInfoVersion(version)) + "\"/>\n  </header>\n</config>\n")
}

// sliceInfoVersion turns "7.2.2.5483" into "07.02.02.5483".
func sliceInfoVersion(v string) string {
	parts := strings.Split(v, ".")
	for i, s := range parts {
		if n, err := strconv.Atoi(s); err == nil {
			parts[i] = fmt.Sprintf("%02d", n)
		}
	}
	return strings.Join(parts, ".")
}

// plateKeyOrder is the order the app writes optional plate keys in.
var plateKeyOrder = []string{
	"bed_type", "print_sequence", "first_layer_print_sequence", "other_layers_print_sequence",
	"other_layers_print_sequence_nums", "spiral_mode", "gcode_file", "thumbnail_file",
	"thumbnail_no_light_file", "top_file", "pick_file", "pattern_bbox_file",
}

func metaLine(indent, key, value string) string {
	return indent + "<metadata key=\"" + xmlEscape(key) + "\" value=\"" + xmlEscape(value) + "\"/>\n"
}

// genModelSettings renders Metadata/model_settings.config.
func (p *Project) genModelSettings() []byte {
	var b strings.Builder
	b.WriteString(xmlDecl + "<config>\n")
	for _, o := range p.settingsObjects() {
		b.WriteString("  <object id=\"" + strconv.Itoa(o.ID) + "\">\n")
		if o.Name != "" {
			b.WriteString(metaLine("    ", "name", o.Name))
		}
		if o.Module != "" {
			b.WriteString(metaLine("    ", "module", o.Module))
		}
		for _, kv := range o.Config {
			b.WriteString(metaLine("    ", kv.Key, kv.Value))
		}
		for _, part := range o.Parts {
			if o.inSettings && !part.inSettings {
				continue
			}
			b.WriteString("    <part id=\"" + strconv.Itoa(part.ID) + "\" subtype=\"" + part.Subtype + "\">\n")
			if part.Name != "" {
				b.WriteString(metaLine("      ", "name", part.Name))
			}
			m := part.Matrix
			if !part.HasMatrix {
				m = mesh.Identity4()
			}
			b.WriteString(metaLine("      ", "matrix", m.String()))
			if s := part.Source; s != nil {
				if s.File != "" {
					b.WriteString(metaLine("      ", "source_file", s.File))
					b.WriteString(metaLine("      ", "source_object_id", s.ObjectID))
					b.WriteString(metaLine("      ", "source_volume_id", s.VolumeID))
					b.WriteString(metaLine("      ", "source_offset_x", s.OffsetX))
					b.WriteString(metaLine("      ", "source_offset_y", s.OffsetY))
					b.WriteString(metaLine("      ", "source_offset_z", s.OffsetZ))
				}
				if s.InInches {
					b.WriteString(metaLine("      ", "source_in_inches", "1"))
				} else if s.InMeters {
					b.WriteString(metaLine("      ", "source_in_meters", "1"))
				}
			}
			for _, kv := range part.Config {
				b.WriteString(metaLine("      ", kv.Key, kv.Value))
			}
			for _, x := range part.extra {
				x.write(&b, "      ")
			}
			stat := part.MeshStat
			if len(stat) == 0 {
				stat = KVs{{"edges_fixed", "0"}, {"degenerate_facets", "0"}, {"facets_removed", "0"}, {"facets_reversed", "0"}, {"backwards_edges", "0"}}
			}
			b.WriteString("      <mesh_stat")
			for _, kv := range stat {
				b.WriteString(" " + kv.Key + "=\"" + attrEscape(kv.Value) + "\"")
			}
			b.WriteString("/>\n")
			b.WriteString("    </part>\n")
		}
		for _, x := range o.extra {
			x.write(&b, "    ")
		}
		b.WriteString("  </object>\n")
	}
	for _, pl := range p.Plates {
		b.WriteString("  <plate>\n")
		b.WriteString(metaLine("    ", "plater_id", strconv.Itoa(pl.Index)))
		b.WriteString(metaLine("    ", "plater_name", pl.Name))
		b.WriteString(metaLine("    ", "locked", strconv.FormatBool(pl.Locked)))
		written := map[string]bool{}
		for _, key := range plateKeyOrder {
			if v, ok := pl.Config.Get(key); ok {
				b.WriteString(metaLine("    ", key, v))
				written[key] = true
			}
		}
		for _, kv := range pl.Config {
			if !written[kv.Key] {
				b.WriteString(metaLine("    ", kv.Key, kv.Value))
			}
		}
		for _, in := range pl.Instances {
			b.WriteString("    <model_instance>\n")
			b.WriteString(metaLine("      ", "object_id", strconv.Itoa(in.ObjectID)))
			b.WriteString(metaLine("      ", "instance_id", strconv.Itoa(in.InstanceID)))
			b.WriteString(metaLine("      ", "identify_id", strconv.Itoa(in.IdentifyID)))
			b.WriteString("    </model_instance>\n")
		}
		for _, x := range pl.extra {
			x.write(&b, "    ")
		}
		b.WriteString("  </plate>\n")
	}
	b.WriteString("  <assemble>\n")
	for _, it := range p.Assemble {
		b.WriteString("   <assemble_item")
		for _, kv := range it.Attrs {
			b.WriteString(" " + kv.Key + "=\"" + attrEscape(kv.Value) + "\"")
		}
		b.WriteString(" />\n")
	}
	b.WriteString("  </assemble>\n")
	b.WriteString("</config>\n")
	return []byte(b.String())
}

// genLayerRanges renders Metadata/layer_config_ranges.xml, or nil when no
// object has a height range. The app builds it with boost property_tree and
// then post processes the text; the same replacements are applied here.
func (p *Project) genLayerRanges() []byte {
	var b strings.Builder
	any := false
	for i, o := range p.Objects {
		if len(o.LayerRanges) == 0 {
			continue
		}
		if !any {
			b.WriteString("<objects>")
			any = true
		}
		b.WriteString("<object id=\"" + strconv.Itoa(i+1) + "\">")
		for _, r := range o.LayerRanges {
			b.WriteString("<range min_z=\"" + fmtG17(r.MinZ) + "\" max_z=\"" + fmtG17(r.MaxZ) + "\">")
			for _, opt := range r.Options {
				b.WriteString("<option opt_key=\"" + xmlEscape(opt.Key) + "\">" + xmlEscape(opt.Value) + "</option>")
			}
			b.WriteString("</range>")
		}
		b.WriteString("</object>")
	}
	if !any {
		return nil
	}
	b.WriteString("</objects>")
	out := b.String()
	// Sequential replace_all calls, in the app's order (a single pass would
	// treat overlapping patterns differently).
	for _, rep := range [][2]string{
		{"><object", ">\n <object"},
		{"><range", ">\n  <range"},
		{"><option", ">\n   <option"},
		{"></range>", ">\n  </range>"},
		{"></object>", ">\n </object>"},
		{"><", ">\n<"},
	} {
		out = strings.ReplaceAll(out, rep[0], rep[1])
	}
	return []byte("<?xml version=\"1.0\" encoding=\"utf-8\"?>\n" + out)
}

func fmtG17(v float64) string {
	if v == 0 {
		return "0"
	}
	return strconv.FormatFloat(v, 'g', 17, 64)
}

// genGCodes renders Metadata/custom_gcode_per_layer.xml (property_tree style:
// one tag per line, no trailing newline).
func (p *Project) genGCodes() []byte {
	var b strings.Builder
	b.WriteString("<custom_gcodes_per_layer>")
	for _, pg := range p.GCodes {
		b.WriteString("<plate><plate_info id=\"" + strconv.Itoa(pg.Plate) + "\"/>")
		for _, it := range pg.Items {
			gcode := it.GCode
			if gcode == "" {
				gcode = p.gcodeText(it)
			}
			b.WriteString("<layer top_z=\"" + fmtG17(it.TopZ) + "\" type=\"" + strconv.Itoa(it.Type) + "\" extruder=\"" + strconv.Itoa(it.Extruder) +
				"\" color=\"" + attrEscape(it.Color) + "\" extra=\"" + attrEscape(it.Extra) + "\" gcode=\"" + attrEscape(gcode) + "\"/>")
		}
		mode := pg.Mode
		if mode == "" {
			mode = ModeMultiExtruder
		}
		b.WriteString("<mode value=\"" + xmlEscape(mode) + "\"/></plate>")
	}
	b.WriteString("</custom_gcodes_per_layer>")
	out := strings.ReplaceAll(b.String(), "><", ">\n<")
	return []byte("<?xml version=\"1.0\" encoding=\"utf-8\"?>\n" + out)
}

// gcodeText is the "gcode" attribute the app writes for an item: the
// printer's pause G-code, the template G-code, "tool_change", or the item's own text.
func (p *Project) gcodeText(it GCodeItem) string {
	switch it.Type {
	case GCodePausePrint:
		if p.Settings != nil {
			if v := p.Settings.String("machine_pause_gcode"); v != "" {
				return v
			}
		}
		return "M601"
	case GCodeTemplate:
		if p.Settings != nil {
			return p.Settings.String("template_custom_gcode")
		}
		return ""
	case GCodeToolChange:
		return "tool_change"
	}
	return it.Extra
}

// settingsObjects returns the objects in the order model_settings.config
// listed them (the app writes them in memory order, which differs from the
// resource order), then any object that had no entry.
func (p *Project) settingsObjects() []*Object {
	out := make([]*Object, 0, len(p.Objects))
	seen := map[*Object]bool{}
	for _, id := range p.settingsOrder {
		if o := p.findObject(id); o != nil && !seen[o] {
			out = append(out, o)
			seen[o] = true
		}
	}
	for _, o := range p.Objects {
		if !seen[o] {
			out = append(out, o)
		}
	}
	return out
}
