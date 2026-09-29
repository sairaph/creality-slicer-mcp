package catalog

import (
	"strings"
	"testing"
)

func TestCLIFlagBoolCoversVectorBools(t *testing.T) {
	for _, v := range Versions() {
		t.Run(v, func(t *testing.T) { checkCLIFlagBoolCoversVectorBools(t, mustLoad(t, v)) })
	}
}

func checkCLIFlagBoolCoversVectorBools(t *testing.T, c *Catalog) {
	// the CLI reads no value token after a coBool or a coBools flag, so every
	// bool-typed setting with a flag must report isBool, vector or not
	vectorBools := 0
	for _, o := range c.Options() {
		flag, isBool, ok := c.CLIFlag(o.Key)
		if o.ValueType != "bool" {
			if ok && isBool {
				t.Errorf("%s (%s) reports isBool", o.Key, o.ValueType)
			}
			continue
		}
		if !ok {
			continue // nocli
		}
		if !isBool || !strings.HasPrefix(flag, "--") {
			t.Errorf("%s: CLIFlag = %q %v", o.Key, flag, isBool)
		}
		if o.IsVector {
			vectorBools++
		}
	}
	if vectorBools < 15 {
		t.Errorf("only %d vector bool settings with a flag; the check is not exercising them", vectorBools)
	}
	if flag, isBool, ok := c.CLIFlag("filament_is_support"); !ok || !isBool || flag != "--filament-is-support" {
		t.Errorf("filament_is_support: %q %v %v", flag, isBool, ok)
	}
	// fixture: a vector bool (none) is covered by the real data above; scalar bool stays bool
	if _, isBool, ok := fixture(t).CLIFlag("enable_support"); !ok || !isBool {
		t.Error("scalar bool")
	}
}

func TestUnknownKeys(t *testing.T) {
	c := fixture(t)
	got := c.UnknownKeys([]string{"layer_height", "zzz_new", "aaa_new", "zzz_new", "enable_support"})
	if len(got) != 2 || got[0] != "aaa_new" || got[1] != "zzz_new" {
		t.Errorf("UnknownKeys = %q", got)
	}
	if got := c.UnknownKeys(nil); got != nil {
		t.Errorf("UnknownKeys(nil) = %q", got)
	}
	if got := c.UnknownKeys([]string{"layer_height"}); got != nil {
		t.Errorf("all known = %q", got)
	}
}

func TestNoSLAOptionsInEmbeddedCatalog(t *testing.T) {
	for _, v := range Versions() {
		t.Run(v, func(t *testing.T) { checkNoSLAOptionsInEmbeddedCatalog(t, mustLoad(t, v)) })
	}
}

func checkNoSLAOptionsInEmbeddedCatalog(t *testing.T, c *Catalog) {
	for _, o := range c.Options() {
		if strings.HasPrefix(o.Owner, "sla_") {
			t.Errorf("SLA option %s in the FDM catalog", o.Key)
		}
		for _, p := range o.PresetTypes {
			if strings.HasPrefix(p, "sla_") {
				t.Errorf("%s lists SLA preset type %s", o.Key, p)
			}
		}
	}
	for _, h := range c.Search("layer", Filter{}) {
		if strings.HasPrefix(h.Option.Owner, "sla_") {
			t.Errorf("search returned SLA option %s", h.Option.Key)
		}
	}
	if _, ok := c.Get("pad_enable"); ok {
		t.Error("pad_enable (SLA) must not be in the catalog")
	}
}

// walkConds visits every condition node of an option.
func walkConds(o *Option, visit func(n *Cond)) {
	var walk func(n *Cond)
	walk = func(n *Cond) {
		if n == nil {
			return
		}
		visit(n)
		for _, a := range n.A {
			walk(a)
		}
	}
	for _, g := range o.GatedBy {
		walk(g.When)
		for _, x := range g.Context {
			walk(x)
		}
	}
	for _, f := range o.ForcedBy {
		for _, x := range f.When {
			walk(x)
		}
	}
}

func isIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		ok := r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (i > 0 && r >= '0' && r <= '9')
		if !ok {
			return false
		}
	}
	return true
}

func TestEmbeddedConditionsHoldNoCodeFragments(t *testing.T) {
	for _, v := range Versions() {
		t.Run(v, func(t *testing.T) { checkEmbeddedConditionsHoldNoCodeFragments(t, mustLoad(t, v)) })
	}
}

func checkEmbeddedConditionsHoldNoCodeFragments(t *testing.T, c *Catalog) {
	for _, o := range c.Options() {
		walkConds(o, func(n *Cond) {
			if n.K == "text" {
				if s, _ := n.V.(string); !isIdent(s) {
					t.Errorf("%s: text node with a code fragment %q", o.Key, s)
				}
			}
		})
		for _, f := range o.ForcedBy {
			if strings.ContainsAny(f.Set, "?()<>") || strings.Contains(f.Set, "ConfigOption") || strings.Contains(f.Set, "->") {
				t.Errorf("%s: forced value %q is not a plain literal", o.Key, f.Set)
			}
		}
		if d, ok := c.Deps(o.Key); ok {
			for _, l := range d.Lines() {
				if strings.Contains(l, "->") || strings.Contains(l, "ConfigOption") || strings.Contains(l, "static_cast") || strings.Contains(l, "::") {
					t.Errorf("%s: dependency sentence shows code: %s", o.Key, l)
				}
			}
		}
	}
}

func TestUnparsedConditionsArePhrasedNotShown(t *testing.T) {
	c := fixture(t)
	only := &Cond{K: "unparsed"}
	if got := c.phrase(only, false); got != "a condition the app computes in code holds" {
		t.Errorf("unparsed = %q", got)
	}
	mixed := &Cond{K: "and", A: []*Cond{{K: "opt", Key: "a", Kind: "bool"}, {K: "unparsed"}}}
	if got := c.phrase(mixed, false); got != "a is on" {
		t.Errorf("and with an unparsed part = %q", got)
	}
	if got := c.joinConds([]*Cond{{K: "unparsed"}, {K: "opt", Key: "b", Kind: "bool"}}); got != "b is on" {
		t.Errorf("joinConds = %q", got)
	}
	if got := c.gateSentence(Gate{Effect: "line", When: only}); got != "Shown only when a condition the app computes in code holds." {
		t.Errorf("gateSentence = %q", got)
	}
	lt := &Cond{K: "cmp", Op: "<", A: []*Cond{{K: "opt", Key: "layer_height", Kind: "float"}, {K: "text", V: "EPSILON"}}}
	if got := c.phrase(lt, false); got != "layer_height is below zero" {
		t.Errorf("EPSILON = %q", got)
	}
}
