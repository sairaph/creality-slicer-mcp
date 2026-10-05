package projects

import (
	"fmt"
	"image/color"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sairaph/creality-slicer-mcp/internal/mesh"
	"github.com/sairaph/creality-slicer-mcp/internal/profiles"
	"github.com/sairaph/creality-slicer-mcp/internal/render"
	"github.com/sairaph/creality-slicer-mcp/internal/threemf"
)

// K2Model is the printer_model of the K2 Combo family the tools target.
const K2Model = "Creality K2"

// FilamentInfo is one filament slot.
type FilamentInfo struct {
	Index      int // 1 based
	Preset     string
	Type       string
	Colour     string
	FilamentID string
	IsSupport  bool
	// Spool is the CFS spool the filament was made from, nil for a plain filament.
	Spool *SpoolInfo
}

// PlateInfo is one plate.
type PlateInfo struct {
	Index         int
	Name          string
	Objects       int
	BedType       string
	PrintSequence string
	// BedTemps is the first layer bed temperature (C) of each filament for the
	// plate's bed type, from the filament's <plate>_temp_initial_layer.
	BedTemps []string
	Locked   bool
	Actions  []ActionInfo
}

// RangeInfo is one height range of an object.
type RangeInfo struct {
	From, To float64
	Settings map[string]string
}

// PartInfo is one part of an object.
type PartInfo struct {
	ID        int
	Name      string
	Subtype   string
	Overrides int
	Painted   bool
	// Size and Center are the extent and the centre of a modifier, negative
	// part, support enforcer or support blocker on its plate, in mm (zero for
	// a normal part).
	Size, Center [3]float64
}

// ObjectInfo is one object with its first instance.
type ObjectInfo struct {
	ID        int
	Name      string
	Plate     int
	Instances int
	// Size is the bounding box size in mm; Position is the centre of the box in
	// x and y and its lowest z; Rotation is in degrees (X, Y, Z, applied in
	// that order); Scale per axis.
	Size      [3]float64
	Position  [3]float64
	Rotation  [3]float64
	Scale     [3]float64
	Filament  int
	Overrides int
	Ranges    int
	// HeightRanges lists the height ranges with their settings.
	HeightRanges []RangeInfo
	Parts        []PartInfo
	Painted      bool
	Outside      bool
	Triangles    int
	// IdentifyID of the first instance (for --skip-objects).
	IdentifyID int
}

// Warning is something the agent should know about a project.
type Warning struct {
	Code    string
	Message string
}

// Info is the state of a project, the "project fields" of the tool replies.
type Info struct {
	ID         string
	Name       string
	Revision   int
	Created    time.Time
	Updated    time.Time
	SourcePath string
	// AppVersion is the version of the application that wrote the file.
	AppVersion string
	Printer    string
	Process    string
	Filaments  []FilamentInfo
	Plates     []PlateInfo
	Objects    []ObjectInfo
	// Overrides counts the project level settings changed against the presets.
	Overrides int
	// OverrideKeys lists them (process, filament and printer changes).
	OverrideKeys []string
	// FlushMode is "auto" or "manual".
	FlushMode string
	// FlushMultiplier and FlushMatrix (N*N, row major, source to destination).
	FlushMultiplier string
	FlushMatrix     []string
	// Painted lists the names of objects with painted data.
	Painted []string
	// SlicedInFile is true when the file carries an embedded slice result.
	SlicedInFile bool
	LastSlice    *SliceStamp
	Warnings     []Warning
	// Drift lists settings of the presets that the catalog does not know.
	Drift []string
}

// meshEntry caches the merged mesh of an object (its normal parts, in the
// object's frame).
type meshEntry struct {
	mesh *mesh.Mesh
	err  error
}

// cfg returns the project settings or an error for a project without them.
func (h *handle) cfg() (*threemf.Config, error) {
	if h.p.Settings == nil {
		return nil, invalidf("this project has no project_settings.config; create a new project and add its models with add_model",
			"project %s has no settings", h.id)
	}
	return h.p.Settings, nil
}

