package main

// cond.go: turn the C++ boolean conditions of the GUI gating code into a small
// structured tree (Cond) that the runtime can render as plain words. Only the
// shapes that occur in ConfigManipulation.cpp and Tab.cpp are understood;
// anything else becomes a "text" node holding the source text.

import (
	"regexp"
	"strconv"
	"strings"
)

// Cond is one node of a parsed condition.
//
//	and, or   A holds the operands
//	not       A holds one operand
//	cmp       Op is one of == != > < >= <=, A holds left and right
//	opt       a config value read: Key, Kind (bool int float enum string), T (C++ enum type)
//	num, bool, enum   V holds the value (enum: the option's string key)
//	cap       Name is a vendor or printer capability flag
//	call      Name is a helper function, A its argument
//	text      V is source text that could not be interpreted
type Cond struct {
	K    string      `json:"k"`
	Op   string      `json:"op,omitempty"`
	A    []*Cond     `json:"a,omitempty"`
	Key  string      `json:"key,omitempty"`
	Kind string      `json:"kind,omitempty"`
	T    string      `json:"t,omitempty"`
	Name string      `json:"name,omitempty"`
	V    interface{} `json:"v,omitempty"`
}

var (
	reCondOpt    = regexp.MustCompile(`^(?:.*[.>])?(opt_\w+|option)(?:<(.*)>)?\(\s*"([\w#]+)"\s*(?:,.*)?\)(?:\s*->\s*value)?$`)
	reCondNum    = regexp.MustCompile(`^-?\d+(?:\.\d*)?[fF]?$`)
	reCondCall   = regexp.MustCompile(`^(\w+)\((.*)\)$`)
	reCondEnumer = regexp.MustCompile(`^(?:(\w+)::)?(\w+)$`)
	reCondSet    = regexp.MustCompile(`^std::set<(\w+)>\s*\{(.*)\}\s*\.count\((.*)\)$`)
	reCondHas    = regexp.MustCompile(`^(?:.*[.>])?has\(\s*"([\w#]+)"\s*\)$`)
)

var condCapabilities = map[string]bool{
	"is_BBL_Printer": true, "is_BBL_printer": true, "is_CX_Printer": true, "is_CX_printer": true,
	"is_creality_vendor": true, "is_belt_machine": true, "bSEMM": true, "use_creality_tower": true,
	"can_flush_into_skeleton_for_printer": true, "support_multi_bed_types": true, "is_marlin_flavor": true,
	"is_global_config": true, "is_object_config": true, "is_plate_config": true, "is_bbl_vendor": true, "is_cx_vendor": true,
}

type condParser struct {
	s    string
	i    int
	maps map[string]*EnumMap
}

// parseCond parses expr; it never fails (unparsable parts become text nodes).
func parseCond(expr string, maps map[string]*EnumMap) *Cond {
	p := &condParser{s: strings.TrimSpace(expr), maps: maps}
	c := p.parseOr()
	p.skip()
	if p.i < len(p.s) {
		return &Cond{K: "text", V: shortText(expr)}
	}
	return simplify(c)
}

func shortText(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 120 {
		s = s[:120] + "..."
	}
	return s
}

func (p *condParser) skip() {
	for p.i < len(p.s) && (p.s[p.i] == ' ' || p.s[p.i] == '\t' || p.s[p.i] == '\n') {
		p.i++
	}
}

func (p *condParser) peek(tok string) bool {
	p.skip()
	return strings.HasPrefix(p.s[p.i:], tok)
}

func (p *condParser) parseOr() *Cond {
	first := p.parseAnd()
	ops := []*Cond{first}
	for p.peek("||") {
		p.i += 2
		ops = append(ops, p.parseAnd())
	}
	if len(ops) == 1 {
		return first
	}
	return &Cond{K: "or", A: ops}
}

func (p *condParser) parseAnd() *Cond {
	first := p.parseCmp()
	ops := []*Cond{first}
	for p.peek("&&") {
		p.i += 2
		ops = append(ops, p.parseCmp())
	}
	if len(ops) == 1 {
		return first
	}
	return &Cond{K: "and", A: ops}
}

func (p *condParser) parseCmp() *Cond {
	l := p.parseUnary()
	p.skip()
	for _, op := range []string{"==", "!=", ">=", "<=", ">", "<"} {
		if strings.HasPrefix(p.s[p.i:], op) {
			p.i += len(op)
			r := p.parseUnary()
			return resolveCmp(&Cond{K: "cmp", Op: op, A: []*Cond{l, r}}, p.maps)
		}
	}
	return l
}

