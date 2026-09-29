package catalog

import (
	"fmt"
	"regexp"
	"strings"
)

// Deps describes, in plain sentences, what the Creality Print GUI does around
// one setting: when it is shown or editable, when it is overwritten, and which
// choices it offers.
type Deps struct {
	Key          string
	Gates        []string // "Shown only when ...", "Editable only when ..."
	Forced       []string // "Set to X automatically when ..."
	Restrictions []string // "While ..., only these choices are offered: ..."
	Drivers      []string // settings whose value influences this one
}

// Empty reports whether the setting has no dependency information.
func (d Deps) Empty() bool {
	return len(d.Gates) == 0 && len(d.Forced) == 0 && len(d.Restrictions) == 0
}

// Lines returns all sentences in reading order.
func (d Deps) Lines() []string {
	out := append([]string(nil), d.Gates...)
	out = append(out, d.Forced...)
	return append(out, d.Restrictions...)
}

// Deps humanises gated_by, forced_by and enum restrictions of a setting.
func (c *Catalog) Deps(key string) (Deps, bool) {
	o, ok := c.byKey[key]
	if !ok {
		return Deps{}, false
	}
	d := Deps{Key: key}
	seen := map[string]bool{}
	add := func(dst *[]string, s string) {
		if s != "" && !seen[s] {
			seen[s] = true
			*dst = append(*dst, s)
		}
	}
	drivers := map[string]bool{}
	for _, g := range o.GatedBy {
		for _, dr := range g.Drivers {
			if _, isKey := c.byKey[dr]; isKey && dr != key && !drivers[dr] {
				drivers[dr] = true
				d.Drivers = append(d.Drivers, dr)
			}
		}
		add(&d.Gates, c.gateSentence(g))
	}
	for _, f := range o.ForcedBy {
		s := "Set to " + f.Set + " automatically"
		if conds := c.joinConds(f.When); conds != "" {
			s += " when " + conds
		}
		add(&d.Forced, s+".")
	}
	for _, r := range o.EnumRestr {
		add(&d.Restrictions, c.restrictionSentence(o, r))
	}
	return d, true
}

func (c *Catalog) gateSentence(g Gate) string {
	line := g.Effect == "line"
	switch g.Constant {
	case "false":
		if line {
			return "Always hidden by the GUI code."
		}
		return "Always disabled (greyed out) by the GUI code."
	case "true":
		return "" // unconditional show or enable: nothing to tell
	}
	if g.When == nil {
		return ""
	}
	verb := "Editable"
	if line {
		verb = "Shown"
	}
	s := verb + " only when " + c.phrase(g.When, false)
	if ctx := c.joinConds(g.Context); ctx != "" {
		s += " (this rule applies while " + ctx + ")"
	}
	return s + "."
}

func (c *Catalog) joinConds(cs []*Cond) string {
	var parts []string
	for _, n := range cs {
		if n == nil {
			continue
		}
		if n.K == "unparsed" {
			continue // computed in code: nothing to say
		}
		p := c.phrase(n, false)
		if n.K == "or" && len(cs) > 1 {
			p = "(" + p + ")"
		}
		parts = append(parts, p)
	}
	return strings.Join(parts, " and ")
}

func (c *Catalog) restrictionSentence(o *Option, r Restriction) string {
	vals := make([]string, len(r.Values))
	for i, v := range r.Values {
		vals[i] = v
		if l := o.Enum.Label(v); l != "" && l != v {
			vals[i] = fmt.Sprintf("%s (%s)", v, l)
		}
	}
	list := strings.Join(vals, ", ")
	if len(vals) > 12 {
		list = fmt.Sprintf("%d of the %d values", len(vals), len(o.enumValues()))
	}
	return fmt.Sprintf("While %s, the GUI offers only: %s.", r.When, list)
}

func (o *Option) enumValues() []string {
	if o.Enum == nil {
		return nil
	}
	return o.Enum.Values
}

// ---- condition phrasing -----------------------------------------------------

