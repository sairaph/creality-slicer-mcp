package projects

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/sairaph/creality-slicer-mcp/internal/catalog"
	"github.com/sairaph/creality-slicer-mcp/internal/threemf"
)

// Setting scopes of update_settings.
const (
	ScopeProject    = "project"
	ScopeObject     = "object"
	ScopePart       = "part"
	ScopeLayerRange = "layer_range"
	ScopePlate      = "plate"
)

// SettingsRequest is update_settings.
type SettingsRequest struct {
	Scope string // project (default), object, part, layer_range or plate
	// Target names what is changed: an object id or name; a part as
	// "object/part" (part id or name); a height range as "object/N" (N from 1);
	// a plate number. Empty for the project scope.
	Target string
	// Targets is Target for several objects, parts, height ranges or plates of
	// one scope (exclusive with Target): all are checked, and nothing changes
	// unless every one passes; one revision.
	Targets []string
	// Values maps setting keys to values: string, number, bool or a list for
	// vector settings. nil removes the override.
	Values      map[string]any
	AllowLocked bool
}

// Change is one setting that changed.
type Change struct {
	Key     string
	Label   string
	Scope   string
	Target  string
	Old     string
	New     string
	Removed bool
	// Forced is true for a change the app itself would make as a consequence of
	// another (forced_by).
	Forced bool
	Note   string
}

// SettingsResult reports the change.
type SettingsResult struct {
	Info     *Info
	Changed  []Change
	Forced   []Change
	Warnings []string
}

// keyErrors collects one validation problem per key.
type keyErrors struct {
	list []string
	// filHint is the hint of a filament number that does not exist; ext counts
	// the problems of that kind. When every problem is one, it is the hint.
	filHint string
	ext     int
}

func (k *keyErrors) add(key, msg string) { k.list = append(k.list, fmt.Sprintf("%s: %s", key, msg)) }

// addFilament records a filament number the project does not have.
func (k *keyErrors) addFilament(key, msg, hint string) {
	k.add(key, msg)
	k.filHint = hint
	k.ext++
}

func (k *keyErrors) err(hint string) error {
	if len(k.list) == 0 {
		return nil
	}
	if k.ext == len(k.list) {
		hint = k.filHint
	}
	e := invalidf(hint, "%s", strings.Join(k.list, "; "))
	e.Fields = map[string]any{"errors": k.list}
	return e
}

// reservedProjectKeys are managed by set_presets or by the tools themselves
// and cannot be set through update_settings.
var reservedProjectKeys = map[string]string{
	"filament_colour": "set_presets", "filament_settings_id": "set_presets", "printer_settings_id": "set_presets",
	"print_settings_id": "set_presets", "filament_ids": "set_presets", "flush_volumes_matrix": "set_presets",
	"flush_multiplier": "set_presets", "flush_volumes_changed": "set_presets", "flush_volumes_vector": "set_presets",
	"transmittance_matrix": "set_presets", "different_settings_to_system": "", "name": "", "from": "", "version": "",
	"print_compatible_printers": "set_presets",
}

// plateKeyName maps a plate scope setting to the plate metadata key.
var plateKeyName = map[string]string{
	"curr_bed_type": "bed_type", "print_sequence": "print_sequence",
	"first_layer_print_sequence": "first_layer_print_sequence", "other_layers_print_sequence": "other_layers_print_sequence",
	"other_layers_print_sequence_nums": "other_layers_print_sequence_nums", "spiral_mode": "spiral_mode",
}

func (h *handle) objectByRef(ref string) (*threemf.Object, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, invalidf("give the object id or name from get_project", "no object given")
	}
	if id, err := strconv.Atoi(ref); err == nil {
		if o := h.p.Object(id); o != nil {
			return o, nil
		}
	}
	var hits []*threemf.Object
	for _, o := range h.p.Objects {
		if strings.EqualFold(o.Name, ref) {
			hits = append(hits, o)
		}
	}
	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		return nil, notFoundf(h.objectNamesHint(), "project %s has no object %q", h.id, ref)
	}
	var ids []string
	for _, o := range hits {
		ids = append(ids, strconv.Itoa(o.ID))
	}
	return nil, invalidf("use the object id", "%d objects are named %q (ids %s)", len(hits), ref, strings.Join(ids, ", "))
}

