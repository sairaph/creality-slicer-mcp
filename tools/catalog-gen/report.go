package main

// report.go: catalog comparison and stratified sampling. Both operate only on
// generated catalog JSON files.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"sort"
	"strings"
)

func loadCatalog(path string) (map[string]interface{}, map[string]map[string]interface{}, []string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, nil, err
	}
	var top map[string]interface{}
	if err := json.Unmarshal(data, &top); err != nil {
		return nil, nil, nil, err
	}
	byID := map[string]map[string]interface{}{}
	var order []string
	opts, _ := top["options"].([]interface{})
	for _, o := range opts {
		m := o.(map[string]interface{})
		id := m["id"].(string)
		byID[id] = m
		order = append(order, id)
	}
	return top, byID, order, nil
}

func jsonStr(v interface{}) string {
	if v == nil {
		return "null"
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
	s := strings.TrimSpace(buf.String())
	if len(s) > 90 {
		s = s[:90] + "..."
	}
	return s
}

func strList(v interface{}) []string {
	var out []string
	if l, ok := v.([]interface{}); ok {
		for _, x := range l {
			out = append(out, fmt.Sprint(x))
		}
	}
	return out
}

func setDiff(a, b []string) (onlyA, onlyB []string) {
	sa, sb := map[string]bool{}, map[string]bool{}
	for _, x := range a {
		sa[x] = true
	}
	for _, x := range b {
		sb[x] = true
	}
	for _, x := range a {
		if !sb[x] {
			onlyA = append(onlyA, x)
		}
	}
	for _, x := range b {
		if !sa[x] {
			onlyB = append(onlyB, x)
		}
	}
	return
}

type Change struct {
	ID     string `json:"id"`
	Field  string `json:"field"`
	Old    string `json:"old"`
	New    string `json:"new"`
	Detail string `json:"detail,omitempty"`
}

func locPaths(m map[string]interface{}) []string {
	var out []string
	if l, ok := m["gui_locations"].([]interface{}); ok {
		for _, x := range l {
			lm := x.(map[string]interface{})
			out = append(out, fmt.Sprint(lm["path"]))
		}
	}
	return out
}

func enumValues(m map[string]interface{}) []string {
	if e, ok := m["enum"].(map[string]interface{}); ok {
		return strList(e["values"])
	}
	return nil
}

func scopesOf(m map[string]interface{}) string {
	sc, _ := m["scopes"].(map[string]interface{})
	var parts []string
	for _, k := range []string{"per_object", "per_part_or_modifier", "per_layer_range", "per_plate"} {
		if b, _ := sc[k].(bool); b {
			parts = append(parts, k)
		}
	}
	return strings.Join(parts, ",")
}

func runDiff(oldPath, newPath, outPath string) error {
	oldTop, oldBy, oldOrder, err := loadCatalog(oldPath)
	if err != nil {
		return err
	}
	newTop, newBy, newOrder, err := loadCatalog(newPath)
	if err != nil {
		return err
	}
	oldRef := fmt.Sprint(oldTop["meta"].(map[string]interface{})["ref"])
	newRef := fmt.Sprint(newTop["meta"].(map[string]interface{})["ref"])

	var added, removed []string
	for _, id := range newOrder {
		if _, ok := oldBy[id]; !ok {
			added = append(added, id)
		}
	}
	for _, id := range oldOrder {
		if _, ok := newBy[id]; !ok {
			removed = append(removed, id)
		}
	}
	var changes []Change
	fields := []string{"value_type", "is_vector", "nullable", "owner", "ui_level", "gui_location", "min", "max", "sidetext", "category", "cli", "label", "tooltip"}
	for _, id := range newOrder {
		o, ok := oldBy[id]
		if !ok {
			continue
		}
		n := newBy[id]
		for _, f := range fields {
			if !reflect.DeepEqual(o[f], n[f]) {
				changes = append(changes, Change{ID: id, Field: f, Old: jsonStr(o[f]), New: jsonStr(n[f])})
			}
		}
		if !reflect.DeepEqual(o["default"], n["default"]) {
			changes = append(changes, Change{ID: id, Field: "default", Old: jsonStr(o["default"]), New: jsonStr(n["default"])})
		}
		if oe, ne := enumValues(o), enumValues(n); !reflect.DeepEqual(oe, ne) {
			oa, na := setDiff(oe, ne)
			changes = append(changes, Change{ID: id, Field: "enum_values", Old: fmt.Sprintf("%d values", len(oe)), New: fmt.Sprintf("%d values", len(ne)), Detail: fmt.Sprintf("removed=%v added=%v", oa, na)})
		}
		if op, np := locPaths(o), locPaths(n); !reflect.DeepEqual(op, np) {
			oa, na := setDiff(op, np)
			changes = append(changes, Change{ID: id, Field: "gui_locations", Old: strings.Join(op, " | "), New: strings.Join(np, " | "), Detail: fmt.Sprintf("removed=%v added=%v", oa, na)})
		}
		if os, ns := scopesOf(o), scopesOf(n); os != ns {
			changes = append(changes, Change{ID: id, Field: "scopes", Old: os, New: ns})
		}
		// gating summary
		og, _ := o["gated_by"].([]interface{})
		ng, _ := n["gated_by"].([]interface{})
		if len(og) != len(ng) {
			changes = append(changes, Change{ID: id, Field: "gating_rules", Old: fmt.Sprint(len(og)), New: fmt.Sprint(len(ng))})
		}
	}

	// GUI structure diff
	oldPages := guiStructure(oldTop)
	newPages := guiStructure(newTop)
	var pagesAdded, pagesRemoved, groupsAdded, groupsRemoved []string
	for p := range newPages {
		if _, ok := oldPages[p]; !ok {
			pagesAdded = append(pagesAdded, p)
		}
	}
	for p := range oldPages {
		if _, ok := newPages[p]; !ok {
			pagesRemoved = append(pagesRemoved, p)
		}
	}
	for p, gs := range newPages {
		for g := range gs {
			if og, ok := oldPages[p]; ok {
				if _, ok := og[g]; !ok {
					groupsAdded = append(groupsAdded, p+" > "+g)
				}
			}
		}
	}
	for p, gs := range oldPages {
		for g := range gs {
			if ng, ok := newPages[p]; ok {
				if _, ok := ng[g]; !ok {
					groupsRemoved = append(groupsRemoved, p+" > "+g)
				}
			}
		}
	}
	for _, s := range [][]string{pagesAdded, pagesRemoved, groupsAdded, groupsRemoved} {
		sort.Strings(s)
	}

	byField := map[string]int{}
	for _, c := range changes {
		byField[c.Field]++
	}

	if outPath != "" {
		out := map[string]interface{}{
			"old": oldRef, "new": newRef, "added": added, "removed": removed, "changes": changes,
			"pages_added": pagesAdded, "pages_removed": pagesRemoved, "groups_added": groupsAdded, "groups_removed": groupsRemoved,
			"changes_by_field": byField,
		}
		b, _ := json.MarshalIndent(out, "", " ")
		if err := os.WriteFile(outPath, b, 0o644); err != nil {
			return err
		}
	}

	fmt.Printf("# Catalog diff %s -> %s\n\n", oldRef, newRef)
	fmt.Printf("options: %d -> %d; added %d; removed %d; changed %d entries / %d field changes\n\n", len(oldOrder), len(newOrder), len(added), len(removed), countChangedIDs(changes), len(changes))
	fmt.Printf("field changes by kind: %v\n\n", byField)
	fmt.Printf("pages added: %v\npages removed: %v\ngroups added: %v\ngroups removed: %v\n\n", pagesAdded, pagesRemoved, groupsAdded, groupsRemoved)
	fmt.Printf("## added (%d)\n", len(added))
	for _, id := range added {
		m := newBy[id]
		fmt.Printf("- %s | %s | %s | %v | %v\n", id, m["owner"], m["value_type"], m["ui_level"], m["gui_location"])
	}
	fmt.Printf("\n## removed (%d)\n", len(removed))
	for _, id := range removed {
		m := oldBy[id]
		fmt.Printf("- %s | %s | %s | %v | %v\n", id, m["owner"], m["value_type"], m["ui_level"], m["gui_location"])
	}
	// scalar -> nullable vector conversions are reported as one group
	conv := map[string]bool{}
	var convList []string
	for _, c := range changes {
		if c.Field == "is_vector" && c.Old == "false" && c.New == "true" {
			conv[c.ID] = true
			convList = append(convList, c.ID)
		}
	}
	fmt.Printf("\n## scalar -> vector (per nozzle variant, nullable) conversions (%d)\n%s\n", len(convList), strings.Join(convList, ", "))
	fmt.Printf("\n## other changes\n")
	for _, c := range changes {
		if c.Field == "label" || c.Field == "tooltip" {
			continue
		}
		if conv[c.ID] && (c.Field == "is_vector" || c.Field == "nullable" || c.Field == "default") {
			continue
		}
		fmt.Printf("- %s [%s]: %s -> %s %s\n", c.ID, c.Field, c.Old, c.New, c.Detail)
	}
	var textOnly []string
	seen := map[string]bool{}
	for _, c := range changes {
		if (c.Field == "label" || c.Field == "tooltip") && !seen[c.ID] {
			seen[c.ID] = true
			textOnly = append(textOnly, c.ID)
		}
	}
	fmt.Printf("\n## label or tooltip text changed (%d): %s\n", len(textOnly), strings.Join(textOnly, ", "))
	return nil
}

func countChangedIDs(cs []Change) int {
	m := map[string]bool{}
	for _, c := range cs {
		m[c.ID] = true
	}
	return len(m)
}

func guiStructure(top map[string]interface{}) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	gui, _ := top["gui"].(map[string]interface{})
	tabs, _ := gui["tabs"].([]interface{})
	for _, t := range tabs {
		tm := t.(map[string]interface{})
		tabName := fmt.Sprint(tm["tab"])
		pages, _ := tm["pages"].([]interface{})
		for _, p := range pages {
			pm := p.(map[string]interface{})
			key := tabName + " > " + fmt.Sprint(pm["page"])
			if out[key] == nil {
				out[key] = map[string]bool{}
			}
			groups, _ := pm["groups"].([]interface{})
			for _, g := range groups {
				gm := g.(map[string]interface{})
				out[key][fmt.Sprint(gm["group"])] = true
			}
		}
	}
	return out
}

