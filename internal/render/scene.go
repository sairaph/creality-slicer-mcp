// Package render draws what a slicing agent needs to look at: a plate of
// models (previews and the five project thumbnails), one layer of a G-code
// file, and it embeds thumbnails into G-code. It is a software rasteriser in
// pure Go: no cgo, no GPU, no font files. Every image is a PNG.
package render

import (
	"image/color"

	"github.com/sairaph/creality-slicer-mcp/internal/mesh"
)

// Rect is a rectangle on the bed in mm: X0,Y0 is the corner nearest the
// origin, X1,Y1 the far one.
type Rect struct {
	X0, Y0, X1, Y1 float64
}

// Empty reports whether the rectangle has no area (the zero value).
func (r Rect) Empty() bool { return r.X1 <= r.X0 || r.Y1 <= r.Y0 }

func (r Rect) width() float64  { return r.X1 - r.X0 }
func (r Rect) height() float64 { return r.Y1 - r.Y0 }

// Object is one model instance on the plate.
type Object struct {
	Mesh *mesh.Mesh
	// Transform places the mesh on the plate (mm), in the 3MF order.
	Transform mesh.Matrix
	// Colour is the filament colour of the object. A zero value (alpha 0) draws
	// as a neutral grey.
	Colour color.NRGBA
	// Name is the object's label; it is not drawn, but tests and tools use it
	// to tell objects apart.
	Name string
	// InstanceID is the instance's identify_id (as in model_settings.config).
	// The pick image encodes it little endian in the colour: R = id & 0xFF,
	// G = (id >> 8) & 0xFF, B = (id >> 16) & 0xFF, alpha 255.
	InstanceID int
	// Label is the text RenderView draws at the object's top centre (an id and
	// a short name); empty draws none. Thumbnails and Preview ignore it.
	Label string
	// Parts are the modifier, negative part, support enforcer and support
	// blocker volumes of the object, in plate coordinates. Only RenderView
	// draws them.
	Parts []Part
	// Ranges are the height ranges of the object in mm above the bed. Only
	// RenderView draws them.
	Ranges []Range
}

// PartKind is what a part of an object does.
type PartKind string

// The part kinds.
const (
	PartModifier PartKind = "modifier"
	PartNegative PartKind = "negative_part"
	PartEnforcer PartKind = "support_enforcer"
	PartBlocker  PartKind = "support_blocker"
)

// Part is a volume attached to an object.
type Part struct {
	Mesh *mesh.Mesh
	// Transform places the mesh on the plate (mm), like Object.Transform.
	Transform mesh.Matrix
	Kind      PartKind
	Name      string
}

// Range is a band of an object between two heights, in mm above the bed.
type Range struct {
	From, To float64
}

// Scene is one plate.
type Scene struct {
	// Bed is the bed outline, from the printer's bed shape (K2: 0,0 to
	// 260,260). Empty means the default 260 by 260 mm.
	Bed Rect
	// Printable is the printable area; an object outside it is tinted red in
	// previews. Empty means the bed.
	Printable Rect
	// MaxHeight is the printable height in mm; 0 means it is not checked.
	MaxHeight float64
	Objects   []Object
	// WipeTower is the footprint of the wipe tower on the bed; nil means none.
	WipeTower *Rect
	// Plate is the plate number (1-based) used in the thumbnail file names;
	// 0 means 1.
	Plate int
}

// DefaultBed is the bed used when a scene gives none: the K2 family's 260 mm
// square.
var DefaultBed = Rect{0, 0, 260, 260}

// DefaultWipeTowerDepth is the depth in mm of a wipe tower whose depth is not
// known, for callers that build the WipeTower rectangle from its position and
// width.
const DefaultWipeTowerDepth = 30.0

func (s *Scene) bed() Rect {
	if s.Bed.Empty() {
		return DefaultBed
	}
	return s.Bed
}

func (s *Scene) printable() Rect {
	if s.Printable.Empty() {
		return s.bed()
	}
	return s.Printable
}

func (s *Scene) plate() int {
	if s.Plate <= 0 {
		return 1
	}
	return s.Plate
}

// bounds returns the world bounding box of the object's mesh after its
// transform, and false for an empty mesh.
func (o *Object) bounds() (mesh.BBox, bool) {
	if o.Mesh == nil || len(o.Mesh.Vertices) == 0 {
		return mesh.BBox{}, false
	}
	first := true
	var b mesh.BBox
	for _, v := range o.Mesh.Vertices {
		p := o.Transform.ApplyF32(v)
		if first {
			b = mesh.BBox{Min: p, Max: p}
			first = false
			continue
		}
		for i := 0; i < 3; i++ {
			if p[i] < b.Min[i] {
				b.Min[i] = p[i]
			}
			if p[i] > b.Max[i] {
				b.Max[i] = p[i]
			}
		}
	}
	return b, true
}

// Outside reports whether the object leaves the printable area (its bounding
// box crosses the area's edge, or exceeds MaxHeight when that is set).
func (s *Scene) Outside(o Object) bool {
	b, ok := o.bounds()
	if !ok {
		return false
	}
	p := s.printable()
	const eps = 1e-3
	if float64(b.Min[0]) < p.X0-eps || float64(b.Max[0]) > p.X1+eps ||
		float64(b.Min[1]) < p.Y0-eps || float64(b.Max[1]) > p.Y1+eps {
		return true
	}
	return s.MaxHeight > 0 && float64(b.Max[2]) > s.MaxHeight+eps
}