// presets loads the presets the project is based on from the profiles store.
func (h *handle) presets() (composeInput, error) {
	cfg, err := h.cfg()
	if err != nil {
		return composeInput{}, err
	}
	in := composeInput{Cat: h.s.cfg.Catalog, Version: h.s.version(), Plates: len(h.p.Plates)}
	get := func(t profiles.Type, name string) (profiles.Preset, error) {
		p, err := h.s.cfg.Profiles.Get(t, name)
		if err != nil {
			return p, notFoundf("use set_presets to choose presets that exist on this machine",
				"the %s preset %q of project %s is not installed on this machine", t, name, h.id)
		}
		return p, nil
	}
	if in.Printer, err = get(profiles.TypePrinter, cfg.String("printer_settings_id")); err != nil {
		return in, err
	}
	if in.Process, err = get(profiles.TypeProcess, cfg.String("print_settings_id")); err != nil {
		return in, err
	}
	for _, name := range cfg.List("filament_settings_id") {
		f, err := get(profiles.TypeFilament, name)
		if err != nil {
			return in, err
		}
		in.Filaments = append(in.Filaments, f)
	}
	in.Colours = cfg.List("filament_colour")
	for len(in.Colours) < len(in.Filaments) {
		in.Colours = append(in.Colours, "#FFFFFF")
	}
	in.BedType = cfg.String("curr_bed_type")
	return in, nil
}

// geometry is the printer's bed as the renderer and the placement need it.
type geometry struct {
	Bed       render.Rect
	MaxHeight float64
}

func (h *handle) geometry() geometry {
	g := geometry{Bed: bedOf(h.p.Settings)}
	if cfg := h.p.Settings; cfg != nil {
		if hgt, err := strconv.ParseFloat(cfg.String("printable_height"), 64); err == nil && hgt > 0 {
			g.MaxHeight = hgt
		}
	}
	return g
}

func (g geometry) rect() rect { return rect{g.Bed.X0, g.Bed.Y0, g.Bed.X1, g.Bed.Y1} }

// towerRect is the footprint kept free for the wipe tower of a plate, or false
// when the plate needs none (one filament, tower off, or printing by object).
func (h *handle) towerRect(plate int) (rect, bool) {
	cfg := h.p.Settings
	if cfg == nil || len(cfg.List("filament_colour")) < 2 || cfg.String("enable_prime_tower") != "1" ||
		cfg.String("print_sequence") == "by object" {
		return rect{}, false
	}
	if pl := h.p.Plate(plate); pl != nil && pl.Config.Value("print_sequence") == "by object" {
		return rect{}, false
	}
	plates := len(h.p.Plates)
	if plate < 1 || plate > plates {
		return rect{}, false
	}
	rel := towerRelative(cfg, plates, towerDefault(h.s.cfg.Catalog))[plate-1]
	x, y := rel[0], rel[1]
	w, err3 := strconv.ParseFloat(cfg.String("prime_tower_width"), 64)
	if err3 != nil {
		return rect{}, false
	}
	return rect{x, y, x + w, y + render.DefaultWipeTowerDepth}, true
}

// objectMesh is the merged geometry of an object's normal parts in the object's
// own frame (the part transforms applied).
func (h *handle) objectMesh(o *threemf.Object) (*mesh.Mesh, error) {
	if h.meshes == nil {
		h.meshes = map[int]*meshEntry{}
	}
	if e, ok := h.meshes[o.ID]; ok {
		return e.mesh, e.err
	}
	merged := &mesh.Mesh{}
	var err error
	for _, part := range o.Parts {
		if part.Subtype != threemf.SubtypeNormal {
			continue
		}
		m, lerr := h.p.LoadMesh(part)
		if lerr != nil {
			err = lerr
			break
		}
		merged.Append(m.Transformed(part.ComponentTransform))
	}
	if err == nil && len(merged.Triangles) == 0 {
		err = fmt.Errorf("object %q has no geometry", o.Name)
	}
	h.meshes[o.ID] = &meshEntry{merged, err}
	return merged, err
}

