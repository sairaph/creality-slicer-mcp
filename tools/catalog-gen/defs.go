package main

// defs.go: interpret the ConfigDef-building code in PrintConfig.cpp.
//
// The definitions are straight-line statements of the form
//     def = this->add("key", coType);
//     def->label = L("..."); ... def->set_default_value(new ConfigOptionX(...));
// plus three small generators (a lambda, an axis loop, and a filament-override
// loop). This file evaluates exactly those forms and reports anything else.

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

type Opt struct {
	Key      string
	DefClass string // "print" for PrintConfigDef, else the lower-cased *ConfigDef class
	Func     string // function or constructor that defined it
	Tech     string // "any", "FFF", "SLA"
	File     string
	Line     int
	Order    int

	TypeName string // coFloat ...
	Nullable bool

	Label, FullLabel, Tooltip, Sidetext, Category string
	RatioOver, GuiFlags, Cli, CliParams, GuiType  string
	Mode                                          string // simple, advanced, develop ("" = unset -> simple)
	ModeSet                                       bool

	Min, Max, MaxLiteral *float64
	MinRaw, MaxRaw       string
	Height               *int
	Readonly             bool
	Multiline            bool
	FullWidth            bool

	EnumValues, EnumLabels []string
	EnumType               string // C++ enum type from enum_keys_map
	Aliases                []string

	DefaultClass  string
	DefaultRaw    string
	Default       interface{}
	DefaultParsed bool

	Redefined []int // extra lines where add() was called again for the same key
	Notes     []string

	BuildMacro string // preprocessor macro that guards the definition, if any
	defOpen    string // "(" or "{" used in the default's constructor call
}

func isVectorType(t string) bool {
	switch t {
	case "coFloats", "coInts", "coStrings", "coPercents", "coFloatsOrPercents", "coPoints", "coBools", "coEnums":
		return true
	}
	return false
}

func valueTypeName(t string) string {
	switch t {
	case "coFloat", "coFloats":
		return "float"
	case "coInt", "coInts":
		return "int"
	case "coString", "coStrings":
		return "string"
	case "coPercent", "coPercents":
		return "percent"
	case "coFloatOrPercent", "coFloatsOrPercents":
		return "float_or_percent"
	case "coPoint", "coPoints":
		return "point"
	case "coPoint3":
		return "point3"
	case "coBool", "coBools":
		return "bool"
	case "coEnum", "coEnums":
		return "enum"
	case "coNone":
		return "none"
	}
	return strings.TrimPrefix(t, "co")
}

// EnumMap is a C++ t_config_enum_values map: string key -> enumerator name.
type EnumMap struct {
	Name  string
	Pairs [][2]string // key, enumerator
	Line  int
}

func (m *EnumMap) keyOf(enumerator string) (string, bool) {
	enumerator = stripQual(enumerator)
	for _, p := range m.Pairs {
		if stripQual(p[1]) == enumerator {
			return p[0], true
		}
	}
	return "", false
}

func stripQual(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "(int)")
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "int(") && strings.HasSuffix(s, ")") {
		s = strings.TrimSpace(s[4 : len(s)-1])
	}
	if i := strings.LastIndex(s, "::"); i >= 0 {
		return s[i+2:]
	}
	return s
}

type axisRow struct {
	name            string
	feed, acc, jerk string
}

type DefParser struct {
	ct          *CText
	enumMaps    map[string]*EnumMap
	classes     map[string]map[string]*Opt // def class -> key -> opt
	order       []*Opt                     // definition order (unique)
	orderN      int
	warnings    []string
	unknown     int
	addSites    map[string]int // raw add-site counts per def class (from tree walk)
	genNote     map[string]int // generated-option counters
	dupKeys     []string
	lambdaCalls int
	consts      map[string]float64 // numeric constants from directly included headers
}

func newDefParser(ct *CText, maps map[string]*EnumMap) *DefParser {
	return &DefParser{ct: ct, enumMaps: maps, classes: map[string]map[string]*Opt{}, addSites: map[string]int{}, genNote: map[string]int{}}
}

type denv struct {
	class, fn, tech string
	cur             *Opt
	ptrs            map[string]*Opt
	strs            map[string]string
	nums            map[string]float64
	loop            map[string]string
	lists           map[string][]string
	axes            []axisRow
	src             *Opt // it_opt->second inside the filament override loop
	lastIf          *bool
}

func (p *DefParser) warn(line int, format string, a ...interface{}) {
	p.warnings = append(p.warnings, fmt.Sprintf("%s:%d: %s", p.ct.Rel, line, fmt.Sprintf(format, a...)))
}

// ---- expression evaluation ------------------------------------------------

var reWrap = regexp.MustCompile(`(?s)^(?:_u8L|_L|_|L|std::string|wxString)\s*\((.*)\)$`)
var reFormat = regexp.MustCompile(`(?s)^\(\s*boost::format\(\s*(".*?")\s*\)\s*%\s*(\w+)\s*\)\.str\(\)$`)
var reSrcProp = regexp.MustCompile(`^it_opt->second\.(\w+)$`)
var reDefProp = regexp.MustCompile(`^(\w+)\s*->\s*(\w+)$`)

