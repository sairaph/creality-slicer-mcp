package projects

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/sairaph/creality-slicer-mcp/internal/catalog"
	"github.com/sairaph/creality-slicer-mcp/internal/threemf"
)

// Plate actions.
const (
	PlateAdd    = "add"
	PlateRemove = "remove"
	PlateRename = "rename"
	PlateLock   = "lock"
	PlateUnlock = "unlock"
	PlateMove   = "move" // move an object instance to another plate
)

// PlatesRequest is manage_plates.
type PlatesRequest struct {
	Action string
	// Plate is the plate acted on (remove, rename, lock, unlock) or the
	// destination of move.
	Plate int
	Name  string
	// Object and Instance name what move takes to Plate.
	Object   string
	Instance int
	// X and Y place a moved object on the destination (default automatic).
	X, Y *float64
}

// PlatesResult reports the plates after the change.
type PlatesResult struct {
	Info     *Info
	Warnings []string
}

// plateVectors are the project settings that hold one value per plate.
var plateVectors = []string{"wipe_tower_x", "wipe_tower_y"}

// ManagePlates adds, removes, renames and locks plates and moves objects
// between them.
func (s *Store) ManagePlates(ref string, req PlatesRequest) (*PlatesResult, error) {
	res := &PlatesResult{}
	err := s.write(ref, func(h *handle) error {
		switch req.Action {
		case PlateAdd:
			// The number of plates decides the layout of the scene: carry the objects
			// and the wipe towers along.
			rel, towers := h.relativeAll(), h.towersRelative()
			pl := h.p.AddPlate(strings.TrimSpace(req.Name))
			if towers != nil {
				h.replotTowers(append(towers, towerDefault(h.s.cfg.Catalog)))
			}
			if err := h.applyRelative(rel); err != nil {
				return err
			}
			h.touchPlate(pl.Index)
		case PlateRemove:
			if h.p.Plate(req.Plate) == nil {
				return notFoundf("call get_project to see the plates", "project %s has no plate %d", h.id, req.Plate)
			}
			rel, towers := h.relativeAll(), h.towersRelative()
			if err := h.p.RemovePlate(req.Plate); err != nil {
				return invalidf("move or remove the objects of the plate first", "%v", err)
			}
			if towers != nil && req.Plate <= len(towers) {
				h.replotTowers(append(towers[:req.Plate-1:req.Plate-1], towers[req.Plate:]...))
			}
			if err := h.applyRelative(rel); err != nil {
				return err
			}
			h.touchAll()
		case PlateRename:
			if strings.TrimSpace(req.Name) == "" {
				return invalidf("give name", "a plate needs a name")
			}
			if err := h.p.RenamePlate(req.Plate, strings.TrimSpace(req.Name)); err != nil {
				return threemfError(err)
			}
			h.changed = true
		case PlateLock, PlateUnlock:
			if err := h.p.LockPlate(req.Plate, req.Action == PlateLock); err != nil {
				return notFoundf("call get_project to see the plates", "%v", err)
			}
			h.changed = true
		case PlateMove:
			return h.moveInstance(req, res)
		default:
			return invalidf("actions: add, remove, rename, lock, unlock, move", "%q is not a plate action", req.Action)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	err = s.read(ref, func(h *handle) error {
		var ierr error
		res.Info, ierr = h.info()
		return ierr
	})
	return res, err
}

func (h *handle) moveInstance(req PlatesRequest, res *PlatesResult) error {
	if (req.X == nil) != (req.Y == nil) {
		return invalidf("give both x and y, or neither", "x and y go together")
	}
	o, err := h.objectByRef(req.Object)
	if err != nil {
		return err
	}
	dst := h.p.Plate(req.Plate)
	if dst == nil {
		return notFoundf("call get_project to see the plates", "project %s has no plate %d", h.id, req.Plate)
	}
	items := h.p.ItemsOf(o.ID)
	if req.Instance < 0 || req.Instance >= len(items) {
		return notFoundf("", "object %q has %d instance(s); instance %d does not exist", o.Name, len(items), req.Instance)
	}
	m, err := h.objectMesh(o)
	if err != nil {
		return invalidf("", "object %q has no usable geometry: %v", o.Name, err)
	}
	cur := h.itemT(o.ID, req.Instance)
	b, _ := bboxOf(m, cur)
	sz := size3(b)
	c := center3(b)
	from := 0
	if pl := h.p.PlateOf(o.ID, req.Instance); pl != nil {
		from = pl.Index
	}
	cx, cy := c[0], c[1]
	if req.X != nil && req.Y != nil {
		cx, cy = *req.X, *req.Y
	} else if from != req.Plate {
		h.placeHeight = sz[2]
		x, y, ok := h.findSpot(req.Plate, sz[0], sz[1], h.occupied(req.Plate, o.ID, req.Instance))
		if !ok {
			return conflictf("make room on that plate, or give x and y", "%q (%.1f x %.1f mm) does not fit on plate %d", o.Name, sz[0], sz[1], req.Plate)
		}
		cx, cy = x, y
	}
	if err := h.p.MoveInstance(o.ID, req.Instance, req.Plate); err != nil {
		return errf(CodeInternal, "", "%v", err)
	}
	if cx != c[0] || cy != c[1] || from != req.Plate {
		T := cur
		T[9] += cx - c[0]
		T[10] += cy - c[1]
		if err := h.setItemT(o.ID, req.Instance, T); err != nil {
			return errf(CodeInternal, "", "%v", err)
		}
	}
	if from != 0 {
		h.touchPlate(from)
	}
	h.touchPlate(req.Plate)
	return nil
}

// RangeSpec is one height range of an object: [From, To) mm with settings.
type RangeSpec struct {
	From, To float64
	Settings map[string]any
}

// SetHeightRanges replaces the height ranges of an object (an empty list
// removes them all).
func (s *Store) SetHeightRanges(ref, object string, ranges []RangeSpec) (*Info, error) {
	res, err := s.SetHeightRangesDetailed(ref, object, ranges)
	if err != nil {
		return nil, err
	}
	return res.Info, nil
}

// HeightRangesResult is the reply of SetHeightRangesDetailed.
type HeightRangesResult struct {
	Info *Info
	// Notes say what the tools added to the ranges: a layer_height where the
	// caller gave none.
	Notes []string
}

// SetHeightRangesDetailed is SetHeightRanges with the notes on what was added.
func (s *Store) SetHeightRangesDetailed(ref, object string, ranges []RangeSpec) (*HeightRangesResult, error) {
	var notes []string
	err := s.write(ref, func(h *handle) error {
		o, err := h.objectByRef(object)
		if err != nil {
			return err
		}
		var out []threemf.LayerRange
		var errs keyErrors
		specs := append([]RangeSpec(nil), ranges...)
		sort.SliceStable(specs, func(i, j int) bool { return specs[i].From < specs[j].From })
		for i, r := range specs {
			if !(r.From >= 0) || !(r.To > r.From) || math.IsInf(r.To, 0) {
				return invalidf("give from below to, both in mm above the bed", "range %d [%g, %g) is not valid", i+1, r.From, r.To)
			}
			if i > 0 && r.From < specs[i-1].To-1e-9 {
				return invalidf("ranges must not overlap", "range %d [%g, %g) overlaps range %d [%g, %g)", i+1, r.From, r.To, i, specs[i-1].From, specs[i-1].To)
			}
			if len(r.Settings) == 0 {
				return invalidf("give settings: {key: value}", "range %d [%g, %g) has no settings", i+1, r.From, r.To)
			}
			lr := threemf.LayerRange{MinZ: r.From, MaxZ: r.To}
			for _, key := range sortedValueKeys(r.Settings) {
				v := r.Settings[key]
				if v == nil {
					errs.add(key, "needs a value")
					continue
				}
				if opt, ok := h.validate(key, v, catalog.ScopeLayerRange, false, &errs); ok {
					if key == "extruder" {
						h.checkExtruder(key, opt, v, 0, &errs)
					}
					lr.Options.Set(key, objectValue(opt, v))
				}
			}
			// The slicer reads layer_height of every range and crashes (an access
			// violation in 7.2.2 and 7.3) when it is missing, as it is for a range
			// that only sets a region setting like wall_loops. The app always writes
			// it: it is the object's own value unless the range sets another.
			if lr.Options.Value("layer_height") == "" {
				v := h.baseLayerHeight(o)
				lr.Options.Set("layer_height", v)
				notes = append(notes, fmt.Sprintf("range %d [%s, %s) got layer_height %s added (the slicer crashes on a range without it; it is the object's own layer height, so the range keeps its layers)", i+1, formatNumber(r.From), formatNumber(r.To), v))
			}
			out = append(out, lr)
		}
		if err := errs.err("call describe_setting for the valid values of a setting"); err != nil {
			return err
		}
		if err := h.p.SetLayerRanges(o.ID, out); err != nil {
			return invalidf("", "%v", err)
		}
		h.touchObject(o.ID)
		return nil
	})
	if err != nil {
		return nil, err
	}
	info, err := s.info(ref)
	if err != nil {
		return nil, err
	}
	return &HeightRangesResult{Info: info, Notes: notes}, nil
}

func (s *Store) info(ref string) (*Info, error) {
	var info *Info
	err := s.read(ref, func(h *handle) error {
		var ierr error
		info, ierr = h.info()
		return ierr
	})
	return info, err
}

// GetProject is get_project.
func (s *Store) GetProject(ref string) (*Info, error) { return s.info(ref) }

// Layer action kinds.
const (
	ActionColorChange = "color_change"
	ActionPause       = "pause"
	ActionToolChange  = "tool_change"
	ActionTemplate    = "template"
	ActionCustom      = "custom"
)

var actionTypes = map[string]int{
	ActionColorChange: threemf.GCodeColorChange, ActionPause: threemf.GCodePausePrint, ActionToolChange: threemf.GCodeToolChange,
	ActionTemplate: threemf.GCodeTemplate, ActionCustom: threemf.GCodeCustom,
}

// LayerAction is one custom action at a layer.
type LayerAction struct {
	// Layer (1 based) or Z (mm, the top of the layer) says where; give one.
	Layer int
	Z     float64
	Kind  string
	// Filament is the slot for a color_change or tool_change.
	Filament int
	// Colour is the new colour of a color_change (#RRGGBB).
	Colour string
	// GCode is the text of a custom action; Template names a template action.
	GCode string
}

// ActionInfo is a layer action as stored, with its layer.
type ActionInfo struct {
	Layer    int
	Z        float64
	Kind     string
	Filament int
	Colour   string
	GCode    string
}

// layerToZ is the top height of a layer (1 based): the first layer height
// plus the layer height per further layer.
func layerToZ(cfg *threemf.Config, layer int) (float64, error) {
	first, err1 := strconv.ParseFloat(cfg.String("initial_layer_print_height"), 64)
	lh, err2 := strconv.ParseFloat(cfg.String("layer_height"), 64)
	if err1 != nil || err2 != nil || lh <= 0 {
		return 0, fmt.Errorf("the layer heights of the project are not readable")
	}
	return round6(first + float64(layer-1)*lh), nil
}

func zToLayer(cfg *threemf.Config, z float64) int {
	first, err1 := strconv.ParseFloat(cfg.String("initial_layer_print_height"), 64)
	lh, err2 := strconv.ParseFloat(cfg.String("layer_height"), 64)
	if err1 != nil || err2 != nil || lh <= 0 {
		return 0
	}
	return int(math.Round((z-first)/lh)) + 1
}

// SetLayerActions replaces the custom actions of a plate (pauses, colour
// changes, tool changes, custom G-code at chosen layers).
func (s *Store) SetLayerActions(ref string, plate int, actions []LayerAction) (*Info, error) {
	if plate == 0 {
		plate = 1
	}
	err := s.write(ref, func(h *handle) error {
		cfg, err := h.cfg()
		if err != nil {
			return err
		}
		if h.p.Plate(plate) == nil {
			return notFoundf("call get_project to see the plates", "project %s has no plate %d", h.id, plate)
		}
		nfil := len(cfg.List("filament_settings_id"))
		// A printer without colour change G-code (the K2 with the CFS) changes colour
		// by switching to another filament: M600 would write nothing.
		noColourGCode := strings.TrimSpace(cfg.String("color_change_gcode")) == ""
		filColour := func(slot int, fallback string) string {
			if l := cfg.List("filament_colour"); slot >= 1 && slot <= len(l) {
				if c, ok := normColour(l[slot-1]); ok {
					return c
				}
			}
			if c, ok := normColour(fallback); ok {
				return c
			}
			return ""
		}
		hasToolChange := false
		var items []threemf.GCodeItem
		for i, a := range actions {
			typ, ok := actionTypes[a.Kind]
			if !ok {
				return invalidf("kinds: color_change, pause, tool_change, template, custom", "action %d: %q is not an action kind", i+1, a.Kind)
			}
			z := a.Z
			switch {
			case a.Layer > 0 && a.Z > 0:
				return invalidf("give layer or z, not both", "action %d has both a layer and a z", i+1)
			case a.Layer > 0:
				if z, err = layerToZ(cfg, a.Layer); err != nil {
					return errf(CodeInternal, "", "%v", err)
				}
			case a.Z > 0:
			default:
				return invalidf("give layer (from 1) or z in mm", "action %d has no position", i+1)
			}
			it := threemf.GCodeItem{TopZ: z, Type: typ}
			switch a.Kind {
			case ActionColorChange:
				if noColourGCode {
					if nfil < 2 {
						return invalidf("the K2 changes colour by switching to another CFS filament: add a filament with set_presets, then use color_change or tool_change to it",
							"action %d: this printer has no colour change G-code (M600), so a colour change needs a second filament slot", i+1)
					}
					slot := max(a.Filament, 1)
					if slot > nfil {
						return invalidf(fmt.Sprintf("use a filament from 1 to %d", nfil), "action %d: filament %d does not exist", i+1, slot)
					}
					it.Type, it.Extruder, it.Color = threemf.GCodeToolChange, slot, filColour(slot, a.Colour)
					break
				}
				if !colourRE.MatchString(a.Colour) {
					return invalidf("write the colour as #RRGGBB", "action %d: a color_change needs the new colour, got %q", i+1, a.Colour)
				}
				it.Color = strings.ToUpper(a.Colour)
				it.Extruder = max(a.Filament, 1)
			case ActionToolChange:
				if a.Filament < 1 || a.Filament > nfil {
					return invalidf(fmt.Sprintf("use a filament from 1 to %d", nfil), "action %d: filament %d does not exist", i+1, a.Filament)
				}
				it.Extruder, it.Color = a.Filament, filColour(a.Filament, a.Colour)
			case ActionCustom:
				if strings.TrimSpace(a.GCode) == "" {
					return invalidf("give gcode", "action %d: a custom action needs G-code text", i+1)
				}
				// The slicer reads the text of a custom action from "extra" (verified with
				// 7.3: text in the gcode attribute alone is dropped).
				it.Extra = a.GCode
			case ActionTemplate:
				it.Extra = a.GCode
			}
			if a.Kind == ActionColorChange && it.Extruder > nfil {
				return invalidf(fmt.Sprintf("use a filament from 1 to %d", nfil), "action %d: filament %d does not exist", i+1, it.Extruder)
			}
			hasToolChange = hasToolChange || it.Type == threemf.GCodeToolChange
			items = append(items, it)
		}
		mode := h.p.CustomGCodes(plate).Mode
		if mode == "" {
			mode = threemf.ModeSingleExtruder
		}
		// A tool change needs the multi extruder mode (the app writes it so).
		if hasToolChange && nfil >= 2 && mode == threemf.ModeSingleExtruder {
			mode = threemf.ModeMultiExtruder
		}
		if err := h.p.SetCustomGCodes(plate, mode, items); err != nil {
			return invalidf("", "%v", err)
		}
		h.touchPlate(plate)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.info(ref)
}

// LayerActions lists the custom actions of a plate.
func (h *handle) layerActions(plate int) []ActionInfo {
	var out []ActionInfo
	names := map[int]string{}
	for k, v := range actionTypes {
		names[v] = k
	}
	for _, it := range h.p.CustomGCodes(plate).Items {
		a := ActionInfo{Z: it.TopZ, Kind: names[it.Type], Filament: it.Extruder, Colour: it.Color, GCode: firstNonEmpty(it.GCode, it.Extra)}
		if h.p.Settings != nil {
			a.Layer = zToLayer(h.p.Settings, it.TopZ)
		}
		out = append(out, a)
	}
	return out
}

func isPlateVector(key string) bool {
	for _, k := range plateVectors {
		if k == key {
			return true
		}
	}
	return false
}

// replotTowers rewrites the wipe tower positions after the number of plates
// changed: every plate keeps its place on the plate, and the plates of the new
// layout get their scene coordinates.
func (h *handle) replotTowers(rel [][2]float64) {
	if h.p.Settings != nil {
		setTowerScene(h.p.Settings, len(h.p.Plates), rel)
	}
}

func (h *handle) towersRelative() [][2]float64 {
	if h.p.Settings == nil {
		return nil
	}
	return towerRelative(h.p.Settings, len(h.p.Plates), towerDefault(h.s.cfg.Catalog))
}

// baseLayerHeight is the layer height a height range starts from: the
// object's own override, else the project's.
func (h *handle) baseLayerHeight(o *threemf.Object) string {
	if v := o.Config.Value("layer_height"); v != "" {
		return v
	}
	if h.p.Settings != nil {
		if v := h.p.Settings.String("layer_height"); v != "" {
			return v
		}
	}
	return "0.2"
}
