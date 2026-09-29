package mcpserver

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sairaph/mcp-wizard/render"

	"github.com/sairaph/creality-slicer-mcp/internal/catalog"
	"github.com/sairaph/creality-slicer-mcp/internal/profiles"
)

func (s *Server) registerPresetTools() {
	addTool(s.mcpServer, "list_presets",
		withRange(withEnum(withEnum(inputSchema[listPresetsInput](nil), "type", "printer", "process", "filament"), "source", "system", "user", "all"), 1, 1e6, "page"),
		s.listPresets)
	addTool(s.mcpServer, "get_preset", withEnum(withEnum(inputSchema[getPresetInput](nil), "type", "printer", "process", "filament"), "level", levelValues...), s.getPreset)
}

// presetStore returns the profile store, or the error result to send.
func (s *Server) presetStore(ctx context.Context) (ProfileStore, *toolResult) {
	st, err := s.env.profileStore(ctx)
	if err != nil {
		return nil, unavailable("read presets", err)
	}
	return st, nil
}

// --- list_presets ---

type listPresetsInput struct {
	Type         string  `json:"type"`
	Printer      *string `json:"printer,omitempty"`
	FilamentType *string `json:"filament_type,omitempty"`
	Source       *string `json:"source,omitempty"`
	Page         *int    `json:"page,omitempty"`
}

type listPresetsFront struct {
	Type            string `yaml:"type"`
	Printer         string `yaml:"printer"`
	Count           int    `yaml:"count"`
	ProfileVersion  string `yaml:"profile_version,omitempty"`
	render.PageMeta `yaml:",inline"`
}

func (s *Server) listPresets(ctx context.Context, _ *mcp.CallToolRequest, in listPresetsInput) (*mcp.CallToolResult, any, error) {
	return s.listPresetsResult(ctx, in, false), nil, nil
}

// PresetListArgs are the arguments of PresetList, the same as list_presets.
type PresetListArgs struct {
	Type, Printer, FilamentType, Source string
}

// PresetList is list_presets without paging, for the command line: the same
// filters and the same body, every preset on one page.
func (s *Server) PresetList(ctx context.Context, a PresetListArgs) *mcp.CallToolResult {
	in := listPresetsInput{Type: a.Type}
	if a.Printer != "" {
		in.Printer = &a.Printer
	}
	if a.FilamentType != "" {
		in.FilamentType = &a.FilamentType
	}
	if a.Source != "" {
		in.Source = &a.Source
	}
	return s.listPresetsResult(ctx, in, true)
}

