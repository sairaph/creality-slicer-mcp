package catalog

import (
	"sort"
	"strings"
	"unicode"
)

// Filter narrows Search and Tree.
type Filter struct {
	Owner      string // preset type or owner: process, filament, printer, project, plate, sla_*; "" for any
	Level      Level  // beginner, advanced or all (zero value: all)
	Scope      Scope  // object, part, layer_range or plate; "" for any (ScopePreset: preset-level settings)
	PathPrefix string // GUI path such as "process/Quality"; matched per segment, case-insensitive
}

// Hit is one search result.
type Hit struct {
	Option *Option
	Score  int
}

func (f Filter) allows(o *Option) bool {
	if levelRank(o.UILevel) > f.Level.maxRank() {
		return false
	}
	if f.Owner != "" {
		ok := o.Owner == f.Owner
		for _, p := range o.PresetTypes {
			if p == f.Owner {
				ok = true
			}
		}
		if !ok {
			return false
		}
	}
	if f.Scope != "" && !o.HasScope(f.Scope) {
		return false
	}
	if f.PathPrefix != "" && !pathHasPrefix(o.GUIPath(), f.PathPrefix) {
		return false
	}
	return true
}

func splitPath(p string) []string {
	var out []string
	for _, s := range strings.Split(p, "/") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// pathHasPrefix compares segment by segment ignoring case; the last prefix
// segment may be a prefix of the path segment.
func pathHasPrefix(path, prefix string) bool {
	ps, qs := splitPath(path), splitPath(prefix)
	if len(qs) > len(ps) {
		return false
	}
	for i, q := range qs {
		p := ps[i]
		if i == len(qs)-1 {
			if !strings.HasPrefix(strings.ToLower(p), strings.ToLower(q)) {
				return false
			}
		} else if !strings.EqualFold(p, q) {
			return false
		}
	}
	return true
}

func tokens(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

// Search finds settings whose key, label, full label, category, GUI path,
// enum labels or (when texts are attached) tooltip contain every query token.
// Results are ordered by score, then by catalog order, so the ranking is
// stable. An empty query returns every setting the filter allows in catalog
// order.
func (c *Catalog) Search(query string, f Filter) []Hit {
	qt := tokens(query)
	whole := strings.ToLower(strings.TrimSpace(query))
	var hits []Hit
	for _, o := range c.opts {
		if !f.allows(o) {
			continue
		}
		if len(qt) == 0 {
			hits = append(hits, Hit{Option: o})
			continue
		}
		if s, ok := c.score(o, qt, whole); ok {
			hits = append(hits, Hit{Option: o, Score: s})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	return hits
}

func (c *Catalog) score(o *Option, qt []string, whole string) (int, bool) {
	key := strings.ToLower(o.Key)
	keyWords := tokens(o.Key)
	label := strings.ToLower(o.Label)
	full := strings.ToLower(o.FullLabel)
	cat := strings.ToLower(o.Category)
	path := strings.ToLower(o.GUIPath())
	var enumLabels string
	if o.Enum != nil {
		enumLabels = strings.ToLower(strings.Join(o.Enum.Labels, " "))
	}
	var tip string
	if t, ok := c.Tooltip(o.Key); ok {
		tip = strings.ToLower(t)
	}
	total := 0
	switch {
	case key == whole:
		total += 1000
	case label != "" && label == whole:
		total += 800
	}
	for _, t := range qt {
		best := 0
		set := func(s int) {
			if s > best {
				best = s
			}
		}
		for _, w := range keyWords {
			switch {
			case w == t:
				set(120)
			case strings.HasPrefix(w, t):
				set(90)
			}
		}
		if strings.Contains(key, t) {
			set(70)
		}
		if strings.Contains(label, t) {
			set(80)
		}
		if strings.Contains(full, t) {
			set(60)
		}
		if strings.Contains(cat, t) || strings.Contains(path, t) {
			set(30)
		}
		if strings.Contains(enumLabels, t) {
			set(25)
		}
		if tip != "" && strings.Contains(tip, t) {
			set(10)
		}
		if best == 0 {
			return 0, false
		}
		total += best
	}
	return total, true
}
