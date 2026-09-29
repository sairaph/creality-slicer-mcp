package projects

import (
	"sort"
	"strconv"
	"strings"

	"github.com/sairaph/creality-slicer-mcp/internal/catalog"
	"github.com/sairaph/creality-slicer-mcp/internal/flush"
	"github.com/sairaph/creality-slicer-mcp/internal/profiles"
	"github.com/sairaph/creality-slicer-mcp/internal/threemf"
)

// excludedKeys are catalog settings of the preset types that the app does not
// write into project_settings.config (compatibility lists, print host
// credentials, inherits, the retraction override pair). The list is the
// difference between the catalog and a project the app saved.
var excludedKeys = map[string]bool{
	"compatible_printers": true, "compatible_printers_condition": true,
	"compatible_prints": true, "compatible_prints_condition": true, "inherits": true,
	"print_host": true, "print_host_webui": true, "printhost_apikey": true, "printhost_cafile": true,
	"printhost_password": true, "printhost_port": true, "printhost_user": true,
	"enable_retraction_distance_when_cut_override": true, "retraction_distance_when_cut_override": true,
}

// extraKeys are the settings the app writes into a project that belong to no
// preset type: project, plate and internal settings with catalog defaults.
var extraKeys = []string{
	"bbl_calib_mark_logo", "dont_slow_down_outer_wall", "first_layer_print_sequence",
	"has_scarf_joint_seam", "infill_combination_max_layer_height", "other_layers_print_sequence",
	"other_layers_print_sequence_nums", "printer_select_mac", "smart_cooling_zones", "start_end_points",
}

// metaKeys of a preset that are identity or file bookkeeping, never settings.
var metaKeys = map[string]bool{
	"type": true, "from": true, "name": true, "instantiation": true, "setting_id": true, "filament_id": true,
	"version": true, "base_id": true, "is_custom_defined": true, "inherits": true, "user_id": true,
	"updated_time": true, "sync_info": true,
}

// diffExcluded are preset owned settings the project sets on its own and that
// therefore never count as a change against the preset.
var diffExcluded = map[string]bool{"curr_bed_type": true}

type slotKind int

const (
	slotPrinter slotKind = iota
	slotProcess
	slotFilament
)

func hasType(o *catalog.Option, names ...string) bool {
	for _, t := range o.PresetTypes {
		for _, n := range names {
			if t == n {
				return true
			}
		}
	}
	return false
}

// isPresetOption reports whether the option belongs to a printer, process or
// filament preset.
func isPresetOption(o *catalog.Option) bool {
	return hasType(o, "print", "process", "printer", "filament")
}

// optionSlot is the preset a setting is taken from. Settings shared by process
// and filament or by process and printer follow the more specific rule of the
// app: filament vectors stay per slot, the printer/process pair follows process.
func optionSlot(o *catalog.Option) slotKind {
	switch {
	case hasType(o, "filament"):
		return slotFilament
	case hasType(o, "print", "process"):
		return slotProcess
	}
	return slotPrinter
}

// composeInput is what a project config is built from.
type composeInput struct {
	Cat       *catalog.Catalog
	Printer   profiles.Preset
	Process   profiles.Preset
	Filaments []profiles.Preset
	Colours   []string // #RRGGBB per slot
	Version   string   // installed version and build, "7.3.0.6149"
	BedType   string   // curr_bed_type; "" leaves the catalog default
	Plates    int      // number of plates (per plate vectors)
	// Manual is a user matrix (N*N numbers); nil computes it from the colours.
	Manual []string
	// FlushMultiplier overrides the printer's default_flush_multiplier when set.
	FlushMultiplier string
}

func (in *composeInput) n() int { return len(in.Filaments) }

func presetVal(v any) val {
	switch t := v.(type) {
	case string:
		return sval(t)
	case []string:
		return lval(t...)
	}
	return sval("")
}

func (in *composeInput) preset(o *catalog.Option) profiles.Preset {
	if optionSlot(o) == slotProcess {
		return in.Process
	}
	return in.Printer
}

// filamentElem is the element of a per slot setting for slot i: the
// filament preset's own value, else the catalog default.
func (in *composeInput) filamentElem(o *catalog.Option, i int) string {
	p := in.Filaments[i]
	raw, ok := p.Values[o.Key]
	if !ok {
		// A setting the preset does not mention: unset ("nil") when the setting can
		// be unset, else the catalog default.
		if o.Nullable {
			return "nil"
		}
		if elems := valElems(defaultValue(o)); len(elems) > 0 {
			return elems[0]
		}
		return ""
	}
	elems := valElems(shape(o, presetVal(raw)))
	if len(elems) == 0 {
		return ""
	}
	return elems[0]
}