func (p *condParser) parseUnary() *Cond {
	p.skip()
	if p.i < len(p.s) && p.s[p.i] == '!' && !strings.HasPrefix(p.s[p.i:], "!=") {
		p.i++
		return &Cond{K: "not", A: []*Cond{p.parseUnary()}}
	}
	if p.i < len(p.s) && p.s[p.i] == '(' {
		// group, unless it is a C-style cast like (int)x
		end := matchParen(p.s, p.i)
		if end > 0 {
			inner := p.s[p.i+1 : end]
			if inner == "int" || inner == "float" || inner == "double" || inner == "bool" {
				p.i = end + 1
				return p.parseUnary()
			}
			// "(x).member" or "(x)->member" is an atom, not a group
			if rest := strings.TrimSpace(p.s[end+1:]); strings.HasPrefix(rest, ".") || strings.HasPrefix(rest, "->") {
				return p.parseAtom()
			}
			p.i = end + 1
			sub := &condParser{s: inner, maps: p.maps}
			c := sub.parseOr()
			sub.skip()
			if sub.i < len(sub.s) {
				return &Cond{K: "text", V: shortText(inner)}
			}
			return c
		}
	}
	return p.parseAtom()
}

func (p *condParser) parseAtom() *Cond {
	p.skip()
	start := p.i
	depth := 0
	for p.i < len(p.s) {
		c := p.s[p.i]
		if c == '"' {
			p.i++
			for p.i < len(p.s) && p.s[p.i] != '"' {
				if p.s[p.i] == '\\' {
					p.i++
				}
				p.i++
			}
			p.i++
			continue
		}
		if c == '(' || c == '{' || c == '[' {
			depth++
			p.i++
			continue
		}
		if c == ')' || c == '}' || c == ']' {
			if depth == 0 {
				break
			}
			depth--
			p.i++
			continue
		}
		if depth == 0 {
			if strings.HasPrefix(p.s[p.i:], "->") {
				p.i += 2
				continue
			}
			if strings.HasPrefix(p.s[p.i:], "&&") || strings.HasPrefix(p.s[p.i:], "||") ||
				strings.HasPrefix(p.s[p.i:], "==") || strings.HasPrefix(p.s[p.i:], "!=") ||
				strings.HasPrefix(p.s[p.i:], ">=") || strings.HasPrefix(p.s[p.i:], "<=") {
				break
			}
			if c == '<' || c == '>' {
				if c == '<' && p.i > start && isIdentByte(p.s[p.i-1]) {
					// template argument list directly after an identifier
					if end := templateEnd(p.s, p.i); end > 0 {
						p.i = end + 1
						continue
					}
				}
				break
			}
		}
		p.i++
	}
	return interpretAtom(strings.TrimSpace(p.s[start:p.i]), p.maps)
}

// templateEnd returns the index of the '>' closing the template list that
// opens at s[open], or -1 when the text does not look like a template list.
func templateEnd(s string, open int) int {
	depth := 0
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '<':
			depth++
		case '>':
			depth--
			if depth == 0 {
				return i
			}
		default:
			c := s[i]
			if !(isIdentByte(c) || c == ':' || c == ',' || c == ' ') {
				return -1
			}
		}
	}
	return -1
}