func (p *DefParser) evalString(expr string, e *denv) (string, bool) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return "", false
	}
	// concatenation with '+' at depth 0
	if parts := splitTop(expr, '+'); len(parts) > 1 {
		var b strings.Builder
		for _, part := range parts {
			s, ok := p.evalString(part, e)
			if !ok {
				return "", false
			}
			b.WriteString(s)
		}
		return b.String(), true
	}
	// pure literal sequence
	if expr[0] == '"' {
		lits := stringLiterals(expr)
		// verify nothing but whitespace between literals
		rest := expr
		var b strings.Builder
		for _, l := range lits {
			b.WriteString(l.Val)
		}
		_ = rest
		// crude check: after removing all quoted parts only whitespace must remain
		if onlyLiterals(expr) {
			return b.String(), true
		}
		return "", false
	}
	if m := reFormat.FindStringSubmatch(expr); m != nil {
		f, ok := p.evalString(m[1], e)
		if !ok {
			return "", false
		}
		arg, ok := p.lookupString(m[2], e)
		if !ok {
			return "", false
		}
		return strings.ReplaceAll(f, "%1%", arg), true
	}
	if m := reWrap.FindStringSubmatch(expr); m != nil {
		return p.evalString(m[1], e)
	}
	if expr[0] == '(' && matchParen(expr, 0) == len(expr)-1 {
		return p.evalString(expr[1:len(expr)-1], e)
	}
	if m := reSrcProp.FindStringSubmatch(expr); m != nil && e.src != nil {
		return srcStringProp(e.src, m[1])
	}
	if m := reDefProp.FindStringSubmatch(expr); m != nil {
		var o *Opt
		if m[1] == "def" {
			o = e.cur
		} else {
			o = e.ptrs[m[1]]
		}
		if o != nil {
			return srcStringProp(o, m[2])
		}
	}
	return p.lookupString(expr, e)
}

func onlyLiterals(expr string) bool {
	i := 0
	for i < len(expr) {
		c := expr[i]
		if c == ' ' || c == '\n' || c == '\t' {
			i++
			continue
		}
		if c != '"' {
			return false
		}
		i++
		for i < len(expr) && expr[i] != '"' {
			if expr[i] == '\\' {
				i++
			}
			i++
		}
		i++
	}
	return true
}

func srcStringProp(o *Opt, name string) (string, bool) {
	switch name {
	case "label":
		return o.Label, true
	case "full_label":
		return o.FullLabel, true
	case "tooltip":
		return o.Tooltip, true
	case "sidetext":
		return o.Sidetext, true
	case "category":
		return o.Category, true
	case "ratio_over":
		return o.RatioOver, true
	}
	return "", false
}

func (p *DefParser) lookupString(id string, e *denv) (string, bool) {
	id = strings.TrimSpace(id)
	if v, ok := e.loop[id]; ok {
		return v, true
	}
	if v, ok := e.strs[id]; ok {
		return v, true
	}
	return "", false
}

func (p *DefParser) evalNum(expr string, e *denv) (float64, bool) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return 0, false
	}
	if expr[0] == '(' && matchParen(expr, 0) == len(expr)-1 {
		return p.evalNum(expr[1:len(expr)-1], e)
	}
	for _, op := range []byte{'+', '-'} {
		if parts := splitTopLast(expr, op); parts != nil {
			a, ok1 := p.evalNum(parts[0], e)
			b, ok2 := p.evalNum(parts[1], e)
			if ok1 && ok2 {
				if op == '+' {
					return a + b, true
				}
				return a - b, true
			}
			return 0, false
		}
	}
	for _, op := range []byte{'*', '/'} {
		if parts := splitTopLast(expr, op); parts != nil {
			a, ok1 := p.evalNum(parts[0], e)
			b, ok2 := p.evalNum(parts[1], e)
			if ok1 && ok2 {
				if op == '*' {
					return a * b, true
				}
				if b == 0 {
					return 0, false
				}
				return a / b, true
			}
			return 0, false
		}
	}
	if strings.HasPrefix(expr, "-") {
		v, ok := p.evalNum(expr[1:], e)
		return -v, ok
	}
	if strings.HasPrefix(expr, "static_cast<") {
		if i := strings.Index(expr, ">("); i > 0 && strings.HasSuffix(expr, ")") {
			return p.evalNum(expr[i+2:len(expr)-1], e)
		}
	}
	s := strings.TrimRight(expr, "fFuUlL")
	if s == "" {
		return 0, false
	}
	if v, err := strconv.ParseFloat(s, 64); err == nil {
		return v, true
	}
	if v, ok := e.nums[expr]; ok {
		return v, true
	}
	if v, ok := p.consts[expr]; ok {
		return v, true
	}
	if v, ok := e.loop[expr]; ok {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f, true
		}
	}
	return 0, false
}

// splitTopLast splits at the last depth-0 occurrence of op that is a binary
// operator (not a leading sign or part of an exponent like 1e-3).
func splitTopLast(s string, op byte) []string {
	depth := 0
	idx := -1
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch ch {
		case '"':
			i++
			for i < len(s) && s[i] != '"' {
				if s[i] == '\\' {
					i++
				}
				i++
			}
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		default:
			if ch == op && depth == 0 && i > 0 {
				prev := strings.TrimRight(s[:i], " ")
				if prev == "" {
					continue
				}
				pc := prev[len(prev)-1]
				if (op == '-' || op == '+') && (pc == 'e' || pc == 'E') && len(prev) >= 2 && prev[len(prev)-2] >= '0' && prev[len(prev)-2] <= '9' {
					continue
				}
				if pc == '*' || pc == '/' || pc == '+' || pc == '-' || pc == '(' {
					continue
				}
				idx = i
			}
		}
	}
	if idx < 0 {
		return nil
	}
	return []string{s[:idx], s[idx+1:]}
}

func parseBool(s string) (bool, bool) {
	switch strings.TrimSpace(s) {
	case "true", "1":
		return true, true
	case "false", "0":
		return false, true
	}
	return false, false
}

