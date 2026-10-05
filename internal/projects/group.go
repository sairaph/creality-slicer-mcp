package projects

import (
	"fmt"
	"sort"
	"strings"

	"github.com/sairaph/creality-slicer-mcp/internal/catalog"
	"github.com/sairaph/creality-slicer-mcp/internal/threemf"
)

// GroupRequest merges objects into the first one.
type GroupRequest struct {
	// Objects are ids or names; the first is the base and keeps its id.
	Objects []string
	// Name renames the base object; empty keeps its name.
	Name string
}

// GroupedPart is a part of the grouped object as the reply lists it.
type GroupedPart struct {
	Name     string
	Subtype  string
	Filament int // the filament the part prints with (its own, else the object's)
}

// GroupResult reports what GroupObjects did.
type GroupResult struct {
	Info     *Info
	Object   ObjectInfo
	Parts    []GroupedPart
	Warnings []string
}

// GroupObjects turns every object after the first into parts of the first. Each
// part keeps its place in the world and its mesh, is named after its old object
// and prints with the filament its old object used (written as the part's
// extruder). Settings of a merged object that are valid on a part move to its
// parts; the others, and its height ranges, are dropped with a warning.
func (s *Store) GroupObjects(ref string, req GroupRequest) (*GroupResult, error) {
	res := &GroupResult{}
	var baseID int
	err := s.write(ref, func(h *handle) error { return h.groupObjects(req, res, &baseID) })
	if err != nil {
		return nil, err
	}
	res.Info, err = s.info(ref)
	if err != nil {
		return nil, err
	}
	for _, o := range res.Info.Objects {
		if o.ID != baseID {
			continue
		}
		res.Object = o
		for _, p := range o.Parts {
			fil := p.Filament
			if fil == 0 {
				fil = o.Filament
			}
			res.Parts = append(res.Parts, GroupedPart{Name: p.Name, Subtype: p.Subtype, Filament: fil})
		}
	}
	return res, nil
}

// groupObjects is GroupObjects on an open project.
func (h *handle) groupObjects(req GroupRequest, res *GroupResult, baseOut *int) error {
	if len(req.Objects) < 2 {
		return invalidf("give at least two objects: the first is the base that keeps its id", "grouping needs at least 2 objects, got %d", len(req.Objects))
	}
	var objs []*threemf.Object
	seen := map[int]bool{}
	plate := 0
	for _, r := range req.Objects {
		o, err := h.objectByRef(r)
		if err != nil {
			return err
		}
		if seen[o.ID] {
			return invalidf("list every object once", "object %q (id %d) is listed twice", o.Name, o.ID)
		}
		seen[o.ID] = true
		if n := len(h.p.ItemsOf(o.ID)); n != 1 {
			return invalidf("grouping works on objects with one instance", "object %q has %d instances", o.Name, n)
		}
		pl := h.p.PlateOf(o.ID, 0)
		if pl == nil {
			return invalidf("", "object %q is on no plate", o.Name)
		}
		if plate == 0 {
			plate = pl.Index
		} else if pl.Index != plate {
			return invalidf("move the objects to one plate first (update_object plate)", "objects %q and %q are on different plates (%d and %d)", objs[0].Name, o.Name, plate, pl.Index)
		}
		objs = append(objs, o)
	}
	base := objs[0]
	*baseOut = base.ID
	baseInv, ok := h.p.ItemsOf(base.ID)[0].Transform.Inverse()
	if !ok {
		return invalidf("", "the transform of object %q cannot be inverted", base.Name)
	}
	cat := h.s.cfg.Catalog
	type move struct {
		spec threemf.PartSpec
	}
	var moves []move
	var warnings []string
	for _, o := range objs[1:] {
		item := h.p.ItemsOf(o.ID)[0].Transform
		objExt := max(o.Extruder(), 1)
		// Object overrides: valid at part scope they go to the parts.
		var carry threemf.KVs
		var dropped []string
		for _, kv := range o.Config {
			if kv.Key == "extruder" {
				continue
			}
			if opt, ok := cat.Get(kv.Key); ok && opt.HasScope(catalog.ScopePart) {
				carry.Set(kv.Key, kv.Value)
			} else {
				dropped = append(dropped, kv.Key)
			}
		}
		if len(dropped) > 0 {
			sort.Strings(dropped)
			warnings = append(warnings, fmt.Sprintf("object %q: the settings %s cannot be set on a part and were dropped", o.Name, strings.Join(dropped, ", ")))
		}
		if len(o.LayerRanges) > 0 {
			warnings = append(warnings, fmt.Sprintf("object %q: its %d height range(s) were dropped (ranges belong to the base object)", o.Name, len(o.LayerRanges)))
		}
		if o.Painted().Any() {
			warnings = append(warnings, fmt.Sprintf("object %q: painted data is not carried into the group; paint it again in the app", o.Name))
		}
		for _, pt := range o.Parts {
			m, err := h.p.LoadMesh(pt)
			if err != nil {
				return invalidf("", "part %q of object %q cannot be read: %v", pt.Name, o.Name, err)
			}
			// A mirrored item (negative determinant) needs no winding change here: the
			// app stores the matrix beside the unchanged mesh ("as we stored matrix
			// separately ... we don't need to consider this left hand case",
			// bbs_3mf.cpp:8458) and readers flip the triangles when they apply it.
			tr := pt.ComponentTransform.Then(item).Then(baseInv)
			name := o.Name
			if len(o.Parts) > 1 && strings.TrimSpace(pt.Name) != "" {
				name = o.Name + " " + pt.Name
			}
			var cfg threemf.KVs
			for _, kv := range carry {
				cfg.Set(kv.Key, kv.Value)
			}
			for _, kv := range pt.Config { // the part's own settings win
				cfg.Set(kv.Key, kv.Value)
			}
			if pt.Subtype == threemf.SubtypeNormal && cfg.Value("extruder") == "" {
				cfg.Set("extruder", fmt.Sprint(objExt))
			}
			moves = append(moves, move{threemf.PartSpec{Subtype: pt.Subtype, Name: name, Mesh: m, Transform: &tr, Config: cfg}})
		}
	}
	for _, mv := range moves {
		spec := mv.spec
		if _, err := h.p.AddPart(base.ID, spec); err != nil {
			return invalidf("", "%v", err)
		}
	}
	for _, o := range objs[1:] {
		if err := h.p.RemoveObject(o.ID); err != nil {
			return errf(CodeInternal, "", "%v", err)
		}
	}
	if strings.TrimSpace(req.Name) != "" {
		if err := h.p.RenameObject(base.ID, strings.TrimSpace(req.Name)); err != nil {
			return threemfError(err)
		}
	}
	h.meshes = nil
	h.touchPlate(plate)
	res.Warnings = warnings
	return nil
}