// runSample prints a stratified random sample for hand verification.
func runSample(path string, n int, seed int64) error {
	_, byID, order, err := loadCatalog(path)
	if err != nil {
		return err
	}
	rng := rand.New(rand.NewSource(seed))
	type stratum struct {
		name string
		want int
		pick func(m map[string]interface{}) bool
	}
	isB := func(m map[string]interface{}, k string) bool { b, _ := m[k].(bool); return b }
	strata := []stratum{
		{"enum", 3, func(m map[string]interface{}) bool {
			return m["value_type"] == "enum" && !isB(m, "is_vector") && m["owner"] != "placeholder"
		}},
		{"vector", 3, func(m map[string]interface{}) bool {
			return isB(m, "is_vector") && m["value_type"] != "enum" && m["owner"] != "placeholder"
		}},
		{"percent_or_float_or_percent", 2, func(m map[string]interface{}) bool {
			return (m["value_type"] == "percent" || m["value_type"] == "float_or_percent") && m["owner"] != "placeholder"
		}},
		{"string", 2, func(m map[string]interface{}) bool {
			return m["value_type"] == "string" && !isB(m, "is_vector") && m["owner"] != "placeholder"
		}},
		{"bool", 2, func(m map[string]interface{}) bool {
			return m["value_type"] == "bool" && !isB(m, "is_vector") && m["owner"] != "placeholder"
		}},
		{"per_object", 3, func(m map[string]interface{}) bool {
			sc, _ := m["scopes"].(map[string]interface{})
			return sc["per_object"] == true
		}},
	}
	total := 0
	for _, s := range strata {
		total += s.want
	}
	fmt.Printf("sample seed=%d size=%d (strata: enum 3, vector 3, percent 2, string 2, bool 2, per_object 3)\n", seed, total)
	used := map[string]bool{}
	for _, s := range strata {
		var pool []string
		for _, id := range order {
			if s.pick(byID[id]) && !used[id] {
				pool = append(pool, id)
			}
		}
		rng.Shuffle(len(pool), func(i, j int) { pool[i], pool[j] = pool[j], pool[i] })
		for i := 0; i < s.want && i < len(pool); i++ {
			id := pool[i]
			used[id] = true
			m := byID[id]
			src := m["source"].(map[string]interface{})
			sc, _ := m["scopes"].(map[string]interface{})
			fmt.Printf("\n[%s] %s\n  source: %v:%v (%v)\n  type: %v vector=%v nullable=%v cpp=%v owner=%v levels=%v\n  default: %s (raw %v)\n  min=%s max=%s sidetext=%q\n  label=%q\n  gui=%v\n  per_object=%v per_part=%v per_plate=%v\n",
				s.name, id, src["file"], src["line"], src["function"],
				m["value_type"], m["is_vector"], m["nullable"], m["cpp_storage_type"], m["owner"], m["ui_level"],
				jsonStr(m["default"]), m["default_raw"], jsonStr(m["min"]), jsonStr(m["max"]), jsonStr(m["sidetext"]),
				jsonStr(m["label"]), m["gui_location"], sc["per_object"], sc["per_part_or_modifier"], sc["per_plate"])
			if e, ok := m["enum"].(map[string]interface{}); ok {
				fmt.Printf("  enum: %s values=%s labels=%s\n", e["values_source"], jsonStr(e["values"]), jsonStr(e["labels"]))
			}
		}
	}
	return nil
}