// ---- walking ---------------------------------------------------------------

var (
	reAdd        = regexp.MustCompile(`(?s)^(?:auto\s+(\w+)\s*=\s*)?(\w+)\s*=\s*this\s*->\s*(add|add_nullable)\s*\((.*)\)$`)
	reSetProp    = regexp.MustCompile(`(?s)^(\w+)\s*->\s*(\w+)\s*=\s*(.*)$`)
	reListOp     = regexp.MustCompile(`(?s)^(\w+)\s*->\s*(enum_values|enum_labels|aliases)\s*\.\s*(push_back|emplace_back)\s*\((.*)\)$`)
	reSetDef     = regexp.MustCompile(`(?s)^(\w+)\s*->\s*set_default_value\s*\((.*)\)$`)
	reVarDef     = regexp.MustCompile(`(?s)^(?:const\s+|static\s+)*(?:std::string|int|double|float|size_t|bool|auto)\s+(\w+)\s*=\s*(.*)$`)
	reFor        = regexp.MustCompile(`(?s)^for\s*\((.*)\)$`)
	reRange      = regexp.MustCompile(`(?s)^(?:const\s+)?[\w:<>]+\s*[&*]?\s*(\w+)\s*:\s*(.+)$`)
	reAppendEnum = regexp.MustCompile(`(?s)^append\(\s*(\w+)\s*->\s*(enum_values|enum_labels)\s*,\s*(\w+)\s*->\s*(?:enum_values|enum_labels)\s*\)$`)
	reInitEnum   = regexp.MustCompile(`(?s)^init_extruder_enum\s*\((.*)\)$`)
	reStrcmp     = regexp.MustCompile(`strcmp\(\s*(\w+)\s*,\s*"([^"]*)"\s*\)\s*==\s*0`)
	reAxesDecl   = regexp.MustCompile(`(?s)^std::vector<AxisDefault>\s+axes\s*(\{.*\})$`)
)

// walkDefs processes all definition functions in the tree.
func (p *DefParser) walkDefs(root *Node) {
	var visit func(n *Node)
	visit = func(n *Node) {
		for _, c := range n.Children {
			if c.Kind != nBlock {
				continue
			}
			class, fn, tech, ok := defFuncInfo(c.Header)
			if ok {
				if class == "" { // init_extruder_option_keys etc. are handled elsewhere
					continue
				}
				env := &denv{class: class, fn: fn, tech: tech, ptrs: map[string]*Opt{}, strs: map[string]string{}, nums: map[string]float64{}, loop: map[string]string{}, lists: map[string][]string{}}
				p.runNodes(c.Children, env)
				continue
			}
			visit(c)
		}
	}
	visit(root)
}

var reCtor = regexp.MustCompile(`^(\w+ConfigDef)::(\w+)\s*\(\s*\)$`)

// defFuncInfo recognizes the function headers that build ConfigDefs.
func defFuncInfo(h string) (class, fn, tech string, ok bool) {
	h = strings.TrimSpace(h)
	switch {
	case strings.HasSuffix(h, "PrintConfigDef::init_common_params()"):
		return "print", "PrintConfigDef::init_common_params", "any", true
	case strings.HasSuffix(h, "PrintConfigDef::init_fff_params()"):
		return "print", "PrintConfigDef::init_fff_params", "FFF", true
	case strings.HasSuffix(h, "PrintConfigDef::init_sla_params()"):
		return "print", "PrintConfigDef::init_sla_params", "SLA", true
	case strings.HasSuffix(h, "PrintConfigDef::init_extruder_option_keys()"), strings.HasSuffix(h, "PrintConfigDef::init_filament_option_keys()"):
		return "", "", "", true
	}
	if m := reCtor.FindStringSubmatch(h); m != nil && m[1] == m[2] && m[1] != "PrintConfigDef" {
		return strings.ToLower(strings.TrimSuffix(m[1], "ConfigDef")), m[1] + "::" + m[1], "any", true
	}
	return "", "", "", false
}

func (p *DefParser) runNodes(nodes []*Node, e *denv) {
	for _, n := range nodes {
		switch n.Kind {
		case nStmt:
			p.runStmt(n.Text, n.Line, e)
		case nBlock:
			p.runBlock(n, e)
		}
	}
}

func (p *DefParser) runBlock(n *Node, e *denv) {
	h := strings.TrimSpace(n.Header)
	switch {
	case h == "":
		p.runNodes(n.Children, e)
	case strings.HasPrefix(h, "struct "):
		// AxisDefault helper struct
	case strings.HasPrefix(h, "for"):
		p.runFor(h, n.Children, n.Line, e)
	case strings.HasPrefix(h, "if"):
		cond := strings.TrimSpace(h[2:])
		if v, ok := p.evalStrcmpCond(cond, e); ok {
			e.lastIf = &v
			if v {
				p.runNodes(n.Children, e)
			}
		} else {
			p.warn(n.Line, "unevaluated if in definition code: %s", oneLine(h))
			e.lastIf = nil
		}
	case strings.HasPrefix(h, "else"):
		if e.lastIf != nil && !*e.lastIf {
			p.runNodes(n.Children, e)
		} else if e.lastIf == nil {
			p.warn(n.Line, "else after unevaluated if skipped")
		}
	case strings.HasPrefix(h, "switch"):
		// Default-value conversion switch inside the filament override loop: each
		// case wraps the source option's default in the nullable option class.
		if e.src != nil && e.cur != nil {
			o := e.cur
			o.Default, o.DefaultParsed, o.DefaultRaw = e.src.Default, e.src.DefaultParsed, e.src.DefaultRaw
			o.DefaultClass = map[string]string{
				"coFloats": "ConfigOptionFloatsNullable", "coPercents": "ConfigOptionPercentsNullable",
				"coBools": "ConfigOptionBoolsNullable", "coEnums": "ConfigOptionEnumsGenericNullable",
			}[o.TypeName]
			o.Nullable = true
			o.Notes = append(o.Notes, "default copied from '"+e.src.Key+"' (filament override)")
		}
	default:
		p.warn(n.Line, "unknown block in definition code: %s", oneLine(h))
	}
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 120 {
		s = s[:120] + "..."
	}
	return s
}