var capPhrases = map[string][2]string{
	"is_BBL_Printer":                      {"the printer vendor is Bambu Lab", "the printer vendor is not Bambu Lab"},
	"is_BBL_printer":                      {"the printer vendor is Bambu Lab", "the printer vendor is not Bambu Lab"},
	"is_bbl_vendor":                       {"the printer vendor is Bambu Lab", "the printer vendor is not Bambu Lab"},
	"is_CX_Printer":                       {"the printer vendor is Creality", "the printer vendor is not Creality"},
	"is_CX_printer":                       {"the printer vendor is Creality", "the printer vendor is not Creality"},
	"is_cx_vendor":                        {"the printer vendor is Creality", "the printer vendor is not Creality"},
	"is_creality_vendor":                  {"the printer vendor is Creality", "the printer vendor is not Creality"},
	"is_belt_machine":                     {"the printer is a belt printer", "the printer is not a belt printer"},
	"bSEMM":                               {"single_extruder_multi_material is on", "single_extruder_multi_material is off"},
	"use_creality_tower":                  {"the printer uses the Creality multi-color tower logic", "the printer does not use the Creality multi-color tower logic"},
	"can_flush_into_skeleton_for_printer": {"the printer is a K2-series printer", "the printer is not a K2-series printer"},
	"is_k2_series_printer":                {"the printer is a K2-series printer", "the printer is not a K2-series printer"},
	"support_multi_bed_types":             {"the printer supports several bed types", "the printer does not support several bed types"},
	"is_marlin_flavor":                    {"the G-code flavor is Marlin, Marlin 2, Klipper or RepRapFirmware", "the G-code flavor is none of Marlin, Marlin 2, Klipper, RepRapFirmware"},
	"is_global_config":                    {"the global preset is being edited", "a per-object or per-plate override is being edited"},
	"is_object_config":                    {"a per-object override is being edited", "a per-object override is not being edited"},
	"is_plate_config":                     {"a per-plate override is being edited", "a per-plate override is not being edited"},
}

var textPhrases = map[string][2]string{
	"have_prime_tower":        {"enable_prime_tower is on", "enable_prime_tower is off"},
	"have_raft":               {"raft_layers is above 0", "raft_layers is 0"},
	"have_arachne":            {"wall_generator is \"Arachne\"", "wall_generator is \"Classic\""},
	"is_zag":                  {"sparse_infill_pattern is a zag pattern", "sparse_infill_pattern is not a zag pattern"},
	"is_cone":                 {"prime_tower_enhance_type is cone", "prime_tower_enhance_type is not cone"},
	"use_firmware_retraction": {"use_firmware_retraction is on", "use_firmware_retraction is off"},
	"gcflavor":                {"the G-code flavor", "the G-code flavor"},
	"gcfKlipper":              {"Klipper", "Klipper"},
	"gcfMarlinLegacy":         {"Marlin (legacy)", "Marlin (legacy)"},
	"otherwise":               {"the previous condition does not hold", "the previous condition holds"},
	"EPSILON":                 {"zero", "zero"},
}

var callPhrases = map[string][2]string{
	"is_auto":                     {"%s is an automatic type", "%s is not an automatic type"},
	"is_tree":                     {"%s is a tree type", "%s is not a tree type"},
	"has_effective_sparse_infill": {"sparse infill is in use", "sparse infill is not in use"},
}

var reIdent = regexp.MustCompile(`^[A-Za-z_]\w*$`)

// noun renders the operand of a comparison or call as a name (keys stay keys).
func (c *Catalog) noun(n *Cond) string {
	switch n.K {
	case "opt":
		return n.Key
	case "num":
		if f, ok := n.V.(float64); ok {
			return fmtNum(f)
		}
	case "text":
		s, _ := n.V.(string)
		if p, ok := textPhrases[s]; ok {
			return p[0]
		}
		if reIdent.MatchString(s) {
			return strings.ReplaceAll(s, "_", " ")
		}
		return "(" + s + ")"
	case "enum":
		if s, ok := n.V.(string); ok {
			return fmt.Sprintf("%q", s)
		}
	case "and", "or", "not", "cmp", "cap", "call", "bool", "in", "has", "unparsed":
		return c.phrase(n, false)
	}
	return "(" + n.K + ")"
}

func opPhrase(op string, neg bool) string {
	if neg {
		switch op {
		case "==":
			op = "!="
		case "!=":
			op = "=="
		case ">":
			op = "<="
		case "<":
			op = ">="
		case ">=":
			op = "<"
		case "<=":
			op = ">"
		}
	}
	switch op {
	case "==":
		return "is"
	case "!=":
		return "is not"
	case ">":
		return "is above"
	case "<":
		return "is below"
	case ">=":
		return "is at least"
	case "<=":
		return "is at most"
	}
	return op
}

