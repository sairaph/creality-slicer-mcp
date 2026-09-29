package main

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
)

// Build holds everything parsed from one ref.
type Build struct {
	Ref, Commit               string
	CfgCt, HppCt, TabCt, CmCt *CText
	PresetCt, BundleCt        *CText

	EnumMaps   map[string]*EnumMap
	Defs       *DefParser
	Lists      map[string]*KeyList
	Classes    map[string]*StaticClass
	ClassOrder []string
	Gui        *GuiParser
	Gates      []Gate
	Forced     []Forced
	EnumRestr  []EnumRestriction
	UserMode   map[string]string
	DocInfo    map[string]docInfo
	DocBase    string
	warnings   []string
}

type docInfo struct{ URL, Page, Group string }

type EnumRestriction struct {
	Key       string   `json:"key"`
	Condition string   `json:"when"`
	Values    []string `json:"values"`
	Function  string   `json:"function"`
	Line      int      `json:"line"`
}

// ---- catalog structures -----------------------------------------------------

type EnumInfo struct {
	Type         string       `json:"cpp_enum_type,omitempty"`
	ValuesSource string       `json:"values_source"`
	Values       []string     `json:"values"`
	Labels       []string     `json:"labels,omitempty"`
	Options      []EnumOption `json:"options,omitempty"`
}

type EnumOption struct {
	Value string `json:"value"`
	Label string `json:"label,omitempty"`
}

type GuiLoc struct {
	Tab        string   `json:"tab"`
	Page       string   `json:"page"`
	Group      string   `json:"group"`
	LineLabel  string   `json:"line_label,omitempty"`
	Path       string   `json:"path"`
	Widget     bool     `json:"custom_widget,omitempty"`
	Conditions []string `json:"build_conditions,omitempty"`
	Macro      string   `json:"build_macro,omitempty"`
	Source     string   `json:"source"`
}

type Scopes struct {
	PerObject      bool `json:"per_object"`
	PerPartOrMod   bool `json:"per_part_or_modifier"`
	PerLayerRange  bool `json:"per_layer_range"`
	PerPlate       bool `json:"per_plate"`
	ShownPerObject bool `json:"shown_in_per_object_tab"`
}

type SourceRef struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	Function string `json:"function"`
}

type Entry struct {
	ID       string `json:"id"`
	Key      string `json:"key"`
	DefClass string `json:"def_class"`
	Owner    string `json:"owner"`
	OwnerHow string `json:"owner_basis"`

	PresetTypes []string `json:"preset_types"`
	Tech        string   `json:"printer_technology"`
	CliOnly     bool     `json:"cli_only,omitempty"`

	ValueType string `json:"value_type"`
	CoType    string `json:"config_option_type"`
	IsVector  bool   `json:"is_vector"`
	Nullable  bool   `json:"nullable"`
	CppType   string `json:"cpp_storage_type,omitempty"`

	Default       interface{} `json:"default"`
	DefaultRaw    string      `json:"default_raw,omitempty"`
	DefaultClass  string      `json:"default_class,omitempty"`
	DefaultParsed bool        `json:"default_parsed"`

	Min          *float64 `json:"min,omitempty"`
	Max          *float64 `json:"max,omitempty"`
	MinRaw       string   `json:"min_raw,omitempty"`
	MaxRaw       string   `json:"max_raw,omitempty"`
	MinEffective *int     `json:"min_effective_int,omitempty"`
	MaxEffective *int     `json:"max_effective_int,omitempty"`
	MaxLiteral   *float64 `json:"max_literal,omitempty"`

	Enum *EnumInfo `json:"enum,omitempty"`

	Sidetext  string `json:"sidetext,omitempty"`
	Label     string `json:"label,omitempty"`
	FullLabel string `json:"full_label,omitempty"`
	Tooltip   string `json:"tooltip,omitempty"`
	Category  string `json:"category,omitempty"`

	UILevel         string   `json:"ui_level"`
	UILevelExplicit bool     `json:"ui_level_explicit"`
	GuiType         string   `json:"gui_type,omitempty"`
	GuiFlags        string   `json:"gui_flags,omitempty"`
	RatioOver       string   `json:"ratio_over,omitempty"`
	Cli             string   `json:"cli,omitempty"`
	CliFlags        []string `json:"cli_flags,omitempty"`
	CliParams       string   `json:"cli_params,omitempty"`
	Readonly        bool     `json:"readonly,omitempty"`
	Multiline       bool     `json:"multiline,omitempty"`
	FullWidth       bool     `json:"full_width,omitempty"`
	Height          *int     `json:"height,omitempty"`
	Aliases         []string `json:"aliases,omitempty"`

	GuiLocation  string   `json:"gui_location"`
	GuiLocations []GuiLoc `json:"gui_locations,omitempty"`

	Scopes Scopes `json:"scopes"`

	StaticClasses    []string `json:"static_config_classes,omitempty"`
	ExtruderOption   bool     `json:"extruder_option_key,omitempty"`
	ProjectOption    bool     `json:"project_option,omitempty"`
	PerNozzleVariant []string `json:"per_nozzle_variant_sets,omitempty"`

	GatedBy       []Gate   `json:"gated_by,omitempty"`
	ForcedChanges []Forced `json:"forced_by,omitempty"`

	DocURL       string `json:"doc_url,omitempty"`
	VendorPolicy string `json:"creality_vendor_policy,omitempty"`
	TooltipDash  bool   `json:"tooltip_has_em_or_en_dash,omitempty"`

	BuildMacro string `json:"build_macro,omitempty"`

	// OriginHints are comments the code authors left near the definition and
	// provenance section comments of the preset key lists (inference only).
	OriginHints []string `json:"origin_hints,omitempty"`
	OriginTags  []string `json:"origin_tags,omitempty"`

	Source    SourceRef `json:"source"`
	Redefined []int     `json:"redefined_at_lines,omitempty"`
	Notes     []string  `json:"notes,omitempty"`
}

