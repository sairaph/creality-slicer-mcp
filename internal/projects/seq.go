package projects

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Printing by object (one object after another) has clearance rules the
// slicer checks and fails with OBJECT_COLLISION_IN_SEQ_PRINT (-63), taken from
// Print.cpp sequential_print_clearance_valid:
//
//   - Every object's outline grows by d, and two grown outlines must not touch.
//     d is half the printer's extruder_clearance_radius plus the skirt offset,
//     minus 0.1 mm; when every object is lower than the nozzle_height of the
//     printer, d is only max(2 mm, skirt offset) minus 0.1 mm.
//   - The objects print in the order of the object list. An object printed
//     before another must be lower than extruder_clearance_height_to_lid (or
//     extruder_clearance_height_to_rod when a later object overlaps it along
//     Y), the last one lower than the printable height.
//
// The tools work with the bounding box of an object (a little more careful than
// the slicer's convex outline).

// seqIssue is one violated clearance rule.
type seqIssue struct {
	Kind     string // "close" or "tall"
	A, B     string // object names ("tall": A)
	Distance float64
	Need     float64
	Height   float64
	Limit    float64
}

func (i seqIssue) text() string {
	if i.Kind == "exclusion" {
		return fmt.Sprintf("%q is too close to the exclusion area of the bed", i.A)
	}
	if i.Kind == "tall" {
		return fmt.Sprintf("%q is %.0f mm tall but only %.0f mm is allowed while objects printed after it are in reach of the toolhead rod or lid", i.A, i.Height, i.Limit)
	}
	return fmt.Sprintf("%q and %q are %.0f mm apart, printing one after another needs %.0f mm", i.A, i.B, i.Distance, i.Need)
}

// plateSequence is the print sequence of a plate: its own setting, else the project's.
func (h *handle) plateSequence(plate int) string {
	if pl := h.p.Plate(plate); pl != nil {
		if v := pl.Config.Value("print_sequence"); v != "" {
			return v
		}
	}
	if h.p.Settings != nil {
		return h.p.Settings.String("print_sequence")
	}
	return ""
}

func cfgFloat(h *handle, key string, def float64) float64 {
	if h.p.Settings == nil {
		return def
	}
	if v, err := strconv.ParseFloat(strings.TrimSuffix(h.p.Settings.String(key), "%"), 64); err == nil {
		return v
	}
	return def
}

type seqBox struct {
	name string
	r    rect
	z    float64 // height
}

func (h *handle) seqBoxes(plate int) []seqBox {
	var out []seqBox
	for _, p := range h.plateInstances(plate) {
		b, ok := bboxOf(p.m, p.transform)
		if !ok {
			continue
		}
		out = append(out, seqBox{name: p.name, r: footprint(b), z: float64(b.Max[2])})
	}
	return out
}

// seqHalf is d of the rule above: how far each object's outline grows.
func (h *handle) seqHalf(boxes []seqBox) float64 {
	radius := cfgFloat(h, "extruder_clearance_radius", 60)
	nozzleH := cfgFloat(h, "nozzle_height", 0)
	if h.placeHeight > 0 { // the object being placed counts for the "all short" rule
		boxes = append(append([]seqBox(nil), boxes...), seqBox{z: h.placeHeight})
	}
	short := len(boxes) > 0 && nozzleH > 0
	for _, b := range boxes {
		if b.z >= nozzleH {
			short = false
		}
	}
	// The skirt only matters when one is printed around every object
	// (skirt_type perobject with at least one loop; Print::object_skirt_offset).
	skirt := 0.0
	loops := cfgFloat(h, "skirt_loops", 0)
	if loops > 0 && h.p.Settings != nil && h.p.Settings.String("skirt_type") == "perobject" {
		dist := cfgFloat(h, "skirt_distance", 0)
		lineW := 0.45
		width := lineW + (loops-1)*lineW*0.9 // one line plus the spacing of the further loops
		maxLayer := cfgFloat(h, "max_layer_height", 0.32)
		switch {
		case short:
			skirt = dist + width
		case h.p.Settings.String("draft_shield") == "enabled" || cfgFloat(h, "skirt_height", 1)*maxLayer > nozzleH:
			skirt = dist + lineW
		case dist+width > radius/2:
			skirt = dist + width - radius/2
		}
	}
	if short {
		return math.Max(2, skirt) - 0.1
	}
	return 0.5*radius + skirt - 0.1
}

// seqGap is the distance between two objects of a plate printed by object that
// the tools keep (twice d, and a millimetre more), 0 for a plate printed by layer.
func (h *handle) seqGap(plate int) float64 {
	if h.plateSequence(plate) != "by object" {
		return 0
	}
	return 2*h.seqHalf(h.seqBoxes(plate)) + 1
}

func rectDistance(a, b rect) float64 {
	dx := math.Max(0, math.Max(a.x0-b.x1, b.x0-a.x1))
	dy := math.Max(0, math.Max(a.y0-b.y1, b.y0-a.y1))
	return math.Hypot(dx, dy)
}