// filamentColour parses #RRGGBB (or #RRGGBBAA) into a colour.
func filamentColour(s string) color.NRGBA {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(s) != 6 && len(s) != 8 {
		return color.NRGBA{}
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return color.NRGBA{}
	}
	if len(s) == 8 {
		v >>= 8
	}
	return color.NRGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 255}
}

// info builds the Info of the open project.
func (h *handle) info() (*Info, error) {
	in := &Info{
		ID: h.id, Name: h.meta.Name, Revision: h.meta.Revision, Created: h.meta.Created, Updated: h.meta.Updated,
		SourcePath: h.meta.SourcePath, AppVersion: h.appVersion(),
	}
	cfg := h.p.Settings
	if cfg != nil {
		in.Printer = cfg.String("printer_settings_id")
		in.Process = cfg.String("print_settings_id")
		types := cfg.List("filament_type")
		ids := cfg.List("filament_ids")
		colours := cfg.List("filament_colour")
		support := cfg.List("filament_is_support")
		for i, name := range cfg.List("filament_settings_id") {
			f := FilamentInfo{Index: i + 1, Preset: name}
			if i < len(types) {
				f.Type = types[i]
			}
			if i < len(ids) {
				f.FilamentID = ids[i]
			}
			if i < len(colours) {
				f.Colour = colours[i]
			}
			if i < len(support) {
				f.IsSupport = support[i] == "1"
			}
			in.Filaments = append(in.Filaments, f)
		}
		names := make([]string, len(in.Filaments))
		for i, f := range in.Filaments {
			names[i] = f.Preset
		}
		for i, sp := range h.spoolInfos(names) {
			in.Filaments[i].Spool = sp
		}
		in.FlushMode = "auto"
		if cfg.String("flush_volumes_changed") == "1" {
			in.FlushMode = "manual"
		}
		in.FlushMultiplier = cfg.String("flush_multiplier")
		in.FlushMatrix = cfg.List("flush_volumes_matrix")
		for i, d := range cfg.List("different_settings_to_system") {
			for _, k := range strings.Split(d, ";") {
				if k != "" {
					in.OverrideKeys = append(in.OverrideKeys, k)
					_ = i
				}
			}
		}
		in.Overrides = len(in.OverrideKeys)
	}
	for _, pl := range h.p.Plates {
		pi := PlateInfo{Index: pl.Index, Name: pl.Name, Objects: len(pl.Instances), Locked: pl.Locked,
			BedType: pl.Config.Value("bed_type"), PrintSequence: pl.Config.Value("print_sequence")}
		if pi.BedType == "" && cfg != nil {
			pi.BedType = cfg.String("curr_bed_type")
		}
		if pi.PrintSequence == "" && cfg != nil {
			pi.PrintSequence = cfg.String("print_sequence")
		}
		if cfg != nil {
			if key := bedTempKey(pi.BedType); key != "" {
				pi.BedTemps = cfg.List(key)
			}
		}
		pi.Actions = h.layerActions(pl.Index)
		in.Plates = append(in.Plates, pi)
	}
	geo := h.geometry()
	for _, o := range h.p.Objects {
		oi := h.objectInfo(o, geo)
		in.Objects = append(in.Objects, oi)
		if oi.Painted {
			in.Painted = append(in.Painted, oi.Name)
		}
	}
	in.SlicedInFile = len(h.p.SliceResults()) > 0
	if h.meta.LastSlice != nil {
		in.LastSlice = h.meta.LastSlice.stamp(h.meta)
	}
	in.Warnings = h.warnings(in, geo)
	if pin, err := h.presets(); err == nil {
		in.Drift = pin.drift()
	}
	return in, nil
}