func (c *Catalog) phrase(n *Cond, neg bool) string {
	if n == nil {
		return ""
	}
	switch n.K {
	case "opt":
		switch n.Kind {
		case "bool":
			if neg {
				return n.Key + " is off"
			}
			return n.Key + " is on"
		case "int", "float":
			if neg {
				return n.Key + " is 0"
			}
			return n.Key + " is not 0"
		}
		if neg {
			return n.Key + " is not set"
		}
		return n.Key + " is set"
	case "cmp":
		if len(n.A) != 2 {
			return "(unreadable comparison)"
		}
		l, r := n.A[0], n.A[1]
		right := c.noun(r)
		if r.K == "enum" {
			v, _ := r.V.(string)
			right = fmt.Sprintf("%q", v)
			if l.K == "opt" {
				if o, ok := c.byKey[l.Key]; ok {
					if lab := o.Enum.Label(v); lab != "" && lab != v {
						right = fmt.Sprintf("%q (%s)", v, lab)
					}
				}
			}
		}
		return fmt.Sprintf("%s %s %s", c.noun(l), opPhrase(n.Op, neg), right)
	case "and", "or":
		op := n.K
		if neg { // De Morgan
			if op == "and" {
				op = "or"
			} else {
				op = "and"
			}
		}
		parts := make([]string, 0, len(n.A))
		operands := n.A
		if kept := withoutUnparsed(operands); len(kept) > 0 {
			operands = kept // a part computed in code is left out when the rest can be stated
		}
		for _, a := range operands {
			p := c.phrase(a, neg)
			if inner := effectiveOp(a, neg); inner != "" && inner != op {
				p = "(" + p + ")"
			}
			parts = append(parts, p)
		}
		return strings.Join(parts, " "+op+" ")
	case "not":
		if len(n.A) == 1 {
			return c.phrase(n.A[0], !neg)
		}
	case "cap":
		if p, ok := capPhrases[n.Name]; ok {
			if neg {
				return p[1]
			}
			return p[0]
		}
		if neg {
			return "not: " + strings.ReplaceAll(n.Name, "_", " ")
		}
		return strings.ReplaceAll(n.Name, "_", " ")
	case "call":
		arg := ""
		if len(n.A) > 0 {
			arg = c.noun(n.A[0])
		}
		if p, ok := callPhrases[n.Name]; ok {
			s := p[0]
			if neg {
				s = p[1]
			}
			if strings.Contains(s, "%s") {
				return fmt.Sprintf(s, arg)
			}
			return s
		}
		s := strings.ReplaceAll(n.Name, "_", " ")
		if arg != "" {
			s += " of " + arg
		}
		if neg {
			return "not: " + s
		}
		return s
	case "unparsed":
		return "a condition the app computes in code holds"
	case "in":
		return c.inPhrase(n, neg)
	case "has":
		if neg {
			return n.Key + " is not defined"
		}
		return n.Key + " is defined"
	case "bool":
		b, _ := n.V.(bool)
		if b != neg {
			return "always"
		}
		return "never"
	case "text":
		s, _ := n.V.(string)
		if p, ok := textPhrases[s]; ok {
			if neg {
				return p[1]
			}
			return p[0]
		}
		if reIdent.MatchString(s) {
			s = strings.ReplaceAll(s, "_", " ")
		} else {
			s = "(" + s + ")"
		}
		if neg {
			return "not: " + s
		}
		return s
	}
	return c.noun(n)
}

func effectiveOp(n *Cond, neg bool) string {
	if n.K == "not" && len(n.A) == 1 {
		return effectiveOp(n.A[0], !neg)
	}
	if n.K != "and" && n.K != "or" {
		return ""
	}
	if !neg {
		return n.K
	}
	if n.K == "and" {
		return "or"
	}
	return "and"
}

// inPhrase renders a membership test ("x is one of a, b").
func (c *Catalog) inPhrase(n *Cond, neg bool) string {
	subject := "the value"
	var opt *Option
	if len(n.A) == 1 {
		subject = c.noun(n.A[0])
		if n.A[0].K == "opt" {
			opt = c.byKey[n.A[0].Key]
		}
	}
	vals, _ := n.V.([]any)
	parts := make([]string, 0, len(vals))
	for _, v := range vals {
		s, _ := v.(string)
		p := fmt.Sprintf("%q", s)
		if opt != nil {
			if l := opt.Enum.Label(s); l != "" && l != s {
				p = fmt.Sprintf("%q (%s)", s, l)
			}
		}
		parts = append(parts, p)
	}
	verb := "is one of"
	if neg {
		verb = "is none of"
	}
	return subject + " " + verb + " " + strings.Join(parts, ", ")
}

func withoutUnparsed(cs []*Cond) []*Cond {
	var out []*Cond
	for _, c := range cs {
		if c != nil && c.K != "unparsed" {
			out = append(out, c)
		}
	}
	return out
}
