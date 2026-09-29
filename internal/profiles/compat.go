package profiles

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Compatible reports whether a process or filament preset can be used with a
// printer preset (dev_docs/11-presets-and-projects.md (a), Preset.cpp:712-803).
//
// The rule: compatible when compatible_printers is empty and there is no
// condition, or the list names the printer (or, for a user printer, the
// printer's parent), or, when the list is empty, compatible_printers_condition
// evaluates true over the printer's flattened config. A condition that cannot
// be parsed or evaluated counts as compatible, like in the app, and
// evaluated is then false. evaluated is true whenever the answer came from the
// lists or from a condition that ran.
func Compatible(printer, preset Preset) (compatible, evaluated bool) {
	return compatibleWith(printer, preset, "compatible_printers", "compatible_printers_condition")
}

// CompatiblePrint is Compatible for a filament against a process preset
// (compatible_prints and compatible_prints_condition).
func CompatiblePrint(process, filament Preset) (compatible, evaluated bool) {
	return compatibleWith(process, filament, "compatible_prints", "compatible_prints_condition")
}

func compatibleWith(target, preset Preset, listKey, condKey string) (bool, bool) {
	var list []string
	for _, n := range preset.List(listKey) {
		if strings.TrimSpace(n) != "" {
			list = append(list, n)
		}
	}
	if len(list) > 0 {
		for _, n := range list {
			if n == target.Name {
				return true, true
			}
			// A user printer also matches through its parent, one level.
			if target.Source == SourceUser && target.Parent() != "" && n == target.Parent() {
				return true, true
			}
		}
		return false, true
	}
	cond := strings.TrimSpace(preset.String(condKey))
	if cond == "" {
		return true, true
	}
	ok, err := EvalCondition(cond, target.Values)
	if err != nil {
		return true, false
	}
	return ok, true
}

// EvalCondition evaluates a compatibility condition over a flattened config.
// The supported subset: string ("..." or '...'), number, true and false
// literals; config variables, optionally indexed (nozzle_diameter[0]); the
// operators ==, !=, <, <=, >, >=, =~ and !~ (with a /regex/ or /regex/i right
// side); and, or, not (also &&, || and !); and parentheses. Anything else is
// an error.
func EvalCondition(expr string, cfg map[string]any) (bool, error) {
	toks, err := lexCondition(expr)
	if err != nil {
		return false, err
	}
	p := &condParser{toks: toks, cfg: cfg}
	v, err := p.parseOr()
	if err != nil {
		return false, err
	}
	if p.pos != len(p.toks) {
		return false, fmt.Errorf("unexpected %q", p.toks[p.pos].text)
	}
	if v.kind != kindBool {
		return false, fmt.Errorf("condition is not a boolean expression")
	}
	return v.b, nil
}

type tokKind int

const (
	tokString tokKind = iota
	tokNumber
	tokIdent
	tokOp
	tokRegex
	tokLParen
	tokRParen
	tokIndex // [n] directly after an identifier
)

type token struct {
	kind tokKind
	text string
	flag string // regex flags
}

var identRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*`)
var numberRE = regexp.MustCompile(`^-?[0-9]+(\.[0-9]+)?([eE][-+]?[0-9]+)?`)

func lexCondition(s string) ([]token, error) {
	var toks []token
	i := 0
	for i < len(s) {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '(':
			toks = append(toks, token{kind: tokLParen, text: "("})
			i++
		case c == ')':
			toks = append(toks, token{kind: tokRParen, text: ")"})
			i++
		case c == '"' || c == '\'':
			j := i + 1
			var sb strings.Builder
			for j < len(s) && s[j] != c {
				if s[j] == '\\' && j+1 < len(s) {
					j++
				}
				sb.WriteByte(s[j])
				j++
			}
			if j >= len(s) {
				return nil, fmt.Errorf("unterminated string")
			}
			toks = append(toks, token{kind: tokString, text: sb.String()})
			i = j + 1
		case c == '[' && len(toks) > 0 && toks[len(toks)-1].kind == tokIdent:
			j := strings.IndexByte(s[i:], ']')
			if j < 0 {
				return nil, fmt.Errorf("unterminated index")
			}
			toks = append(toks, token{kind: tokIndex, text: strings.TrimSpace(s[i+1 : i+j])})
			i += j + 1
		case strings.HasPrefix(s[i:], "=~") || strings.HasPrefix(s[i:], "!~"):
			toks = append(toks, token{kind: tokOp, text: s[i : i+2]})
			i += 2
			for i < len(s) && s[i] == ' ' {
				i++
			}
			if i >= len(s) || s[i] != '/' {
				return nil, fmt.Errorf("%s needs a /regex/", toks[len(toks)-1].text)
			}
			j := i + 1
			for j < len(s) && s[j] != '/' {
				if s[j] == '\\' {
					j++
				}
				j++
			}
			if j >= len(s) {
				return nil, fmt.Errorf("unterminated regex")
			}
			re := s[i+1 : j]
			j++
			flags := ""
			for j < len(s) && (s[j] == 'i' || s[j] == 's' || s[j] == 'm') {
				flags += string(s[j])
				j++
			}
			toks = append(toks, token{kind: tokRegex, text: re, flag: flags})
			i = j
		case strings.HasPrefix(s[i:], "==") || strings.HasPrefix(s[i:], "!=") || strings.HasPrefix(s[i:], "<=") ||
			strings.HasPrefix(s[i:], ">=") || strings.HasPrefix(s[i:], "&&") || strings.HasPrefix(s[i:], "||"):
			toks = append(toks, token{kind: tokOp, text: s[i : i+2]})
			i += 2
		case c == '<' || c == '>' || c == '!':
			toks = append(toks, token{kind: tokOp, text: string(c)})
			i++
		case c >= '0' && c <= '9' || c == '-' && i+1 < len(s) && s[i+1] >= '0' && s[i+1] <= '9':
			m := numberRE.FindString(s[i:])
			if m == "" {
				return nil, fmt.Errorf("bad number at %q", s[i:])
			}
			toks = append(toks, token{kind: tokNumber, text: m})
			i += len(m)
		case c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z':
			m := identRE.FindString(s[i:])
			toks = append(toks, token{kind: tokIdent, text: m})
			i += len(m)
		default:
			return nil, fmt.Errorf("unsupported character %q", string(c))
		}
	}
	return toks, nil
}

type valKind int

const (
	kindBool valKind = iota
	kindNumber
	kindString
)

type value struct {
	kind valKind
	b    bool
	n    float64
	s    string
	// fromConfig marks a config variable, whose text may be a number.
	fromConfig bool
}

type condParser struct {
	toks []token
	pos  int
	cfg  map[string]any
}

func (p *condParser) peek() *token {
	if p.pos < len(p.toks) {
		return &p.toks[p.pos]
	}
	return nil
}

func (p *condParser) isWord(word string, symbols ...string) bool {
	t := p.peek()
	if t == nil {
		return false
	}
	if t.kind == tokIdent && t.text == word {
		return true
	}
	if t.kind == tokOp {
		for _, s := range symbols {
			if t.text == s {
				return true
			}
		}
	}
	return false
}

func (p *condParser) parseOr() (value, error) {
	l, err := p.parseAnd()
	if err != nil {
		return l, err
	}
	for p.isWord("or", "||") {
		p.pos++
		r, err := p.parseAnd()
		if err != nil {
			return r, err
		}
		a, b, err := bothBool(l, r)
		if err != nil {
			return l, err
		}
		l = value{kind: kindBool, b: a || b}
	}
	return l, nil
}

func (p *condParser) parseAnd() (value, error) {
	l, err := p.parseNot()
	if err != nil {
		return l, err
	}
	for p.isWord("and", "&&") {
		p.pos++
		r, err := p.parseNot()
		if err != nil {
			return r, err
		}
		a, b, err := bothBool(l, r)
		if err != nil {
			return l, err
		}
		l = value{kind: kindBool, b: a && b}
	}
	return l, nil
}

func bothBool(l, r value) (bool, bool, error) {
	if l.kind != kindBool || r.kind != kindBool {
		return false, false, fmt.Errorf("and/or need boolean operands")
	}
	return l.b, r.b, nil
}

func (p *condParser) parseNot() (value, error) {
	if p.isWord("not", "!") {
		p.pos++
		v, err := p.parseNot()
		if err != nil {
			return v, err
		}
		if v.kind != kindBool {
			return v, fmt.Errorf("not needs a boolean operand")
		}
		return value{kind: kindBool, b: !v.b}, nil
	}
	return p.parseCompare()
}

func (p *condParser) parseCompare() (value, error) {
	l, err := p.parsePrimary()
	if err != nil {
		return l, err
	}
	t := p.peek()
	if t == nil || t.kind != tokOp {
		return l, nil
	}
	switch t.text {
	case "==", "!=", "<", "<=", ">", ">=":
		p.pos++
		r, err := p.parsePrimary()
		if err != nil {
			return r, err
		}
		return compareValues(t.text, l, r)
	case "=~", "!~":
		p.pos++
		re := p.peek()
		if re == nil || re.kind != tokRegex {
			return l, fmt.Errorf("%s needs a /regex/", t.text)
		}
		p.pos++
		pattern := re.text
		if strings.Contains(re.flag, "i") {
			pattern = "(?i)" + pattern
		}
		if strings.Contains(re.flag, "s") {
			pattern = "(?s)" + pattern
		}
		if strings.Contains(re.flag, "m") {
			pattern = "(?m)" + pattern
		}
		rx, err := regexp.Compile(pattern)
		if err != nil {
			return l, err
		}
		if l.kind == kindBool {
			return l, fmt.Errorf("a regex needs a string operand")
		}
		text := l.s
		if l.kind == kindNumber {
			text = strconv.FormatFloat(l.n, 'f', -1, 64)
		}
		return value{kind: kindBool, b: rx.MatchString(text) == (t.text == "=~")}, nil
	}
	return l, nil
}

func compareValues(op string, l, r value) (value, error) {
	if l.kind == kindBool || r.kind == kindBool {
		if l.kind != r.kind || (op != "==" && op != "!=") {
			return l, fmt.Errorf("cannot compare a boolean with %s", op)
		}
		return value{kind: kindBool, b: (l.b == r.b) == (op == "==")}, nil
	}
	// Numeric when a number literal meets something numeric, or both are numbers.
	ln, lok := numeric(l)
	rn, rok := numeric(r)
	if lok && rok && (l.kind == kindNumber || r.kind == kindNumber) {
		var b bool
		switch op {
		case "==":
			b = ln == rn
		case "!=":
			b = ln != rn
		case "<":
			b = ln < rn
		case "<=":
			b = ln <= rn
		case ">":
			b = ln > rn
		case ">=":
			b = ln >= rn
		}
		return value{kind: kindBool, b: b}, nil
	}
	if l.kind == kindNumber || r.kind == kindNumber {
		return l, fmt.Errorf("cannot compare a number with a non numeric string")
	}
	var b bool
	switch op {
	case "==":
		b = l.s == r.s
	case "!=":
		b = l.s != r.s
	case "<":
		b = l.s < r.s
	case "<=":
		b = l.s <= r.s
	case ">":
		b = l.s > r.s
	case ">=":
		b = l.s >= r.s
	}
	return value{kind: kindBool, b: b}, nil
}

func numeric(v value) (float64, bool) {
	switch v.kind {
	case kindNumber:
		return v.n, true
	case kindString:
		f, err := strconv.ParseFloat(strings.TrimSpace(v.s), 64)
		return f, err == nil
	}
	return 0, false
}

func (p *condParser) parsePrimary() (value, error) {
	t := p.peek()
	if t == nil {
		return value{}, fmt.Errorf("unexpected end of condition")
	}
	switch t.kind {
	case tokLParen:
		p.pos++
		v, err := p.parseOr()
		if err != nil {
			return v, err
		}
		if c := p.peek(); c == nil || c.kind != tokRParen {
			return v, fmt.Errorf("missing )")
		}
		p.pos++
		return v, nil
	case tokString:
		p.pos++
		return value{kind: kindString, s: t.text}, nil
	case tokNumber:
		p.pos++
		f, err := strconv.ParseFloat(t.text, 64)
		if err != nil {
			return value{}, err
		}
		return value{kind: kindNumber, n: f}, nil
	case tokIdent:
		p.pos++
		switch t.text {
		case "true":
			return value{kind: kindBool, b: true}, nil
		case "false":
			return value{kind: kindBool, b: false}, nil
		case "and", "or", "not":
			return value{}, fmt.Errorf("unexpected %q", t.text)
		}
		index := -1
		if n := p.peek(); n != nil && n.kind == tokIndex {
			p.pos++
			i, err := strconv.Atoi(n.text)
			if err != nil || i < 0 {
				return value{}, fmt.Errorf("unsupported index [%s]", n.text)
			}
			index = i
		}
		return p.variable(t.text, index)
	}
	return value{}, fmt.Errorf("unexpected %q", t.text)
}

// variable reads a config variable. A vector needs an index; a scalar takes
// none. An unknown variable is an error (the app would report it too).
func (p *condParser) variable(name string, index int) (value, error) {
	v, ok := p.cfg[name]
	if !ok {
		return value{}, fmt.Errorf("unknown variable %s", name)
	}
	switch t := v.(type) {
	case string:
		if index > 0 {
			return value{}, fmt.Errorf("%s has no element %d", name, index)
		}
		return value{kind: kindString, s: t, fromConfig: true}, nil
	case []string:
		if index < 0 {
			return value{}, fmt.Errorf("%s is a vector and needs an index", name)
		}
		if index >= len(t) {
			return value{}, fmt.Errorf("%s has no element %d", name, index)
		}
		return value{kind: kindString, s: t[index], fromConfig: true}, nil
	}
	return value{}, fmt.Errorf("variable %s has an unsupported type", name)
}