func (p *DefParser) evalStrcmpCond(cond string, e *denv) (bool, bool) {
	ms := reStrcmp.FindAllStringSubmatch(cond, -1)
	if len(ms) == 0 {
		return false, false
	}
	// strip all strcmp terms, parens, and '||'; nothing else may remain
	rest := reStrcmp.ReplaceAllString(cond, "")
	rest = strings.NewReplacer("(", "", ")", "", "|", "", " ", "", "\n", "", "\t", "").Replace(rest)
	if rest != "" {
		return false, false
	}
	for _, m := range ms {
		if e.loop[m[1]] == m[2] {
			return true, true
		}
	}
	return false, true
}

func (p *DefParser) runFor(h string, body []*Node, line int, e *denv) {
	m := reFor.FindStringSubmatch(h)
	if m == nil {
		p.warn(line, "unparsed for header: %s", oneLine(h))
		return
	}
	r := reRange.FindStringSubmatch(strings.TrimSpace(m[1]))
	if r == nil {
		p.warn(line, "unparsed range-for: %s", oneLine(h))
		return
	}
	varName, rng := r[1], strings.TrimSpace(r[2])
	switch {
	case strings.HasPrefix(rng, "{"):
		var vals []string
		for _, l := range stringLiterals(rng) {
			vals = append(vals, l.Val)
		}
		for _, v := range vals {
			e.loop[varName] = v
			if e.class != "" {
				if src := p.classes[e.class][v]; src != nil {
					e.src = src
				} else {
					e.src = nil
				}
			}
			p.runNodes(body, e)
		}
		delete(e.loop, varName)
		e.src = nil
	case rng == "axes":
		for _, ax := range e.axes {
			e.loop["axis.name"] = ax.name
			e.loop["axis.max_feedrate"] = ax.feed
			e.loop["axis.max_acceleration"] = ax.acc
			e.loop["axis.max_jerk"] = ax.jerk
			p.runNodes(body, e)
		}
		for _, k := range []string{"axis.name", "axis.max_feedrate", "axis.max_acceleration", "axis.max_jerk"} {
			delete(e.loop, k)
		}
	default:
		p.warn(line, "unsupported range expression %q", rng)
	}
}

func (p *DefParser) runStmt(text string, line int, e *denv) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	// macro calls new_def(...) may appear back to back without a semicolon
	if strings.HasPrefix(text, "new_def(") {
		for _, call := range splitNewDefs(text) {
			p.newDef(call, line, e)
		}
		return
	}
	// inline control statements: for (...) stmt / if (...) stmt
	if strings.HasPrefix(text, "for") || strings.HasPrefix(text, "if") {
		if i := strings.Index(text, "("); i > 0 && strings.TrimSpace(text[:i]) != "" && (strings.TrimSpace(text[:i]) == "for" || strings.TrimSpace(text[:i]) == "if") {
			if j := matchParen(text, i); j > 0 {
				hdr := strings.TrimSpace(text[:j+1])
				rest := strings.TrimSpace(text[j+1:])
				if strings.HasPrefix(hdr, "for") {
					p.runFor(hdr, []*Node{{Kind: nStmt, Text: rest, Line: line}}, line, e)
				} else {
					cond := strings.TrimSpace(hdr[2:])
					if v, ok := p.evalStrcmpCond(cond, e); ok {
						e.lastIf = &v
						if v {
							p.runStmt(rest, line, e)
						}
					} else {
						p.warn(line, "unevaluated inline if: %s", oneLine(hdr))
					}
				}
				return
			}
		}
	}
	if strings.HasPrefix(text, "else") && (len(text) == 4 || !isIdentByte(text[4])) {
		rest := strings.TrimSpace(text[4:])
		if e.lastIf != nil && !*e.lastIf {
			p.runStmt(rest, line, e)
		}
		return
	}
	if m := reAdd.FindStringSubmatch(text); m != nil {
		p.addSite(m, line, e)
		return
	}
	if m := reInitEnum.FindStringSubmatch(text); m != nil {
		p.initExtruderEnum(m[1], line, e)
		return
	}
	if m := reListOp.FindStringSubmatch(text); m != nil {
		o := p.target(m[1], e)
		if o == nil {
			p.warn(line, "list op on unknown target %s", m[1])
			return
		}
		val, ok := p.evalString(m[4], e)
		if !ok {
			val = strings.TrimSpace(m[4])
			o.Notes = append(o.Notes, "unresolved "+m[2]+" element: "+oneLine(val))
		}
		switch m[2] {
		case "enum_values":
			o.EnumValues = append(o.EnumValues, val)
		case "enum_labels":
			o.EnumLabels = append(o.EnumLabels, val)
		case "aliases":
			o.Aliases = append(o.Aliases, val)
		}
		return
	}
	if m := reSetDef.FindStringSubmatch(text); m != nil {
		o := p.target(m[1], e)
		if o == nil {
			p.warn(line, "set_default_value on unknown target %s", m[1])
			return
		}
		p.setDefault(o, m[2], e, line)
		return
	}
	if m := reAppendEnum.FindStringSubmatch(text); m != nil {
		dst, src := p.target(m[1], e), e.ptrs[m[3]]
		if dst == nil || src == nil {
			p.warn(line, "append() on unknown target: %s", oneLine(text))
			return
		}
		if m[2] == "enum_values" {
			dst.EnumValues = append(dst.EnumValues, src.EnumValues...)
		} else {
			dst.EnumLabels = append(dst.EnumLabels, src.EnumLabels...)
		}
		return
	}
	if strings.HasPrefix(text, "(void)") || strings.HasPrefix(text, "assert(") || text == "break" || strings.HasPrefix(text, "case ") {
		return
	}
	if m := reSetProp.FindStringSubmatch(text); m != nil && !strings.Contains(m[1]+m[2], " ") && m[3] != "" && !strings.HasPrefix(m[3], "=") {
		if o := p.target(m[1], e); o != nil {
			p.setProp(o, m[2], m[3], e, line)
			return
		}
	}
	if reAxesDecl.MatchString(text) {
		e.axes = parseAxes(text)
		return
	}
	if m := reVarDef.FindStringSubmatch(text); m != nil {
		name, val := m[1], m[2]
		if strings.Contains(val, "boost::to_upper_copy") {
			if a := regexp.MustCompile(`\((.*)\)$`).FindStringSubmatch(val); a != nil {
				if s, ok := p.lookupString(a[1], e); ok {
					e.strs[name] = strings.ToUpper(s)
				}
			}
			return
		}
		if s, ok := p.evalString(val, e); ok && (strings.Contains(val, "\"") || strings.Contains(val, "L(")) {
			e.strs[name] = s
			return
		}
		if f, ok := p.evalNum(val, e); ok {
			e.nums[name] = f
			return
		}
		if strings.HasPrefix(val, "options.find(") || strings.HasPrefix(val, "[") {
			return // it_opt lookup, or the init_extruder_enum lambda
		}
		p.warn(line, "unresolved variable definition: %s", oneLine(text))
		return
	}
	if strings.HasPrefix(text, "ConfigOptionDef") || strings.HasPrefix(text, "const size_t") || text == "" {
		return
	}
	p.unknown++
	p.warn(line, "unrecognized statement in definition code: %s", oneLine(text))
}

