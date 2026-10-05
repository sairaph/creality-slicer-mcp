package projects

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/sairaph/creality-slicer-mcp/internal/profiles"
	"github.com/sairaph/creality-slicer-mcp/internal/threemf"
)

// PresetsRequest is set_presets: the new presets of a project. Empty fields
// keep what the project has. Filaments, when given, replaces the whole list;
// an entry with an empty Preset or Colour keeps the one of the same slot.
type PresetsRequest struct {
	Printer   string
	Process   string
	Filaments []FilamentSpec
	// Spools replaces the filaments with ones made from CFS spools (exclusive
	// with Filaments).
	Spools []SpoolSpec
	// KeepChanges keeps the project's changed settings of a preset that is
	// replaced (those of an unchanged preset are always kept).
	KeepChanges bool
	// FlushMultiplier sets the purge multiplier (auto flush matrix); "" keeps it.
	FlushMultiplier string
	// FlushMatrix is an edited purge matrix: N*N volumes in mm3, row major, from
	// the source filament (row) to the destination (column). It switches the
	// project to a manual matrix. AutoFlush goes back to the calculated one.
	FlushMatrix []int
	AutoFlush   bool
}

// PresetsResult reports what set_presets did.
type PresetsResult struct {
	Info *Info
	// Changed lists what was replaced, in words.
	Changed []string
	// Dropped lists settings whose changes went away with a replaced preset.
	Dropped  []string
	Warnings []string
}

// referencedSlots returns the highest filament slot used by objects, parts
// and layer actions, with a description of who uses it.
func (h *handle) highestSlot() (int, string) {
	best, who := 0, ""
	note := func(n int, what string) {
		if n > best {
			best, who = n, what
		}
	}
	for _, o := range h.p.Objects {
		note(o.Extruder(), fmt.Sprintf("object %q", o.Name))
		for _, p := range o.Parts {
			if v := p.Config.Value("extruder"); v != "" {
				var n int
				fmt.Sscanf(v, "%d", &n)
				note(n, fmt.Sprintf("part %q of object %q", p.Name, o.Name))
			}
		}
		for i, r := range o.LayerRanges {
			note(atoi0(r.Options.Value("extruder")), fmt.Sprintf("height range %d of object %q", i+1, o.Name))
		}
	}
	for _, pl := range h.p.Plates {
		for _, it := range h.p.CustomGCodes(pl.Index).Items {
			if it.Type == threemf.GCodeToolChange || it.Type == threemf.GCodeColorChange {
				note(it.Extruder, fmt.Sprintf("a layer action on plate %d", pl.Index))
			}
		}
	}
	return best, who
}

