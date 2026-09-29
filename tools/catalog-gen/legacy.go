package main

// legacy.go: the keys Creality Print still finds in old preset files and
// project configs but no longer defines. They are not drift: the application
// renames, converts or silently drops them on load.
//
// Sources, all in PrintConfig.cpp unless noted:
//   - renamed / dropped: the if-else chain in PrintConfigDef::handle_legacy
//     (opt_key == "old" ... opt_key = "new"; opt_key = "" erases the key);
//   - ignored: the `ignore` set at the end of handle_legacy;
//   - retired: definitions that were commented out, and keys commented out of the
//     preset key lists in Preset.cpp (the app then discards them as unknown).

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// SlimLegacy is one legacy key of the slim catalog.
type SlimLegacy struct {
	Key         string `json:"key"`
	Kind        string `json:"kind"` // renamed, dropped, ignored, retired
	Replacement string `json:"replacement,omitempty"`
	Source      string `json:"source"` // file:line of the evidence
}

var (
	reLegacyKey    = regexp.MustCompile(`opt_key\s*==\s*"([^"]+)"`)
	reLegacyAssign = regexp.MustCompile(`opt_key\s*=\s*"([^"]*)"`)
	reCommentedKey = regexp.MustCompile(`(?://+|/\*)\s*"([A-Za-z0-9_#]+)"`)
	reCommentedDef = regexp.MustCompile(`^\s*//+\s*def\s*=\s*this->add(?:_nullable)?\(\s*"([A-Za-z0-9_]+)"`)
)

// legacyKeys extracts the legacy key set. Keys that the catalog defines as
// print options are never legacy (some are only value-converted or erased under
// a condition, for example initial_layer_speed given as a percentage).
func (b *Build) legacyKeys() []SlimLegacy {
	defined := map[string]bool{}
	for k := range b.Defs.classes["print"] {
		defined[k] = true
	}
	found := map[string]SlimLegacy{}
	add := func(l SlimLegacy) {
		if l.Key == "" || defined[l.Key] {
			return
		}
		if _, ok := found[l.Key]; !ok {
			found[l.Key] = l
		}
	}
	src := func(ct *CText, line int) string { return fmt.Sprintf("%s:%d", ct.Rel, line) }

	// 1. handle_legacy: renames and explicit drops
	funcs := newGuiParser(b.CfgCt, b.CfgTree).funcs
	body := funcs["PrintConfigDef::handle_legacy"]
	if body == nil {
		b.warnings = append(b.warnings, "PrintConfigDef::handle_legacy not found; legacy keys not extracted")
	} else {
		for _, n := range body.Children {
			header, texts := legacyElement(n)
			if header == "" {
				continue
			}
			var assigned []string
			for _, t := range texts {
				for _, m := range reLegacyAssign.FindAllStringSubmatch(t, -1) {
					assigned = append(assigned, m[1])
				}
			}
			for _, m := range reLegacyKey.FindAllStringSubmatch(header, -1) {
				key := m[1]
				repl, dropped := "", false
				for _, a := range assigned {
					switch {
					case a == "":
						dropped = true
					case a != key && repl == "":
						repl = a
					}
				}
				line := n.Line
				switch {
				case repl != "":
					add(SlimLegacy{Key: key, Kind: "renamed", Replacement: repl, Source: src(b.CfgCt, line)})
				case dropped:
					add(SlimLegacy{Key: key, Kind: "dropped", Source: src(b.CfgCt, line)})
				}
			}
		}
	}

	// 2. the ignore set
	if kl := b.Lists["ignore"]; kl != nil {
		for _, it := range kl.Items {
			add(SlimLegacy{Key: it.Key, Kind: "ignored", Source: src(b.CfgCt, it.Line)})
		}
	} else {
		b.warnings = append(b.warnings, "handle_legacy ignore list not found")
	}

	// 3. retired: commented-out definitions and commented-out preset list entries
	for i, ln := range b.CfgCt.Raw {
		if m := reCommentedDef.FindStringSubmatch(ln); m != nil {
			add(SlimLegacy{Key: m[1], Kind: "retired", Source: src(b.CfgCt, i+1)})
		}
	}
	for _, name := range []string{"s_Preset_print_options", "s_Preset_filament_options", "s_Preset_printer_options", "s_Preset_machine_limits_options"} {
		kl := b.Lists[name]
		if kl == nil {
			continue
		}
		for l := kl.Line; l-1 < len(b.PresetCt.Raw); l++ {
			raw := b.PresetCt.Raw[l-1]
			for _, m := range reCommentedKey.FindAllStringSubmatch(raw, -1) {
				add(SlimLegacy{Key: m[1], Kind: "retired", Source: src(b.PresetCt, l)})
			}
			if l > kl.Line && strings.Contains(raw, "};") {
				break
			}
		}
	}

	out := make([]SlimLegacy, 0, len(found))
	for _, l := range found {
		out = append(out, l)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// legacyElement returns the condition text of one link of the if-else chain in
// handle_legacy and the texts of the statements it runs.
func legacyElement(n *Node) (header string, bodies []string) {
	switch n.Kind {
	case nBlock:
		h := strings.TrimSpace(n.Header)
		h = strings.TrimSpace(strings.TrimPrefix(h, "else"))
		if !strings.HasPrefix(h, "if") {
			return "", nil
		}
		var collect func(x *Node)
		collect = func(x *Node) {
			for _, c := range x.Children {
				if c.Kind == nStmt {
					bodies = append(bodies, c.Text)
				} else {
					// nested if headers of the block matter too: they hold the values
					collect(c)
				}
			}
		}
		collect(n)
		return h, bodies
	case nStmt:
		t := strings.TrimSpace(n.Text)
		t = strings.TrimSpace(strings.TrimPrefix(t, "else"))
		if !strings.HasPrefix(t, "if") {
			return "", nil
		}
		i := strings.Index(t, "(")
		if i < 0 {
			return "", nil
		}
		j := matchParen(t, i)
		if j < 0 {
			return "", nil
		}
		return t[i : j+1], []string{t[j+1:]}
	}
	return "", nil
}
