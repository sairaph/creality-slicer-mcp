package main

// gui.go: extract the settings-page layout (tab > page > group > option) from the
// Tab::build functions in Tab.cpp, and the enable/visibility logic from the
// toggle functions (Tab.cpp, ConfigManipulation.cpp).

import (
	"fmt"
	"regexp"
	"strings"
)

type GuiOption struct {
	Key       string   `json:"key"`
	Line      int      `json:"line"`
	LineLabel string   `json:"line_label,omitempty"`
	Widget    bool     `json:"custom_widget,omitempty"`
	Synthetic bool     `json:"gui_only,omitempty"`
	Conds     []string `json:"build_conditions,omitempty"`
	Macro     string   `json:"build_macro,omitempty"`
	Func      string   `json:"function,omitempty"`
}

type GuiGroup struct {
	Title   string      `json:"group"`
	Line    int         `json:"line"`
	Options []GuiOption `json:"options"`
}

type GuiPage struct {
	Title   string      `json:"page"`
	Dynamic string      `json:"dynamic,omitempty"`
	Line    int         `json:"line"`
	Conds   []string    `json:"build_conditions,omitempty"`
	Groups  []*GuiGroup `json:"groups"`
}

type GuiTab struct {
	Name     string     `json:"tab"`
	Scope    string     `json:"scope"`
	Function string     `json:"function"`
	Pages    []*GuiPage `json:"pages"`
}

type gctx struct {
	tab      *GuiTab
	page     *GuiPage
	group    *GuiGroup
	lastOpt  string
	lineKeys []string
	lineLbl  string
	lineOpen bool
	lists    map[string][]string
	loop     map[string]string
	strs     map[string]string
	conds    []string
	fn       string
	depth    int
}

type GuiParser struct {
	ct    *CText
	funcs map[string]*Node // "Class::name" -> body block
	tabs  []*GuiTab
	synth map[string]bool
	warns []string
}

func newGuiParser(ct *CText, root *Node) *GuiParser {
	g := &GuiParser{ct: ct, funcs: map[string]*Node{}, synth: map[string]bool{}}
	reHdr := regexp.MustCompile(`(\w+)::(\w+)\s*\(`)
	var visit func(n *Node)
	visit = func(n *Node) {
		for _, c := range n.Children {
			if c.Kind != nBlock {
				continue
			}
			if m := reHdr.FindStringSubmatch(c.Header); m != nil && !strings.HasPrefix(strings.TrimSpace(c.Header), "for") && !strings.HasPrefix(strings.TrimSpace(c.Header), "if") {
				name := m[1] + "::" + m[2]
				if _, dup := g.funcs[name]; !dup {
					g.funcs[name] = c
				}
				continue
			}
			visit(c)
		}
	}
	visit(root)
	return g
}

func (g *GuiParser) warn(line int, format string, a ...interface{}) {
	g.warns = append(g.warns, fmt.Sprintf("%s:%d: %s", g.ct.Rel, line, fmt.Sprintf(format, a...)))
}

var (
	reAddPage   = regexp.MustCompile(`(?s)^(?:(?:auto|PageShp)\s+)?(\w+)\s*=\s*add_options_page\s*\((.*)\)$`)
	reNewGroup  = regexp.MustCompile(`(?s)^(?:(?:auto|ConfigOptionsGroupShp)\s+)?(\w+)\s*=\s*(\w+)\s*->\s*new_optgroup\s*\((.*)\)$`)
	reAppendSO  = regexp.MustCompile(`(?s)^(\w+)\s*->\s*append_single_option_line\s*\((.*)\)$`)
	reGetOpt    = regexp.MustCompile(`(?s)^(?:(?:Option|auto)\s+)?(\w+)\s*=\s*(\w+)\s*->\s*get_option\s*\((.*)\)$`)
	reOptCtor   = regexp.MustCompile(`(?s)^(?:(?:Option|auto)\s+)?(\w+)\s*(?:=\s*Option)?\s*\(\s*\w+\s*,\s*"([^"]+)"\s*\)$`)
	reLineInit  = regexp.MustCompile(`(?s)^(?:(?:Line|auto)\s+)?line\s*(?:=\s*)?(?:Line\s*)?\{(.*)\}$`)
	reLineAppn  = regexp.MustCompile(`(?s)^line\s*\.\s*append_option\s*\((.*)\)$`)
	reLineFlush = regexp.MustCompile(`^\w+\s*->\s*append_line\s*\(\s*line\s*\)$`)
	reCreateW   = regexp.MustCompile(`(?s)^create_line_with_widget\s*\(\s*\w+(?:\.get\(\))?\s*,\s*(.+?)\s*,`)
	reAppendOL  = regexp.MustCompile(`(?s)^(append_option_line|append_override_line|append_single_option_line)\s*\(\s*\w+\s*,\s*(.*)\)$`)
	reStrFmt    = regexp.MustCompile(`(?s)^(?:const\s+)?wxString\s*&?\s*(\w+)\s*=\s*wxString::Format\s*\(\s*"([^"]*)"`)
	reFuncCall  = regexp.MustCompile(`^(?:auto\s+\w+\s*=\s*)?(add_filament_overrides_page|build_unregular_pages|build_kinematics_page)\s*\(`)
)