func splitNewDefs(text string) []string {
	var out []string
	i := 0
	for i < len(text) {
		j := strings.Index(text[i:], "new_def(")
		if j < 0 {
			break
		}
		start := i + j
		open := start + len("new_def")
		end := matchParen(text, open)
		if end < 0 {
			break
		}
		out = append(out, text[start:end+1])
		i = end + 1
	}
	return out
}

func parseAxes(text string) []axisRow {
	var rows []axisRow
	i := strings.Index(text, "{")
	body := text[i+1 : strings.LastIndex(text, "}")]
	for _, r := range splitTop(body, ',') {
		r = strings.TrimSpace(r)
		if !strings.HasPrefix(r, "{") {
			continue
		}
		f := splitTop(r[1:len(r)-1], ',')
		if len(f) != 4 {
			continue
		}
		name := strings.Trim(f[0], `" `)
		rows = append(rows, axisRow{name: name, feed: f[1], acc: f[2], jerk: f[3]})
	}
	return rows
}

func (p *DefParser) target(name string, e *denv) *Opt {
	if name == "def" || name == "enum_def" {
		return e.cur
	}
	return e.ptrs[name]
}

func (p *DefParser) addSite(m []string, line int, e *denv) {
	ptr, varName, kind, args := m[1], m[2], m[3], m[4]
	parts := splitTop(args, ',')
	if len(parts) != 2 {
		p.warn(line, "add() with %d args", len(parts))
		return
	}
	key, ok := p.evalString(parts[0], e)
	if !ok {
		p.warn(line, "cannot resolve option key %q", oneLine(parts[0]))
		return
	}
	typ := strings.TrimSpace(parts[1])
	if typ == "it_opt->second.type" && e.src != nil {
		typ = e.src.TypeName
	}
	o := p.newOpt(key, typ, line, e)
	if kind == "add_nullable" {
		o.Nullable = true
	}
	e.cur = o
	if varName != "def" {
		e.ptrs[varName] = o
	}
	if ptr != "" {
		e.ptrs[ptr] = o
	}
	p.addSites[e.class]++
	// the filament override loop clones properties by explicit assignments in the loop body
}

func (p *DefParser) newOpt(key, typ string, line int, e *denv) *Opt {
	cls := p.classes[e.class]
	if cls == nil {
		cls = map[string]*Opt{}
		p.classes[e.class] = cls
	}
	if o, ok := cls[key]; ok {
		o.Redefined = append(o.Redefined, line)
		o.TypeName = typ
		p.dupKeys = append(p.dupKeys, e.class+":"+key)
		return o
	}
	p.orderN++
	o := &Opt{Key: key, DefClass: e.class, Func: e.fn, Tech: e.tech, File: p.ct.Rel, Line: line, Order: p.orderN, TypeName: typ}
	if m := p.ct.condAt(line); m != "" {
		o.BuildMacro = m
		o.Notes = append(o.Notes, "compiled only when "+m+" is defined (CMake option, default OFF)")
	}
	cls[key] = o
	p.order = append(p.order, o)
	return o
}