func interpretAtom(a string, maps map[string]*EnumMap) *Cond {
	switch a {
	case "":
		return &Cond{K: "text", V: ""}
	case "true":
		return &Cond{K: "bool", V: true}
	case "false":
		return &Cond{K: "bool", V: false}
	}
	if reCondNum.MatchString(a) {
		f, err := strconv.ParseFloat(strings.TrimRight(a, "fF"), 64)
		if err == nil {
			return &Cond{K: "num", V: f}
		}
	}
	if m := reCondOpt.FindStringSubmatch(a); m != nil {
		c := &Cond{K: "opt", Key: strings.SplitN(m[3], "#", 2)[0]}
		fn, targ := m[1], m[2]
		switch {
		case strings.HasPrefix(fn, "opt_"):
			c.Kind = strings.TrimPrefix(fn, "opt_")
		case fn == "option":
			c.Kind = kindFromOptionType(targ)
		}
		if i := strings.Index(targ, "Enum<"); i >= 0 {
			c.Kind = "enum"
			c.T = strings.TrimSuffix(strings.TrimSpace(targ[i+5:]), ">")
		} else if c.Kind == "enum" {
			c.T = strings.TrimSpace(targ)
		}
		if c.Kind == "float_or_percent" || c.Kind == "percent" {
			c.Kind = "float"
		}
		return c
	}
	for _, cp := range []struct{ frag, name string }{
		{"is_bbl_vendor()", "is_bbl_vendor"}, {"is_cx_vendor()", "is_cx_vendor"}, {"machine_is_belt()", "is_belt_machine"},
		{"is_k2_series_printer_from_string", "is_k2_series_printer"},
	} {
		if strings.Contains(a, cp.frag) {
			return &Cond{K: "cap", Name: cp.name}
		}
	}
	if condCapabilities[a] {
		return &Cond{K: "cap", Name: a}
	}
	if m := reCondSet.FindStringSubmatch(a); m != nil {
		// std::set<T>{a, b}.count(x): membership test, "x is one of a, b"
		var keys []interface{}
		for _, e := range splitTop(m[2], ',') {
			e = strings.TrimSpace(e)
			if e == "" {
				continue
			}
			if k := enumKeyFor(m[1], e, maps); k != "" {
				keys = append(keys, k)
			} else {
				keys = append(keys, e)
			}
		}
		return &Cond{K: "in", A: []*Cond{parseCond(m[3], maps)}, V: keys}
	}
	if m := reCondHas.FindStringSubmatch(a); m != nil {
		return &Cond{K: "has", Key: strings.SplitN(m[1], "#", 2)[0]}
	}
	if m := reCondCall.FindStringSubmatch(a); m != nil && !strings.Contains(m[1], "::") {
		args := splitTop(m[2], ',')
		c := &Cond{K: "call", Name: m[1]}
		for _, arg := range args {
			c.A = append(c.A, parseCond(arg, maps))
		}
		return c
	}
	if reCondEnumer.MatchString(a) {
		return &Cond{K: "text", V: a} // resolved against the other side of a comparison
	}
	return &Cond{K: "text", V: shortText(a)}
}

func kindFromOptionType(t string) string {
	switch {
	case strings.Contains(t, "Bool"):
		return "bool"
	case strings.Contains(t, "Enum"):
		return "enum"
	case strings.Contains(t, "Int"):
		return "int"
	case strings.Contains(t, "String"):
		return "string"
	}
	return "float"
}

// resolveCmp turns "opt == EnumeratorName" into an enum comparison with the option's string key.
func resolveCmp(c *Cond, maps map[string]*EnumMap) *Cond {
	l, r := c.A[0], c.A[1]
	if l.K == "in" && r.K == "num" {
		if v, _ := r.V.(float64); v == 0 {
			switch c.Op {
			case "!=":
				return l
			case "==":
				return &Cond{K: "not", A: []*Cond{l}}
			}
		}
	}
	if l.K == "text" && r.K == "opt" {
		l, r = r, l
		c.A[0], c.A[1] = l, r
	}
	if l.K == "opt" && l.Kind == "enum" && r.K == "text" {
		if s, ok := r.V.(string); ok {
			if v := enumKeyFor(l.T, s, maps); v != "" {
				c.A[1] = &Cond{K: "enum", V: v}
			}
		}
	}
	return c
}

func enumKeyFor(typ, enumerator string, maps map[string]*EnumMap) string {
	if i := strings.LastIndex(enumerator, "::"); i >= 0 {
		if q := enumerator[:i]; maps[q] != nil && typ == "" {
			typ = q
		}
	}
	if em := maps[typ]; em != nil {
		if k, ok := em.keyOf(enumerator); ok {
			return k
		}
	}
	return ""
}

// simplify flattens nested and/or of the same kind and removes double negation.
func simplify(c *Cond) *Cond {
	if c == nil {
		return nil
	}
	for i, a := range c.A {
		c.A[i] = simplify(a)
	}
	switch c.K {
	case "and", "or":
		var flat []*Cond
		for _, a := range c.A {
			if a.K == c.K {
				flat = append(flat, a.A...)
			} else {
				flat = append(flat, a)
			}
		}
		if c.K == "and" { // "has(k) && opt(k)" reads as "opt(k)"
			var kept []*Cond
			for _, a := range flat {
				if a.K != "has" {
					kept = append(kept, a)
				}
			}
			if len(kept) > 0 {
				flat = kept
			}
		}
		c.A = flat
		if len(flat) == 1 {
			return flat[0]
		}
	case "not":
		if len(c.A) == 1 && c.A[0].K == "not" {
			return c.A[0].A[0]
		}
	}
	return c
}