func (g *GuiParser) evalStr(expr string, c *gctx) (string, bool) {
	expr = strings.TrimSpace(expr)
	if parts := splitTop(expr, '+'); len(parts) > 1 {
		var b strings.Builder
		for _, p := range parts {
			s, ok := g.evalStr(p, c)
			if !ok {
				return "", false
			}
			b.WriteString(s)
		}
		return b.String(), true
	}
	if expr == "" {
		return "", false
	}
	if expr[0] == '"' && onlyLiterals(expr) {
		var b strings.Builder
		for _, l := range stringLiterals(expr) {
			b.WriteString(l.Val)
		}
		return b.String(), true
	}
	if m := reWrap.FindStringSubmatch(expr); m != nil {
		return g.evalStr(m[1], c)
	}
	if expr[0] == '(' && matchParen(expr, 0) == len(expr)-1 {
		return g.evalStr(expr[1:len(expr)-1], c)
	}
	if v, ok := c.loop[expr]; ok {
		return v, true
	}
	if v, ok := c.strs[expr]; ok {
		return v, true
	}
	if expr == "opt_key" && c.lastOpt != "" {
		return c.lastOpt, true
	}
	return "", false
}

// keyFrom resolves an option-key argument: a literal, a loop variable, or the last get_option key.
func (g *GuiParser) keyFrom(arg string, c *gctx) (string, bool) {
	arg = strings.TrimSpace(arg)
	if arg == "option" && c.lastOpt != "" {
		return c.lastOpt, true
	}
	return g.evalStr(arg, c)
}

func (g *GuiParser) runTab(name, scope, fn string) *GuiTab {
	body := g.funcs[fn]
	if body == nil {
		g.warn(0, "function %s not found", fn)
		return nil
	}
	tab := &GuiTab{Name: name, Scope: scope, Function: fn}
	c := &gctx{tab: tab, lists: map[string][]string{}, loop: map[string]string{}, strs: map[string]string{}, fn: fn}
	g.runNodes(body.Children, c)
	g.flushLine(c)
	g.tabs = append(g.tabs, tab)
	return tab
}

func (g *GuiParser) runNodes(nodes []*Node, c *gctx) {
	for _, n := range nodes {
		if n.Kind == nStmt {
			g.runStmt(n.Text, n.Line, n.Cond, c)
		} else {
			g.runBlock(n, c)
		}
	}
}

func condText(h string) string {
	h = strings.Join(strings.Fields(h), " ")
	if len(h) > 200 {
		h = h[:200] + "..."
	}
	return h
}

func (g *GuiParser) runBlock(n *Node, c *gctx) {
	h := strings.TrimSpace(n.Header)
	switch {
	case h == "":
		g.runNodes(n.Children, c)
	case strings.HasPrefix(h, "for"):
		g.runFor(h, n.Children, n.Line, c)
	case strings.HasPrefix(h, "if"), strings.HasPrefix(h, "else"):
		c.conds = append(c.conds, condText(h))
		g.runNodes(n.Children, c)
		c.conds = c.conds[:len(c.conds)-1]
	case strings.HasPrefix(h, "struct "), strings.HasPrefix(h, "switch"), strings.HasPrefix(h, "while"):
		// nothing to record
	}
}