func (p *DefParser) newDef(call string, line int, e *denv) {
	open := strings.Index(call, "(")
	args := splitTop(call[open+1:len(call)-1], ',')
	if len(args) != 4 {
		p.warn(line, "new_def with %d args", len(args))
		return
	}
	key, ok := p.evalString(args[0], e)
	if !ok {
		p.warn(line, "new_def key unresolved")
		return
	}
	o := p.newOpt(key, strings.TrimSpace(args[1]), line, e)
	e.cur = o
	p.addSites[e.class]++
	if s, ok := p.evalString(args[2], e); ok {
		o.Label = s
	} else {
		o.Notes = append(o.Notes, "unresolved label")
	}
	if s, ok := p.evalString(args[3], e); ok {
		o.Tooltip = s
	} else {
		o.Notes = append(o.Notes, "unresolved tooltip: "+oneLine(args[3]))
	}
}

func (p *DefParser) initExtruderEnum(args string, line int, e *denv) {
	parts := splitTop(args, ',')
	if len(parts) < 4 {
		p.warn(line, "init_extruder_enum args")
		return
	}
	key, ok := p.evalString(parts[0], e)
	if !ok {
		p.warn(line, "init_extruder_enum key")
		return
	}
	o := p.newOpt(key, "coEnums", line, e)
	p.lambdaCalls++
	e.cur = o
	if m := regexp.MustCompile(`ConfigOptionEnum<(\w+)>`).FindStringSubmatch(parts[1]); m != nil {
		o.EnumType = m[1]
	}
	for _, l := range stringLiterals(parts[2]) {
		o.EnumValues = append(o.EnumValues, l.Val)
	}
	dflt := strings.TrimSpace(parts[3])
	o.DefaultClass = "ConfigOptionEnumsGeneric"
	o.DefaultRaw = "{" + dflt + "}"
	if em := p.enumMaps[o.EnumType]; em != nil {
		if k, ok := em.keyOf(dflt); ok {
			o.Default = []interface{}{k}
			o.DefaultParsed = true
		}
	}
	if !o.DefaultParsed {
		o.Default = nil
	}
	o.Notes = append(o.Notes, "defined through the init_extruder_enum lambda (no label or tooltip)")
}

func (p *DefParser) setProp(o *Opt, name, val string, e *denv, line int) {
	val = strings.TrimSpace(val)
	str := func(dst *string) {
		if s, ok := p.evalString(val, e); ok {
			*dst = s
		} else if m := reSrcProp.FindStringSubmatch(val); m != nil && e.src != nil {
			// copied from source option
			if s, ok := srcStringProp(e.src, m[1]); ok {
				*dst = s
			}
		} else {
			*dst = ""
			o.Notes = append(o.Notes, "unresolved "+name+": "+oneLine(val))
			p.warn(line, "unresolved %s for %s: %s", name, o.Key, oneLine(val))
		}
	}
	num := func(dst **float64, raw *string) {
		if f, ok := p.evalNum(val, e); ok {
			v := f
			*dst = &v
			if raw != nil {
				*raw = ""
			}
		} else if reSrcProp.MatchString(val) && e.src != nil {
			m := reSrcProp.FindStringSubmatch(val)
			switch m[1] {
			case "min":
				*dst = e.src.Min
			case "max":
				*dst = e.src.Max
			}
		} else {
			if raw != nil {
				*raw = val
			}
			o.Notes = append(o.Notes, "non-literal "+name+": "+oneLine(val))
		}
	}
	switch name {
	case "label":
		str(&o.Label)
	case "full_label":
		str(&o.FullLabel)
	case "tooltip":
		str(&o.Tooltip)
	case "sidetext":
		str(&o.Sidetext)
	case "category":
		str(&o.Category)
	case "ratio_over":
		str(&o.RatioOver)
	case "gui_flags":
		str(&o.GuiFlags)
	case "cli_params":
		str(&o.CliParams)
	case "cli":
		if strings.Contains(val, "nocli") {
			o.Cli = "nocli"
		} else {
			str(&o.Cli)
		}
	case "mode":
		switch stripQual(val) {
		case "comSimple":
			o.Mode = "simple"
		case "comAdvanced":
			o.Mode = "advanced"
		case "comDevelop":
			o.Mode = "develop"
		default:
			o.Notes = append(o.Notes, "unknown mode "+val)
		}
		o.ModeSet = true
	case "min":
		num(&o.Min, &o.MinRaw)
	case "max":
		num(&o.Max, &o.MaxRaw)
	case "max_literal":
		var raw string
		num(&o.MaxLiteral, &raw)
	case "height":
		if f, ok := p.evalNum(val, e); ok {
			h := int(f)
			o.Height = &h
		}
	case "readonly":
		o.Readonly, _ = parseBool(val)
	case "multiline":
		o.Multiline, _ = parseBool(val)
	case "full_width":
		o.FullWidth, _ = parseBool(val)
	case "nullable":
		o.Nullable, _ = parseBool(val)
	case "gui_type":
		o.GuiType = stripQual(val)
	case "enum_keys_map":
		if m := regexp.MustCompile(`ConfigOptionEnum<(\w+)>`).FindStringSubmatch(val); m != nil {
			o.EnumType = m[1]
		} else if m := regexp.MustCompile(`&\s*s_keys_map_(\w+)`).FindStringSubmatch(val); m != nil {
			o.EnumType = m[1]
		} else if reSrcProp.MatchString(val) && e.src != nil {
			o.EnumType = e.src.EnumType
		} else {
			o.Notes = append(o.Notes, "unresolved enum_keys_map "+oneLine(val))
		}
	case "enum_values", "enum_labels":
		var list []string
		switch {
		case strings.HasPrefix(val, "{"):
			for _, l := range stringLiterals(val) {
				list = append(list, l.Val)
			}
		case reSrcProp.MatchString(val) && e.src != nil:
			if name == "enum_values" {
				list = append(list, e.src.EnumValues...)
			} else {
				list = append(list, e.src.EnumLabels...)
			}
		default:
			if m := regexp.MustCompile(`^(\w+)\s*->\s*(enum_values|enum_labels)$`).FindStringSubmatch(val); m != nil {
				if src := e.ptrs[m[1]]; src != nil {
					if m[2] == "enum_values" {
						list = append(list, src.EnumValues...)
					} else {
						list = append(list, src.EnumLabels...)
					}
				}
			} else {
				o.Notes = append(o.Notes, "unresolved "+name+" assignment "+oneLine(val))
			}
		}
		if name == "enum_values" {
			o.EnumValues = list
		} else {
			o.EnumLabels = list
		}
	case "aliases":
		o.Aliases = nil
		for _, l := range stringLiterals(val) {
			o.Aliases = append(o.Aliases, l.Val)
		}
	default:
		o.Notes = append(o.Notes, "unhandled property "+name)
		p.warn(line, "unhandled property %s on %s", name, o.Key)
	}
	// filament override loop: default comes from the source option (the switch is skipped)
	if name == "min" || name == "max" {
		// nothing more
	}
}