// baseValue is the value a setting has straight from the presets (and the
// catalog defaults), before any project change.
func (in *composeInput) baseValue(o *catalog.Option) val {
	if optionSlot(o) == slotFilament {
		if !o.IsVector {
			if in.n() == 0 {
				return defaultValue(o)
			}
			return sval(in.filamentElem(o, 0))
		}
		elems := make([]string, in.n())
		for i := range elems {
			elems[i] = in.filamentElem(o, i)
		}
		return lval(elems...)
	}
	p := in.preset(o)
	if raw, ok := p.Values[o.Key]; ok {
		v := shape(o, presetVal(raw))
		if o.Key == "thumbnails" && !v.IsList {
			return sval(normalizeThumbnails(v.Str))
		}
		return v
	}
	return defaultValue(o)
}

// composed is the result of compose.
type composed struct {
	Cfg *threemf.Config
	// Drift lists preset settings the catalog does not know (the installed
	// profiles are newer than the catalog). The app does not write them into a
	// project either, so they are not copied.
	Drift []string
	// FlushAuto is true when the matrix was computed.
	FlushAuto bool
}

// compose builds a project_settings.config: catalog defaults for every
// setting of the preset types, the printer, process and filament presets on
// top (filament settings as vectors, one element per slot), the project keys,
// the flush matrix and different_settings_to_system.
func compose(in composeInput) (*composed, error) {
	cfg := threemf.NewConfig()
	n := in.n()
	for _, o := range in.Cat.Options() {
		if !isPresetOption(o) || excludedKeys[o.Key] {
			continue
		}
		cfg.Set(o.Key, in.baseValue(o))
	}
	for _, k := range extraKeys {
		if o, ok := in.Cat.Get(k); ok {
			cfg.Set(k, defaultValue(o))
		}
	}
	if in.BedType != "" {
		putValue(in.Cat, cfg, "curr_bed_type", in.BedType)
	}

	cfg.SetString("name", "project_settings")
	cfg.SetString("from", "project")
	cfg.SetString("version", in.Version)
	cfg.SetString("printer_settings_id", in.Printer.Name)
	cfg.SetString("print_settings_id", in.Process.Name)
	names := make([]string, n)
	ids := make([]string, n)
	for i, f := range in.Filaments {
		names[i] = f.Name
		ids[i] = f.String("filament_id")
	}
	cfg.SetList("filament_settings_id", names...)
	cfg.SetList("filament_ids", ids...)
	cfg.SetList("filament_colour", in.Colours...)
	var compat []string
	if raw, ok := in.Process.Values["compatible_printers"]; ok {
		for _, e := range valElems(presetVal(raw)) {
			if strings.TrimSpace(e) != "" {
				compat = append(compat, e)
			}
		}
	}
	cfg.SetList("print_compatible_printers", compat...)

	plates := in.Plates
	if plates < 1 {
		plates = 1
	}
	// One wipe tower position per plate, at the catalog default on every plate
	// (the values are scene coordinates, see tower.go).
	def := towerDefault(in.Cat)
	rel := make([][2]float64, plates)
	for i := range rel {
		rel[i] = def
	}
	setTowerScene(cfg, plates, rel)
	vec := make([]string, 2*n)
	for i := range vec {
		vec[i] = "140"
	}
	cfg.SetList("flush_volumes_vector", vec...)
	trans := make([]string, n*n)
	for i := range trans {
		trans[i] = "0.8"
	}
	cfg.SetList("transmittance_matrix", trans...)

	c := &composed{Cfg: cfg, Drift: in.drift()}
	if err := setFlush(cfg, in, &c.FlushAuto); err != nil {
		return nil, err
	}
	setDiffs(cfg, in)
	return c, nil
}