func (g *GuiParser) runFor(h string, body []*Node, line int, c *gctx) {
	m := reFor.FindStringSubmatch(h)
	if m == nil {
		return
	}
	inner := strings.TrimSpace(m[1])
	if r := reRange.FindStringSubmatch(inner); r != nil && !strings.Contains(inner, ";") {
		varName, rng := r[1], strings.TrimSpace(r[2])
		var vals []string
		switch {
		case strings.HasPrefix(rng, "{"):
			for _, l := range stringLiterals(rng) {
				vals = append(vals, l.Val)
			}
		default:
			l, ok := c.lists[rng]
			if !ok {
				return // loop over pages, objects, controls: no options declared here
			}
			vals = l
		}
		for _, v := range vals {
			c.loop[varName] = v
			g.runNodes(body, c)
		}
		delete(c.loop, varName)
		return
	}
	// classic for(;;) loop: walk once, tagged with the loop header
	c.conds = append(c.conds, "loop: "+condText(inner))
	g.runNodes(body, c)
	c.conds = c.conds[:len(c.conds)-1]
}

func (g *GuiParser) ensurePage(c *gctx, line int) {
	if c.page == nil {
		c.page = &GuiPage{Title: "(none)", Line: line}
		c.tab.Pages = append(c.tab.Pages, c.page)
	}
	if c.group == nil {
		c.group = &GuiGroup{Title: "", Line: line}
		c.page.Groups = append(c.page.Groups, c.group)
	}
}

func (g *GuiParser) addOpt(c *gctx, key string, line int, macro string, widget bool, label string) {
	g.ensurePage(c, line)
	o := GuiOption{Key: key, Line: line, Widget: widget, LineLabel: label, Macro: macro, Func: c.fn}
	if len(c.conds) > 0 {
		o.Conds = append([]string(nil), c.conds...)
	}
	c.group.Options = append(c.group.Options, o)
}

func (g *GuiParser) flushLine(c *gctx) {
	if c.lineOpen && len(c.lineKeys) > 0 && c.group != nil {
		// options of a multi-option line were already added when appended
	}
	c.lineOpen = false
	c.lineKeys = nil
	c.lineLbl = ""
}