var reIfHeader = regexp.MustCompile(`(?s)^(?:else\s+)?if\s*\((.*)\)\s*$`)

// contextConds converts the if-headers enclosing a statement into conditions.
// Page-selection tests of the GUI (m_active_page->title() == ...) are dropped.
func contextConds(ctx []string, maps map[string]*EnumMap) []*Cond {
	var out []*Cond
	for _, h := range ctx {
		h = strings.TrimSpace(h)
		// page selection and confirmation dialogs are GUI plumbing, not conditions on settings
		if strings.Contains(h, "m_active_page") || strings.Contains(h, "wxID_") || strings.Contains(h, "ShowModal") || strings.Contains(h, "dynamic_cast") || strings.Contains(h, "set.find") || strings.Contains(h, "set.end") {
			continue
		}
		switch {
		case h == "else":
			out = append(out, &Cond{K: "text", V: "otherwise"})
		case strings.HasPrefix(h, "loop:"):
			continue
		default:
			if m := reIfHeader.FindStringSubmatch(h); m != nil {
				c := parseCond(m[1], maps)
				// bare unknown flags (pointer checks such as "others_page") and
				// pure has() tests carry no meaning for a reader
				if s, ok := c.V.(string); ok && c.K == "text" && reIdentOnly.MatchString(s) {
					continue
				}
				if onlyHas(c) {
					continue
				}
				out = append(out, c)
			} else {
				out = append(out, &Cond{K: "text", V: shortText(h)})
			}
		}
	}
	return out
}

var reForcedValue = regexp.MustCompile(`^new\s+ConfigOption(\w+?)(?:Nullable)?(?:<(\w+)>)?\s*\((.*)\)$`)

// forcedValue renders the literal a forced change assigns, as plain text. It
// reports false when the assigned value is computed in code (a variable, a
// conditional expression, a clone of another option): such a rule cannot be
// stated to a reader and is left out of the slim catalog.
func forcedValue(v string, maps map[string]*EnumMap) (string, bool) {
	v = strings.TrimSpace(v)
	m := reForcedValue.FindStringSubmatch(v)
	if m == nil {
		return "", false
	}
	typ, enumT, arg := m[1], m[2], strings.TrimSpace(m[3])
	switch typ {
	case "Bool":
		if arg == "true" || arg == "1" {
			return "on", true
		}
		if arg == "false" || arg == "0" {
			return "off", true
		}
	case "Enum":
		if k := enumKeyFor(enumT, arg, maps); k != "" {
			return k, true
		}
	case "FloatOrPercent":
		if ps := splitTop(arg, ','); len(ps) == 2 && reNumLiteral.MatchString(strings.TrimSpace(ps[0])) {
			if strings.TrimSpace(ps[1]) == "true" {
				return strings.TrimSpace(ps[0]) + "%", true
			}
			return strings.TrimSpace(ps[0]), true
		}
	case "Float", "Int", "Percent":
		if reNumLiteral.MatchString(arg) {
			return strings.TrimRight(arg, "fF"), true
		}
	}
	return "", false
}

var reNumLiteral = regexp.MustCompile(`^-?[0-9]+(?:[.][0-9]*)?[fF]?$`)

// sanitize makes a condition tree safe to commit: text nodes that are not plain
// identifiers (C++ fragments of the application's GUI code) become "unparsed"
// nodes, and a comparison with an unparsed operand becomes unparsed as a whole.
func sanitize(c *Cond) *Cond {
	if c == nil {
		return nil
	}
	switch c.K {
	case "text":
		if s, ok := c.V.(string); ok && (reIdentOnly.MatchString(s) || s == "otherwise") {
			return c
		}
		return &Cond{K: "unparsed"}
	case "cmp":
		for i, a := range c.A {
			c.A[i] = sanitize(a)
			if c.A[i].K == "unparsed" {
				return &Cond{K: "unparsed"}
			}
		}
		return c
	}
	for i, a := range c.A {
		c.A[i] = sanitize(a)
	}
	return c
}

var reIdentOnly = regexp.MustCompile(`^[A-Za-z_]\w*$`)

// onlyHas reports whether every leaf of c is a has() test.
func onlyHas(c *Cond) bool {
	if c.K == "has" {
		return true
	}
	if (c.K == "and" || c.K == "or" || c.K == "not") && len(c.A) > 0 {
		for _, a := range c.A {
			if !onlyHas(a) {
				return false
			}
		}
		return true
	}
	return false
}