func (h *handle) appVersion() string {
	if v := h.p.Creality.Value("AppVersion"); v != "" {
		return v
	}
	app := h.p.Metadata.Value("Application")
	return strings.TrimSpace(strings.TrimPrefix(app, "Creality_Print V"))
}

func (h *handle) objectInfo(o *threemf.Object, geo geometry) ObjectInfo {
	oi := ObjectInfo{ID: o.ID, Name: o.Name, Filament: o.Extruder(), Overrides: overrideCount(o.Config), Ranges: len(o.LayerRanges), Painted: o.Painted().Any()}
	if oi.Filament == 0 {
		oi.Filament = 1
	}
	for _, lr := range o.LayerRanges {
		ri := RangeInfo{From: lr.MinZ, To: lr.MaxZ, Settings: map[string]string{}}
		for _, kv := range lr.Options {
			ri.Settings[kv.Key] = kv.Value
		}
		oi.HeightRanges = append(oi.HeightRanges, ri)
	}
	for _, part := range o.Parts {
		oi.Parts = append(oi.Parts, PartInfo{ID: part.ID, Name: part.Name, Subtype: part.Subtype, Overrides: overrideCount(part.Config), Painted: part.Mesh.Painted.Any()})
		if part.Subtype == threemf.SubtypeNormal {
			oi.Triangles += part.Mesh.Triangles
		}
	}
	items := h.p.ItemsOf(o.ID)
	oi.Instances = len(items)
	if pl := h.p.PlateOf(o.ID, 0); pl != nil {
		oi.Plate = pl.Index
		for _, in := range pl.Instances {
			if in.ObjectID == o.ID && in.InstanceID == 0 {
				oi.IdentifyID = in.IdentifyID
			}
		}
	}
	if len(items) == 0 {
		return oi
	}
	rel := h.itemT(o.ID, 0)
	for i, part := range o.Parts {
		if part.Subtype == threemf.SubtypeNormal {
			continue
		}
		if m, err := h.p.LoadMesh(part); err == nil && m != nil {
			if b, ok := bboxOf(m, part.ComponentTransform.Then(rel)); ok {
				oi.Parts[i].Size, oi.Parts[i].Center = size3(b), center3(b)
			}
		}
	}
	oi.Scale, oi.Rotation = decompose(rel)
	if m, err := h.objectMesh(o); err == nil {
		if b, ok := bboxOf(m, rel); ok {
			oi.Size = size3(b)
			c := center3(b)
			oi.Position = [3]float64{round6(c[0]), round6(c[1]), round6(float64(b.Min[2]))}
			oi.Outside = !geo.rect().contains(footprint(b)) || (geo.MaxHeight > 0 && float64(b.Max[2]) > geo.MaxHeight+1e-3)
		}
	}
	return oi
}