// seqIssues checks a plate printed by object against the clearance rules.
func (h *handle) seqIssues(plate int) []seqIssue {
	if h.plateSequence(plate) != "by object" {
		return nil
	}
	boxes := h.seqBoxes(plate)
	d := h.seqHalf(boxes)
	need := 2 * d
	var out []seqIssue
	for i := range boxes {
		for j := i + 1; j < len(boxes); j++ {
			if dist := rectDistance(boxes[i].r, boxes[j].r); dist < need {
				out = append(out, seqIssue{Kind: "close", A: boxes[i].name, B: boxes[j].name, Distance: dist, Need: need})
			}
		}
	}
	for _, ex := range h.excludeRects() {
		for _, b := range boxes {
			if b.r.overlaps(ex) {
				out = append(out, seqIssue{Kind: "exclusion", A: b.name})
			}
		}
	}
	lid := cfgFloat(h, "extruder_clearance_height_to_lid", 1e9)
	rod := cfgFloat(h, "extruder_clearance_height_to_rod", 1e9)
	radius := cfgFloat(h, "extruder_clearance_radius", 60)
	printable := cfgFloat(h, "printable_height", 1e9)
	for i, b := range boxes {
		limit := printable
		if i < len(boxes)-1 {
			limit = lid
			grownSelf := b.r.inflate(d) // the slicer works with the grown outline, then takes half the radius off
			shrunk := rect{grownSelf.x0, grownSelf.y0 + radius/2, grownSelf.x1, grownSelf.y1 - radius/2}
			for _, later := range boxes[i+1:] {
				grown := later.r.inflate(d)
				if shrunk.y1 > shrunk.y0 && shrunk.y0 < grown.y1 && grown.y0 < shrunk.y1 { // an outline shallower than the radius shrinks to nothing (as in the slicer)
					limit = rod
					break
				}
			}
		}
		if b.z > limit+1e-6 {
			out = append(out, seqIssue{Kind: "tall", A: b.name, Height: b.z, Limit: limit})
		}
	}
	return out
}

// seqHintText is the hint of a -63 failure: what is needed, which objects, and
// what to do. It never suggests printing by layer on 7.2 with several filaments
// (that crashes there).
func (h *handle) seqHintText(plates []int, dialect string) string {
	var parts []string
	var radius, need float64
	radius = cfgFloat(h, "extruder_clearance_radius", 0)
	byLayerOK := true
	for _, pl := range plates {
		for _, is := range h.seqIssues(pl) {
			parts = append(parts, fmt.Sprintf("plate %d: %s", pl, is.text()))
			if is.Need > need {
				need = is.Need
			}
		}
		if dialect == "v72" && h.plateFilamentCount(pl) >= 2 {
			byLayerOK = false
		}
	}
	var b strings.Builder
	if len(parts) > 0 {
		b.WriteString(strings.Join(parts, "; "))
		b.WriteString(". ")
	} else if radius > 0 {
		fmt.Fprintf(&b, "Objects printed one after another need about %.0f mm between them (the printer's extruder clearance radius is %.0f mm; low objects need less). ", radius+1, radius)
	}
	b.WriteString("Call slice_project with arrange true (the tools then pack this plate with the needed clearance) or move the objects apart with update_object")
	if byLayerOK {
		b.WriteString(", or set print_sequence to by layer for this plate")
	}
	b.WriteString(".")
	return b.String()
}

// plateFilamentCount is the number of different filaments the objects of a plate use.
func (h *handle) plateFilamentCount(plate int) int {
	pl := h.p.Plate(plate)
	if pl == nil {
		return 0
	}
	slots := map[int]bool{}
	for _, in := range pl.Instances {
		if o := h.p.Object(in.ObjectID); o != nil {
			slots[max(o.Extruder(), 1)] = true
			for _, p := range o.Parts {
				if n := atoi0(partConfig(o, p).Value("extruder")); n > 0 {
					slots[n] = true
				}
			}
			for _, r := range o.LayerRanges {
				if n := atoi0(r.Options.Value("extruder")); n > 0 {
					slots[n] = true
				}
			}
		}
	}
	return len(slots)
}

// excludeRects are the rectangles of bed_exclude_area (four points each, in
// plate coordinates); an empty or degenerate one (the K2 has "0x0") is skipped.
func (h *handle) excludeRects() []rect {
	if h.p.Settings == nil {
		return nil
	}
	pts := h.p.Settings.List("bed_exclude_area")
	var out []rect
	for i := 0; i+3 < len(pts); i += 4 {
		r := rect{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)}
		ok := true
		for _, pt := range pts[i : i+4] {
			xs, ys, found := strings.Cut(pt, "x")
			x, e1 := strconv.ParseFloat(xs, 64)
			y, e2 := strconv.ParseFloat(ys, 64)
			if !found || e1 != nil || e2 != nil {
				ok = false
				break
			}
			r = rect{math.Min(r.x0, x), math.Min(r.y0, y), math.Max(r.x1, x), math.Max(r.y1, y)}
		}
		if ok && r.x1 > r.x0 && r.y1 > r.y0 {
			out = append(out, r)
		}
	}
	return out
}