// listPresetsResult is list_presets; with all set it returns every preset
// instead of one page.
func (s *Server) listPresetsResult(ctx context.Context, in listPresetsInput, all bool) *mcp.CallToolResult {
	typ, err := profiles.ParseType(in.Type)
	if err != nil {
		return invalidInput(err.Error(), "Use type printer, process or filament.")
	}
	store, fail := s.presetStore(ctx)
	if fail != nil {
		return fail
	}

	printer := strings.TrimSpace(deref(in.Printer))
	filter := profiles.Filter{Source: deref(in.Source), FilamentType: strings.TrimSpace(deref(in.FilamentType))}
	shown := printer
	switch strings.ToLower(printer) {
	case "":
		if typ == profiles.TypePrinter {
			filter.PrinterModel, shown = "Creality K2", "Creality K2 (every nozzle)"
		} else {
			filter.Printer, shown = k2Printer, k2Printer
		}
	case "any":
		shown = "any Creality printer"
	case "all":
		filter.PrinterModel, shown = "Creality K2", "Creality K2 (every nozzle)"
	default:
		filter.Printer = printer
	}
	descs, err := store.List(typ, filter)
	if err != nil {
		if filter.Printer != "" {
			return notFound(fmt.Sprintf("No printer preset %q", filter.Printer),
				"Call list_presets with {\"type\": \"printer\"} to see the printer names, or pass printer \"any\".")
		}
		return invalidInput(err.Error(), "Check the arguments against the tool's input schema.")
	}

	page := 1
	if in.Page != nil {
		page = *in.Page
	}
	window, meta, next, err := paginatePage(descs, page, func(w []profiles.Descriptor) (string, error) { return presetRows(typ, w), nil })
	if err != nil {
		return failure(ctx, "page the presets", err, "")
	}
	if all {
		window, meta, next = descs, render.PageMeta{Page: 1, Total: len(descs), TotalPages: 1}, ""
	}
	front := listPresetsFront{Type: string(typ), Printer: shown, Count: len(descs), ProfileVersion: store.Info().Version, PageMeta: meta}

	var b strings.Builder
	switch {
	case len(descs) == 0:
		fmt.Fprintf(&b, "No %s presets match for %s.", typ, shown)
		if filter.FilamentType != "" {
			b.WriteString(" Try another filament_type, or list without it.")
		}
		b.WriteString(" Call list_presets with printer \"any\" to widen the search.")
	case len(window) == 0:
		fmt.Fprintf(&b, "Page %d is past the end: there are %d presets on %d page(s).", meta.Page, meta.Total, meta.TotalPages)
	default:
		fmt.Fprintf(&b, "%d %s preset(s) for %s; page %d of %d.\n\n", len(descs), typ, shown, meta.Page, meta.TotalPages)
		b.WriteString(presetHeader(typ) + "\n" + presetRows(typ, window) + "\n")
		b.WriteString(strings.TrimSpace(next))
		b.WriteString("\nNext: get_preset with {\"type\": \"" + string(typ) + "\", \"name\": \"<name>\"} for every value, or create_project to use them.")
	}
	return successResult(front, strings.TrimRight(b.String(), "\n"))
}

func presetHeader(t profiles.Type) string {
	switch t {
	case profiles.TypeProcess:
		return "name | source | layer height | walls | infill"
	case profiles.TypeFilament:
		return "name | source | type | vendor | filament_id | nozzle temp"
	}
	return "name | source | nozzle | bed"
}

func presetRows(t profiles.Type, ds []profiles.Descriptor) string {
	lines := make([]string, len(ds))
	for i, d := range ds {
		var facts string
		switch t {
		case profiles.TypeProcess:
			facts = fmt.Sprintf("%s | %s | %s", dash(d.LayerHeight), dash(d.WallLoops), dash(d.InfillDense))
		case profiles.TypeFilament:
			facts = fmt.Sprintf("%s | %s | %s | %s", dash(d.FilamentType), dash(d.FilamentVendor), dash(d.FilamentID), dash(d.NozzleTemp))
		default:
			facts = fmt.Sprintf("%s | %s", dash(d.NozzleDiameter), bedText(d))
		}
		lines[i] = fmt.Sprintf("%s | %s | %s", pipeSafe(d.Name), d.Source, facts)
	}
	return strings.Join(lines, "\n")
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return pipeSafe(s)
}

func bedText(d profiles.Descriptor) string {
	if d.BedX == 0 && d.BedY == 0 {
		return "-"
	}
	return fmt.Sprintf("%gx%g", d.BedX, d.BedY)
}

// --- get_preset ---

type getPresetInput struct {
	Type      string   `json:"type"`
	Name      string   `json:"name"`
	CompareTo *string  `json:"compare_to,omitempty"`
	Keys      []string `json:"keys,omitempty"`
	Level     *string  `json:"level,omitempty"`
}

type getPresetFront struct {
	Type               string   `yaml:"type"`
	Name               string   `yaml:"name"`
	Source             string   `yaml:"source"`
	InheritsChain      []string `yaml:"inherits_chain"`
	CompatiblePrinters []string `yaml:"compatible_printers,omitempty"`
	ValuesShown        int      `yaml:"values_shown"`
	Differences        *int     `yaml:"differences,omitempty"`
	CompareTo          string   `yaml:"compare_to,omitempty"`
}

// shownValue is one key of a preset as shown.
type shownValue struct {
	key, a, b string
	diff      bool
}

