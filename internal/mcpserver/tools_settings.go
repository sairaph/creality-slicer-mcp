package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sairaph/mcp-wizard/render"

	"github.com/sairaph/creality-slicer-mcp/internal/catalog"
	"github.com/sairaph/creality-slicer-mcp/internal/profiles"
)

func (s *Server) registerSettingsTools() {
	addTool(s.mcpServer, "search_settings",
		withRange(withEnum(withEnum(inputSchema[searchInput](nil), "level", levelValues...), "scope", scopeValues...), 1, 1e6, "page"),
		s.searchSettings)
	addTool(s.mcpServer, "describe_setting", inputSchema[describeInput](nil), s.describeSetting)
	addTool(s.mcpServer, "browse_settings", withEnum(inputSchema[browseInput](nil), "level", levelValues...), s.browseSettings)
}

var (
	levelValues = []string{"beginner", "advanced", "all"}
	scopeValues = []string{"printer", "process", "filament", "object", "part", "layer_range", "plate"}
)

// parseLevel maps the level argument to a catalog level (default beginner).
func parseLevel(l *string) (catalog.Level, *toolResult) {
	switch strings.ToLower(strings.TrimSpace(deref(l))) {
	case "", "beginner":
		return catalog.LevelBeginner, nil
	case "advanced":
		return catalog.LevelAdvanced, nil
	case "all":
		return catalog.LevelAll, nil
	}
	return "", invalidInput(fmt.Sprintf("Unknown level %q", deref(l)), "Use level beginner, advanced or all.")
}

// catalogOrFail returns the catalog, or the error result to send.
func (s *Server) catalogOrFail(ctx context.Context) (*catalog.Catalog, string, *toolResult) {
	cat, reason, err := s.env.catalog(ctx)
	if err != nil {
		return nil, "", failure(ctx, "load the settings catalog", err, "")
	}
	return cat, reason, nil
}

// --- search_settings ---

type searchInput struct {
	Query *string `json:"query,omitempty"`
	Area  *string `json:"area,omitempty"`
	Level *string `json:"level,omitempty"`
	Scope *string `json:"scope,omitempty"`
	Page  *int    `json:"page,omitempty"`
}

type searchFront struct {
	Query           string `yaml:"query,omitempty"`
	Count           int    `yaml:"count"`
	CatalogVersion  string `yaml:"catalog_version"`
	Defaults        string `yaml:"defaults"` // presets (K2 presets), catalog (catalog defaults) or mixed
	render.PageMeta `yaml:",inline"`
}

type searchRow struct {
	opt      *catalog.Option
	def      string
	fromCat  bool
	excerpt  string
	levelTxt string
}