func (g *GuiParser) runStmt(text string, line int, macro string, c *gctx) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	// inline if/for with a single statement body
	if strings.HasPrefix(text, "for") || strings.HasPrefix(text, "if") {
		kw := ""
		if i := strings.Index(text, "("); i > 0 {
			kw = strings.TrimSpace(text[:i])
		}
		if kw == "for" || kw == "if" {
			i := strings.Index(text, "(")
			if j := matchParen(text, i); j > 0 {
				hdr := strings.TrimSpace(text[:j+1])
				rest := strings.TrimSpace(text[j+1:])
				body := []*Node{{Kind: nStmt, Text: rest, Line: line, Cond: macro}}
				if kw == "for" {
					g.runFor(hdr, body, line, c)
				} else {
					c.conds = append(c.conds, condText(hdr))
					g.runNodes(body, c)
					c.conds = c.conds[:len(c.conds)-1]
				}
				return
			}
		}
	}
	if m := reAddPage.FindStringSubmatch(text); m != nil {
		g.flushLine(c)
		args := splitTop(m[2], ',')
		title, ok := g.evalStr(args[0], c)
		p := &GuiPage{Line: line}
		if !ok {
			title = strings.TrimSpace(args[0])
			g.warn(line, "page title not literal: %s", title)
		}
		p.Title = title
		if strings.Contains(title, "<n>") {
			p.Dynamic = "one page per extruder"
		}
		if len(c.conds) > 0 {
			p.Conds = append([]string(nil), c.conds...)
		}
		c.tab.Pages = append(c.tab.Pages, p)
		c.page, c.group = p, nil
		return
	}
	if m := reNewGroup.FindStringSubmatch(text); m != nil {
		g.flushLine(c)
		args := splitTop(m[3], ',')
		title, _ := g.evalStr(args[0], c)
		if c.page == nil {
			g.ensurePage(c, line)
		}
		gr := &GuiGroup{Title: title, Line: line}
		c.page.Groups = append(c.page.Groups, gr)
		c.group = gr
		return
	}
	if m := reStrFmt.FindStringSubmatch(text); m != nil {
		c.strs[m[1]] = strings.ReplaceAll(m[2], "%d", "<n>")
		return
	}
	if m := reAppendSO.FindStringSubmatch(text); m != nil {
		args := splitTop(m[2], ',')
		if len(args) == 0 {
			return
		}
		if key, ok := g.keyFrom(args[0], c); ok {
			g.addOpt(c, key, line, macro, false, "")
		} else {
			g.warn(line, "append_single_option_line with unresolved key %q", oneLine(args[0]))
		}
		return
	}
	if m := reGetOpt.FindStringSubmatch(text); m != nil {
		args := splitTop(m[3], ',')
		if key, ok := g.keyFrom(args[0], c); ok {
			c.lastOpt = key
		} else {
			g.warn(line, "get_option with unresolved key %q", oneLine(args[0]))
		}
		return
	}
	if m := reOptCtor.FindStringSubmatch(text); m != nil && strings.Contains(text, "Option") {
		c.lastOpt = m[2]
		g.synth[m[2]] = true
		return
	}
	if m := reLineInit.FindStringSubmatch(text); m != nil {
		g.flushLine(c)
		c.lineOpen = true
		els := splitTop(m[1], ',')
		if len(els) > 0 {
			c.lineLbl, _ = g.evalStr(els[0], c)
		}
		return
	}
	if m := reLineAppn.FindStringSubmatch(text); m != nil {
		arg := strings.TrimSpace(m[1])
		var key string
		var ok bool
		if gm := regexp.MustCompile(`(?s)^\w+\s*->\s*get_option\s*\((.*)\)$`).FindStringSubmatch(arg); gm != nil {
			args := splitTop(gm[1], ',')
			key, ok = g.keyFrom(args[0], c)
		} else {
			key, ok = g.keyFrom(arg, c)
		}
		if ok {
			dup := false
			for _, k := range c.lineKeys {
				if k == key {
					dup = true
				}
			}
			if !dup {
				c.lineKeys = append(c.lineKeys, key)
				g.addOpt(c, key, line, macro, false, c.lineLbl)
			}
		} else {
			g.warn(line, "line.append_option with unresolved key %q", oneLine(arg))
		}
		return
	}
	if reLineFlush.MatchString(text) {
		g.flushLine(c)
		return
	}
	if m := reCreateW.FindStringSubmatch(text); m != nil {
		if key, ok := g.evalStr(m[1], c); ok {
			g.addOpt(c, key, line, macro, true, "")
		}
		return
	}
	if m := reAppendOL.FindStringSubmatch(text); m != nil {
		args := splitTop(m[2], ',')
		if key, ok := g.evalStr(args[0], c); ok {
			g.addOpt(c, key, line, macro, false, "")
		} else {
			g.warn(line, "%s with unresolved key %q", m[1], oneLine(args[0]))
		}
		return
	}
	if reKeyListDecl.MatchString(text) {
		m := reKeyListDecl.FindStringSubmatch(text)
		var vals []string
		for _, l := range stringLiterals(m[2]) {
			vals = append(vals, l.Val)
		}
		c.lists[m[1]] = vals
		return
	}
	if m := regexp.MustCompile(`(?s)^(\w+)\s*=\s*\{(.*)\}$`).FindStringSubmatch(text); m != nil {
		if _, isList := c.lists[m[1]]; isList {
			var vals []string
			for _, l := range stringLiterals(m[2]) {
				vals = append(vals, l.Val)
			}
			c.lists[m[1]] = vals
		}
		return
	}
	if m := reFuncCall.FindStringSubmatch(text); m != nil {
		callee := map[string]string{
			"add_filament_overrides_page": "TabFilament::add_filament_overrides_page",
			"build_unregular_pages":       "TabPrinter::build_unregular_pages",
			"build_kinematics_page":       "TabPrinter::build_kinematics_page",
		}[m[1]]
		if body := g.funcs[callee]; body != nil && c.depth < 4 {
			c.depth++
			savedFn := c.fn
			c.fn = callee
			savedLoop := c.loop
			c.loop = map[string]string{}
			g.runNodes(body.Children, c)
			c.loop = savedLoop
			c.fn = savedFn
			c.depth--
		}
		return
	}
}

// ---------------------------------------------------------------------------
// Gating: toggle_field / toggle_line / toggle_option and forced value changes.

