package projects

import (
	"strings"

	"github.com/sairaph/creality-slicer-mcp/internal/mesh"
	"github.com/sairaph/creality-slicer-mcp/internal/render"
	"github.com/sairaph/creality-slicer-mcp/internal/threemf"
)

// scene builds the render scene of a plate: the instances on it in their
// filament colours (pick images encode the identify id), the printable area
// and the wipe tower footprint.
func (h *handle) scene(plate int) (*render.Scene, error) {
	s, _, err := h.sceneWith(plate, sceneExtras{})
	return s, err
}

// sceneExtras ask scene for more than the thumbnails need: the parts and the
// height ranges of the objects and a label for each. ids of the result are the
// object ids of Scene.Objects, one per entry.
type sceneExtras struct {
	parts, ranges, labels bool
	// keep, when set, decides which objects are drawn.
	keep func(objectID int) bool
}

func (h *handle) sceneWith(plate int, x sceneExtras) (*render.Scene, []int, error) {
	var ids []int
	pl := h.p.Plate(plate)
	if pl == nil {
		return nil, nil, notFoundf("call get_project to see the plates", "project %s has no plate %d", h.id, plate)
	}
	geo := h.geometry()
	s := &render.Scene{Bed: geo.Bed, MaxHeight: geo.MaxHeight, Plate: plate}
	var colours []string
	if h.p.Settings != nil {
		colours = h.p.Settings.List("filament_colour")
	}
	for _, in := range pl.Instances {
		o := h.p.Object(in.ObjectID)
		if o == nil || (x.keep != nil && !x.keep(o.ID)) {
			continue
		}
		items := h.p.ItemsOf(o.ID)
		if in.InstanceID < 0 || in.InstanceID >= len(items) {
			continue
		}
		m, err := h.objectMesh(o)
		if err != nil {
			continue // an object that cannot be read is left out of the picture
		}
		obj := render.Object{Mesh: m, Transform: h.itemT(o.ID, in.InstanceID), Name: o.Name, InstanceID: in.IdentifyID}
		if x.labels {
			obj.Label = viewLabel(o.ID, o.Name)
		}
		if x.parts {
			obj.Parts = h.sceneParts(o, obj.Transform)
		}
		if x.ranges {
			for _, lr := range o.LayerRanges {
				obj.Ranges = append(obj.Ranges, render.Range{From: lr.MinZ, To: lr.MaxZ})
			}
		}
		slot := o.Extruder()
		if slot < 1 {
			slot = 1
		}
		if slot <= len(colours) {
			obj.Colour = filamentColour(colours[slot-1])
		}
		s.Objects = append(s.Objects, obj)
		ids = append(ids, o.ID)
	}
	if t, ok := h.towerRect(plate); ok {
		s.WipeTower = &render.Rect{X0: t.x0, Y0: t.y0, X1: t.x1, Y1: t.y1}
	}
	return s, ids, nil
}

// sceneParts loads the modifier, negative part, support enforcer and support
// blocker volumes of an object and places them with the object's instance
// transform (the part's own volume transform first). A part that cannot be
// read is left out.
func (h *handle) sceneParts(o *threemf.Object, item mesh.Matrix) []render.Part {
	var out []render.Part
	for _, part := range o.Parts {
		var kind render.PartKind
		switch part.Subtype {
		case threemf.SubtypeModifier:
			kind = render.PartModifier
		case threemf.SubtypeNegative:
			kind = render.PartNegative
		case threemf.SubtypeSupportEnforcer:
			kind = render.PartEnforcer
		case threemf.SubtypeSupportBlocker:
			kind = render.PartBlocker
		default:
			continue
		}
		m, err := h.p.LoadMesh(part)
		if err != nil || m == nil {
			continue
		}
		out = append(out, render.Part{Mesh: m, Transform: part.ComponentTransform.Then(item), Kind: kind, Name: part.Name})
	}
	return out
}

// renderThumbnails renders the five thumbnails of every plate that changed
// and stores them in the project (also deleting Metadata/plate_N.json).
func (h *handle) renderThumbnails() error {
	var plates []int
	for _, pl := range h.p.Plates {
		if h.allPlates || h.plates[pl.Index] {
			plates = append(plates, pl.Index)
		}
	}
	for _, idx := range plates {
		s, err := h.scene(idx)
		if err != nil {
			return err
		}
		images, err := render.PlateImages(s)
		if err != nil {
			return errf(CodeInternal, "", "rendering the thumbnails of plate %d failed: %v", idx, err)
		}
		set := map[threemf.ThumbnailKind][]byte{}
		for name, data := range images {
			switch {
			case strings.HasPrefix(name, "plate_no_light_"):
				set[threemf.ThumbNoLight] = data
			case strings.HasSuffix(name, "_small.png"):
				set[threemf.ThumbSmall] = data
			case strings.HasPrefix(name, "plate_"):
				set[threemf.ThumbPlate] = data
			case strings.HasPrefix(name, "top_"):
				set[threemf.ThumbTop] = data
			case strings.HasPrefix(name, "pick_"):
				set[threemf.ThumbPick] = data
			}
		}
		if err := h.p.SetPlateThumbnails(idx, set); err != nil {
			return errf(CodeInternal, "", "storing the thumbnails of plate %d failed: %v", idx, err)
		}
	}
	return nil
}

// PreviewKind names the picture Preview returns.
type PreviewKind string

// Preview kinds.
const (
	PreviewNone       PreviewKind = "none"
	PreviewPlate      PreviewKind = "plate"       // 384 px isometric picture of the plate
	PreviewPlateLarge PreviewKind = "plate_large" // 1024 px
	PreviewTop        PreviewKind = "top"         // top view, 384 px
	PreviewTopLarge   PreviewKind = "top_large"   // top view, 1024 px
)

// Preview renders a picture of a plate of the project.
func (s *Store) Preview(ref string, kind PreviewKind, plate int) ([]byte, error) {
	if kind == "" || kind == PreviewNone {
		return nil, nil
	}
	if plate == 0 {
		plate = 1
	}
	var data []byte
	err := s.read(ref, func(h *handle) error {
		sc, err := h.scene(plate)
		if err != nil {
			return err
		}
		view, size := render.ViewIso, 384
		switch kind {
		case PreviewPlate:
		case PreviewPlateLarge:
			size = 1024
		case PreviewTop:
			view = render.ViewTop
		case PreviewTopLarge:
			view, size = render.ViewTop, 1024
		default:
			return invalidf("use plate, plate_large, top or top_large", "unknown preview %q", kind)
		}
		data, err = render.Preview(sc, view, size)
		if err != nil {
			return errf(CodeInternal, "", "rendering the preview failed: %v", err)
		}
		return nil
	})
	return data, err
}

// StoredThumbnail returns the image the project itself carries for a plate
// (for open_project with preview), or ErrNotFound.
func (s *Store) StoredThumbnail(ref string, plate int) ([]byte, error) {
	if plate == 0 {
		plate = 1
	}
	var data []byte
	err := s.read(ref, func(h *handle) error {
		var err error
		data, err = h.p.Thumbnail(plate, threemf.ThumbPlate)
		if err != nil {
			return notFoundf("slice_project the plate first, then get_view or open_in_app", "plate %d has no stored picture", plate)
		}
		return nil
	})
	return data, err
}
