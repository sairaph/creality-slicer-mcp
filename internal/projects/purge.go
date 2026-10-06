package projects

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/sairaph/creality-slicer-mcp/internal/threemf"
)

// roleFilamentKeys pick a filament for one feature of an object; 0 means the
// object's own filament.
var roleFilamentKeys = []string{"wall_filament", "sparse_infill_filament", "solid_infill_filament", "support_filament", "support_interface_filament"}

// partConfig is the setting overrides of a part as the slicer sees them. The
// importer erases the extruder of the only part of an object
// (bbs_3mf.cpp:2917-2919: a single volume never keeps its own filament), so
// that key is left out for a single part object; the filament lives on the
// object then.
func partConfig(o *threemf.Object, p *threemf.Part) threemf.KVs {
	if len(o.Parts) != 1 {
		return p.Config
	}
	var out threemf.KVs
	for _, kv := range p.Config {
		if kv.Key != "extruder" {
			out = append(out, kv)
		}
	}
	return out
}

func atoi0(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

// mixesFilaments reports whether a set of overrides sends part of an object to
// another filament than ext.
func mixesFilaments(kv threemf.KVs, ext int) bool {
	if n := atoi0(kv.Value("extruder")); n > 0 && n != ext {
		return true
	}
	for _, k := range roleFilamentKeys {
		if n := atoi0(kv.Value(k)); n > 0 && n != ext {
			return true
		}
	}
	return false
}

// plateObjectsUseOneFilament reports whether every object on the plate prints
// with a single filament: its object extruder, no part or height range with
// another one, no role filament, no painted colours, no tool or colour change
// layer action. Then printing by object removes the prime tower and most flushing; a change between objects of different filaments still flushes.
func (h *handle) plateObjectsUseOneFilament(plate int) bool {
	pl := h.p.Plate(plate)
	if pl == nil {
		return false
	}
	for _, it := range h.p.CustomGCodes(plate).Items {
		if it.Type == threemf.GCodeToolChange || it.Type == threemf.GCodeColorChange {
			return false
		}
	}
	global := threemf.KVs{}
	if h.p.Settings != nil {
		for _, k := range roleFilamentKeys {
			global = append(global, threemf.KV{Key: k, Value: h.p.Settings.String(k)})
		}
	}
	seen := map[int]bool{}
	for _, in := range pl.Instances {
		o := h.p.Object(in.ObjectID)
		if o == nil || seen[o.ID] {
			continue
		}
		seen[o.ID] = true
		ext := max(o.Extruder(), 1)
		if o.Painted().Any() || mixesFilaments(o.Config, ext) || mixesFilaments(global, ext) {
			return false
		}
		for _, p := range o.Parts {
			if mixesFilaments(partConfig(o, p), ext) {
				return false
			}
		}
		for _, r := range o.LayerRanges {
			if mixesFilaments(r.Options, ext) {
				return false
			}
		}
	}
	return true
}

// layerFilamentChanges counts the tool changes stored as layer actions of a plate.
func (h *handle) layerFilamentChanges(plate int) int {
	n := 0
	for _, it := range h.p.CustomGCodes(plate).Items {
		if it.Type == threemf.GCodeToolChange {
			n++
		}
	}
	return n
}

// purgeWarning is the note of a sliced plate that printed several filaments by
// layer: how much of the material went into the prime tower and the flush, and
// whether printing by object would avoid it. Empty when there is no waste.
func (h *handle) purgeWarning(plate int, pr PlateResult) string {
	waste := pr.PrimeTowerG + pr.FlushG
	if !pr.Multicolour || waste <= 0 || pr.TotalG <= 0 || h.plateSequence(plate) == "by object" {
		return ""
	}
	est := ""
	if pr.FlushEstimated {
		est = " (flush estimated from the flush matrix)"
	}
	msg := fmt.Sprintf("Purge waste %.1f g of %.1f g (%.0f%%): prime tower %.1f g + flush %.1f g over %d filament changes%s.",
		waste, pr.TotalG, 100*waste/pr.TotalG, pr.PrimeTowerG, pr.FlushG, pr.FlushChanges, est)
	if n := h.layerFilamentChanges(plate); n > 0 && len(h.p.Plate(plate).Instances) == 1 {
		// One object: the changes are the layer actions, by object changes nothing.
		msg += fmt.Sprintf(" The waste comes from the %d layer filament change(s) of this plate: fewer changes, or a smaller flush_multiplier or flush matrix, reduce it.", n)
	} else if h.plateObjectsUseOneFilament(plate) {
		need := 2 * h.seqHalf(h.seqBoxes(plate))
		msg += fmt.Sprintf(" Each object here uses one filament, so printing by object removes the prime tower and most of this flush (a change between objects that use different filaments still flushes): update_settings {\"scope\":\"plate\",\"target\":\"%d\",\"values\":{\"print_sequence\":\"by object\"}} then slice_project with arrange true (by-object needs about %.0f mm between objects).", plate, need)
	} else {
		msg += " Printing by object would not remove it because the objects on this plate mix filaments."
	}
	return msg
}

// PurgeNotes are the purge waste notes of the plates of a slice, in plate
// order; a plate is named when the slice has several.
func (l *LastSlice) PurgeNotes() []string {
	var out []string
	for _, p := range l.Plates {
		if p.PurgeWarning == "" {
			continue
		}
		note := p.PurgeWarning
		if len(l.Plates) > 1 {
			note = fmt.Sprintf("Plate %d: %s", p.Plate, note)
		}
		out = append(out, note)
	}
	return out
}

// plateExtruders lists the filaments (1 based) the objects of a plate print
// with, the way Print::object_extruders collects them (Print.cpp:1352): the
// filament of each object, part and height range, and, per region, the wall
// filament when it prints walls (wall_loops > 0, or a brim), the sparse infill
// filament when the infill density is above 0 and the solid infill filament
// when there are top or bottom shell layers (PrintRegion.cpp:47). A role
// filament of 0 is the region's own filament. Supports are not counted: they go
// through support_material_extruders, which the layer tool change code does not
// consult. painted is true when colour painting adds filaments the file does not
// name; painting of seams, supports, fuzzy skin or faces adds none.
func (h *handle) plateExtruders(plate int) (filaments []int, painted bool) {
	pl := h.p.Plate(plate)
	if pl == nil {
		return nil, false
	}
	set := map[int]bool{}
	global := func(key string) string {
		if h.p.Settings == nil {
			return ""
		}
		return h.p.Settings.String(key)
	}
	seen := map[int]bool{}
	for _, in := range pl.Instances {
		o := h.p.Object(in.ObjectID)
		if o == nil || seen[o.ID] {
			continue
		}
		seen[o.ID] = true
		objExt := max(o.Extruder(), 1)
		set[objExt] = true
		painted = painted || o.Painted().Color
		// region reads the effective value of a key: the first of the layers
		// that sets it, else the project.
		region := func(layers ...threemf.KVs) {
			look := func(key string) string {
				for _, kv := range layers {
					if v, ok := kv.Get(key); ok && strings.TrimSpace(v) != "" {
						return v
					}
				}
				return global(key)
			}
			own := objExt
			if n := atoi0(look("extruder")); n > 0 {
				own = n
				set[n] = true
			}
			role := func(key string) {
				if n := atoi0(look(key)); n > 0 {
					set[n] = true
				} else {
					set[own] = true
				}
			}
			num := func(key string) float64 {
				f, _ := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(look(key)), "%"), 64)
				return f
			}
			brim := look("raft_layers") != "" && atoi0(look("raft_layers")) > 0
			brim = !brim && ((look("brim_type") != "no_brim" && look("brim_type") != "" && num("brim_width") > 0) || look("brim_type") == "auto_brim")
			if num("wall_loops") > 0 || brim {
				role("wall_filament")
			}
			if num("sparse_infill_density") > 0 {
				role("sparse_infill_filament")
			}
			if num("top_shell_layers") > 0 || num("bottom_shell_layers") > 0 {
				role("solid_infill_filament")
			}
		}
		region(o.Config)
		for _, p := range o.Parts {
			if cfg := partConfig(o, p); len(cfg) > 0 {
				region(cfg, o.Config)
			}
		}
		for _, r := range o.LayerRanges {
			region(r.Options, o.Config)
		}
	}
	for n := range set {
		filaments = append(filaments, n)
	}
	sort.Ints(filaments)
	return filaments, painted
}
