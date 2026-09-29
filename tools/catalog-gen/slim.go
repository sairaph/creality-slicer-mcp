package main

// slim.go: the slim catalog embedded by internal/catalog. It carries no
// tooltip text (Creality's tooltips are AGPL text and stay out of committed
// files); each option only has tooltip_hash, the FNV-1a 64 hash of the exact
// msgid, so the runtime can look the wording up in the installed application's
// own .mo files.

import (
	"fmt"
	"hash/fnv"
	"sort"
	"strings"
)

// SlimFormat is bumped when the slim layout changes incompatibly.
const SlimFormat = 1

type SlimFile struct {
	Format  int          `json:"format"`
	Source  SlimSource   `json:"source"`
	Options []SlimOption `json:"options"`
}

type SlimSource struct {
	Ref    string `json:"ref"`
	Commit string `json:"commit"`
}

type SlimEnum struct {
	Values []string `json:"values"`
	Labels []string `json:"labels,omitempty"`
}

type SlimGUI struct {
	Tab   string `json:"tab"`
	Page  string `json:"page"`
	Group string `json:"group"`
}

type SlimGate struct {
	Effect   string   `json:"effect"` // line: row shown or hidden; field: input enabled or disabled
	Constant string   `json:"constant,omitempty"`
	When     *Cond    `json:"when,omitempty"`
	Context  []*Cond  `json:"context,omitempty"`
	Drivers  []string `json:"drivers,omitempty"`
}

type SlimForced struct {
	Set  string  `json:"set"`
	When []*Cond `json:"when,omitempty"`
}

type SlimRestriction struct {
	When   string   `json:"when"`
	Values []string `json:"values"`
}

type SlimOption struct {
	Key         string            `json:"key"`
	Owner       string            `json:"owner"`
	PresetTypes []string          `json:"preset_types"`
	ValueType   string            `json:"value_type"`
	IsVector    bool              `json:"is_vector"`
	Nullable    bool              `json:"nullable"`
	Default     interface{}       `json:"default"`
	Min         *float64          `json:"min,omitempty"`
	Max         *float64          `json:"max,omitempty"`
	Enum        *SlimEnum         `json:"enum,omitempty"`
	Label       string            `json:"label,omitempty"`
	FullLabel   string            `json:"full_label,omitempty"`
	Sidetext    string            `json:"sidetext,omitempty"`
	Category    string            `json:"category,omitempty"`
	UILevel     string            `json:"ui_level"`
	GUI         *SlimGUI          `json:"gui,omitempty"`
	Scopes      []string          `json:"scopes,omitempty"`
	GatedBy     []SlimGate        `json:"gated_by,omitempty"`
	ForcedBy    []SlimForced      `json:"forced_by,omitempty"`
	EnumRestr   []SlimRestriction `json:"gui_enum_restrictions,omitempty"`
	VendorLock  string            `json:"creality_vendor_policy,omitempty"`
	CLIFlags    []string          `json:"cli_flags,omitempty"`
	NoCLI       bool              `json:"nocli,omitempty"`
	TooltipHash string            `json:"tooltip_hash,omitempty"`
}

// TooltipHash is the lowercase 16-digit hex FNV-1a 64 hash of a msgid.
func TooltipHash(msgid string) string {
	h := fnv.New64a()
	h.Write([]byte(msgid))
	return fmt.Sprintf("%016x", h.Sum64())
}