type Gate struct {
	Key       string   `json:"key"`
	Effect    string   `json:"effect"` // field = enable/disable the input, line = show/hide the row
	Condition string   `json:"condition"`
	Expanded  string   `json:"condition_expanded,omitempty"`
	Constant  string   `json:"constant,omitempty"` // "true"/"false" when the condition is a literal
	Context   []string `json:"context,omitempty"`
	Function  string   `json:"function"`
	File      string   `json:"file"`
	Line      int      `json:"line"`
	IndexArg  bool     `json:"per_extruder_index,omitempty"`
	// Drivers lists what the condition depends on: config keys ("key") and
	// printer/vendor capability flags ("capability:name").
	Drivers []string `json:"drivers,omitempty"`

	// Full texts, kept for the slim catalog (never written to the full JSON).
	Full        string   `json:"-"`
	ContextFull []string `json:"-"`
}

var (
	reDriverKey = regexp.MustCompile(`(?:opt_\w+|option)(?:<[^()"]*>)?\(\s*"([\w#]+)"`)
	reDriverCap = regexp.MustCompile(`\b(is_BBL_[Pp]rinter|is_CX_[Pp]rinter|is_creality_vendor|is_belt_machine|gcflavor|bSEMM|use_creality_tower|can_flush_into_skeleton_for_printer|support_multi_bed_types|m_use_silent_mode|is_marlin_flavor|is_global_config|is_object_config|is_plate_config|m_type|from_initial_build|is_bbl_vendor|is_cx_vendor)\b`)
)

// gateDrivers extracts the config keys and capability flags a condition reads.
func gateDrivers(texts ...string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, t := range texts {
		for _, m := range reDriverKey.FindAllStringSubmatch(t, -1) {
			add(strings.SplitN(m[1], "#", 2)[0])
		}
		for _, m := range reDriverCap.FindAllStringSubmatch(t, -1) {
			add("capability:" + m[1])
		}
	}
	return out
}

type Forced struct {
	Key         string   `json:"key"`
	Value       string   `json:"value"`
	Context     []string `json:"context,omitempty"`
	Function    string   `json:"function"`
	File        string   `json:"file"`
	Line        int      `json:"line"`
	ValueFull   string   `json:"-"`
	ContextFull []string `json:"-"`
}

type gateCtx struct {
	fn     string
	file   string
	vars   map[string]string
	lists  map[string][]string
	loop   map[string]string
	conds  []string
	gates  *[]Gate
	forced *[]Forced
}

var (
	reToggle    = regexp.MustCompile(`(?s)^(?:\w+\s*(?:->|\.)\s*)?(toggle_field|toggle_line|toggle_option|toggle_line_in_support|toggle_line_in_others)\s*\((.*)\)$`)
	reGateVar   = regexp.MustCompile(`(?s)^(?:const\s+)?(?:bool|float|int|double|auto|size_t|long|InfillPattern|SupportType|NoiseType|SupportMaterialStyle|GCodeFlavor|PrimeTowerEnhanceType|BrimType|IroningType|WallDirection|SeamScarfType|PerimeterGeneratorType|PrintSequence|GapFillTarget|DraftShield|TimelapseType|FuzzySkinType)\s+(\w+)\s*=\s*(.*)$`)
	reSetKV     = regexp.MustCompile(`(?s)^(\w+)\s*(?:->|\.)\s*set_key_value\s*\(\s*"([^"]+)"\s*,\s*(.*)\)$`)
	reIdentWord = regexp.MustCompile(`[A-Za-z_]\w*`)
)

// expandVars substitutes known local variable names by their definitions, up to
// three levels deep, so that "have_infill" becomes readable.
func expandVars(expr string, vars map[string]string) string {
	for depth := 0; depth < 3; depth++ {
		changed := false
		var out strings.Builder
		for i := 0; i < len(expr); {
			if expr[i] == '"' { // string literals are copied verbatim
				j := i + 1
				for j < len(expr) && expr[j] != '"' {
					if expr[j] == '\x5c' {
						j++
					}
					j++
				}
				if j >= len(expr) {
					j = len(expr) - 1
				}
				out.WriteString(expr[i : j+1])
				i = j + 1
				continue
			}
			// copy the next stretch up to a quote and expand identifiers in it
			j := i
			for j < len(expr) && expr[j] != '"' {
				j++
			}
			out.WriteString(reIdentWord.ReplaceAllStringFunc(expr[i:j], func(w string) string {
				if d, ok := vars[w]; ok && d != w {
					changed = true
					return "(" + d + ")"
				}
				return w
			}))
			i = j
		}
		expr = out.String()
		if !changed {
			break
		}
	}
	return expr
}