func (s *Server) searchSettings(ctx context.Context, _ *mcp.CallToolRequest, in searchInput) (*mcp.CallToolResult, any, error) {
	level, bad := parseLevel(in.Level)
	if bad != nil {
		return bad, nil, nil
	}
	cat, _, fail := s.catalogOrFail(ctx)
	if fail != nil {
		return fail, nil, nil
	}
	f := catalog.Filter{Level: level, PathPrefix: strings.TrimSpace(deref(in.Area))}
	switch sc := strings.ToLower(strings.TrimSpace(deref(in.Scope))); sc {
	case "":
	case "printer", "process", "filament":
		f.Owner = sc
	case "object", "part", "layer_range", "plate":
		f.Scope = catalog.Scope(sc)
	default:
		return invalidInput(fmt.Sprintf("Unknown scope %q", sc), "Use scope printer, process, filament, object, part, layer_range or plate."), nil, nil
	}
	query := strings.TrimSpace(deref(in.Query))
	hits := cat.Search(query, f)

	defs := s.env.k2(ctx)
	rows := make([]searchRow, len(hits))
	fromPresets, fromCatalog := 0, 0
	for i, h := range hits {
		o := h.Option
		r := searchRow{opt: o, levelTxt: levelName(o.UILevel)}
		if v, ok := defs.value(o); ok {
			r.def = v
			fromPresets++
		} else {
			r.def, r.fromCat = showDefault(o.Default), true
			fromCatalog++
		}
		if tip, ok := cat.Tooltip(o.Key); ok {
			r.excerpt = catalog.Excerpt(tip)
		}
		rows[i] = r
	}

	page := 1
	if in.Page != nil {
		page = *in.Page
	}
	window, meta, next, err := paginatePage(rows, page, func(w []searchRow) (string, error) { return renderSearchRows(w), nil })
	if err != nil {
		return failure(ctx, "page the search results", err, ""), nil, nil
	}
	defaults := "catalog"
	switch {
	case fromPresets > 0 && fromCatalog > 0:
		defaults = "mixed"
	case fromPresets > 0:
		defaults = "presets"
	}
	front := searchFront{Query: query, Count: len(hits), CatalogVersion: cat.Version, Defaults: defaults, PageMeta: meta}

	var b strings.Builder
	switch {
	case len(hits) == 0:
		b.WriteString("No setting matches")
		if query != "" {
			fmt.Fprintf(&b, " %q", query)
		}
		b.WriteString(" at this level and scope.")
		if level != catalog.LevelAll {
			b.WriteString(" Try level \"all\", a shorter query, or browse_settings to walk the areas.")
		} else {
			b.WriteString(" Try a shorter query, or browse_settings to walk the areas.")
		}
	case len(window) == 0:
		fmt.Fprintf(&b, "Page %d is past the end: there are %d matches on %d page(s).", meta.Page, meta.Total, meta.TotalPages)
	default:
		fmt.Fprintf(&b, "%d setting(s) match; page %d of %d. Columns: key | label | area | unit | K2 default | level.\n", len(hits), meta.Page, meta.TotalPages)
		b.WriteString(defaultsNote(defs != nil, fromCatalog > 0))
		b.WriteString("\n\n")
		b.WriteString(renderSearchRows(window))
		b.WriteString("\n" + strings.TrimSpace(next))
		b.WriteString("\nNext: describe_setting with {\"key\": \"<key>\"} for the meaning, range and dependencies.")
	}
	return successResult(front, strings.TrimRight(b.String(), "\n")), nil, nil
}

func defaultsNote(store, anyCatalog bool) string {
	switch {
	case !store:
		return "K2 default: the installed K2 presets could not be read, so every default is the catalog's (marked \"(catalog)\")."
	case anyCatalog:
		return fmt.Sprintf("K2 default: the value in the flattened `%s`, `%s` and `%s` presets; \"(catalog)\" marks a setting those presets do not set, where the catalog's own default is shown.", k2Printer, k2Process, k2Filament)
	}
	return fmt.Sprintf("K2 default: the value in the flattened `%s`, `%s` and `%s` presets.", k2Printer, k2Process, k2Filament)
}

func levelName(uiLevel string) string {
	if uiLevel == "simple" {
		return "beginner"
	}
	return uiLevel
}

func renderSearchRows(rows []searchRow) string {
	var b strings.Builder
	for i, r := range rows {
		if i > 0 {
			b.WriteString("\n")
		}
		o := r.opt
		def := r.def
		if def == "" {
			def = "-"
		}
		if r.fromCat {
			def += " (catalog)"
		}
		area := areaOf(o)
		if area == "" {
			area = "(no GUI line)"
		}
		fmt.Fprintf(&b, "%s | %s | %s | %s | %s | %s", o.Key, pipeSafe(o.Title()), pipeSafe(area), pipeSafe(unitOf(o)), pipeSafe(def), r.levelTxt)
		if r.excerpt != "" {
			b.WriteString("\n  " + pipeSafe(r.excerpt))
		}
	}
	return b.String()
}

func unitOf(o *catalog.Option) string {
	if o.Sidetext != "" {
		return o.Sidetext
	}
	if o.ValueType == "percent" {
		return "%"
	}
	return ""
}

// --- describe_setting ---

type describeInput struct {
	Key     string  `json:"key"`
	Preset  *string `json:"preset,omitempty"`
	Project *string `json:"project,omitempty"`
}

