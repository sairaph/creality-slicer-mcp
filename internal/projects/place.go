package projects

import (
	"math"
	"sort"

	"github.com/sairaph/creality-slicer-mcp/internal/mesh"
)

// placed is one instance the automatic placement works on.
type placed struct {
	objectID, instanceID int
	name                 string
	transform            mesh.Matrix
	m                    *mesh.Mesh
}

// planePlacements lists the instances of a plate that have geometry.
func (h *handle) plateInstances(plate int) []placed {
	pl := h.p.Plate(plate)
	if pl == nil {
		return nil
	}
	var out []placed
	for _, in := range pl.Instances {
		o := h.p.Object(in.ObjectID)
		items := h.p.ItemsOf(in.ObjectID)
		if o == nil || in.InstanceID < 0 || in.InstanceID >= len(items) {
			continue
		}
		m, err := h.objectMesh(o)
		if err != nil {
			continue
		}
		out = append(out, placed{in.ObjectID, in.InstanceID, o.Name, h.itemT(in.ObjectID, in.InstanceID), m})
	}
	return out
}

// autoPlace is the tools' own "orient" and "arrange", applied to the project
// (the slicer's placement is never used, so the result is the same whatever
// the installed version does). Orient lays every instance on its largest flat
// face; arrange packs the instances of the plate with the same rules as
// add_model (gap, margin, wipe tower kept free), largest first. Both keep the
// scale and write the new transforms into the project.
func (h *handle) autoPlace(plate int, orient, arrange bool) error {
	insts := h.plateInstances(plate)
	if orient {
		for i, p := range insts {
			scale, rot := decompose(p.transform)
			L := linearFor(scale, rot)
			flat, ok := mesh.LayFlat(p.m.Transformed(L))
			if !ok {
				continue
			}
			L = L.Then(flat)
			oldB, _ := bboxOf(p.m, p.transform)
			newB, ok := bboxOf(p.m, L)
			if !ok {
				continue
			}
			oc, nc := center3(oldB), center3(newB)
			T := L
			T[9], T[10], T[11] = oc[0]-nc[0], oc[1]-nc[1], -float64(newB.Min[2])
			if T != p.transform {
				if err := h.setItemT(p.objectID, p.instanceID, T); err != nil {
					return errf(CodeInternal, "", "%v", err)
				}
				insts[i].transform = T
			}
		}
	}
	if arrange {
		type item struct {
			p       placed
			w, d, h float64
		}
		items := make([]item, 0, len(insts))
		for _, p := range insts {
			b, ok := bboxOf(p.m, p.transform)
			if !ok {
				continue
			}
			s := size3(b)
			items = append(items, item{p, s[0], s[1], s[2]})
		}
		sort.SliceStable(items, func(i, j int) bool { return items[i].w*items[i].d > items[j].w*items[j].d })
		var taken []rect
		var takenH []float64
		for _, it := range items {
			h.placeHeight = it.h
			h.takenHeights = takenH
			x, y, ok := h.findSpot(plate, it.w, it.d, taken)
			if !ok {
				return conflictf("remove or shrink objects, move some to another plate, or place them yourself with update_object",
					"cannot arrange plate %d: %q (%.1f x %.1f mm) does not fit with a %g mm gap between objects and a %g mm margin to the bed edge",
					plate, it.p.name, it.w, it.d, PlacementGap, PlacementMargin)
			}
			b, _ := bboxOf(it.p.m, it.p.transform)
			c := center3(b)
			T := it.p.transform
			T[9] += x - c[0]
			T[10] += y - c[1]
			if math.Abs(x-c[0]) > 1e-9 || math.Abs(y-c[1]) > 1e-9 {
				if err := h.setItemT(it.p.objectID, it.p.instanceID, T); err != nil {
					return errf(CodeInternal, "", "%v", err)
				}
			}
			taken = append(taken, rect{x - it.w/2, y - it.d/2, x + it.w/2, y + it.d/2})
			takenH = append(takenH, it.h)
		}
	}
	h.touchPlate(plate)
	return nil
}

// AutoPlace applies orient and/or arrange to a plate (0 = every plate with
// objects) as a project change, for slice_project and for direct use.
func (s *Store) AutoPlace(ref string, plate int, orient, arrange bool) (*Info, error) {
	if !orient && !arrange {
		return nil, invalidf("choose orient, arrange or both", "nothing to do")
	}
	err := s.write(ref, func(h *handle) error { _, err := h.autoPlaceScope(plate, orient, arrange); return err })
	if err != nil {
		return nil, err
	}
	return s.info(ref)
}

// ObjectMove is one object the tools' own placement moved: where it was and
// where it is now, as positions on its plate (the centre of its bounding box),
// and whether orient turned it.
type ObjectMove struct {
	ObjectID int
	Name     string
	Plate    int
	From, To [2]float64
	Rotated  bool
}

func (h *handle) autoPlaceScope(plate int, orient, arrange bool) ([]ObjectMove, error) {
	if plate != 0 && h.p.Plate(plate) == nil {
		return nil, notFoundf("call get_project to see the plates", "project %s has no plate %d", h.id, plate)
	}
	var moves []ObjectMove
	for _, pl := range h.p.Plates {
		if (plate != 0 && pl.Index != plate) || len(pl.Instances) == 0 {
			continue
		}
		before := h.plateInstances(pl.Index)
		if err := h.autoPlace(pl.Index, orient, arrange); err != nil {
			return nil, err
		}
		for _, b := range before {
			ob, ok1 := bboxOf(b.m, b.transform)
			now := h.itemT(b.objectID, b.instanceID)
			nb, ok2 := bboxOf(b.m, now)
			if !ok1 || !ok2 {
				continue
			}
			oc, nc := center3(ob), center3(nb)
			mv := ObjectMove{ObjectID: b.objectID, Name: b.name, Plate: pl.Index, From: [2]float64{round6(oc[0]), round6(oc[1])}, To: [2]float64{round6(nc[0]), round6(nc[1])}}
			for i := 0; i < 9; i++ {
				if math.Abs(b.transform[i]-now[i]) > 1e-9 {
					mv.Rotated = true
				}
			}
			if math.Abs(mv.From[0]-mv.To[0]) > 0.05 || math.Abs(mv.From[1]-mv.To[1]) > 0.05 || mv.Rotated {
				moves = append(moves, mv)
			}
		}
	}
	return moves, nil
}