// warnings lists what is wrong or worth knowing about a project.
func (h *handle) warnings(in *Info, geo geometry) []Warning {
	var w []Warning
	add := func(code, format string, a ...any) { w = append(w, Warning{code, fmt.Sprintf(format, a...)}) }
	for _, o := range in.Objects {
		if o.Outside {
			add("outside_bed", "object %q (id %d) is not fully inside the printable area of plate %d", o.Name, o.ID, o.Plate)
		}
		if o.Filament > len(in.Filaments) {
			add("missing_filament", "object %q uses filament %d but the project has %d filament(s)", o.Name, o.Filament, len(in.Filaments))
		}
	}
	// Printing by object: the clearance rules of the slicer (-63), and the crash
	// of 7.2.2 on several filaments printed by layer (D2, D3).
	for _, pl := range in.Plates {
		if is := h.seqIssues(pl.Index); len(is) > 0 {
			var parts []string
			for _, x := range is {
				parts = append(parts, x.text())
			}
			add("sequence_clearance", "plate %d is printed by object and will fail with -63: %s. slice_project with arrange true packs the plate with the needed clearance", pl.Index, strings.Join(parts, "; "))
		}
		if h.s.cfg.Install.Dialect == "v72" && h.plateSequence(pl.Index) != "by object" && h.plateFilamentCount(pl.Index) >= 2 {
			add("v72_by_layer_crash", "plate %d uses %d filaments and prints by layer: Creality Print 7.2.2 crashes on that; update to 7.3 or set print_sequence to by object for this plate", pl.Index, h.plateFilamentCount(pl.Index))
		}
	}
	if h.s.cfg.Install.Dialect == "v72" && len(in.Objects) == 0 && len(in.Filaments) >= 2 && h.plateSequence(1) != "by object" {
		add("v72_by_layer_crash", "this project has %d filaments and prints by layer: Creality Print 7.2.2 crashes when one plate uses two or more of them; update to 7.3 or set print_sequence to by object", len(in.Filaments))
	}
	// By layer, a plate that mixes filaments gets a prime tower: purge material
	// and time that a single colour print does not spend. Not on 7.2, where the
	// same plate crashes the slicer and v72_by_layer_crash is the warning.
	if h.s.cfg.Install.Dialect != "v72" && h.p.Settings != nil && h.p.Settings.String("enable_prime_tower") == "1" {
		var towered []int
		for _, pl := range in.Plates {
			if h.plateSequence(pl.Index) != "by object" && h.plateFilamentCount(pl.Index) >= 2 {
				towered = append(towered, pl.Index)
			}
		}
		if len(in.Objects) == 0 && len(in.Filaments) >= 2 && h.plateSequence(1) != "by object" {
			towered = []int{1}
		}
		if len(towered) > 0 {
			add("prime_tower", "plate(s) %v print several filaments by layer: the slicer adds a prime tower with its own purge material and print time on top of the objects; every filament change also purges a flush volume (flush_volumes_matrix times flush_multiplier) into waste, often several times the tower itself (slice_project reports the tower grams and the flush per plate after slicing); when each object uses one filament, print_sequence by object removes the tower and most of the flush (a change between objects of different filaments still flushes)", towered)
		}
	}
	// A range without layer_height crashes the slicer (found when a project with
	// a range from another tool is opened).
	for _, o := range in.Objects {
		for i, r := range o.HeightRanges {
			if r.Settings["layer_height"] == "" {
				add("range_no_layer_height", "height range %d of object %q sets no layer_height: the slicer crashes on it; set_height_ranges rewrites the ranges with one", i+1, o.Name)
			}
		}
	}
	// Layer actions that cannot do what they say (PR5, verified with 7.3 on the
	// K2: tool changes, pauses and custom G-code are honoured; a colour change
	// writes nothing because the printer preset has no colour change G-code).
	for _, pl := range in.Plates {
		top := 0.0
		for _, o := range in.Objects {
			if o.Plate == pl.Index && o.Position[2]+o.Size[2] > top {
				top = o.Position[2] + o.Size[2]
			}
		}
		for _, a := range pl.Actions {
			if top > 0 && a.Z > top+1e-6 {
				add("action_above_model", "the %s at layer %d (z %.2f mm) on plate %d is above the top of its objects (%.2f mm) and does nothing", strings.ReplaceAll(a.Kind, "_", " "), a.Layer, a.Z, pl.Index, top)
			}
			if a.Kind == ActionColorChange && (h.p.Settings == nil || h.p.Settings.String("color_change_gcode") == "") {
				add("color_change_no_gcode", "the colour change at layer %d on plate %d writes nothing: the printer preset has no colour change G-code; call set_layer_actions again with the same actions to store it as a filament (tool) change", a.Layer, pl.Index)
			}
		}
	}
	// Creality Print applies layer filament changes only when every object of the
	// plate prints with one filament (ToolOrdering.cpp: object_extruders().size()
	// == 1 before custom_tool_changes); with more they are dropped silently.
	for _, pl := range in.Plates {
		var layers []string
		for _, a := range pl.Actions {
			if a.Kind == ActionToolChange {
				layers = append(layers, strconv.Itoa(a.Layer))
			}
		}
		if len(layers) == 0 {
			continue
		}
		fils, painted := h.plateExtruders(pl.Index)
		if len(fils) < 2 && !painted {
			continue
		}
		var names []string
		for _, f := range fils {
			names = append(names, strconv.Itoa(f))
		}
		here := "filaments " + strings.Join(names, ", ")
		if painted {
			here += " and painted colours"
		}
		add("layer_tool_change_ignored", "the tool changes at layer(s) %s on plate %d have no effect: Creality Print applies layer filament changes only when every object on the plate prints with one filament (here: %s); put the objects that change filament on their own plate, or give them all the same filament", strings.Join(layers, ", "), pl.Index, here)
	}
	if in.LastSlice != nil && in.LastSlice.Stale {
		add("stale_slice", "plate(s) %s changed after they were sliced (the project is at revision %d): slice them again", intList(in.LastSlice.StalePlates), in.Revision)
	}
	if len(in.Painted) > 0 {
		add("painted", "painted data (supports, seams, colours) on: %s; the tools keep it but cannot edit it", strings.Join(in.Painted, ", "))
	}
	if pin, err := h.presets(); err == nil {
		if m := pin.Printer.String("printer_model"); m != "" && !strings.EqualFold(m, K2Model) {
			add("other_printer", "the printer %q is not a %s: only the K2 Combo is tested", pin.Printer.Name, K2Model)
		}
		if ok, _ := profiles.Compatible(pin.Printer, pin.Process); !ok {
			add("incompatible_presets", "the process preset %q is not compatible with the printer %q", pin.Process.Name, pin.Printer.Name)
		}
		for _, f := range pin.Filaments {
			if ok, _ := profiles.Compatible(pin.Printer, f); !ok {
				add("incompatible_presets", "the filament preset %q is not compatible with the printer %q", f.Name, pin.Printer.Name)
			}
		}
	} else if ae := AsError(err); ae.Code == CodeNotFound {
		add("missing_preset", "%s", ae.Message)
	}
	if h.p.Settings == nil {
		add("no_settings", "the project has no project_settings.config")
	}
	if v := h.appVersion(); v != "" && h.s.cfg.Install.Version != "" && threemfNewer(v, h.s.version()) {
		add("newer_file", "the file was saved by Creality Print %s, newer than the installed %s", v, h.s.version())
	}
	return w
}