func (s *Server) getPreset(ctx context.Context, _ *mcp.CallToolRequest, in getPresetInput) (*mcp.CallToolResult, any, error) {
	typ, err := profiles.ParseType(in.Type)
	if err != nil {
		return invalidInput(err.Error(), "Use type printer, process or filament."), nil, nil
	}
	level, bad := parseLevel(in.Level)
	if bad != nil {
		return bad, nil, nil
	}
	store, fail := s.presetStore(ctx)
	if fail != nil {
		return fail, nil, nil
	}
	cat, _, fail2 := s.catalogOrFail(ctx)
	if fail2 != nil {
		return fail2, nil, nil
	}
	name := strings.TrimSpace(in.Name)
	listHint := "Call list_presets with {\"type\": \"" + string(typ) + "\"} to see the preset names."
	preset, err := store.Get(typ, name)
	if err != nil {
		return notFound(fmt.Sprintf("No %s preset %q", typ, name), listHint), nil, nil
	}

	front := getPresetFront{
		Type: string(typ), Name: preset.Name, Source: preset.Source, InheritsChain: preset.InheritsChain,
		CompatiblePrinters: preset.List("compatible_printers"),
	}
	wanted := map[string]bool{}
	for _, k := range in.Keys {
		if k = strings.TrimSpace(k); k != "" {
			wanted[k] = true
		}
	}

	var values []shownValue
	if cmp := strings.TrimSpace(deref(in.CompareTo)); cmp != "" {
		other := preset
		if strings.EqualFold(cmp, "parent") {
			parent := preset.Parent()
			if parent == "" {
				return invalidInput(fmt.Sprintf("%s has no parent preset to compare with", preset.Name), "Pass compare_to as the name of another "+string(typ)+" preset."), nil, nil
			}
			cmp = parent
		}
		if other, err = store.Get(typ, cmp); err != nil {
			return notFound(fmt.Sprintf("No %s preset %q to compare with", typ, cmp), listHint), nil, nil
		}
		front.CompareTo = other.Name
		for _, d := range profiles.Diff(preset, other) {
			if len(wanted) > 0 && !wanted[d.Key] {
				continue
			}
			a, b := diffText(cat, d.Key, d.A, d.InA), diffText(cat, d.Key, d.B, d.InB)
			if a == b {
				continue // 15 and 15% are the same percentage
			}
			values = append(values, shownValue{key: d.Key, a: a, b: b, diff: true})
		}
		n := len(values)
		front.Differences = &n
	} else {
		for key, v := range preset.Values {
			if len(wanted) > 0 && !wanted[key] {
				continue
			}
			values = append(values, shownValue{key: key, a: shownPresetValue(cat, key, v)})
		}
	}
	// Level limits the keys shown when none were asked for by name.
	beforeLevel := len(values)
	if len(wanted) == 0 {
		kept := values[:0]
		for _, v := range values {
			if o, ok := cat.Get(v.key); ok {
				if levelAllows(level, o) {
					kept = append(kept, v)
				}
			} else if level == catalog.LevelAll {
				kept = append(kept, v)
			}
		}
		values = kept
	}
	front.ValuesShown = len(values)

	body := presetBody(cat, preset, front.CompareTo, values, beforeLevel, level)
	var notes []string
	for k := range wanted {
		found := false
		for _, v := range values {
			if v.key == k {
				found = true
			}
		}
		if !found {
			notes = append(notes, k)
		}
	}
	sort.Strings(notes)
	if len(notes) > 0 {
		body += "\n\nNot set in this preset (or unchanged in compare mode): " + strings.Join(notes, ", ") + "."
	}
	if len(wanted) == 0 && level != catalog.LevelAll {
		body += "\n\nShowing " + string(level) + " level settings; pass level \"all\" for every key, or keys for specific ones."
	}
	return successResult(front, capText(body, maxOutputBytes, "pass keys for the settings you need, or a lower level")), nil, nil
}

