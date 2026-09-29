package threemf

import (
	"fmt"
	"strconv"
	"strings"
)

// A plate's slice result (the embedded G-code, its checksum, its entry in
// slice_info.config and the relationship that points at the G-code) is only
// true for the objects, placements and settings it was made from. Every
// change to a plate resets it, so a project never carries a result that no
// longer matches. The header of slice_info.config stays.

const memberGCodeRels = "Metadata/_rels/model_settings.config.rels"

// plateKeysThatDoNotChangeTheSlice are per plate keys that name files or
// results, not print settings.
var plateKeysThatDoNotChangeTheSlice = map[string]bool{
	"thumbnail_file": true, "thumbnail_no_light_file": true, "top_file": true, "pick_file": true,
	"pattern_bbox_file": true, "gcode_file": true,
}

// ResetSliceResult removes the slice result of a plate: Metadata/plate_N.gcode
// and its .md5, the plate entry of slice_info.config, the gcode relationship
// and the plate's gcode_file key. It does nothing for a plate without a result.
func (p *Project) ResetSliceResult(plate int) {
	for _, name := range []string{
		"Metadata/plate_" + strconv.Itoa(plate) + ".gcode",
		"Metadata/plate_" + strconv.Itoa(plate) + ".gcode.md5",
	} {
		if p.byName[name] != nil {
			p.removeMember(name)
		}
	}
	if pl := p.Plate(plate); pl != nil && pl.Config.Delete("gcode_file") {
		p.settingsDirty = true
	}
	p.dropSliceInfoPlate(plate)
	p.dropGCodeRelationship(plate)
}

func (p *Project) resetAllSliceResults() {
	for _, pl := range p.Plates {
		p.ResetSliceResult(pl.Index)
	}
}

// platesOf returns the indices of the plates that hold instances of an object.
func (p *Project) platesOf(objectID int) []int {
	var out []int
	for _, pl := range p.Plates {
		for _, in := range pl.Instances {
			if in.ObjectID == objectID {
				out = append(out, pl.Index)
				break
			}
		}
	}
	return out
}

func (p *Project) resetPlatesOf(objectID int) {
	for _, idx := range p.platesOf(objectID) {
		p.ResetSliceResult(idx)
	}
}

// dropSliceInfoPlate removes the <plate> entry with the given index from
// slice_info.config, keeping the header. The file is only rewritten when an
// entry was removed.
func (p *Project) dropSliceInfoPlate(index int) {
	m := p.byName[memberSliceInfo]
	if m == nil {
		return
	}
	data, err := m.content()
	if err != nil {
		return
	}
	root, err := parseTree(data)
	if err != nil || root.Name != "config" {
		return
	}
	kept := root.Children[:0:0]
	removed := false
	for _, c := range root.Children {
		if c.Name == "plate" && plateEntryIndex(c) == index {
			removed = true
			continue
		}
		kept = append(kept, c)
	}
	if !removed {
		return
	}
	root.Children = kept
	var b strings.Builder
	b.WriteString(xmlDecl + "<config>\n")
	for _, c := range root.Children {
		c.write(&b, "  ")
	}
	b.WriteString("</config>\n")
	p.setMember(memberSliceInfo, []byte(b.String()))
}

// plateEntryIndex reads the index metadata of a slice_info plate entry.
func plateEntryIndex(n *node) int {
	for _, c := range n.Children {
		if c.Name == "metadata" && c.attrValue("key") == "index" {
			i, err := strconv.Atoi(c.attrValue("value"))
			if err != nil {
				return -1
			}
			return i
		}
	}
	return -1
}

// dropGCodeRelationship removes the relationship to Metadata/plate_N.gcode
// from Metadata/_rels/model_settings.config.rels; the file goes when no
// relationship is left.
func (p *Project) dropGCodeRelationship(index int) {
	m := p.byName[memberGCodeRels]
	if m == nil {
		return
	}
	data, err := m.content()
	if err != nil {
		return
	}
	root, err := parseTree(data)
	if err != nil || root.Name != "Relationships" {
		return
	}
	target := "/Metadata/plate_" + strconv.Itoa(index) + ".gcode"
	var kept []*node
	removed := false
	for _, c := range root.Children {
		if c.Name == "Relationship" && c.attrValue("Target") == target {
			removed = true
			continue
		}
		kept = append(kept, c)
	}
	if !removed {
		return
	}
	if len(kept) == 0 {
		p.removeMember(memberGCodeRels)
		return
	}
	var b strings.Builder
	b.WriteString(xmlDecl + "<Relationships xmlns=\"http://schemas.openxmlformats.org/package/2006/relationships\">\n")
	for _, c := range kept {
		b.WriteString(" <Relationship")
		for _, a := range c.Attrs {
			b.WriteString(" " + a.Key + "=\"" + attrEscape(a.Value) + "\"")
		}
		b.WriteString("/>\n")
	}
	b.WriteString("</Relationships>")
	p.setMember(memberGCodeRels, []byte(b.String()))
}

// SliceResult describes the embedded slice result of a plate, if any.
type SliceResult struct {
	Plate int
	// GCode is the member holding the G-code ("" when the plate has none).
	GCode string
}

// SliceResults lists the plates that carry an embedded G-code member.
func (p *Project) SliceResults() []SliceResult {
	var out []SliceResult
	for _, pl := range p.Plates {
		name := "Metadata/plate_" + strconv.Itoa(pl.Index) + ".gcode"
		if p.byName[name] != nil {
			out = append(out, SliceResult{Plate: pl.Index, GCode: name})
		}
	}
	return out
}

func plateFile(prefix string, index int, suffix string) string {
	return fmt.Sprintf("Metadata/%s%d%s", prefix, index, suffix)
}