func (h *handle) partByRef(o *threemf.Object, ref string) (*threemf.Part, error) {
	ref = strings.TrimSpace(ref)
	if id, err := strconv.Atoi(ref); err == nil {
		if p := o.Part(id); p != nil {
			return p, nil
		}
	}
	var hits []*threemf.Part
	for _, p := range o.Parts {
		if strings.EqualFold(p.Name, ref) {
			hits = append(hits, p)
		}
	}
	if len(hits) == 1 {
		return hits[0], nil
	}
	names := make([]string, len(o.Parts))
	for i, p := range o.Parts {
		names[i] = fmt.Sprintf("%q", p.Name)
	}
	hint := "the parts of " + fmt.Sprintf("%q", o.Name) + " are " + strings.Join(names, ", ") + " (get_project shows them)"
	if len(hits) > 1 {
		hint = "several parts are named " + fmt.Sprintf("%q", ref) + ": use the part id; " + hint
	}
	return nil, notFoundf(hint, "object %q has no unique part %q", o.Name, ref)
}

// objectNamesHint lists the names of the project's objects as get_project shows
// them, for the hint of an object that was not found.
func (h *handle) objectNamesHint() string {
	if len(h.p.Objects) == 0 {
		return "the project has no objects: add_model adds one"
	}
	names := make([]string, len(h.p.Objects))
	for i, o := range h.p.Objects {
		names[i] = o.Name
	}
	return "the objects are " + compactNames(names, 40) + " (get_project shows them with their ids)"
}

var numberedRE = regexp.MustCompile(`^(.*?)(\d+)$`)

// compactNames lists names for a hint: runs of numbered names (cube_1, cube_2,
// ...) collapse to "cube_1 .. cube_400 (400)", and after maxEntries entries the
// rest is counted. It bounds what the hint shows, not any data.
func compactNames(names []string, maxEntries int) string {
	var entries []string
	for i := 0; i < len(names); {
		j := i
		m := numberedRE.FindStringSubmatch(names[i])
		if m != nil {
			for j+1 < len(names) {
				n := numberedRE.FindStringSubmatch(names[j+1])
				if n == nil || n[1] != m[1] {
					break
				}
				j++
			}
		}
		if j > i {
			entries = append(entries, fmt.Sprintf("%q .. %q (%d)", names[i], names[j], j-i+1))
		} else {
			entries = append(entries, fmt.Sprintf("%q", names[i]))
		}
		i = j + 1
	}
	if len(entries) > maxEntries {
		rest := len(entries) - maxEntries
		entries = append(entries[:maxEntries], fmt.Sprintf("and %d more: call get_project", rest))
	}
	return strings.Join(entries, ", ")
}

func (h *handle) plateByRef(ref string) (*threemf.Plate, error) {
	n, err := strconv.Atoi(strings.TrimSpace(ref))
	if err != nil {
		return nil, invalidf("give the plate number, from 1", "%q is not a plate number", ref)
	}
	pl := h.p.Plate(n)
	if pl == nil {
		return nil, notFoundf("call get_project to see the plates", "project %s has no plate %d", h.id, n)
	}
	return pl, nil
}