var reNewOpt = regexp.MustCompile(`(?s)^new\s+(ConfigOption[\w:]*(?:<[\w:]+>)?)\s*([({])(.*)$`)

func (p *DefParser) setDefault(o *Opt, expr string, e *denv, line int) {
	expr = strings.TrimSpace(expr)
	m := reNewOpt.FindStringSubmatch(expr)
	if m == nil {
		o.DefaultRaw = oneLine(expr)
		p.warn(line, "unparsed default for %s: %s", o.Key, oneLine(expr))
		return
	}
	cls, open, rest := m[1], m[2], m[3]
	o.defOpen = open
	closeCh := ")"
	if open == "{" {
		closeCh = "}"
	}
	// strip the closing bracket and the set_default_value paren (already stripped by regex)
	rest = strings.TrimSpace(rest)
	rest = strings.TrimSuffix(rest, closeCh)
	rest = strings.TrimSpace(rest)
	o.DefaultClass = cls
	o.DefaultRaw = strings.TrimSpace(rest)
	if strings.Contains(cls, "Nullable") {
		o.Nullable = true
	}
	// axis loop substitution
	for k, v := range e.loop {
		if strings.HasPrefix(k, "axis.") && strings.Contains(rest, k) {
			rest = strings.ReplaceAll(rest, k, v)
			o.DefaultRaw = strings.TrimSpace(rest)
		}
	}
	// filament override loop: default copied from the source option
	if e.src != nil && strings.Contains(rest, "it_opt->second") {
		o.Default = e.src.Default
		o.DefaultParsed = e.src.DefaultParsed
		o.DefaultRaw = e.src.DefaultRaw
		o.Notes = append(o.Notes, "default copied from '"+e.src.Key+"' (filament override)")
		return
	}
	val, ok := p.parseDefault(o, cls, open, rest, e)
	o.Default, o.DefaultParsed = val, ok
}

