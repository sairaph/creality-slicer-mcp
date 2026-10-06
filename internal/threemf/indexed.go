package threemf

import (
	"fmt"
	"strconv"
	"strings"
)

// Members whose entries are numbered by the 1 based index of a model object. The
// app numbers model objects in the order their build items first appear and
// leaves out resource objects nobody builds (bbs_3mf.cpp:5011-5021 adds an
// object when its first build item is read, 4378 deletes the others; the index
// maps are read with object.second+1 at 2732-2745), so the numbers are positions
// in builtObjects, not in the resource list.
const (
	memberLayerHeights = "Metadata/layer_heights_profile.txt"
	memberBrimEars     = "Metadata/brim_ear_points.txt"
	memberCutInfo      = "Metadata/cut_information.xml"
)

// builtObjects lists the objects the way the app numbers them: distinct objects
// in the order of their first build item.
func (p *Project) builtObjects() []*Object {
	var out []*Object
	seen := map[int]bool{}
	for _, it := range p.Items {
		if seen[it.ObjectID] {
			continue
		}
		if o := p.findObject(it.ObjectID); o != nil {
			seen[it.ObjectID] = true
			out = append(out, o)
		}
	}
	return out
}

// loadIndexed reads the per object members: the variable layer height profile,
// the brim ear points and the cut information. Entries naming an object that
// does not exist are dropped.
func (p *Project) loadIndexed() {
	built := p.builtObjects()
	at := func(id int) *Object {
		if id < 1 || id > len(built) {
			return nil
		}
		return built[id-1]
	}
	if data, err := p.Read(memberLayerHeights); err == nil {
		for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
			head, vals, ok := strings.Cut(line, "|")
			_, idText, ok2 := strings.Cut(head, "=")
			id, err := strconv.Atoi(strings.TrimSpace(idText))
			if o := at(id); ok && ok2 && err == nil && o != nil {
				o.LayerHeightProfile = strings.TrimSpace(vals)
			}
		}
		p.hasIndexed = true
	}
	if data, err := p.Read(memberBrimEars); err == nil {
		lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
		if len(lines) > 0 && strings.HasPrefix(lines[0], "brim_points_format_version=") {
			p.brimVersion = strings.TrimPrefix(lines[0], "brim_points_format_version=")
			lines = lines[1:]
		}
		for _, line := range lines {
			head, vals, ok := strings.Cut(line, "|")
			_, idText, ok2 := strings.Cut(head, "=")
			id, err := strconv.Atoi(strings.TrimSpace(idText))
			if o := at(id); ok && ok2 && err == nil && o != nil {
				o.BrimPoints = strings.TrimSpace(vals)
			}
		}
		p.hasIndexed = true
	}
	if data, err := p.Read(memberCutInfo); err == nil {
		if root, err := parseTree(data); err == nil {
			for _, on := range root.Children {
				if on.Name != "object" {
					continue
				}
				id, _ := strconv.Atoi(on.attrValue("id"))
				if o := at(id); o != nil {
					o.CutInfo = on
				}
			}
		}
		p.hasIndexed = true
	}
}

// saveIndexed writes the three members again with the numbers the objects have
// now: the entries of removed objects are gone and the others follow their
// object. A member with no entry left is removed.
func (p *Project) saveIndexed() {
	built := p.builtObjects()
	var heights, brims, cuts strings.Builder
	cutAny := false
	for i, o := range built {
		n := strconv.Itoa(i + 1)
		if o.LayerHeightProfile != "" {
			heights.WriteString("object_id=" + n + "|" + o.LayerHeightProfile + "\n")
		}
		if o.BrimPoints != "" {
			brims.WriteString("object_id=" + n + "|" + o.BrimPoints + "\n")
		}
		if o.CutInfo != nil {
			if !cutAny {
				cuts.WriteString("<?xml version=\"1.0\" encoding=\"utf-8\"?>\n<objects>\n")
				cutAny = true
			}
			c := *o.CutInfo
			c.Attrs = append(KVs(nil), o.CutInfo.Attrs...)
			c.Attrs.Set("id", n)
			c.write(&cuts, " ")
		}
	}
	set := func(name, text string) {
		if text == "" {
			if p.byName[name] != nil {
				p.removeMember(name)
			}
			return
		}
		p.setMember(name, []byte(text))
	}
	set(memberLayerHeights, heights.String())
	if brims.Len() > 0 {
		v := p.brimVersion
		if v == "" {
			v = "1"
		}
		set(memberBrimEars, "brim_points_format_version="+v+"\n"+brims.String())
	} else {
		set(memberBrimEars, "")
	}
	if cutAny {
		cuts.WriteString("</objects>\n")
	}
	set(memberCutInfo, cuts.String())
}

// SetLayerHeightProfile stores a variable layer height profile for an object
// (the values joined by ";", as the member holds them); an empty text removes it.
// The tools only read profiles that came with a file; this is for tests and for
// carrying a profile over.
func (p *Project) SetLayerHeightProfile(objectID int, profile string) error {
	o := p.findObject(objectID)
	if o == nil {
		return fmt.Errorf("%w: object %d", ErrNotFound, objectID)
	}
	o.LayerHeightProfile = strings.TrimSpace(profile)
	p.hasIndexed = true
	p.modelDirty = true
	p.resetPlatesOf(o.ID)
	return nil
}
