package projects

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/sairaph/creality-slicer-mcp/internal/threemf"
)

// roleFilamentKeys pick a filament for one feature of an object; 0 means the
// object's own filament.
var roleFilamentKeys = []string{"wall_filament", "sparse_infill_filament", "solid_infill_filament", "support_filament", "support_interface_filament"}

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
			if mixesFilaments(p.Config, ext) {
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
	if h.plateObjectsUseOneFilament(plate) {
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