// runTree prints the GUI layout of a catalog as a compact markdown tree.
// Markers after a key: O per-object override, P also per-part/modifier,
// L also per-layer-range, T per-plate.
func runTree(path string) error {
	top, byID, _, err := loadCatalog(path)
	if err != nil {
		return err
	}
	marker := func(key string) string {
		m, ok := byID[key]
		if !ok {
			return "*"
		}
		sc, _ := m["scopes"].(map[string]interface{})
		s := ""
		if sc["per_object"] == true {
			s += "O"
		}
		if sc["per_part_or_modifier"] == true {
			s += "P"
		}
		if sc["per_layer_range"] == true {
			s += "L"
		}
		if sc["per_plate"] == true {
			s += "T"
		}
		if s == "" {
			return ""
		}
		return ":" + s
	}
	gui, _ := top["gui"].(map[string]interface{})
	tabs, _ := gui["tabs"].([]interface{})
	for _, t := range tabs {
		tm := t.(map[string]interface{})
		fmt.Printf("#### tab `%v` (%v)\n\n", tm["tab"], tm["function"])
		pages, _ := tm["pages"].([]interface{})
		for _, p := range pages {
			pm := p.(map[string]interface{})
			extra := ""
			if d, ok := pm["dynamic"].(string); ok && d != "" {
				extra += " [" + d + "]"
			}
			if bc := strList(pm["build_conditions"]); len(bc) > 0 {
				extra += " [built when: " + strings.Join(bc, "; ") + "]"
			}
			fmt.Printf("- **%v**%s\n", pm["page"], extra)
			groups, _ := pm["groups"].([]interface{})
			for _, g := range groups {
				gm := g.(map[string]interface{})
				opts, _ := gm["options"].([]interface{})
				var keys []string
				seen := map[string]bool{}
				for _, o := range opts {
					om := o.(map[string]interface{})
					k := fmt.Sprint(om["key"])
					if seen[k] {
						continue
					}
					seen[k] = true
					mk := ""
					if om["build_macro"] != nil && fmt.Sprint(om["build_macro"]) != "" {
						mk = "^"
					}
					keys = append(keys, k+marker(k)+mk)
				}
				title := fmt.Sprint(gm["group"])
				if title == "" {
					title = "(no title)"
				}
				fmt.Printf("  - %s: %s\n", title, strings.Join(keys, ", "))
			}
		}
		fmt.Println()
	}
	return nil
}

