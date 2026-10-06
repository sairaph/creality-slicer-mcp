package projects

import "github.com/sairaph/creality-slicer-mcp/internal/threemf"

// OverrideLine is one setting set below the project level.
type OverrideLine struct {
	Key, Label, Value string
}

// PartOverrides is a part or modifier of an object with its settings.
type PartOverrides struct {
	Name     string
	Kind     string
	Filament int // 0 = the object's
	Lines    []OverrideLine
}

// RangeOverrides is a height range with its settings.
type RangeOverrides struct {
	From, To float64
	Filament int // 0 = the object's
	Lines    []OverrideLine
}

// ObjectOverrides is an object on the plate with everything set on it.
type ObjectOverrides struct {
	ID       int
	Name     string
	Filament int
	Lines    []OverrideLine
	Parts    []PartOverrides
	Ranges   []RangeOverrides
}

// OverridesReport lists the plate, object, part and height range settings of a
// plate, as they were when it was sliced.
type OverridesReport struct {
	Plate      int
	PlateLines []OverrideLine
	Objects    []ObjectOverrides
	// FromSlice is true when the values come from the project file the slice
	// used; false when only the current project was available (the slice is older
	// than the file kept for the last slice), so they may differ from the slice.
	FromSlice bool
	Stale     bool
}

// plateOverrideKeys are the plate metadata keys that are settings.
var plateOverrideKeys = map[string]string{
	"bed_type": "curr_bed_type", "print_sequence": "print_sequence", "first_layer_print_sequence": "first_layer_print_sequence",
	"other_layers_print_sequence": "other_layers_print_sequence", "other_layers_print_sequence_nums": "other_layers_print_sequence_nums",
	"spiral_mode": "spiral_mode",
}

// Overrides reads the settings set below the project level for the objects of
// a sliced plate. The values are the ones recorded when the plate was sliced
// (job.json keeps them; the project copy the slicer read is not kept). A slice
// made before they were recorded falls back to the current project, and
// FromSlice says which.
func (s *Store) Overrides(ref string, plate int) (*OverridesReport, error) {
	id, err := s.resolve(ref)
	if err != nil {
		return nil, err
	}
	m, err := s.readMeta(id)
	if err != nil {
		return nil, notFoundf("", "project %s cannot be read: %v", id, err)
	}
	if m.LastSlice == nil {
		return nil, notFoundf("call slice_project first", "project %s has not been sliced", id)
	}
	if len(m.LastSlice.Plates) == 0 {
		return nil, notFoundf("call slice_project first", "project %s has no sliced plate", id)
	}
	if plate == 0 {
		plate = m.LastSlice.Plates[0].Plate
	}
	var pr *PlateResult
	for i := range m.LastSlice.Plates {
		if m.LastSlice.Plates[i].Plate == plate {
			pr = &m.LastSlice.Plates[i]
		}
	}
	if pr == nil {
		return nil, notFoundf("slice the plate with slice_project", "plate %d has not been sliced in project %s", plate, id)
	}
	if pr.Overrides != nil {
		rep := *pr.Overrides
		rep.FromSlice, rep.Stale = true, m.plateStale(*pr)
		return &rep, nil
	}
	rep := &OverridesReport{Plate: plate, Stale: m.plateStale(*pr)}
	cur, err := s.openLocked(id)
	if err != nil {
		return nil, err
	}
	defer cur.close()
	cur.fillOverrides(rep)
	return rep, nil
}

func (h *handle) overrideLines(kv threemf.KVs, skip ...string) []OverrideLine {
	var out []OverrideLine
next:
	for _, e := range kv {
		for _, k := range skip {
			if e.Key == k {
				continue next
			}
		}
		l := OverrideLine{Key: e.Key, Value: e.Value, Label: e.Key}
		if o, ok := h.s.cfg.Catalog.Get(e.Key); ok {
			l.Label = o.Title()
		}
		out = append(out, l)
	}
	return out
}

func (h *handle) fillOverrides(rep *OverridesReport) {
	pl := h.p.Plate(rep.Plate)
	if pl == nil {
		return
	}
	for _, e := range pl.Config {
		if key, ok := plateOverrideKeys[e.Key]; ok && e.Value != "" {
			l := OverrideLine{Key: key, Value: e.Value, Label: key}
			if o, ok := h.s.cfg.Catalog.Get(key); ok {
				l.Label = o.Title()
			}
			rep.PlateLines = append(rep.PlateLines, l)
		}
	}
	seen := map[int]bool{}
	for _, in := range pl.Instances {
		o := h.p.Object(in.ObjectID)
		if o == nil || seen[o.ID] {
			continue
		}
		seen[o.ID] = true
		oo := ObjectOverrides{ID: o.ID, Name: o.Name, Filament: max(o.Extruder(), 1), Lines: h.overrideLines(o.Config, "extruder")}
		for _, p := range o.Parts {
			lines := h.overrideLines(p.Config, "extruder")
			fil := atoi0(partConfig(o, p).Value("extruder"))
			if len(lines) == 0 && fil == 0 && p.Subtype == threemf.SubtypeNormal {
				continue
			}
			kind := p.Subtype
			if kind == threemf.SubtypeModifier {
				kind = "modifier"
			} else if kind == threemf.SubtypeNegative {
				kind = "negative_part"
			}
			oo.Parts = append(oo.Parts, PartOverrides{Name: p.Name, Kind: kind, Filament: fil, Lines: lines})
		}
		for _, r := range o.LayerRanges {
			oo.Ranges = append(oo.Ranges, RangeOverrides{From: r.MinZ, To: r.MaxZ, Filament: atoi0(r.Options.Value("extruder")), Lines: h.overrideLines(r.Options, "extruder")})
		}
		rep.Objects = append(rep.Objects, oo)
	}
}