// SetPresets replaces the printer, process or filament presets of a project.
// The settings are composed again from the new presets; the changes the
// project holds are kept for every preset that stays (or all, with
// KeepChanges), the project only settings (wipe tower position, plate type)
// are kept, and the flush matrix follows the new colours unless it was edited.
func (s *Store) SetPresets(ref string, req PresetsRequest) (*PresetsResult, error) {
	res := &PresetsResult{}
	err := s.write(ref, func(h *handle) error {
		cfg, err := h.cfg()
		if err != nil {
			return err
		}
		old, err := h.presets()
		if err != nil {
			return err
		}
		oldDiffs := cfg.List("different_settings_to_system")
		oldN := old.n()

		printer, process := old.Printer, old.Process
		if req.Printer != "" && req.Printer != old.Printer.Name {
			if printer, err = s.getPreset(profiles.TypePrinter, req.Printer, ""); err != nil {
				return err
			}
			res.Changed = append(res.Changed, fmt.Sprintf("printer: %s -> %s", old.Printer.Name, printer.Name))
		}
		if req.Process != "" && req.Process != old.Process.Name {
			if process, err = s.getPreset(profiles.TypeProcess, req.Process, printer.Name); err != nil {
				return err
			}
			res.Changed = append(res.Changed, fmt.Sprintf("process: %s -> %s", old.Process.Name, process.Name))
		}
		if len(req.Spools) > 0 && req.Filaments != nil {
			return invalidf("give spools or filaments, not both", "spools and filaments contradict each other")
		}
		var links []SpoolLink
		if len(req.Spools) > 0 {
			if req.Filaments, links, err = s.resolveSpools(printer.Name, req.Spools); err != nil {
				return err
			}
		}
		newFil := old.Filaments
		colours := old.Colours
		if req.Filaments != nil {
			specs := make([]FilamentSpec, len(req.Filaments))
			for i, f := range req.Filaments {
				if f.Preset == "" && i < oldN {
					f.Preset = old.Filaments[i].Name
				}
				if f.Colour == "" && i < len(old.Colours) {
					f.Colour = old.Colours[i]
				}
				specs[i] = f
			}
			newFil, colours, err = s.resolveFilaments(printer.Name, specs)
			if err != nil {
				return err
			}
			for i, f := range newFil {
				switch {
				case i >= oldN:
					res.Changed = append(res.Changed, fmt.Sprintf("filament %d added: %s %s", i+1, f.Name, colours[i]))
				case f.Name != old.Filaments[i].Name || !strings.EqualFold(colours[i], old.Colours[i]):
					res.Changed = append(res.Changed, fmt.Sprintf("filament %d: %s %s -> %s %s", i+1, old.Filaments[i].Name, old.Colours[i], f.Name, colours[i]))
				}
			}
			if len(newFil) < oldN {
				res.Changed = append(res.Changed, fmt.Sprintf("filaments: %d -> %d", oldN, len(newFil)))
			}
		}
		if len(req.Spools) > 0 && !sameLinks(links, h.meta.Spools) {
			res.Changed = append(res.Changed, "filaments follow the CFS spools")
		}
		if len(res.Changed) == 0 && req.FlushMultiplier == "" && req.FlushMatrix == nil && !req.AutoFlush {
			return invalidf("give printer, process or filaments that differ from the project's", "nothing to change: the project already uses these presets")
		}
		if len(newFil) < oldN {
			if slot, who := h.highestSlot(); slot > len(newFil) {
				return conflictf("move those to another filament first (update_object, set_layer_actions)",
					"cannot go down to %d filament(s): %s uses filament %d", len(newFil), who, slot)
			}
		}
		if err := s.checkCompatible(printer, process, newFil); err != nil {
			return err
		}

		in := composeInput{Cat: s.cfg.Catalog, Printer: printer, Process: process, Filaments: newFil, Colours: colours,
			Version: s.version(), BedType: cfg.String("curr_bed_type"), Plates: len(h.p.Plates)}
		// The purge multiplier is kept when the user changed it from the printer default.
		if m := cfg.String("flush_multiplier"); m != "" && m != old.Printer.String("default_flush_multiplier") && m != cfg.String("default_flush_multiplier") {
			in.FlushMultiplier = m
		}
		if req.FlushMultiplier != "" {
			in.FlushMultiplier = req.FlushMultiplier
		}
		if req.FlushMatrix != nil && req.AutoFlush {
			return invalidf("give flush_matrix or auto_flush, not both", "flush_matrix and auto_flush contradict each other")
		}
		if req.FlushMatrix != nil {
			n := len(newFil)
			if len(req.FlushMatrix) != n*n {
				return invalidf(fmt.Sprintf("give %d volumes for %d filaments, row by row (from filament, to filament)", n*n, n), "the flush matrix needs %d values, got %d", n*n, len(req.FlushMatrix))
			}
			man := make([]string, len(req.FlushMatrix))
			for i, v := range req.FlushMatrix {
				if v < 0 {
					return invalidf("volumes are in mm3 and cannot be negative", "flush volume %d is %d", i+1, v)
				}
				man[i] = strconv.Itoa(v)
			}
			in.Manual = man
			res.Changed = append(res.Changed, "flush matrix edited")
		} else if !req.AutoFlush && cfg.String("flush_volumes_changed") == "1" {
			if m := cfg.List("flush_volumes_matrix"); len(m) == len(newFil)*len(newFil) {
				in.Manual = m
			} else {
				res.Warnings = append(res.Warnings, "the edited flush matrix does not fit the new number of filaments, so it was calculated again")
			}
		}
		c, err := compose(in)
		if err != nil {
			return errf(CodeInternal, "", "composing the project settings failed: %v", err)
		}
		ncfg := c.Cfg

		// Keep the changes of every preset that stays.
		keepSlot := func(kind slotKind, i int) bool {
			if req.KeepChanges {
				return true
			}
			switch kind {
			case slotProcess:
				return process.Name == old.Process.Name
			case slotPrinter:
				return printer.Name == old.Printer.Name
			}
			return i < oldN && i < len(newFil) && newFil[i].Name == old.Filaments[i].Name
		}
		dropped := map[string]bool{}
		diffAt := func(i int) map[string]bool {
			set := map[string]bool{}
			if i < len(oldDiffs) {
				for _, k := range strings.Split(oldDiffs[i], ";") {
					if k != "" {
						set[k] = true
					}
				}
			}
			return set
		}
		cat := s.cfg.Catalog
		apply := func(keys map[string]bool, kind slotKind, slot int) {
			for k := range keys {
				o, ok := cat.Get(k)
				if !ok || !isPresetOption(o) || excludedKeys[k] {
					continue
				}
				if _, exists := ncfg.Get(k); !exists {
					continue
				}
				if !keepSlot(kind, slot) {
					dropped[k] = true
					continue
				}
				ov, _ := cfg.Get(k)
				if kind == slotFilament && o.IsVector {
					nv, _ := ncfg.Get(k)
					items := valElems(nv)
					oe := valElems(ov)
					if slot < len(items) && slot < len(oe) {
						items[slot] = oe[slot]
						ncfg.SetList(k, items...)
					}
					continue
				}
				ncfg.Set(k, ov)
			}
		}
		apply(diffAt(0), slotProcess, 0)
		for i := 0; i < oldN && i < len(newFil); i++ {
			apply(diffAt(1+i), slotFilament, i)
		}
		apply(diffAt(oldN+1), slotPrinter, 0)
		// Keep the project only settings.
		for _, k := range extraKeys {
			if v, ok := cfg.Get(k); ok {
				ncfg.Set(k, v)
			}
		}
		// The wipe tower of every plate keeps its place on the plate.
		setTowerScene(ncfg, len(h.p.Plates), towerRelative(cfg, len(h.p.Plates), towerDefault(s.cfg.Catalog)))
		if len(dropped) > 0 {
			res.Dropped = sortedKeys(dropped)
			res.Warnings = append(res.Warnings, fmt.Sprintf("the project's changes to %d setting(s) went away with the replaced preset(s): %s", len(dropped), strings.Join(firstN(res.Dropped, 6), ", ")+ellipsis(len(res.Dropped), 6)))
		}
		// Multiplier and the matrix depend on the (possibly kept) printer values.
		if err := setFlush(ncfg, in, &c.FlushAuto); err != nil {
			return errf(CodeInternal, "", "%v", err)
		}
		setDiffs(ncfg, in)
		// Another printer can have another bed size, which moves the plate origins:
		// every object keeps its place on its plate.
		relObjects := h.relativeAll()
		h.p.SetSettings(ncfg)
		if err := h.applyRelative(relObjects); err != nil {
			return err
		}

		// Custom G-code mode follows the number of filaments.
		for _, pl := range h.p.Plates {
			g := h.p.CustomGCodes(pl.Index)
			mode := g.Mode
			if len(newFil) > 1 && mode == threemf.ModeSingleExtruder {
				mode = threemf.ModeMultiExtruder
			} else if len(newFil) == 1 && mode == threemf.ModeMultiExtruder {
				mode = threemf.ModeSingleExtruder
			}
			if mode != g.Mode {
				if err := h.p.SetCustomGCodes(pl.Index, mode, g.Items); err != nil {
					return errf(CodeInternal, "", "%v", err)
				}
			}
		}
		if len(req.Spools) > 0 {
			h.meta.Spools = links
		} else if req.Filaments != nil {
			h.meta.Spools = nil // filaments given by hand: the old spool links no longer describe them
		}
		if len(newFil) != oldN {
			res.Warnings = append(res.Warnings, "the number of filaments changed: check the wipe tower and the filament of every object")
		}
		h.touchAll()
		return nil
	})
	if err != nil {
		return nil, err
	}
	res.Info, err = s.info(ref)
	return res, err
}