// runDeps prints which options are gated by which config keys or capabilities.
func runDeps(path string) error {
	_, byID, order, err := loadCatalog(path)
	if err != nil {
		return err
	}
	type dep struct{ key, effect string }
	byDriver := map[string]map[dep]bool{}
	constant := map[string][]string{}
	for _, id := range order {
		m := byID[id]
		gs, _ := m["gated_by"].([]interface{})
		for _, g := range gs {
			gm := g.(map[string]interface{})
			d := dep{fmt.Sprint(m["key"]), fmt.Sprint(gm["effect"])}
			if c, ok := gm["constant"].(string); ok && c != "" {
				constant[c] = append(constant[c], d.key+"("+d.effect+")")
				continue
			}
			for _, dr := range strList(gm["drivers"]) {
				if byDriver[dr] == nil {
					byDriver[dr] = map[dep]bool{}
				}
				byDriver[dr][d] = true
			}
		}
	}
	var names []string
	for k := range byDriver {
		names = append(names, k)
	}
	sort.Strings(names)
	printGroup := func(title string, filter func(string) bool) {
		fmt.Printf("#### %s\n\n", title)
		for _, n := range names {
			if !filter(n) {
				continue
			}
			var items []string
			seen := map[string]bool{}
			for d := range byDriver[n] {
				s := d.key
				if d.effect == "field" {
					s += "~"
				}
				if !seen[s] {
					seen[s] = true
					items = append(items, s)
				}
			}
			sort.Strings(items)
			fmt.Printf("- `%s` -> %s\n", strings.TrimPrefix(n, "capability:"), strings.Join(items, ", "))
		}
		fmt.Println()
	}
	printGroup("Capability and scope flags", func(n string) bool { return strings.HasPrefix(n, "capability:") })
	printGroup("Config-key drivers (key gated by other keys)", func(n string) bool { return !strings.HasPrefix(n, "capability:") })
	for _, c := range []string{"false", "true"} {
		sort.Strings(constant[c])
		fmt.Printf("#### Gated by the literal `%s`\n\n%s\n\n", c, strings.Join(constant[c], ", "))
	}
	return nil
}