type Catalog struct {
	Meta         map[string]interface{} `json:"meta"`
	Options      []Entry                `json:"options"`
	Gui          map[string]interface{} `json:"gui"`
	Dependencies map[string]interface{} `json:"dependencies"`
	Presets      map[string]interface{} `json:"presets"`
	Validation   map[string]interface{} `json:"validation"`
	Lineage      map[string]interface{} `json:"lineage"`
}

func sorted(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (b *Build) list(name string) *KeyList { return b.Lists[name] }

// presetMembership returns the preset types a key belongs to (FFF and SLA).
func (b *Build) presetMembership(key string) []string {
	var out []string
	add := func(t string, ok bool) {
		if ok {
			out = append(out, t)
		}
	}
	add("process", b.list("s_Preset_print_options").Has(key))
	add("filament", b.list("s_Preset_filament_options").Has(key))
	printer := b.list("s_Preset_printer_options").Has(key) || b.list("s_Preset_machine_limits_options").Has(key) || b.list("m_extruder_option_keys").Has(key)
	add("printer", printer)
	add("sla_print", b.list("s_Preset_sla_print_options").Has(key))
	add("sla_material", b.list("s_Preset_sla_material_options").Has(key))
	add("sla_printer", b.list("s_Preset_sla_printer_options").Has(key))
	return out
}

func (b *Build) parseUserMode(data []byte) {
	var v struct {
		Params []struct {
			Key  string `json:"key"`
			Type string `json:"type"`
		} `json:"params_list"`
	}
	if err := json.Unmarshal(data, &v); err != nil {
		b.warnings = append(b.warnings, "CrealityUserMode.json: "+err.Error())
		return
	}
	for _, p := range v.Params {
		b.UserMode[p.Key] = p.Type
	}
}

func (b *Build) parseProcessCfg(data []byte) {
	var v struct {
		Group struct {
			URL  string `json:"url"`
			Data []struct {
				Page   string `json:"page"`
				Groups []struct {
					Name    string `json:"name"`
					Options []struct {
						Key string `json:"key"`
						URL string `json:"url"`
					} `json:"options"`
				} `json:"option_group"`
			} `json:"data"`
		} `json:"config_options_group"`
	}
	if err := json.Unmarshal(data, &v); err != nil {
		b.warnings = append(b.warnings, "ProcessConfig.json: "+err.Error())
		return
	}
	b.DocBase = strings.Replace(v.Group.URL, "/zh/", "/en/", 1)
	b.DocInfo = map[string]docInfo{}
	for _, pg := range v.Group.Data {
		for _, g := range pg.Groups {
			for _, o := range g.Options {
				if _, dup := b.DocInfo[o.Key]; !dup {
					b.DocInfo[o.Key] = docInfo{URL: o.URL, Page: pg.Page, Group: g.Name}
				}
			}
		}
	}
}

var enumSetVars = map[string]struct{ key, when string }{
	"enum_set_normal": {"support_style", "support_type is not a tree type"},
	"enum_set_tree":   {"support_style", "support_type is a tree type"},
	"enum_set_AI":     {"sparse_infill_pattern", "ai_infill is enabled"},
	"enum_set_Normal": {"sparse_infill_pattern", "ai_infill is disabled"},
}

func enumRestrictions(ct *CText, funcs map[string]*Node, maps map[string]*EnumMap, dp *DefParser) []EnumRestriction {
	var out []EnumRestriction
	body := funcs["TabPrint::toggle_options"]
	if body == nil {
		return out
	}
	re := regexp.MustCompile(`(?s)^std::vector<int>\s+(\w+)\s*=\s*\{(.*)\}$`)
	var visit func(n *Node)
	visit = func(n *Node) {
		for _, c := range n.Children {
			if c.Kind == nBlock {
				visit(c)
				continue
			}
			m := re.FindStringSubmatch(c.Text)
			if m == nil {
				continue
			}
			meta, ok := enumSetVars[m[1]]
			if !ok {
				continue
			}
			var typ string
			for _, o := range dp.classes["print"] {
				if o.Key == meta.key {
					typ = o.EnumType
				}
			}
			em := maps[typ]
			var vals []string
			for _, e := range splitTop(m[2], ',') {
				e = strings.TrimSpace(e)
				if e == "" {
					continue
				}
				if em != nil {
					if k, ok := em.keyOf(e); ok {
						vals = append(vals, k)
						continue
					}
				}
				vals = append(vals, "?"+e)
			}
			out = append(out, EnumRestriction{Key: meta.key, Condition: meta.when, Values: vals, Function: "TabPrint::toggle_options", Line: c.Line})
		}
	}
	visit(body)
	return out
}

// ---- assemble ---------------------------------------------------------------

func hasDash(s string) bool {
	return strings.ContainsRune(s, rune(0x2013)) || strings.ContainsRune(s, rune(0x2014))
}

func (b *Build) assemble() *Catalog {
	dp := b.Defs
	// GUI locations by key
	locs := map[string][]GuiLoc{}
	guiKeys := map[string]bool{}
	for _, tab := range b.Gui.tabs {
		for _, pg := range tab.Pages {
			for _, gr := range pg.Groups {
				for _, o := range gr.Options {
					guiKeys[o.Key] = true
					loc := GuiLoc{
						Tab: tab.Name, Page: pg.Title, Group: gr.Title, LineLabel: o.LineLabel,
						Path:   tab.Name + " > " + pg.Title + " > " + gr.Title,
						Widget: o.Widget, Conditions: o.Conds, Macro: o.Macro,
						Source: fmt.Sprintf("%s:%d", fTabCpp, o.Line),
					}
					if gr.Title == "" {
						loc.Path = tab.Name + " > " + pg.Title
					}
					locs[o.Key] = append(locs[o.Key], loc)
				}
			}
		}
	}

	// static classes
	objKeys := map[string]StaticMember{}
	for k, v := range allKeys(b.Classes, "PrintObjectConfig") {
		objKeys[k] = v
	}
	for k, v := range allKeys(b.Classes, "PrintRegionConfig") {
		objKeys[k] = v
	}
	regionKeys := allKeys(b.Classes, "PrintRegionConfig")
	classesOf := map[string][]string{}
	cppType := map[string]string{}
	for _, name := range b.ClassOrder {
		for _, m := range b.Classes[name].Members {
			classesOf[m.Key] = append(classesOf[m.Key], name)
			if _, ok := cppType[m.Key]; !ok {
				cppType[m.Key] = m.CppType
			}
		}
	}

	printList := b.list("s_Preset_print_options")
	plateList := b.list("plate_keys")
	projectList := b.list("s_project_options")

	variantSets := map[string]map[string]bool{}
	for _, name := range []string{"print_options_with_variant", "filament_options_with_variant", "printer_options_with_variant_1", "printer_options_with_variant_2", "printer_extruder_options"} {
		variantSets[name] = map[string]bool{}
		for _, k := range b.list(name).Keys() {
			variantSets[name][k] = true
		}
	}

	gatesBy := map[string][]Gate{}
	for _, g := range b.Gates {
		gatesBy[g.Key] = append(gatesBy[g.Key], g)
	}
	forcedBy := map[string][]Forced{}
	for _, f := range b.Forced {
		forcedBy[f.Key] = append(forcedBy[f.Key], f)
	}

	// enum defaults may be parsed before enum_keys_map is assigned; retry now
	for _, o := range dp.order {
		if (o.TypeName == "coEnum" || o.TypeName == "coEnums") && !o.DefaultParsed && o.DefaultClass != "" && o.DefaultRaw != "" {
			env := &denv{strs: map[string]string{}, nums: map[string]float64{}, loop: map[string]string{}}
			o.Default, o.DefaultParsed = dp.parseDefault(o, o.DefaultClass, o.defOpen, o.DefaultRaw, env)
		}
	}

	sameLine := map[int]int{}
	for _, o := range dp.order {
		sameLine[o.Line]++
	}
	var entries []Entry
	for _, o := range dp.order {
		e := Entry{
			Key: o.Key, DefClass: o.DefClass, Tech: o.Tech,
			ValueType: valueTypeName(o.TypeName), CoType: o.TypeName,
			IsVector: isVectorType(o.TypeName), Nullable: o.Nullable,
			Default: o.Default, DefaultRaw: o.DefaultRaw, DefaultClass: o.DefaultClass, DefaultParsed: o.DefaultParsed,
			Min: o.Min, Max: o.Max, MinRaw: o.MinRaw, MaxRaw: o.MaxRaw, MaxLiteral: o.MaxLiteral,
			Sidetext: o.Sidetext, Label: o.Label, FullLabel: o.FullLabel, Tooltip: o.Tooltip, Category: o.Category,
			GuiType: o.GuiType, GuiFlags: o.GuiFlags, RatioOver: o.RatioOver, Cli: o.Cli, CliParams: o.CliParams,
			Readonly: o.Readonly, Multiline: o.Multiline, FullWidth: o.FullWidth, Height: o.Height, Aliases: o.Aliases,
			Redefined: o.Redefined, Notes: o.Notes, BuildMacro: o.BuildMacro,
			Source: SourceRef{File: o.File, Line: o.Line, Function: o.Func},
		}
		e.ID = o.Key
		if o.DefClass != "print" {
			e.ID = o.DefClass + ":" + o.Key
		}
		if o.DefClass == "print" {
			e.OriginHints, e.OriginTags = b.originHints(o, sameLine)
		}
		if o.Min != nil {
			if v := int(*o.Min); float64(v) != *o.Min {
				e.MinEffective = &v
			}
		}
		if o.Max != nil {
			if v := int(*o.Max); float64(v) != *o.Max {
				e.MaxEffective = &v
			}
		}
		if (o.DefClass == "print" || strings.HasPrefix(o.DefClass, "cli")) && o.Cli != "nocli" {
			// ConfigOptionDef::cli_args (Config.cpp): explicit alternatives split on '|',
			// otherwise the key with '_' replaced by '-'
			names := []string{strings.ReplaceAll(o.Key, "_", "-")}
			if o.Cli != "" {
				names = strings.Split(o.Cli, "|")
			}
			for _, a := range names {
				if len(a) == 1 {
					e.CliFlags = append(e.CliFlags, "-"+a)
				} else {
					e.CliFlags = append(e.CliFlags, "--"+a)
				}
			}
		}
		e.UILevel = "simple"
		if o.ModeSet {
			e.UILevel, e.UILevelExplicit = o.Mode, true
		}
		e.TooltipDash = hasDash(o.Tooltip) || hasDash(o.Label) || hasDash(o.FullLabel)

		// enum
		if o.TypeName == "coEnum" || o.TypeName == "coEnums" || len(o.EnumValues) > 0 {
			ei := &EnumInfo{Type: o.EnumType, ValuesSource: "def", Values: o.EnumValues, Labels: o.EnumLabels}
			if len(ei.Values) == 0 {
				ei.Values, ei.ValuesSource = dp.enumKeys(o), "keys_map"
			}
			if len(ei.Values) > 0 && len(ei.Labels) == len(ei.Values) {
				for i := range ei.Values {
					ei.Options = append(ei.Options, EnumOption{Value: ei.Values[i], Label: ei.Labels[i]})
				}
			}
			if len(ei.Labels) != 0 && len(ei.Labels) != len(ei.Values) {
				e.Notes = append(e.Notes, fmt.Sprintf("enum has %d values but %d labels", len(ei.Values), len(ei.Labels)))
			}
			e.Enum = ei
		}

		// ownership
		switch o.DefClass {
		case "cliactions", "climisc", "clitransform":
			e.Owner, e.OwnerHow, e.CliOnly = "cli", "defined in "+o.Func+" (command-line only)", true
			e.PresetTypes = []string{}
		case "print":
			e.PresetTypes = b.presetMembership(o.Key)
			b.decideOwner(&e, locs[o.Key], printList, plateList, projectList, classesOf)
		default:
			e.Owner, e.OwnerHow = "placeholder", "defined in "+o.Func+" (G-code placeholder variable, not a setting)"
			e.PresetTypes = []string{}
		}
		if e.PresetTypes == nil {
			e.PresetTypes = []string{}
		}

		if o.DefClass == "print" {
			e.GuiLocations = locs[o.Key]
			if len(e.GuiLocations) > 0 {
				e.GuiLocation = e.GuiLocations[0].Path
			}
			e.StaticClasses = classesOf[o.Key]
			e.CppType = cppType[o.Key]
			e.ExtruderOption = b.list("m_extruder_option_keys").Has(o.Key)
			e.ProjectOption = projectList.Has(o.Key)
			for name, set := range variantSets {
				if set[o.Key] {
					e.PerNozzleVariant = append(e.PerNozzleVariant, name)
				}
			}
			sort.Strings(e.PerNozzleVariant)
			inPrint := printList.Has(o.Key)
			_, inObj := objKeys[o.Key]
			_, inReg := regionKeys[o.Key]
			e.Scopes.PerObject = inPrint && inObj
			e.Scopes.PerPartOrMod = inPrint && inReg
			e.Scopes.PerLayerRange = inPrint && (inReg || o.Key == "layer_height")
			e.Scopes.PerPlate = plateList.Has(o.Key)
			for _, l := range e.GuiLocations {
				if l.Tab == "process" && e.Scopes.PerObject {
					e.Scopes.ShownPerObject = true
				}
			}
			e.GatedBy = gatesBy[o.Key]
			e.ForcedChanges = forcedBy[o.Key]
			if t, ok := b.UserMode[o.Key]; ok {
				switch t {
				case "1":
					e.VendorPolicy = "read_only_for_creality_vendor_presets (type 1)"
				case "2":
					e.VendorPolicy = "hidden_for_creality_vendor_presets (type 2)"
				default:
					e.VendorPolicy = "type " + t
				}
			}
			if di, ok := b.DocInfo[o.Key]; ok && di.URL != "" {
				e.DocURL = b.DocBase + di.URL
			}
		}
		entries = append(entries, e)
	}

	cat := &Catalog{Options: entries}
	cat.Gui = map[string]interface{}{"tabs": b.Gui.tabs, "gui_only_keys": sorted(b.Gui.synth)}
	cat.Dependencies = map[string]interface{}{
		"gui_enum_restrictions": b.EnumRestr,
		"unmatched_gates":       b.unmatchedGates(),
		"gate_count":            len(b.Gates),
		"forced_change_count":   len(b.Forced),
	}
	lists := map[string]interface{}{}
	for name, kl := range b.Lists {
		lists[name] = map[string]interface{}{"file": kl.File, "line": kl.Line, "keys": kl.Keys()}
	}
	classes := map[string]interface{}{}
	for _, name := range b.ClassOrder {
		sc := b.Classes[name]
		var keys []string
		for _, m := range sc.Members {
			keys = append(keys, m.Key)
		}
		classes[name] = map[string]interface{}{"parents": sc.Parents, "line": sc.Line, "own_keys": keys}
	}
	cat.Presets = map[string]interface{}{"key_lists": lists, "static_classes": classes, "plate_keys": plateList.Keys()}
	cat.Validation = b.validate(cat, guiKeys)
	cat.Lineage = b.lineage()
	cat.Meta = map[string]interface{}{
		"generator": "tools/catalog-gen",
		"ref":       b.Ref, "commit": b.Commit,
		"files": []string{fPrintConfigCpp, fPrintConfigHpp, fPresetCpp, fPresetBundle, fTabCpp, fConfigManip, fUserModeJSON, fProcessCfgJSON},
		"notes": []string{
			"ui_level: option visible when its mode <= the GUI mode (simple < advanced < develop); unset mode means simple (Config.hpp ConfigOptionDef::mode default).",
			"owner is derived from the GUI tab that shows the option, else from the C++ preset key lists; preset_types lists every preset list containing the key.",
			"min/max are stored as int in ConfigOptionDef; *_effective_int shows the truncated value when the source literal is fractional.",
			"scopes come from the key sets the per-object/part/layer/plate tabs are constructed with (Tab.cpp TabPrintObject, TabPrintPart, TabPrintLayer, TabPrintPlate).",
			"source tooltips and labels are copied verbatim; em/en dashes in them are written as JSON unicode escapes.",
		},
	}
	return cat
}

func (b *Build) decideOwner(e *Entry, locs []GuiLoc, printList, plateList, projectList *KeyList, classesOf map[string][]string) {
	// 1. GUI tab
	for _, l := range locs {
		switch l.Tab {
		case "process", "filament", "printer":
			e.Owner, e.OwnerHow = l.Tab, "shown in the "+l.Tab+" tab"
			return
		}
	}
	// 2. preset lists
	prefer := []string{"process", "filament", "printer", "sla_print", "sla_material", "sla_printer"}
	if e.Tech == "SLA" {
		prefer = []string{"sla_print", "sla_material", "sla_printer", "process", "filament", "printer"}
	}
	for _, p := range prefer {
		for _, t := range e.PresetTypes {
			if t == p {
				e.Owner, e.OwnerHow = p, "first matching preset key list (not shown in a preset tab)"
				return
			}
		}
	}
	for _, l := range locs {
		if l.Tab == "plate" {
			e.Owner, e.OwnerHow = "plate", "only in the per-plate tab"
			return
		}
	}
	switch {
	case projectList.Has(e.Key):
		e.Owner, e.OwnerHow = "project", "listed in s_project_options (stored in the 3MF project config)"
	case plateList.Has(e.Key):
		e.Owner, e.OwnerHow = "plate", "listed in plate_keys"
	case len(classesOf[e.Key]) > 0:
		e.Owner, e.OwnerHow = "internal", "member of a static config class but in no preset list or GUI page"
	default:
		e.Owner, e.OwnerHow = "other", "defined but referenced by no preset list, static class or GUI page"
	}
}

func (b *Build) unmatchedGates() []string {
	defined := map[string]bool{}
	for _, o := range b.Defs.order {
		defined[o.Key] = true
	}
	seen := map[string]bool{}
	for _, g := range b.Gates {
		if !defined[g.Key] {
			seen[g.Key] = true
		}
	}
	for _, f := range b.Forced {
		if !defined[f.Key] {
			seen[f.Key] = true
		}
	}
	return sorted(seen)
}

// ---- validation ---------------------------------------------------------------

type rawScanResult struct {
	Sites         int
	LiteralKeys   map[string]int
	NonLiteral    []string
	LambdaPresent bool
	LambdaCalls   int
	AxisRows      int
	FilamentKeys  int
	AxisSites     int
	FilamentSites int
	LambdaSites   int
	Unexplained   []string
}

var (
	reRawSite    = regexp.MustCompile(`(?:this->add(?:_nullable)?|\bnew_def)\s*\(`)
	reRawLit     = regexp.MustCompile(`(?:this->add(?:_nullable)?|\bnew_def)\s*\(\s*"([^"]+)"\s*,`)
	reFilLoop    = regexp.MustCompile(`(?s)for\s*\(\s*const\s+char\s*\*\s*opt_key\s*:\s*\{(.*?)\}\s*\)`)
	reAxisRows   = regexp.MustCompile(`\{\s*"[a-z]"\s*,\s*\{`)
	reLambdaDef  = regexp.MustCompile(`auto\s+init_extruder_enum\s*=`)
	reLambdaCall = regexp.MustCompile(`\binit_extruder_enum\s*\(`)
)

// rawScan counts option definition call sites with a deliberately simple,
// line-based scanner that shares no code with the statement-tree parser.
func rawScan(lines []string) rawScanResult {
	res := rawScanResult{LiteralKeys: map[string]int{}}
	inBlock := false
	dead := 0
	inDefine := false
	var cleaned []string
	for _, ln := range lines {
		// strip comments naively but quote-aware
		var b strings.Builder
		inStr := false
		for i := 0; i < len(ln); i++ {
			c := ln[i]
			if inBlock {
				if c == '*' && i+1 < len(ln) && ln[i+1] == '/' {
					inBlock = false
					i++
				}
				continue
			}
			if inStr {
				b.WriteByte(c)
				if c == '\\' && i+1 < len(ln) {
					b.WriteByte(ln[i+1])
					i++
				} else if c == '"' {
					inStr = false
				}
				continue
			}
			if c == '"' {
				inStr = true
				b.WriteByte(c)
				continue
			}
			if c == '/' && i+1 < len(ln) && ln[i+1] == '/' {
				break
			}
			if c == '/' && i+1 < len(ln) && ln[i+1] == '*' {
				inBlock = true
				i++
				continue
			}
			b.WriteByte(c)
		}
		s := b.String()
		t := strings.TrimSpace(s)
		if strings.HasPrefix(t, "#") {
			fields := strings.Fields(strings.ReplaceAll(t, "#", "# "))
			if len(fields) >= 2 {
				switch fields[1] {
				case "if":
					if dead > 0 {
						dead++
					} else if len(fields) >= 3 && fields[2] == "0" {
						dead = 1
					}
				case "ifdef", "ifndef":
					if dead > 0 {
						dead++
					}
				case "endif":
					if dead > 0 {
						dead--
					}
				case "else":
					if dead == 1 {
						dead = 0
					}
				}
			}
			inDefine = strings.HasSuffix(strings.TrimRight(ln, " \t"), "\\")
			cleaned = append(cleaned, "")
			continue
		}
		if inDefine {
			inDefine = strings.HasSuffix(strings.TrimRight(ln, " \t"), "\\")
			cleaned = append(cleaned, "")
			continue
		}
		if dead > 0 {
			cleaned = append(cleaned, "")
			continue
		}
		cleaned = append(cleaned, s)
	}
	text := strings.Join(cleaned, "\n")
	for _, m := range reRawSite.FindAllStringIndex(text, -1) {
		res.Sites++
		end := m[1]
		if end > len(text) {
			end = len(text)
		}
		snippet := text[m[0]:min(len(text), m[1]+80)]
		if !reRawLit.MatchString(snippet) {
			res.NonLiteral = append(res.NonLiteral, oneLine(snippet))
		}
	}
	for _, m := range reRawLit.FindAllStringSubmatch(text, -1) {
		res.LiteralKeys[m[1]]++
	}
	res.LambdaPresent = reLambdaDef.MatchString(text)
	res.LambdaCalls = len(reLambdaCall.FindAllStringIndex(text, -1))
	if m := reFilLoop.FindStringSubmatch(text); m != nil {
		res.FilamentKeys = len(stringLiterals(m[1]))
	}
	res.AxisRows = len(reAxisRows.FindAllStringIndex(text, -1))
	for _, nl := range res.NonLiteral {
		switch {
		case strings.Contains(nl, "axis.name"):
			res.AxisSites++
		case strings.Contains(nl, "opt_key"):
			res.FilamentSites++
		case strings.Contains(nl, "(key,") || strings.Contains(nl, "(key ,"):
			res.LambdaSites++
		default:
			res.Unexplained = append(res.Unexplained, nl)
		}
	}
	return res
}

func (b *Build) validate(cat *Catalog, guiKeys map[string]bool) map[string]interface{} {
	dp := b.Defs
	raw := rawScan(b.CfgCt.Raw)
	expected := raw.Sites - raw.AxisSites - raw.FilamentSites - raw.LambdaSites +
		raw.AxisSites*raw.AxisRows + raw.FilamentSites*raw.FilamentKeys + raw.LambdaSites*raw.LambdaCalls
	parsedSites := 0
	for _, n := range dp.addSites {
		parsedSites += n
	}
	parsedSites += dp.lambdaCalls // init_extruder_enum calls are not add() statements
	unique := len(dp.order)

	// literal key set check
	literalMissing := []string{}
	for k := range raw.LiteralKeys {
		found := false
		for _, cls := range dp.classes {
			if _, ok := cls[k]; ok {
				found = true
				break
			}
		}
		if !found {
			literalMissing = append(literalMissing, k)
		}
	}
	sort.Strings(literalMissing)

	// per-class site counts
	perClass := map[string]int{}
	for _, e := range cat.Options {
		perClass[e.DefClass]++
	}

	// list keys with no definition
	defPrint := dp.classes["print"]
	missingFromDefs := map[string][]string{}
	for _, name := range []string{"s_Preset_print_options", "s_Preset_filament_options", "s_Preset_printer_options", "s_Preset_machine_limits_options", "m_extruder_option_keys", "s_Preset_sla_print_options", "s_Preset_sla_material_options", "s_Preset_sla_printer_options", "s_project_options", "plate_keys"} {
		kl := b.list(name)
		if kl == nil {
			missingFromDefs[name] = []string{"<list not found>"}
			continue
		}
		for _, it := range kl.Items {
			if _, ok := defPrint[it.Key]; !ok {
				missingFromDefs[name] = append(missingFromDefs[name], it.Key)
			}
		}
	}
	guiMissing := []string{}
	for k := range guiKeys {
		if _, ok := defPrint[k]; !ok && !b.Gui.synth[k] {
			guiMissing = append(guiMissing, k)
		}
	}
	sort.Strings(guiMissing)
	// cross-check: option keys mentioned as literals inside the layout-building
	// functions that never made it into the parsed tree
	litNames := []string{"TabPrint::build", "TabFilament::build", "TabFilament::add_filament_overrides_page", "TabPrinter::build_fff", "TabPrinter::build_kinematics_page", "TabPrinter::build_unregular_pages", "TabPrintPlate::build", "TabPrintModel::build"}
	guiLitNotInTree := []string{}
	for k := range b.Gui.functionLiterals(litNames) {
		if _, ok := defPrint[k]; ok && !guiKeys[k] {
			guiLitNotInTree = append(guiLitNotInTree, k)
		}
	}
	sort.Strings(guiLitNotInTree)
	noDefault := []string{}
	for _, e := range cat.Options {
		if e.DefClass == "print" && e.DefaultClass == "" {
			noDefault = append(noDefault, e.ID)
		}
	}
	// filament_* override options are created by a loop; check GUI keys resolve
	ownerCounts := map[string]int{}
	typeCounts := map[string]int{}
	levelCounts := map[string]int{}
	vec, nullable, withGUI, defaultUnparsed := 0, 0, 0, 0
	scope := map[string]int{}
	unparsedKeys := []string{}
	for _, e := range cat.Options {
		ownerCounts[e.Owner]++
		typeCounts[e.ValueType]++
		levelCounts[e.UILevel]++
		if e.IsVector {
			vec++
		}
		if e.Nullable {
			nullable++
		}
		if len(e.GuiLocations) > 0 {
			withGUI++
		}
		if !e.DefaultParsed && e.DefaultRaw != "" {
			defaultUnparsed++
			unparsedKeys = append(unparsedKeys, e.ID)
		}
		if e.Scopes.PerObject {
			scope["per_object"]++
		}
		if e.Scopes.PerPartOrMod {
			scope["per_part_or_modifier"]++
		}
		if e.Scopes.PerLayerRange {
			scope["per_layer_range"]++
		}
		if e.Scopes.PerPlate {
			scope["per_plate"]++
		}
	}
	sort.Strings(unparsedKeys)
	dashKeys := []string{}
	for _, e := range cat.Options {
		if e.TooltipDash {
			dashKeys = append(dashKeys, e.ID)
		}
	}
	allWarn := append([]string{}, dp.warnings...)
	allWarn = append(allWarn, b.Gui.warns...)
	allWarn = append(allWarn, b.warnings...)

	return map[string]interface{}{
		"raw_add_call_sites":          raw.Sites,
		"raw_literal_key_sites":       len(raw.LiteralKeys),
		"raw_non_literal_sites":       raw.NonLiteral,
		"raw_unexplained_non_literal": raw.Unexplained,
		"generator_sites": map[string]int{
			"init_extruder_enum_calls": raw.LambdaCalls, "axis_rows": raw.AxisRows, "filament_override_keys": raw.FilamentKeys,
			"axis_loop_sites": raw.AxisSites, "filament_loop_sites": raw.FilamentSites, "lambda_sites": raw.LambdaSites,
		},
		"expected_definitions":            expected,
		"parsed_definitions":              parsedSites,
		"definitions_match":               expected == parsedSites,
		"duplicate_adds":                  dp.dupKeys,
		"unique_options_parsed":           unique,
		"unique_equals_defs_minus_dups":   unique == parsedSites-len(dp.dupKeys),
		"literal_keys_missing_from_parse": literalMissing,
		"options_per_def_class":           perClass,
		"counts_by_owner":                 ownerCounts,
		"counts_by_value_type":            typeCounts,
		"counts_by_ui_level":              levelCounts,
		"vector_options":                  vec,
		"nullable_options":                nullable,
		"options_with_gui_location":       withGUI,
		"scope_counts":                    scope,
		"list_keys_without_definition":    missingFromDefs,
		"gui_keys_without_definition":     guiMissing,
		"gui_literal_keys_not_in_tree":    guiLitNotInTree,
		"print_options_without_default":   noDefault,
		"defaults_not_parsed":             unparsedKeys,
		"labels_or_tooltips_with_dash":    dashKeys,
		"parser_warnings":                 allWarn,
	}
}

func (b *Build) printReport(cat *Catalog, path string, size int, src *GitSource) {
	v := cat.Validation
	fmt.Printf("wrote %s (%d bytes)\n", path, size)
	fmt.Printf("options: %d  (per def class: %v)\n", len(cat.Options), v["options_per_def_class"])
	fmt.Printf("by owner: %v\n", v["counts_by_owner"])
	fmt.Printf("by value type: %v\n", v["counts_by_value_type"])
	fmt.Printf("by ui level: %v\n", v["counts_by_ui_level"])
	fmt.Printf("vector: %v  nullable: %v  with GUI location: %v\n", v["vector_options"], v["nullable_options"], v["options_with_gui_location"])
	fmt.Printf("scopes: %v\n", v["scope_counts"])
	fmt.Println("--- validation")
	fmt.Printf("source add/new_def call sites (independent line scan): %v\n", v["raw_add_call_sites"])
	fmt.Printf("generator sites: %v\n", v["generator_sites"])
	fmt.Printf("expected definitions: %v, parsed definitions: %v, match: %v\n", v["expected_definitions"], v["parsed_definitions"], v["definitions_match"])
	fmt.Printf("duplicate adds (same key added twice): %v\n", v["duplicate_adds"])
	fmt.Printf("unique options parsed: %v (defs minus duplicates consistent: %v)\n", v["unique_options_parsed"], v["unique_equals_defs_minus_dups"])
	fmt.Printf("literal keys in source missing from parse: %v\n", v["literal_keys_missing_from_parse"])
	fmt.Printf("non-literal add sites: %v\n", v["raw_non_literal_sites"])
	fmt.Printf("unexplained non-literal sites: %v\n", v["raw_unexplained_non_literal"])
	fmt.Printf("preset list keys with no definition: %v\n", v["list_keys_without_definition"])
	fmt.Printf("GUI keys with no definition: %v\n", v["gui_keys_without_definition"])
	fmt.Printf("option keys named in layout functions but absent from the tree: %v\n", v["gui_literal_keys_not_in_tree"])
	fmt.Printf("print options without default: %v\n", v["print_options_without_default"])
	fmt.Printf("defaults not parsed: %d\n", len(v["defaults_not_parsed"].([]string)))
	fmt.Printf("labels/tooltips containing em/en dash (escaped in JSON): %d\n", len(v["labels_or_tooltips_with_dash"].([]string)))
	warns := v["parser_warnings"].([]string)
	fmt.Printf("parser warnings: %d\n", len(warns))
	for i, w := range warns {
		if i >= 60 {
			fmt.Printf("  ... %d more\n", len(warns)-60)
			break
		}
		fmt.Println("  " + w)
	}
	_ = os.Stdout
}

// ---- lineage hints ------------------------------------------------------------

var originTagRes = []struct {
	tag string
	re  *regexp.Regexp
}{
	{"BBS", regexp.MustCompile(`\bBBS\b`)},
	{"Orca", regexp.MustCompile(`(?i)\borca\b|softfever|\bSF\b`)},
	{"Creality", regexp.MustCompile(`(?i)creality|\bCX\b`)},
	{"Prusa", regexp.MustCompile(`(?i)\bPS\b|prusa`)},
}

var reTrailingComment = regexp.MustCompile(`//+\s*(.+?)\s*$`)

// originHints gathers comment text near each definition (a few lines above the
// add() call and inside its block) and the section comments of the Preset.cpp
// key lists. This only shows what the code authors wrote; it is not proof of
// origin.
func (b *Build) originHints(o *Opt, sameLine map[int]int) ([]string, []string) {
	if sameLine[o.Line] > 1 {
		return nil, nil // generated in a loop or lambda: comments would apply to many keys
	}
	raw := b.CfgCt.Raw
	end := o.Line + 40
	// stop at the next add() below
	for l := o.Line + 1; l <= len(raw) && l <= end; l++ {
		if reRawSite.MatchString(raw[l-1]) {
			end = l - 1
			break
		}
	}
	var hints []string
	// contiguous pure comments directly above
	for l := o.Line - 1; l >= 1 && l >= o.Line-4; l-- {
		if m := reCommentLine.FindStringSubmatch(raw[l-1]); m != nil && m[1] != "" {
			hints = append([]string{m[1]}, hints...)
		} else {
			break
		}
	}
	for l := o.Line; l <= end && l <= len(raw); l++ {
		if m := reTrailingComment.FindStringSubmatch(raw[l-1]); m != nil && !strings.Contains(raw[l-1][:strings.Index(raw[l-1], "//")], `"`) {
			hints = append(hints, m[1])
		}
	}
	tagSet := map[string]bool{}
	var kept []string
	for _, h := range hints {
		matched := false
		for _, t := range originTagRes {
			if t.re.MatchString(h) {
				tagSet[t.tag] = true
				matched = true
			}
		}
		if matched || len(h) <= 40 && !strings.HasPrefix(strings.TrimSpace(h), "def") {
			kept = append(kept, oneLine(h))
		}
	}
	// list section comments
	for _, name := range []string{"s_Preset_print_options", "s_Preset_filament_options", "s_Preset_printer_options", "s_Preset_machine_limits_options"} {
		if kl := b.list(name); kl != nil {
			for _, it := range kl.Items {
				if it.Key == o.Key && it.Section != "" {
					// Section headings are only the nearest preceding comment line of a long
					// list and are often stale, so they are shown as a hint but never as a tag.
					kept = append(kept, fmt.Sprintf("Preset.cpp %s section comment: %s", name, oneLine(it.Section)))
					break
				}
			}
		}
	}
	if len(kept) > 6 {
		kept = kept[:6]
	}
	return kept, sorted(tagSet)
}

var reCrealityComment = regexp.MustCompile(`(?i)creality|\bcx\b|\bK2\b|\bCFS\b`)

// lineage collects comments that mention Creality-specific things, and counts
// upstream-tag comments, across the analysed files. It is a hint about what the
// code authors chose to annotate, not a diff against upstream.
func (b *Build) lineage() map[string]interface{} {
	type item struct {
		File string `json:"file"`
		Line int    `json:"line"`
		Text string `json:"text"`
	}
	var found []item
	tagCounts := map[string]int{}
	for _, ct := range []*CText{b.CfgCt, b.HppCt, b.PresetCt, b.BundleCt, b.TabCt, b.CmCt} {
		for i, ln := range ct.Raw {
			idx := strings.Index(ln, "//")
			if idx < 0 || strings.Count(ln[:idx], `"`)%2 == 1 {
				continue
			}
			text := strings.TrimSpace(ln[idx:])
			for _, t := range originTagRes {
				if t.tag != "Creality" && t.re.MatchString(text) {
					tagCounts[ct.Rel+" "+t.tag]++
				}
			}
			if reCrealityComment.MatchString(text) {
				found = append(found, item{ct.Rel, i + 1, oneLine(text)})
			}
		}
	}
	return map[string]interface{}{
		"note":                        "comments mentioning Creality, CX, K2 or CFS, and counts of BBS/Orca/Prusa tag comments per file; hints only, not a diff against upstream",
		"creality_related_comments":   found,
		"upstream_tag_comment_counts": tagCounts,
	}
}