func firstN(s []string, n int) []string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func ellipsis(total, shown int) string {
	if total > shown {
		return ", ..."
	}
	return ""
}

// threemfNewer reports whether version a (dotted numbers) is newer than b.
func threemfNewer(a, b string) bool { return profiles.CompareVersions(a, b) > 0 }

// sortedKeys returns the keys of a set, sorted.
func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// overrideCount counts the setting overrides of an object or part; the
// filament ("extruder") is not a setting override, it has its own field.
func overrideCount(kv threemf.KVs) int {
	n := 0
	for _, e := range kv {
		if e.Key != "extruder" {
			n++
		}
	}
	return n
}

// bedTempKey is the filament setting that holds the first layer bed
// temperature for a plate type ("Textured PEI Plate" is textured_plate_temp_initial_layer).
func bedTempKey(bedType string) string {
	switch bedType {
	case "Cool Plate":
		return "cool_plate_temp_initial_layer"
	case "Engineering Plate":
		return "eng_plate_temp_initial_layer"
	case "High Temp Plate":
		return "hot_plate_temp_initial_layer"
	case "Textured PEI Plate":
		return "textured_plate_temp_initial_layer"
	case "Customized Plate":
		return "customized_plate_temp_initial_layer"
	case "Epoxy Resin Plate":
		return "epoxy_resin_plate_temp_initial_layer"
	}
	return ""
}