// walkGates scans the named functions for toggle calls and forced changes.
func walkGates(ct *CText, funcs map[string]*Node, names []string, gates *[]Gate, forced *[]Forced) []string {
	var warns []string
	for _, name := range names {
		body := funcs[name]
		if body == nil {
			warns = append(warns, fmt.Sprintf("%s: function %s not found", ct.Rel, name))
			continue
		}
		gc := &gateCtx{fn: name, file: ct.Rel, vars: map[string]string{}, lists: map[string][]string{}, loop: map[string]string{}, gates: gates, forced: forced}
		gc.runNodes(body.Children)
	}
	return warns
}

func (gc *gateCtx) runNodes(nodes []*Node) {
	for _, n := range nodes {
		if n.Kind == nStmt {
			gc.runStmt(n.Text, n.Line)
		} else {
			gc.runBlock(n)
		}
	}
}

func (gc *gateCtx) runBlock(n *Node) {
	h := strings.TrimSpace(n.Header)
	switch {
	case h == "":
		gc.runNodes(n.Children)
	case strings.HasPrefix(h, "for"):
		gc.runFor(h, n.Children)
	case strings.HasPrefix(h, "if"), strings.HasPrefix(h, "else"):
		gc.conds = append(gc.conds, condFull(h))
		gc.runNodes(n.Children)
		gc.conds = gc.conds[:len(gc.conds)-1]
	}
}

func (gc *gateCtx) runFor(h string, body []*Node) {
	m := reFor.FindStringSubmatch(h)
	if m == nil {
		return
	}
	inner := strings.TrimSpace(m[1])
	r := reRange.FindStringSubmatch(inner)
	if r == nil || strings.Contains(inner, ";") {
		gc.conds = append(gc.conds, "loop: "+condFull(inner))
		gc.runNodes(body)
		gc.conds = gc.conds[:len(gc.conds)-1]
		return
	}
	varName, rng := r[1], strings.TrimSpace(r[2])
	var vals []string
	switch {
	case strings.HasPrefix(rng, "{"):
		for _, l := range stringLiterals(rng) {
			vals = append(vals, l.Val)
		}
	default:
		l, ok := gc.lists[rng]
		if !ok {
			return
		}
		vals = l
	}
	for _, v := range vals {
		gc.loop[varName] = v
		gc.runNodes(body)
	}
	delete(gc.loop, varName)
}

func (gc *gateCtx) resolveKey(expr string) (string, bool) {
	expr = strings.TrimSpace(expr)
	if v, ok := gc.loop[expr]; ok {
		return v, true
	}
	if parts := splitTop(expr, '+'); len(parts) > 1 {
		var b strings.Builder
		for _, p := range parts {
			p = strings.TrimSpace(p)
			switch {
			case strings.HasPrefix(p, "std::to_string"):
				b.WriteString("<i>")
			case strings.HasPrefix(p, "\""):
				for _, l := range stringLiterals(p) {
					b.WriteString(l.Val)
				}
			default:
				if v, ok := gc.loop[p]; ok {
					b.WriteString(v)
				} else {
					return "", false
				}
			}
		}
		return b.String(), true
	}
	if len(expr) > 1 && expr[0] == '"' && onlyLiterals(expr) {
		var b strings.Builder
		for _, l := range stringLiterals(expr) {
			b.WriteString(l.Val)
		}
		return b.String(), true
	}
	return "", false
}