func (p *DefParser) parseDefault(o *Opt, cls, open, args string, e *denv) (interface{}, bool) {
	base := cls
	if i := strings.Index(base, "<"); i >= 0 {
		base = base[:i]
	}
	args = strings.TrimSpace(args)
	scalarNum := func(s string) (interface{}, bool) {
		f, ok := p.evalNum(s, e)
		if !ok {
			return nil, false
		}
		if f == float64(int64(f)) && !strings.ContainsAny(s, ".eE") {
			return int64(f), true
		}
		return f, true
	}
	listOf := func(conv func(string) (interface{}, bool)) (interface{}, bool) {
		body := args
		if strings.HasPrefix(body, "{") && strings.HasSuffix(body, "}") {
			body = body[1 : len(body)-1]
		}
		var out []interface{}
		for _, part := range splitTop(body, ',') {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			v, ok := conv(part)
			if !ok {
				return nil, false
			}
			out = append(out, v)
		}
		if out == nil {
			out = []interface{}{}
		}
		return out, true
	}
	switch base {
	case "ConfigOptionBool":
		if b, ok := parseBool(args); ok {
			return b, true
		}
		if args == "" {
			return false, true
		}
	case "ConfigOptionBools", "ConfigOptionBoolsNullable":
		return listOf(func(s string) (interface{}, bool) {
			b, ok := parseBool(s)
			return b, ok
		})
	case "ConfigOptionInt", "ConfigOptionFloat", "ConfigOptionPercent":
		if args == "" {
			return 0, true
		}
		return scalarNum(args)
	case "ConfigOptionInts", "ConfigOptionFloats", "ConfigOptionPercents", "ConfigOptionFloatsNullable", "ConfigOptionIntsNullable", "ConfigOptionPercentsNullable":
		if open == "(" && len(splitTop(args, ',')) == 2 && !strings.HasPrefix(args, "{") {
			// (count, value)
			ps := splitTop(args, ',')
			cnt, ok1 := p.evalNum(ps[0], e)
			v, ok2 := scalarNum(ps[1])
			if ok1 && ok2 {
				out := []interface{}{}
				for i := 0; i < int(cnt); i++ {
					out = append(out, v)
				}
				return out, true
			}
			return nil, false
		}
		return listOf(scalarNum)
	case "ConfigOptionString":
		if args == "" {
			return "", true
		}
		if s, ok := p.evalString(args, e); ok {
			return s, true
		}
	case "ConfigOptionStrings":
		if args == "" {
			return []interface{}{}, true
		}
		return listOf(func(s string) (interface{}, bool) {
			v, ok := p.evalString(s, e)
			return v, ok
		})
	case "ConfigOptionFloatOrPercent":
		ps := splitTop(args, ',')
		if len(ps) == 2 {
			v, ok1 := p.evalNum(ps[0], e)
			pc, ok2 := parseBool(ps[1])
			if ok1 && ok2 {
				return map[string]interface{}{"value": v, "percent": pc}, true
			}
		}
	case "ConfigOptionFloatsOrPercents", "ConfigOptionFloatsOrPercentsNullable":
		if args == "" || args == "{}" {
			return []interface{}{}, true
		}
		return listOf(func(s string) (interface{}, bool) {
			s = strings.TrimSpace(s)
			s = strings.TrimPrefix(s, "FloatOrPercent")
			s = strings.TrimSpace(s)
			if strings.HasPrefix(s, "{") || strings.HasPrefix(s, "(") {
				s = s[1 : len(s)-1]
			}
			ps := splitTop(s, ',')
			if len(ps) != 2 {
				return nil, false
			}
			v, ok1 := p.evalNum(ps[0], e)
			pc, ok2 := parseBool(ps[1])
			if !ok1 || !ok2 {
				return nil, false
			}
			return map[string]interface{}{"value": v, "percent": pc}, true
		})
	case "ConfigOptionPoint":
		part := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(args), "Vec2d"))
		if strings.HasPrefix(part, "(") && strings.HasSuffix(part, ")") {
			part = part[1 : len(part)-1]
		}
		xy := splitTop(part, ',')
		if len(xy) == 2 {
			x, ok1 := p.evalNum(xy[0], e)
			y, ok2 := p.evalNum(xy[1], e)
			if ok1 && ok2 {
				return []interface{}{x, y}, true
			}
		}
	case "ConfigOptionEnum":
		typ := o.EnumType
		if i := strings.Index(cls, "<"); i >= 0 && strings.HasSuffix(cls, ">") && typ == "" {
			typ = cls[i+1 : len(cls)-1]
		}
		em := p.enumMaps[typ]
		if em == nil {
			return nil, false
		}
		if k, ok := em.keyOf(args); ok {
			return k, true
		}
	case "ConfigOptionEnumsGeneric", "ConfigOptionEnumsGenericNullable":
		em := p.enumMaps[o.EnumType]
		if args == "" {
			return []interface{}{}, true
		}
		if em == nil {
			return listOf(scalarNum)
		}
		return listOf(func(s string) (interface{}, bool) {
			if k, ok := em.keyOf(s); ok {
				return k, true
			}
			return scalarNum(s)
		})
	case "ConfigOptionPoints":
		body := args
		if strings.HasPrefix(body, "{") && strings.HasSuffix(body, "}") {
			body = body[1 : len(body)-1]
		}
		out := []interface{}{}
		for _, part := range splitTop(body, ',') {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			part = strings.TrimPrefix(part, "Vec2d")
			part = strings.TrimSpace(part)
			if strings.HasPrefix(part, "(") {
				part = part[1 : len(part)-1]
			}
			xy := splitTop(part, ',')
			if len(xy) != 2 {
				return nil, false
			}
			x, ok1 := p.evalNum(xy[0], e)
			y, ok2 := p.evalNum(xy[1], e)
			if !ok1 || !ok2 {
				return nil, false
			}
			out = append(out, []interface{}{x, y})
		}
		return out, true
	}
	return nil, false
}

// enumKeys returns the enum_values of an option in declaration order, filling
// missing ones from the C++ enum key map when the def has none.
func (p *DefParser) enumKeys(o *Opt) []string {
	if len(o.EnumValues) > 0 {
		return o.EnumValues
	}
	if em := p.enumMaps[o.EnumType]; em != nil {
		var out []string
		for _, pr := range em.Pairs {
			out = append(out, pr[0])
		}
		return out
	}
	return nil
}

// parseEnumMaps reads every "t_config_enum_values s_keys_map_X { {"k", e}, ... };" in a file.
func parseEnumMaps(ct *CText, root *Node) map[string]*EnumMap {
	out := map[string]*EnumMap{}
	// Several maps can end up in one statement because the CONFIG_OPTION_ENUM_DEFINE_STATIC_MAPS
	// macro invocations that separate them carry no semicolon.
	re := regexp.MustCompile(`t_config_enum_values\s+s_keys_map_(\w+)\s*(?:=\s*)?\{`)
	pair := regexp.MustCompile(`\{\s*"([^"]*)"\s*,\s*(?:int\(\s*)?([\w:]+)\s*\)?\s*\}`)
	var visit func(n *Node)
	visit = func(n *Node) {
		for _, c := range n.Children {
			if c.Kind != nStmt {
				visit(c)
				continue
			}
			for _, loc := range re.FindAllStringSubmatchIndex(c.Text, -1) {
				open := loc[1] - 1
				end := matchBrace(c.Text, open)
				if end < 0 {
					continue
				}
				name := c.Text[loc[2]:loc[3]]
				em := &EnumMap{Name: name, Line: ct.lineAt(c.Off + loc[0])}
				for _, pm := range pair.FindAllStringSubmatch(c.Text[open+1:end], -1) {
					em.Pairs = append(em.Pairs, [2]string{pm[1], pm[2]})
				}
				out[name] = em
			}
		}
	}
	visit(root)
	return out
}

// matchBrace returns the index of the '}' matching the '{' at s[open], or -1.
func matchBrace(s string, open int) int {
	depth := 0
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '"':
			i++
			for i < len(s) && s[i] != '"' {
				if s[i] == '\\' {
					i++
				}
				i++
			}
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}
