package projects

import (
	"math"

	"github.com/sairaph/creality-slicer-mcp/internal/mesh"
	"github.com/sairaph/creality-slicer-mcp/internal/threemf"
)

// The build item transforms of a project are in one scene in which every plate
// has an origin (threemf.PlateOrigin). Everything the tools show and accept is
// plate relative, so every read of an item transform subtracts the origin of
// its plate and every write adds it back.

// plateSize is the width and depth of the plate in whole millimetres, the size
// the application lays the plates out with.
func (h *handle) plateSize() (w, d float64) {
	g := h.geometry()
	return math.Round(g.Bed.X1 - g.Bed.X0), math.Round(g.Bed.Y1 - g.Bed.Y0)
}

// origin is the scene position of a plate (1 based) with the project's current
// number of plates.
func (h *handle) origin(plate int) [2]float64 {
	w, d := h.plateSize()
	return threemf.PlateOrigin(plate, len(h.p.Plates), w, d)
}

// itemT is the plate relative transform of an instance.
func (h *handle) itemT(objectID, instanceID int) mesh.Matrix {
	items := h.p.ItemsOf(objectID)
	if instanceID < 0 || instanceID >= len(items) {
		return mesh.Identity() // callers check the instance first; never index out of range
	}
	t := items[instanceID].Transform
	if pl := h.p.PlateOf(objectID, instanceID); pl != nil {
		o := h.origin(pl.Index)
		t[9] -= o[0]
		t[10] -= o[1]
	}
	return t
}

// absolute converts a plate relative transform to scene coordinates.
func (h *handle) absolute(rel mesh.Matrix, plate int) mesh.Matrix {
	o := h.origin(plate)
	rel[9] += o[0]
	rel[10] += o[1]
	return rel
}

// setItemT writes a plate relative transform for an instance, using the plate
// it is on now.
func (h *handle) setItemT(objectID, instanceID int, rel mesh.Matrix) error {
	plate := 1
	if pl := h.p.PlateOf(objectID, instanceID); pl != nil {
		plate = pl.Index
	}
	return h.p.SetTransform(objectID, instanceID, h.absolute(rel, plate))
}

type instanceKey struct{ object, instance int }

// relativeAll remembers the plate relative transform of every instance; with
// applyRelative it carries the objects along when the number of plates changes
// the layout of the scene (three plates sit in two columns, five in three).
func (h *handle) relativeAll() map[instanceKey]mesh.Matrix {
	out := map[instanceKey]mesh.Matrix{}
	for _, pl := range h.p.Plates {
		for _, in := range pl.Instances {
			if items := h.p.ItemsOf(in.ObjectID); in.InstanceID >= 0 && in.InstanceID < len(items) {
				out[instanceKey{in.ObjectID, in.InstanceID}] = h.itemT(in.ObjectID, in.InstanceID)
			}
		}
	}
	return out
}

func (h *handle) applyRelative(rel map[instanceKey]mesh.Matrix) error {
	for k, t := range rel {
		items := h.p.ItemsOf(k.object)
		if k.instance < 0 || k.instance >= len(items) {
			continue
		}
		cur := items[k.instance].Transform
		plate := 1
		if pl := h.p.PlateOf(k.object, k.instance); pl != nil {
			plate = pl.Index
		}
		if abs := h.absolute(t, plate); abs != cur {
			if err := h.p.SetTransform(k.object, k.instance, abs); err != nil {
				return errf(CodeInternal, "", "%v", err)
			}
		}
	}
	return nil
}