func levelAllows(l catalog.Level, o *catalog.Option) bool {
	rank := map[string]int{"simple": 0, "advanced": 1, "develop": 2}[o.UILevel]
	switch l {
	case catalog.LevelBeginner:
		return rank == 0
	case catalog.LevelAdvanced:
		return rank <= 1
	}
	return true
}

func diffText(cat *catalog.Catalog, key string, v any, present bool) string {
	if !present {
		return "(not set)"
	}
	return shownPresetValue(cat, key, v)
}

// shownPresetValue is a preset value as text. A percentage always carries its
// percent sign: presets write 15 in some files and 15% in others.
func shownPresetValue(cat *catalog.Catalog, key string, v any) string {
	if o, ok := cat.Get(key); ok && o.ValueType == "percent" {
		switch x := v.(type) {
		case string:
			v = percentText(x)
		case []string:
			list := make([]string, len(x))
			for i, e := range x {
				list[i] = percentText(e)
			}
			v = list
		}
	}
	return showPresetValue(v)
}

func percentText(s string) string {
	if _, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil {
		return strings.TrimSpace(s) + "%"
	}
	return s
}

// presetBody renders the values grouped by GUI area, in the app's order.
func presetBody(cat *catalog.Catalog, p profiles.Preset, compare string, values []shownValue, total int, level catalog.Level) string {
	order := map[string]int{}
	for i, o := range cat.Options() {
		order[o.Key] = i
	}
	type group struct {
		area  string
		first int
		vals  []shownValue
	}
	groups := map[string]*group{}
	for _, v := range values {
		area, idx := "Other (no GUI line)", 1<<30
		if o, ok := cat.Get(v.key); ok {
			idx = order[v.key]
			if a := areaOf(o); a != "" {
				area = a
			}
		}
		g := groups[area]
		if g == nil {
			g = &group{area: area, first: idx}
			groups[area] = g
		}
		g.first = min(g.first, idx)
		g.vals = append(g.vals, v)
	}
	var list []*group
	for _, g := range groups {
		sort.SliceStable(g.vals, func(i, j int) bool { return keyOrder(order, g.vals[i].key) < keyOrder(order, g.vals[j].key) })
		list = append(list, g)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].first < list[j].first })

	var b strings.Builder
	if compare != "" {
		if total > len(values) {
			fmt.Fprintf(&b, "%s `%s` compared with `%s`: shows %d of %d differing key(s) at level %s; pass level all or keys for the rest. Shown as `this -> other`.\n", p.Type, p.Name, compare, len(values), total, level)
		} else {
			fmt.Fprintf(&b, "%s `%s` compared with `%s`: %d differing key(s), shown as `this -> other`.\n", p.Type, p.Name, compare, len(values))
		}
	} else {
		fmt.Fprintf(&b, "%s `%s` (%s; inherits %s): %d value(s).\n", p.Type, p.Name, p.Source, strings.Join(p.InheritsChain[min(1, len(p.InheritsChain)):], " > "), len(values))
		if len(p.InheritsChain) <= 1 {
			b.Reset()
			fmt.Fprintf(&b, "%s `%s` (%s; no parent): %d value(s).\n", p.Type, p.Name, p.Source, len(values))
		}
	}
	if len(values) == 0 {
		b.WriteString("\nNothing to show at this level.")
		return b.String()
	}
	for _, g := range list {
		fmt.Fprintf(&b, "\n### %s\n\n", g.area)
		for _, v := range g.vals {
			label := ""
			if o, ok := cat.Get(v.key); ok && o.Title() != v.key {
				label = " (" + o.Title() + ")"
			}
			if v.diff {
				fmt.Fprintf(&b, "- %s%s: %s -> %s\n", v.key, label, v.a, v.b)
			} else {
				fmt.Fprintf(&b, "- %s%s = %s\n", v.key, label, v.a)
			}
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func keyOrder(order map[string]int, key string) int {
	if i, ok := order[key]; ok {
		return i
	}
	return 1 << 30
}