// slim builds the slim catalog from an assembled full catalog. Only options of
// the print definition class (the real settings) are included; command-line
// options and G-code placeholders are not part of it.
func (b *Build) slim(cat *Catalog) *SlimFile {
	sf := &SlimFile{Format: SlimFormat, Source: SlimSource{Ref: b.Ref, Commit: b.Commit}}
	for i := range cat.Options {
		e := &cat.Options[i]
		// SLA options have no GUI in this build and are irrelevant for FDM printers
		if e.DefClass != "print" || strings.HasPrefix(e.Owner, "sla_") {
			continue
		}
		so := SlimOption{
			Key: e.Key, Owner: e.Owner, PresetTypes: e.PresetTypes, ValueType: e.ValueType,
			IsVector: e.IsVector, Nullable: e.Nullable, Default: e.Default,
			Label: e.Label, FullLabel: e.FullLabel, Sidetext: e.Sidetext, Category: e.Category,
			UILevel: e.UILevel, CLIFlags: e.CliFlags, NoCLI: e.Cli == "nocli",
		}
		// keys shared with SLA presets keep only their FDM preset types
		fdm := []string{}
		for _, p := range so.PresetTypes {
			if !strings.HasPrefix(p, "sla_") {
				fdm = append(fdm, p)
			}
		}
		so.PresetTypes = fdm
		// Effective limits: ConfigOptionDef stores min and max as int, so the
		// truncated value is what the application enforces.
		so.Min, so.Max = e.Min, e.Max
		if e.MinEffective != nil {
			v := float64(*e.MinEffective)
			so.Min = &v
		}
		if e.MaxEffective != nil {
			v := float64(*e.MaxEffective)
			so.Max = &v
		}
		if e.Enum != nil && len(e.Enum.Values) > 0 {
			se := &SlimEnum{Values: e.Enum.Values}
			if len(e.Enum.Labels) == len(e.Enum.Values) {
				se.Labels = e.Enum.Labels
			}
			so.Enum = se
		}
		if len(e.GuiLocations) > 0 {
			l := e.GuiLocations[0]
			so.GUI = &SlimGUI{Tab: l.Tab, Page: l.Page, Group: l.Group}
		}
		if e.Scopes.PerObject {
			so.Scopes = append(so.Scopes, "object")
		}
		if e.Scopes.PerPartOrMod {
			so.Scopes = append(so.Scopes, "part")
		}
		if e.Scopes.PerLayerRange {
			so.Scopes = append(so.Scopes, "layer_range")
		}
		if e.Scopes.PerPlate {
			so.Scopes = append(so.Scopes, "plate")
		}
		switch {
		case strings.Contains(e.VendorPolicy, "type 1"):
			so.VendorLock = "read_only"
		case strings.Contains(e.VendorPolicy, "type 2"):
			so.VendorLock = "hidden"
		}
		for _, g := range e.GatedBy {
			sg := SlimGate{Effect: g.Effect, Constant: g.Constant, Drivers: g.Drivers}
			if g.Constant == "" {
				sg.When = sanitize(parseCond(g.Full, b.EnumMaps))
			}
			sg.Context = sanitizeAll(contextConds(g.ContextFull, b.EnumMaps))
			so.GatedBy = append(so.GatedBy, sg)
		}
		for _, f := range e.ForcedChanges {
			set, ok := forcedValue(f.ValueFull, b.EnumMaps)
			if !ok {
				continue // the value is computed in code; nothing to tell a reader
			}
			so.ForcedBy = append(so.ForcedBy, SlimForced{Set: set, When: sanitizeAll(contextConds(f.ContextFull, b.EnumMaps))})
		}
		for _, r := range b.EnumRestr {
			if r.Key == e.Key {
				so.EnumRestr = append(so.EnumRestr, SlimRestriction{When: r.Condition, Values: r.Values})
			}
		}
		if e.Tooltip != "" {
			so.TooltipHash = TooltipHash(e.Tooltip)
		}
		sf.Options = append(sf.Options, so)
	}
	// Order: settings shown by the GUI first, in the order the tabs, pages,
	// groups and lines are built; then the others in definition order. The
	// runtime keeps this order for browsing and for ties in search ranking.
	seq := map[string]int{}
	n := 0
	var tabs []*GuiTab
	if b.Gui != nil {
		tabs = b.Gui.tabs
	}
	for _, tab := range tabs {
		for _, pg := range tab.Pages {
			for _, gr := range pg.Groups {
				for _, o := range gr.Options {
					if _, ok := seq[o.Key]; !ok {
						seq[o.Key] = n
						n++
					}
				}
			}
		}
	}
	sort.SliceStable(sf.Options, func(i, j int) bool {
		si, iok := seq[sf.Options[i].Key]
		sj, jok := seq[sf.Options[j].Key]
		if iok != jok {
			return iok
		}
		return iok && si < sj
	})
	return sf
}

func sanitizeAll(cs []*Cond) []*Cond {
	for i, c := range cs {
		cs[i] = sanitize(c)
	}
	return cs
}