type describeFront struct {
	Key     string   `yaml:"key"`
	Label   string   `yaml:"label,omitempty"`
	Type    string   `yaml:"type"`
	Vector  bool     `yaml:"vector,omitempty"`
	Unit    string   `yaml:"unit,omitempty"`
	Default string   `yaml:"default,omitempty"`
	Min     *float64 `yaml:"min,omitempty"`
	Max     *float64 `yaml:"max,omitempty"`
	Enum    []string `yaml:"enum,omitempty"`
	Level   string   `yaml:"level"`
	Area    string   `yaml:"area,omitempty"`
	Scopes  []string `yaml:"scopes"`
	Locked  string   `yaml:"locked,omitempty"`
	Current *string  `yaml:"current,omitempty"`
	Origin  string   `yaml:"origin,omitempty"`
}

func (s *Server) describeSetting(ctx context.Context, _ *mcp.CallToolRequest, in describeInput) (*mcp.CallToolResult, any, error) {
	cat, textsReason, fail := s.catalogOrFail(ctx)
	if fail != nil {
		return fail, nil, nil
	}
	key := strings.TrimSpace(in.Key)
	o, ok := cat.Get(key)
	if !ok {
		hint := "Call search_settings with a word from the setting's name or purpose."
		if near := cat.Closest(key, 5); len(near) > 0 {
			hint = "Did you mean: " + strings.Join(near, ", ") + "? Call describe_setting with one of them, or call search_settings with a word from the setting's name."
		}
		return notFound(fmt.Sprintf("Unknown setting %q", key), hint), nil, nil
	}

	front := describeFront{
		Key: o.Key, Label: o.Title(), Type: o.ValueType, Vector: o.IsVector, Unit: unitOf(o), Default: showDefault(o.Default),
		Min: o.Min, Max: o.Max, Level: levelName(o.UILevel), Area: areaOf(o), Scopes: settingScopes(o), Locked: o.VendorLock,
	}
	if o.Enum != nil {
		for i, v := range o.Enum.Values {
			label := ""
			if i < len(o.Enum.Labels) {
				label = o.Enum.Labels[i]
			}
			if label != "" && label != v {
				front.Enum = append(front.Enum, v+": "+label)
			} else {
				front.Enum = append(front.Enum, v)
			}
		}
	}

	var presetNote string
	if p := strings.TrimSpace(deref(in.Preset)); p != "" {
		typ, name, err := splitPresetRef(p)
		if err != nil {
			return invalidInput(err.Error(), "Pass preset as type:name, for example process:0.20mm Standard @Creality K2 0.4 nozzle."), nil, nil
		}
		store, err := s.env.profileStore(ctx)
		if err != nil {
			return unavailable("read presets", err), nil, nil
		}
		preset, err := store.Get(typ, name)
		if err != nil {
			return notFound(fmt.Sprintf("No %s preset %q", typ, name), "Call list_presets with {\"type\": \""+string(typ)+"\"} to see the preset names."), nil, nil
		}
		if v, ok := preset.Values[o.Key]; ok {
			cur := showPresetValue(v)
			front.Current = &cur
			front.Origin = preset.Origin[o.Key]
			presetNote = fmt.Sprintf("In %s `%s` the value is `%s`, set by `%s`.", typ, name, cur, front.Origin)
		} else {
			presetNote = fmt.Sprintf("%s `%s` does not set this key, so the application's built-in default applies.", capitalize(string(typ)), name)
		}
	}

	if ref := strings.TrimSpace(deref(in.Project)); ref != "" {
		be, fail := s.projectsOrFail(ctx)
		if fail != nil {
			return fail, nil, nil
		}
		sv, err := be.Store.SettingValue(ref, o.Key)
		if err != nil {
			return projFailure(err), nil, nil
		}
		if sv.Found {
			front.Current = &sv.Value
			front.Origin = sv.Origin
			presetNote = strings.TrimSpace(presetNote + fmt.Sprintf(" In project %s the value is `%s`: %s.", ref, sv.Value, sv.Origin))
		} else {
			presetNote = strings.TrimSpace(presetNote + fmt.Sprintf(" Project %s has no project level value for this key (it may be set per object, part, range or plate).", ref))
		}
	}

	var b strings.Builder
	b.WriteString("## Description\n\n")
	if tip, ok := cat.Tooltip(o.Key); ok {
		b.WriteString(strings.TrimSpace(tip))
	} else {
		b.WriteString("No description in this Creality Print build.")
		if textsReason != "" {
			b.WriteString(" (The setting descriptions could not be loaded: " + textsReason + ".)")
		}
	}
	if presetNote != "" {
		b.WriteString("\n\n" + presetNote)
	}
	b.WriteString("\n\n## Dependencies\n\n")
	if deps, ok := cat.Deps(o.Key); ok && !deps.Empty() {
		for _, l := range deps.Lines() {
			b.WriteString("- " + shortDependency(l) + "\n")
		}
	} else {
		b.WriteString("None: this setting is always shown and always editable.\n")
	}
	if front.Locked != "" {
		b.WriteString("\n" + lockedText(front.Locked) + "\n")
	}
	if rel := relatedKeys(cat, o, 8); len(rel) > 0 {
		b.WriteString("\n## Related settings\n\nSame group (" + areaOf(o) + "): " + strings.Join(rel, ", ") + ".\n")
	}
	b.WriteString("\n## How to change it\n\n" + changeText(o))
	return successResult(front, strings.TrimRight(b.String(), "\n")), nil, nil
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// splitPresetRef parses "type:name".
func splitPresetRef(s string) (profiles.Type, string, error) {
	t, name, ok := strings.Cut(s, ":")
	if !ok || strings.TrimSpace(name) == "" {
		return "", "", fmt.Errorf("preset %q is not in the form type:name", s)
	}
	typ, err := profiles.ParseType(t)
	if err != nil {
		return "", "", err
	}
	return typ, strings.TrimSpace(name), nil
}

// settingScopes lists where the setting can be written.
func settingScopes(o *catalog.Option) []string {
	var out []string
	if o.HasScope(catalog.ScopePreset) {
		out = append(out, "project")
	}
	out = append(out, o.Scopes...)
	return out
}

func lockedText(policy string) string {
	switch policy {
	case "read_only":
		return "Locked: Creality's own presets keep this setting read-only. update_settings refuses it unless allow_locked is true."
	case "hidden":
		return "Locked: Creality's own presets hide this setting. update_settings refuses it unless allow_locked is true."
	}
	return "Locked by Creality's vendor policy (" + policy + ")."
}

// relatedKeys lists other settings of the same GUI group.
func relatedKeys(cat *catalog.Catalog, o *catalog.Option, limit int) []string {
	if o.GUI == nil {
		return nil
	}
	var out []string
	for _, h := range cat.Search("", catalog.Filter{Level: catalog.LevelAll, PathPrefix: o.GUIPath()}) {
		if h.Option.Key != o.Key {
			out = append(out, h.Option.Key)
		}
		if len(out) == limit {
			break
		}
	}
	return out
}

// changeText tells how to set the setting with update_settings, with an example.
func changeText(o *catalog.Option) string {
	sample := sampleValue(o)
	scope := "project scope (the default)"
	if o.HasScope(catalog.ScopeObject) {
		scope += ", or `scope` `object` with a `target` to change one object"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Call update_settings at %s:\n\n```json\n{\"project\": \"<project>\", \"values\": {%q: %s}}\n```\n", scope, o.Key, sample)
	if o.IsVector {
		b.WriteString("\nThis is a per-filament setting: a list gives one value per filament slot (its length must equal the number of project filaments), and a single value applies to every slot.\n")
	}
	if len(o.Scopes) > 0 {
		fmt.Fprintf(&b, "\nOther scopes for this key: %s.\n", strings.Join(o.Scopes, ", "))
	}
	return b.String()
}

// sampleValue is a JSON literal that would be a valid value for the setting.
func sampleValue(o *catalog.Option) string {
	one := jsonLiteral(o, false)
	if o.IsVector {
		return "[" + one + "]"
	}
	return one
}

func jsonLiteral(o *catalog.Option, _ bool) string {
	def := showDefault(o.Default)
	switch o.ValueType {
	case "bool":
		if def == "true" || def == "1" {
			return "false"
		}
		return "true"
	case "int", "float":
		if def == "" {
			return "1"
		}
		return def
	case "percent":
		if def == "" {
			return "\"20%\""
		}
		return "\"" + strings.TrimSuffix(def, "%") + "%\""
	case "enum":
		if o.Enum != nil {
			for _, v := range o.Enum.Values {
				if v != def {
					return fmt.Sprintf("%q", v)
				}
			}
		}
	}
	if def == "" {
		return "\"...\""
	}
	if _, err := fmt.Sscanf(def, "%f", new(float64)); err == nil && o.ValueType != "string" {
		return def
	}
	return fmt.Sprintf("%q", def)
}

// --- browse_settings ---

type browseInput struct {
	Path  *string `json:"path,omitempty"`
	Level *string `json:"level,omitempty"`
}

type browseFront struct {
	Path     string `yaml:"path"`
	Kind     string `yaml:"kind"`
	Children int    `yaml:"children"`
	Settings int    `yaml:"settings"`
}

func (s *Server) browseSettings(ctx context.Context, _ *mcp.CallToolRequest, in browseInput) (*mcp.CallToolResult, any, error) {
	level, bad := parseLevel(in.Level)
	if bad != nil {
		return bad, nil, nil
	}
	cat, _, fail := s.catalogOrFail(ctx)
	if fail != nil {
		return fail, nil, nil
	}
	path := strings.Trim(strings.TrimSpace(deref(in.Path)), "/")
	node := cat.TreeFiltered(path, 1, catalog.Filter{Level: level})
	if node == nil {
		return notFound(fmt.Sprintf("No settings area %q", path), "Call browse_settings with {} to list the tabs, then go down one path segment at a time."), nil, nil
	}
	shown := node.Path
	if shown == "" {
		shown = "(top)"
	}
	front := browseFront{Path: node.Path, Kind: node.Kind, Children: len(node.Children), Settings: node.Count}

	var b strings.Builder
	fmt.Fprintf(&b, "%s: a %s with %d setting(s) at this level.\n", shown, node.Kind, node.Count)
	if len(node.Children) > 0 {
		b.WriteString("\n")
		for _, ch := range node.Children {
			fmt.Fprintf(&b, "- %s (`%s`): %d setting(s)\n", ch.Name, ch.Path, ch.Count)
		}
		fmt.Fprintf(&b, "\nNext: browse_settings with one of these paths, e.g. {\"path\": %q}.", node.Children[0].Path)
	}
	if len(node.Options) > 0 {
		b.WriteString("\nSettings (label | key | unit | default):\n\n")
		for _, o := range node.Options {
			def := showDefault(o.Default)
			if def == "" {
				def = "-"
			}
			fmt.Fprintf(&b, "%s | %s | %s | %s\n", pipeSafe(o.Title()), o.Key, pipeSafe(unitOf(o)), pipeSafe(def))
		}
		b.WriteString("\nNext: describe_setting with {\"key\": \"<key>\"} for the meaning and range.")
	}
	if len(node.Children) == 0 && len(node.Options) == 0 {
		b.WriteString("\nNothing at this level; try level \"advanced\" or \"all\".")
	}
	return successResult(front, strings.TrimRight(b.String(), "\n")), nil, nil
}

// maxDependencyChars is the longest dependency sentence in a describe_setting
// body.
const maxDependencyChars = 200

// contextClauses are conditions about which editor the app is showing: they
// mean nothing to a caller of the tools.
var contextClauses = []string{
	"a per-plate override is not being edited and ",
	"a per-object override is not being edited and ",
	"a per-plate override is not being edited",
	"a per-object override is not being edited",
}

// shortDependency drops the app's editor context from a dependency sentence
// and cuts a very long one at a whole condition.
func shortDependency(line string) string {
	for _, c := range contextClauses {
		line = strings.Replace(line, c, "", 1)
	}
	line = strings.TrimSpace(line)
	runes := []rune(line)
	if len(runes) <= maxDependencyChars {
		return line
	}
	head := string(runes[:maxDependencyChars])
	cut := max(strings.LastIndex(head, " and "), strings.LastIndex(head, " or "))
	if cut <= 0 {
		cut = strings.LastIndex(head, " ")
	}
	if cut <= 0 {
		cut = len(head)
	}
	return strings.TrimSpace(line[:cut]) + " ... and more conditions"
}