// UpdateSettings changes settings at one scope. Every value is validated
// against the catalog first (unknown key, type, choice, range, scope, vendor
// lock); nothing is changed unless all pass.
func (s *Store) UpdateSettings(ref string, req SettingsRequest) (*SettingsResult, error) {
	scope := req.Scope
	if scope == "" {
		scope = ScopeProject
	}
	if len(req.Values) == 0 {
		return nil, invalidf("give values: {key: value}", "no settings given")
	}
	res := &SettingsResult{}
	if len(req.Targets) > 0 {
		if req.Target != "" {
			return nil, invalidf("give target or targets, not both", "target and targets contradict each other")
		}
		if scope == ScopeProject {
			return nil, invalidf("targets applies to the object, part, layer_range and plate scopes", "the project scope has no targets")
		}
	}
	err := s.write(ref, func(h *handle) error {
		var err error
		one := func(r SettingsRequest) error {
			switch scope {
			case ScopeProject:
				return h.updateProject(r, res)
			case ScopeObject:
				return h.updateObject(r, res)
			case ScopePart:
				return h.updatePart(r, res)
			case ScopeLayerRange:
				return h.updateRange(r, res)
			case ScopePlate:
				return h.updatePlate(r, res)
			}
			return invalidf("scopes: project, object, part, layer_range, plate", "unknown scope %q", scope)
		}
		if len(req.Targets) == 0 {
			err = one(req)
		} else {
			seen := map[string]bool{}
			for _, t := range req.Targets {
				t = strings.TrimSpace(t)
				if t == "" || seen[t] {
					continue
				}
				seen[t] = true
				r := req
				r.Target, r.Targets = t, nil
				if err = one(r); err != nil {
					// A whole call is one change: the first target that fails stops it
					// and the file is not saved.
					var ae *Error
					if errors.As(err, &ae) {
						ae.Message = fmt.Sprintf("target %q: %s", t, ae.Message)
					}
					break
				}
			}
			res.Warnings = dedupeStrings(res.Warnings)
		}
		if err == nil && !res.effective() {
			// Nothing changes: the project is not saved and its revision stays.
			h.changed, h.allPlates, h.plates = false, false, nil
		}
		return err
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

// dedupeStrings drops repeated entries, keeping the first of each.
func dedupeStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func sortedValueKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (h *handle) validate(key string, v any, scope catalog.Scope, allowLocked bool, errs *keyErrors) (*catalog.Option, bool) {
	cat := h.s.cfg.Catalog
	if v == nil {
		o, ok := cat.Get(key)
		if !ok {
			errs.add(key, cat.Validate(key, "", scope, catalog.ValidateOptions{}).Error())
			return nil, false
		}
		if !o.HasScope(scope) {
			errs.add(key, fmt.Sprintf("cannot be set at scope %q", scope))
			return nil, false
		}
		return o, true
	}
	if err := cat.Validate(key, v, scope, catalog.ValidateOptions{AllowLocked: allowLocked, IsCrealityPreset: true}); err != nil {
		errs.add(key, err.Error())
		return nil, false
	}
	o, _ := cat.Get(key)
	return o, true
}

func label(o *catalog.Option) string { return o.Title() }

// isFilamentKey is true for the settings that hold a filament number: the
// extruder of an object, part or range and the filament of a feature (walls,
// infill, supports).
func isFilamentKey(key string) bool {
	if key == "extruder" {
		return true
	}
	for _, k := range roleFilamentKeys {
		if k == key {
			return true
		}
	}
	return false
}

// checkExtruder checks a filament number against the project's filaments: min 1
// for an object, min 0 for a part or a height range (0 = the object's own).
func (h *handle) checkExtruder(key string, opt *catalog.Option, v any, min int, errs *keyErrors) {
	nfil := 1
	if h.p.Settings != nil {
		nfil = len(h.p.Settings.List("filament_settings_id"))
	}
	if n, err := strconv.Atoi(objectValue(opt, v)); err != nil || n < min || n > nfil {
		msg := fmt.Sprintf("the project has %d filament(s), so the number must be from %d to %d", nfil, min, nfil)
		if min == 0 {
			msg += " (0 is the object's own filament)"
		}
		hint := fmt.Sprintf("use a filament number from %d to %d, or add a filament with set_presets", min, nfil)
		if nfil == 1 {
			hint = "add a filament with set_presets first, then use its number"
		}
		errs.addFilament(key, msg, hint)
	}
}

// updateProject changes project_settings.config.
func (h *handle) updateProject(req SettingsRequest, res *SettingsResult) error {
	cfg, err := h.cfg()
	if err != nil {
		return err
	}
	in, perr := h.presets() // needed for reverting and for the change list; may be unavailable
	var errs keyErrors
	type plan struct {
		o      *catalog.Option
		v      val
		remove bool
		tower  bool     // a per plate wipe tower vector: v is in scene coordinates
		rel    []string // the plate relative values as given
	}
	plans := map[string]plan{}
	n := len(cfg.List("filament_settings_id"))
	for _, key := range sortedValueKeys(req.Values) {
		if by, reserved := reservedProjectKeys[key]; reserved {
			hint := "this key is managed by the tools"
			if by != "" {
				hint = "use " + by
			}
			errs.add(key, "cannot be set here ("+hint+")")
			continue
		}
		raw := req.Values[key]
		o, ok := h.validate(key, raw, catalog.ScopePreset, req.AllowLocked, &errs)
		if !ok {
			continue
		}
		if isFilamentKey(key) && raw != nil {
			h.checkExtruder(key, o, raw, 0, &errs) // the project value of a role filament: 0 or a filament of the project
		}
		if _, exists := cfg.Get(key); !exists {
			errs.add(key, "is not part of this project's settings")
			continue
		}
		if isPlateVector(key) {
			// One value per plate, given as plate relative positions.
			plates := len(h.p.Plates)
			if raw == nil {
				def := towerDefault(h.s.cfg.Catalog)
				axis := 0
				if key == "wipe_tower_y" {
					axis = 1
				}
				rel := make([]string, plates)
				for i := range rel {
					rel[i] = formatNumber(def[axis])
				}
				plans[key] = plan{o: o, v: lval(towerSceneValues(cfg, key, plates, rel)...), remove: true, tower: true}
				continue
			}
			v := anyToVal(o, raw)
			if len(v.List) != plates {
				errs.add(key, fmt.Sprintf("holds one value per plate, as a position on that plate: give %d value(s), got %d", plates, len(v.List)))
				continue
			}
			plans[key] = plan{o: o, v: lval(towerSceneValues(cfg, key, plates, v.List)...), tower: true, rel: v.List}
			continue
		}
		if raw == nil {
			if !isPresetOption(o) {
				errs.add(key, "has no preset value to go back to; give a value")
				continue
			}
			if perr != nil {
				errs.add(key, "cannot go back to the preset value: "+perr.Error())
				continue
			}
			plans[key] = plan{o: o, remove: true}
			continue
		}
		v := anyToVal(o, raw)
		if optionSlot(o) == slotFilament && isPresetOption(o) && o.IsVector && n > 0 {
			switch {
			case len(v.List) == n:
			case len(v.List) == 1:
				items := make([]string, n)
				for i := range items {
					items[i] = v.List[0]
				}
				v = lval(items...)
			default:
				errs.add(key, fmt.Sprintf("needs one value or %d values (one per filament), got %d", n, len(v.List)))
				continue
			}
		} else if o.IsVector && o.ValueType != "point" && len(v.List) == 1 && !isPlateVector(key) {
			// A per extruder or per nozzle variant vector: one value applies to
			// every entry the project holds (the count comes from the project).
			if cur, ok := cfg.Get(key); ok && len(cur.List) > 1 {
				items := make([]string, len(cur.List))
				for i := range items {
					items[i] = v.List[0]
				}
				v = lval(items...)
			}
		}
		plans[key] = plan{o: o, v: v}
	}
	if err := errs.err("call describe_setting for the valid values of a setting"); err != nil {
		return err
	}
	for _, key := range sortedValueKeys(req.Values) {
		pl := plans[key]
		old, _ := cfg.Get(key)
		nv := pl.v
		if pl.remove && !pl.tower {
			nv = in.baseValue(pl.o)
		}
		ch := Change{Key: key, Label: label(pl.o), Scope: ScopeProject, Old: valString(old), New: valString(nv), Removed: pl.remove}
		if pl.tower { // shown as plate relative positions
			ch.Old = valString(lval(plateVectorRelative(cfg, key, len(h.p.Plates))...))
			cfg.Set(key, nv)
			ch.New = valString(lval(plateVectorRelative(cfg, key, len(h.p.Plates))...))
			if ch.Old == ch.New {
				ch.Note = "unchanged"
			}
			res.Changed = append(res.Changed, ch)
			continue
		}
		if sameValue(pl.o, old, nv) {
			ch.Note = "unchanged" // an equivalent value: the stored text stays as it is
			ch.New = ch.Old
			res.Changed = append(res.Changed, ch)
			continue
		}
		cfg.Set(key, nv)
		res.Changed = append(res.Changed, ch)
	}
	// The app's own side effects (forced_by) and the gates that make a setting inert.
	env := condEnv{get: func(key string) (string, bool) {
		v, ok := cfg.Get(key)
		if !ok {
			return "", false
		}
		return v.First(), true
	}}
	h.applyForced(cfg, req.Values, env, res)
	h.gateWarnings(req.Values, env, res)
	if perr == nil {
		setDiffs(cfg, in)
	} else {
		res.Warnings = append(res.Warnings, "the presets of this project are not installed, so the list of changed settings was not updated: "+perr.Error())
	}
	h.touchAll()
	return nil
}

// applyForced applies the changes the app would make on its own after the
// given settings changed: settings whose forced_by conditions now hold.
func (h *handle) applyForced(cfg *threemf.Config, changed map[string]any, env condEnv, res *SettingsResult) {
	cat := h.s.cfg.Catalog
	for _, o := range cat.Options() {
		if len(o.ForcedBy) == 0 || !isPresetOption(o) {
			continue
		}
		if _, ok := cfg.Get(o.Key); !ok {
			continue
		}
		for _, f := range o.ForcedBy {
			touched := false
			for _, c := range f.When {
				for key := range changed {
					if mentions(c, key) {
						touched = true
					}
				}
			}
			if !touched || !env.holds(f.When) {
				continue
			}
			nv, ok := forcedValue(o, f.Set)
			if !ok {
				continue
			}
			old, _ := cfg.Get(o.Key)
			if valEqual(old, nv) {
				continue
			}
			cfg.Set(o.Key, nv)
			ch := Change{Key: o.Key, Label: label(o), Scope: ScopeProject, Old: valString(old), New: valString(nv), Forced: true,
				Note: "the app sets this automatically when " + strings.Join(condSentences(f.When), " and ")}
			res.Forced = append(res.Forced, ch)
		}
	}
}

func condSentences(cs []*catalog.Cond) []string {
	var out []string
	for _, c := range cs {
		out = append(out, describeCond(c))
	}
	return out
}

// describeCond is a short plain rendering of a condition for notes.
func describeCond(c *catalog.Cond) string {
	if c == nil {
		return ""
	}
	switch c.K {
	case "opt":
		return c.Key
	case "cmp":
		if len(c.A) == 2 {
			return describeCond(c.A[0]) + " " + c.Op + " " + describeCond(c.A[1])
		}
	case "bool", "num", "enum":
		return fmt.Sprint(c.V)
	case "not":
		if len(c.A) == 1 {
			return "not " + describeCond(c.A[0])
		}
	case "and", "or":
		var parts []string
		for _, a := range c.A {
			parts = append(parts, describeCond(a))
		}
		return "(" + strings.Join(parts, " "+c.K+" ") + ")"
	}
	return c.K
}

// forcedValue turns the "set" of a forced_by rule into a project value when it
// is a plain literal.
func forcedValue(o *catalog.Option, set string) (val, bool) {
	set = strings.TrimSpace(set)
	if set == "" {
		return val{}, false
	}
	switch o.ValueType {
	case "bool":
		switch strings.ToLower(set) {
		case "true", "1":
			return boolVal(o, "1"), true
		case "false", "0":
			return boolVal(o, "0"), true
		}
	case "enum":
		if o.Enum != nil {
			for _, v := range o.Enum.Values {
				if v == set {
					return anyToVal(o, v), true
				}
			}
		}
	case "int", "float", "percent", "float_or_percent":
		if _, err := strconv.ParseFloat(strings.TrimSuffix(set, "%"), 64); err == nil {
			return anyToVal(o, set), true
		}
	}
	return val{}, false
}

func boolVal(o *catalog.Option, one string) val {
	if o.IsVector {
		return lval(one)
	}
	return sval(one)
}

// gateWarnings adds a warning for every changed setting the app would ignore
// or hide with the current values.
func (h *handle) gateWarnings(changed map[string]any, env condEnv, res *SettingsResult) {
	cat := h.s.cfg.Catalog
	for _, key := range sortedValueKeys(changed) {
		o, ok := cat.Get(key)
		if !ok || len(o.GatedBy) == 0 {
			continue
		}
		for _, g := range o.GatedBy {
			if g.When == nil || g.Constant != "" {
				continue
			}
			v := env.eval(g.When)
			if b, isBool := v.v.(bool); v.known && isBool && !b {
				d, _ := cat.Deps(key)
				msg := key + " has no effect with the current settings"
				if len(d.Gates) > 0 {
					msg += ": " + strings.Join(d.Gates, " ")
				}
				res.Warnings = append(res.Warnings, msg)
				break
			}
		}
	}
}

// objectValue is the string a per object or per part override holds.
func objectValue(o *catalog.Option, v any) string {
	vv := anyToVal(o, v)
	if vv.IsList {
		return strings.Join(vv.List, ",")
	}
	return vv.Str
}

func (h *handle) updateObject(req SettingsRequest, res *SettingsResult) error {
	o, err := h.objectByRef(req.Target)
	if err != nil {
		return err
	}
	var errs keyErrors
	opts := map[string]*catalog.Option{}
	for _, key := range sortedValueKeys(req.Values) {
		opt, ok := h.validate(key, req.Values[key], catalog.ScopeObject, req.AllowLocked, &errs)
		if !ok {
			continue
		}
		opts[key] = opt
		if isFilamentKey(key) && req.Values[key] != nil {
			min := 0
			if key == "extruder" {
				min = 1 // an object always prints with a filament
			}
			h.checkExtruder(key, opt, req.Values[key], min, &errs)
		}
	}
	if err := errs.err("call describe_setting for the valid values of a setting"); err != nil {
		return err
	}
	for _, key := range sortedValueKeys(req.Values) {
		opt := opts[key]
		old := o.Config.Value(key)
		if req.Values[key] == nil {
			if _, err := h.p.DeleteObjectOverride(o.ID, key); err != nil {
				return errf(CodeInternal, "", "%v", err)
			}
			res.Changed = append(res.Changed, Change{Key: key, Label: label(opt), Scope: ScopeObject, Target: o.Name, Old: old, New: "(from the project)", Removed: true})
			continue
		}
		nv := objectValue(opt, req.Values[key])
		if old != "" && sameValue(opt, textVal(opt, old), anyToVal(opt, req.Values[key])) {
			nv = old // an equivalent value: the stored text stays as it is
		}
		if err := h.p.SetObjectOverride(o.ID, key, nv); err != nil {
			return errf(CodeInternal, "", "%v", err)
		}
		res.Changed = append(res.Changed, Change{Key: key, Label: label(opt), Scope: ScopeObject, Target: o.Name, Old: old, New: nv})
	}
	if _, ok := req.Values["layer_height"]; ok && o.LayerHeightProfile != "" {
		res.Warnings = append(res.Warnings, layerProfileText(o.Name))
	}
	h.touchObject(o.ID)
	return nil
}

func (h *handle) updatePart(req SettingsRequest, res *SettingsResult) error {
	objRef, partRef, ok := strings.Cut(req.Target, "/")
	if !ok {
		return invalidf(`write the target as "object/part"`, "part scope needs a target like object/part, got %q", req.Target)
	}
	o, err := h.objectByRef(objRef)
	if err != nil {
		return err
	}
	part, err := h.partByRef(o, partRef)
	if err != nil {
		return err
	}
	var errs keyErrors
	opts := map[string]*catalog.Option{}
	for _, key := range sortedValueKeys(req.Values) {
		if opt, ok := h.validate(key, req.Values[key], catalog.ScopePart, req.AllowLocked, &errs); ok {
			opts[key] = opt
			if isFilamentKey(key) && req.Values[key] != nil {
				h.checkExtruder(key, opt, req.Values[key], 0, &errs)
			}
		}
	}
	if err := errs.err("call describe_setting for the valid values of a setting"); err != nil {
		return err
	}
	for _, key := range sortedValueKeys(req.Values) {
		opt := opts[key]
		old := part.Config.Value(key)
		if key == "extruder" && len(o.Parts) == 1 {
			// The importer erases the extruder of the only part of an object
			// (bbs_3mf.cpp:2917-2919): the filament goes on the object, where the app keeps it.
			objOld := o.Config.Value("extruder")
			n := 0
			if v := req.Values[key]; v != nil {
				n = atoi0(objectValue(opt, v))
			}
			if n == 0 {
				// 0 (or removing the override) means "the object's own filament",
				// which a single part always has: nothing to write. A leftover part
				// override is removed; the object keeps its filament.
				if old != "" {
					if _, err := h.p.DeletePartOverride(o.ID, part.ID, key); err != nil {
						return errf(CodeInternal, "", "%v", err)
					}
					res.Changed = append(res.Changed, Change{Key: key, Label: label(opt), Scope: ScopePart, Target: o.Name + "/" + part.Name, Old: old, New: "(from the object)", Removed: true})
				}
				res.Warnings = append(res.Warnings, fmt.Sprintf("object %q has one part, so extruder 0 (use the object's filament) changes nothing: the object keeps its filament (update_object filament changes it)", o.Name))
				continue
			}
			if err := h.p.SetObjectOverride(o.ID, "extruder", strconv.Itoa(n)); err != nil {
				return errf(CodeInternal, "", "%v", err)
			}
			if part.Config.Value("extruder") != "" {
				if _, err := h.p.DeletePartOverride(o.ID, part.ID, key); err != nil {
					return errf(CodeInternal, "", "%v", err)
				}
			}
			nv := strconv.Itoa(n)
			res.Changed = append(res.Changed, Change{Key: key, Label: label(opt), Scope: ScopeObject, Target: o.Name, Old: objOld, New: nv,
				Note: "an object with one part keeps its filament on the object; it was set there"})
			res.Warnings = append(res.Warnings, fmt.Sprintf("object %q has one part, and Creality Print keeps the filament of a single part on the object: the filament was set on the object, not on the part", o.Name))
			continue
		}
		if req.Values[key] == nil {
			if _, err := h.p.DeletePartOverride(o.ID, part.ID, key); err != nil {
				return errf(CodeInternal, "", "%v", err)
			}
			res.Changed = append(res.Changed, Change{Key: key, Label: label(opt), Scope: ScopePart, Target: o.Name + "/" + part.Name, Old: old, New: "(from the object)", Removed: true})
			continue
		}
		nv := objectValue(opt, req.Values[key])
		if old != "" && sameValue(opt, textVal(opt, old), anyToVal(opt, req.Values[key])) {
			nv = old // an equivalent value: the stored text stays as it is
		}
		if err := h.p.SetPartOverride(o.ID, part.ID, key, nv); err != nil {
			return errf(CodeInternal, "", "%v", err)
		}
		res.Changed = append(res.Changed, Change{Key: key, Label: label(opt), Scope: ScopePart, Target: o.Name + "/" + part.Name, Old: old, New: nv})
	}
	h.touchObject(o.ID)
	return nil
}

func (h *handle) updateRange(req SettingsRequest, res *SettingsResult) error {
	objRef, idxRef, ok := strings.Cut(req.Target, "/")
	if !ok {
		return invalidf(`write the target as "object/N" with N the height range number from 1`, "layer_range scope needs a target like object/1, got %q", req.Target)
	}
	o, err := h.objectByRef(objRef)
	if err != nil {
		return err
	}
	idx, err := strconv.Atoi(idxRef)
	if err != nil || idx < 1 || idx > len(o.LayerRanges) {
		return notFoundf("set_height_ranges creates height ranges", "object %q has %d height range(s); %q is not one of them", o.Name, len(o.LayerRanges), idxRef)
	}
	var errs keyErrors
	opts := map[string]*catalog.Option{}
	for _, key := range sortedValueKeys(req.Values) {
		if opt, ok := h.validate(key, req.Values[key], catalog.ScopeLayerRange, req.AllowLocked, &errs); ok {
			opts[key] = opt
			if isFilamentKey(key) && req.Values[key] != nil {
				h.checkExtruder(key, opt, req.Values[key], 0, &errs)
			}
		}
	}
	if err := errs.err("call describe_setting for the valid values of a setting"); err != nil {
		return err
	}
	ranges := append([]threemf.LayerRange(nil), o.LayerRanges...)
	r := ranges[idx-1]
	r.Options = append(threemf.KVs(nil), r.Options...)
	for _, key := range sortedValueKeys(req.Values) {
		opt := opts[key]
		old := r.Options.Value(key)
		if req.Values[key] == nil {
			if key == "layer_height" {
				return invalidf("give a layer height, or remove the whole range with set_height_ranges", "layer_height is always part of a height range (the slicer crashes without it) and cannot be removed")
			}
			r.Options.Delete(key)
			res.Changed = append(res.Changed, Change{Key: key, Label: label(opt), Scope: ScopeLayerRange, Target: req.Target, Old: old, New: "(from the object)", Removed: true})
			continue
		}
		nv := objectValue(opt, req.Values[key])
		if old != "" && sameValue(opt, textVal(opt, old), anyToVal(opt, req.Values[key])) {
			nv = old // an equivalent value: the stored text stays as it is
		}
		r.Options.Set(key, nv)
		res.Changed = append(res.Changed, Change{Key: key, Label: label(opt), Scope: ScopeLayerRange, Target: req.Target, Old: old, New: nv})
	}
	if len(r.Options) == 0 {
		return invalidf("remove the whole range with set_height_ranges", "a height range needs at least one setting")
	}
	ranges[idx-1] = r
	if err := h.p.SetLayerRanges(o.ID, ranges); err != nil {
		return errf(CodeInternal, "", "%v", err)
	}
	h.touchObject(o.ID)
	return nil
}

func (h *handle) updatePlate(req SettingsRequest, res *SettingsResult) error {
	pl, err := h.plateByRef(req.Target)
	if err != nil {
		return err
	}
	var errs keyErrors
	opts := map[string]*catalog.Option{}
	for _, key := range sortedValueKeys(req.Values) {
		if _, known := plateKeyName[key]; !known {
			errs.add(key, "is not a plate setting; plate settings: "+strings.Join(plateSettingNames(), ", "))
			continue
		}
		if req.Values[key] == nil {
			opts[key], _ = h.s.cfg.Catalog.Get(key)
			continue
		}
		if opt, ok := h.validate(key, req.Values[key], catalog.ScopePlate, req.AllowLocked, &errs); ok {
			opts[key] = opt
		} else if o, found := h.s.cfg.Catalog.Get(key); found && o.HasScope(catalog.ScopePreset) && !o.HasScope(catalog.ScopePlate) {
			// curr_bed_type is a printer setting that a plate may override: accept
			// the preset scope check for the plate keys.
			errs.list = errs.list[:len(errs.list)-1]
			if err := h.s.cfg.Catalog.Validate(key, req.Values[key], catalog.ScopePreset, catalog.ValidateOptions{AllowLocked: true}); err != nil {
				errs.add(key, err.Error())
			} else {
				opts[key] = o
			}
		}
	}
	if err := errs.err("plate settings: " + strings.Join(plateSettingNames(), ", ")); err != nil {
		return err
	}
	for _, key := range sortedValueKeys(req.Values) {
		name := plateKeyName[key]
		old := pl.Config.Value(name)
		lab := key
		if opts[key] != nil {
			lab = label(opts[key])
		}
		if req.Values[key] == nil {
			if _, err := h.p.DeletePlateKey(pl.Index, name); err != nil {
				return errf(CodeInternal, "", "%v", err)
			}
			res.Changed = append(res.Changed, Change{Key: key, Label: lab, Scope: ScopePlate, Target: strconv.Itoa(pl.Index), Old: old, New: "(from the project)", Removed: true})
			continue
		}
		nv := plateValue(opts[key], req.Values[key])
		if old != "" && opts[key] != nil && sameValue(opts[key], textVal(opts[key], old), anyToVal(opts[key], req.Values[key])) {
			nv = old
		}
		if err := h.p.SetPlateKey(pl.Index, name, nv); err != nil {
			return errf(CodeInternal, "", "%v", err)
		}
		res.Changed = append(res.Changed, Change{Key: key, Label: lab, Scope: ScopePlate, Target: strconv.Itoa(pl.Index), Old: old, New: nv})
	}
	h.touchPlate(pl.Index)
	return nil
}

func plateSettingNames() []string {
	keys := make([]string, 0, len(plateKeyName))
	for k := range plateKeyName {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// plateValue is the metadata text of a plate setting: booleans as true and
// false (the importer reads spiral_mode with std::boolalpha, so "1" would read
// as false: bbs_3mf.cpp:5284-5287, the app writes true and false), integer lists
// separated by spaces. Both spellings are read back (normElem).
func plateValue(o *catalog.Option, v any) string {
	if o == nil {
		return fmt.Sprint(v)
	}
	vv := anyToVal(o, v)
	if vv.IsList {
		return strings.Join(vv.List, " ")
	}
	if o.ValueType == "bool" {
		switch normElem(o, vv.Str) {
		case "1":
			return "true"
		case "0":
			return "false"
		}
	}
	return vv.Str
}

// effective reports whether a result changed anything: a value set to what it
// already was, or an override removed that did not exist, is not a change. It
// marks those changes "unchanged".
func (r *SettingsResult) effective() bool {
	any := len(r.Forced) > 0
	for i := range r.Changed {
		c := &r.Changed[i]
		noop := (!c.Removed && c.Old == c.New) || (c.Removed && c.Old == "" && c.Scope != ScopeProject)
		if c.Note == "unchanged" {
			noop = true
		}
		if noop {
			c.Note = "unchanged"
			continue
		}
		any = true
	}
	return any
}

// textVal reads the stored text of an object, part or range override as a value
// of its option (vectors are comma separated there).
func textVal(o *catalog.Option, s string) val {
	if o != nil && o.IsVector {
		return lval(strings.Split(s, ",")...)
	}
	return sval(s)
}

// normElem brings one element to a comparable form: numbers without trailing
// zeros, a percent key without its "%", booleans as 1 and 0.
func normElem(o *catalog.Option, s string) string {
	s = strings.Trim(strings.TrimSpace(s), `"`)
	if o == nil {
		return s
	}
	switch o.ValueType {
	case "bool":
		switch strings.ToLower(s) {
		case "true", "yes", "on":
			return "1"
		case "false", "no", "off":
			return "0"
		}
	}
	// Numbers without trailing zeros; a percent key has no sign to compare, the
	// others keep theirs (15 mm and 15 % differ for a float or percent setting).
	num, pct := s, false
	if strings.HasSuffix(s, "%") {
		num, pct = strings.TrimSuffix(s, "%"), true
	}
	if f, err := strconv.ParseFloat(num, 64); err == nil {
		if pct && o.ValueType != "percent" {
			return formatNumber(f) + "%"
		}
		return formatNumber(f)
	}
	return s
}

// sameValue reports whether two values of an option are the same setting: the
// catalog decides what equal means (15 and 15% for a percent, 0.2 and 0.20,
// true and 1, vectors element by element).
func sameValue(o *catalog.Option, a, b val) bool {
	ea, eb := valElems(a), valElems(b)
	if len(ea) != len(eb) || a.IsList != b.IsList && len(ea) != 1 {
		return false
	}
	for i := range ea {
		if normElem(o, ea[i]) != normElem(o, eb[i]) {
			return false
		}
	}
	return true
}