func (gc *gateCtx) runStmt(text string, line int) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	if strings.HasPrefix(text, "for") || strings.HasPrefix(text, "if") {
		kw := ""
		if i := strings.Index(text, "("); i > 0 {
			kw = strings.TrimSpace(text[:i])
		}
		if kw == "for" || kw == "if" {
			i := strings.Index(text, "(")
			if j := matchParen(text, i); j > 0 {
				hdr := strings.TrimSpace(text[:j+1])
				rest := strings.TrimSpace(text[j+1:])
				body := []*Node{{Kind: nStmt, Text: rest, Line: line}}
				if kw == "for" {
					gc.runFor(hdr, body)
				} else {
					gc.conds = append(gc.conds, condFull(hdr))
					gc.runNodes(body)
					gc.conds = gc.conds[:len(gc.conds)-1]
				}
				return
			}
		}
	}
	if m := reToggle.FindStringSubmatch(text); m != nil && m[1] != "" {
		args := splitTop(m[2], ',')
		if len(args) < 2 {
			return
		}
		effect := "line"
		if m[1] == "toggle_field" || m[1] == "toggle_option" {
			effect = "field"
		}
		// toggle_line_in_support(key, visible): same meaning as toggle_line
		key, ok := gc.resolveKey(args[0])
		if !ok {
			return
		}
		cond := strings.TrimSpace(args[1])
		g := Gate{Key: key, Effect: effect, Condition: condText(cond), Function: gc.fn, File: gc.file, Line: line, IndexArg: len(args) > 2}
		full := expandVars(cond, gc.vars)
		if e := condText(full); e != g.Condition {
			g.Expanded = e
		}
		if cond == "true" || cond == "false" {
			g.Constant = cond
		}
		if len(gc.conds) > 0 {
			for _, c := range gc.conds {
				g.ContextFull = append(g.ContextFull, expandVars(c, gc.vars))
			}
			for _, c := range gc.conds {
				g.Context = append(g.Context, truncText(c))
			}
		}
		g.Full = strings.Join(strings.Fields(full), " ")
		g.Drivers = gateDrivers(append([]string{cond, full}, g.Context...)...)
		*gc.gates = append(*gc.gates, g)
		return
	}
	if m := reSetKV.FindStringSubmatch(text); m != nil {
		f := Forced{Key: m[2], Value: condText(m[3]), ValueFull: strings.Join(strings.Fields(m[3]), " "), Function: gc.fn, File: gc.file, Line: line}
		if len(gc.conds) > 0 {
			for _, c := range gc.conds {
				f.ContextFull = append(f.ContextFull, expandVars(c, gc.vars))
			}
			for _, c := range gc.conds {
				f.Context = append(f.Context, truncText(c))
			}
		}
		*gc.forced = append(*gc.forced, f)
		return
	}
	if m := reKeyListDecl.FindStringSubmatch(text); m != nil {
		var vals []string
		for _, l := range stringLiterals(m[2]) {
			vals = append(vals, l.Val)
		}
		gc.lists[m[1]] = vals
		return
	}
	if m := regexp.MustCompile(`(?s)^(\w+)\s*=\s*\{(.*)\}$`).FindStringSubmatch(text); m != nil {
		if _, ok := gc.lists[m[1]]; ok {
			var vals []string
			for _, l := range stringLiterals(m[2]) {
				vals = append(vals, l.Val)
			}
			gc.lists[m[1]] = vals
		}
		return
	}
	if m := reGateVar.FindStringSubmatch(text); m != nil {
		gc.vars[m[1]] = strings.TrimSpace(m[2])
		return
	}
	// plain re-assignment of a known variable
	if m := regexp.MustCompile(`(?s)^(\w+)\s*=\s*(.*)$`).FindStringSubmatch(text); m != nil {
		if _, ok := gc.vars[m[1]]; ok && !reLiteralRHS.MatchString(strings.TrimSpace(m[2])) {
			gc.vars[m[1]] = strings.TrimSpace(m[2])
		}
	}
}

// functionLiterals collects every string literal appearing in the named
// functions (comment-free, dead code removed). It is used only as an
// independent cross-check of the layout extraction.
func (g *GuiParser) functionLiterals(names []string) map[string]bool {
	out := map[string]bool{}
	var walk func(n *Node)
	walk = func(n *Node) {
		for _, l := range stringLiterals(n.Header) {
			out[l.Val] = true
		}
		if n.Kind == nStmt {
			for _, l := range stringLiterals(n.Text) {
				out[l.Val] = true
			}
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	for _, name := range names {
		if body := g.funcs[name]; body != nil {
			walk(body)
		}
	}
	return out
}

// condFull normalizes whitespace without truncating.
func condFull(h string) string { return strings.Join(strings.Fields(h), " ") }

// truncText shortens a normalized condition the way condText does.
func truncText(s string) string {
	if len(s) > 200 {
		return s[:200] + "..."
	}
	return s
}

// reLiteralRHS matches a plain number or bool: assigning one to a local
// variable inside a branch (for example "x = 0") says what the code did to the
// value, not what the variable means in later conditions.
var reLiteralRHS = regexp.MustCompile(`^(?:-?[0-9.]+[fF]?|true|false)$`)