// setFlush writes flush_multiplier, flush_volumes_matrix and
// flush_volumes_changed. Automatic mode computes the raw matrix from the
// colours with the printer's nozzle volume and stores the printer's default
// multiplier (the app stores the raw matrix and multiplies at slice time).
func setFlush(cfg *threemf.Config, in composeInput, auto *bool) error {
	multiplier := in.FlushMultiplier
	if multiplier == "" {
		multiplier = cfg.String("default_flush_multiplier")
	}
	if multiplier == "" {
		multiplier = "1"
	}
	putValue(in.Cat, cfg, "flush_multiplier", multiplier)
	n := in.n()
	if in.Manual != nil {
		if len(in.Manual) != n*n {
			return errManualMatrix(n, len(in.Manual))
		}
		cfg.SetList("flush_volumes_matrix", in.Manual...)
		putValue(in.Cat, cfg, "flush_volumes_changed", "1")
		*auto = false
		return nil
	}
	fc := flush.Config{}
	for _, k := range []string{"nozzle_volume", "enable_long_retraction_when_cut", "long_retractions_when_cut", "retraction_distances_when_cut",
		"filament_long_retractions_when_cut", "filament_retraction_distances_when_cut"} {
		if v, ok := cfg.Get(k); ok {
			if v.IsList {
				fc[k] = v.List
			} else {
				fc[k] = v.Str
			}
		}
	}
	fc["filament_colour"] = in.Colours
	min, err := flush.MinVolumes(fc)
	if err != nil {
		return err
	}
	var support []bool
	for _, e := range cfg.List("filament_is_support") {
		support = append(support, e == "1")
	}
	if len(support) != n {
		support = nil
	}
	m, err := flush.Matrix(in.Colours, min, support)
	if err != nil {
		return err
	}
	items := make([]string, len(m))
	for i, v := range m {
		items[i] = strconv.Itoa(v)
	}
	cfg.SetList("flush_volumes_matrix", items...)
	putValue(in.Cat, cfg, "flush_volumes_changed", "0")
	*auto = true
	return nil
}

type manualMatrixError struct{ n, got int }

func (e manualMatrixError) Error() string {
	return "the flush matrix needs " + strconv.Itoa(e.n*e.n) + " numbers (" + strconv.Itoa(e.n) + " filaments squared), got " + strconv.Itoa(e.got)
}

func errManualMatrix(n, got int) error { return manualMatrixError{n, got} }

// drift lists the settings the presets carry that the catalog does not define.
func (in *composeInput) drift() []string {
	seen := map[string]bool{}
	var keys []string
	add := func(p profiles.Preset) {
		for k := range p.Values {
			if metaKeys[k] || excludedKeys[k] || seen[k] {
				continue
			}
			seen[k] = true
			keys = append(keys, k)
		}
	}
	add(in.Printer)
	add(in.Process)
	for _, f := range in.Filaments {
		add(f)
	}
	unknown := in.Cat.UnknownKeys(keys)
	sort.Strings(unknown)
	return unknown
}

// slotDiffs lists, per slot, the settings whose value in cfg differs from what
// the preset gives: process first, then one entry per filament slot, then the
// printer, the layout of different_settings_to_system.
func slotDiffs(cfg *threemf.Config, in composeInput) [][]string {
	n := in.n()
	out := make([][]string, n+2)
	for _, o := range in.Cat.Options() {
		if !isPresetOption(o) || excludedKeys[o.Key] || diffExcluded[o.Key] {
			continue
		}
		cur, ok := cfg.Get(o.Key)
		if !ok {
			continue
		}
		switch optionSlot(o) {
		case slotProcess:
			if !valEqual(cur, in.baseValue(o)) {
				out[0] = append(out[0], o.Key)
			}
		case slotPrinter:
			if !valEqual(cur, in.baseValue(o)) {
				out[n+1] = append(out[n+1], o.Key)
			}
		case slotFilament:
			elems := valElems(cur)
			slots := n
			if !o.IsVector {
				slots = min(n, 1) // one value for the whole project: compared with the first slot
			}
			for i := 0; i < slots; i++ {
				if i >= len(elems) || elems[i] != in.filamentElem(o, i) {
					out[1+i] = append(out[1+i], o.Key)
				}
			}
		}
	}
	for i := range out {
		sort.Strings(out[i])
	}
	return out
}

// setDiffs recomputes different_settings_to_system.
func setDiffs(cfg *threemf.Config, in composeInput) {
	diffs := slotDiffs(cfg, in)
	items := make([]string, len(diffs))
	for i, d := range diffs {
		items[i] = strings.Join(d, ";")
	}
	cfg.SetList("different_settings_to_system", items...)
}

// putValue writes a setting the way the catalog says it is stored: a vector
// option (one entry per extruder or nozzle variant, possibly nullable) as a
// list, a scalar option as a string. A single element given for a vector that
// the config already holds with several entries is repeated for every entry,
// so the tools never assume the number of variants.
func putValue(cat *catalog.Catalog, cfg *threemf.Config, key string, elems ...string) {
	o, ok := cat.Get(key)
	if !ok || !o.IsVector {
		cfg.SetString(key, elems[0])
		return
	}
	if n := len(cfg.List(key)); len(elems) == 1 && n > 1 {
		items := make([]string, n)
		for i := range items {
			items[i] = elems[0]
		}
		elems = items
	}
	cfg.SetList(key, elems...)
}
