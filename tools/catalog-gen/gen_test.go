package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This module cannot import the main module's internal/userhome/testhome, so
// TestMain does the same job locally: no test here touches a per-user
// directory, but HOME and friends still point at a throwaway directory.
func TestMain(m *testing.M) {
	root, err := os.MkdirTemp("", "cgt")
	if err != nil {
		os.Stderr.WriteString("TestMain: " + err.Error() + "\n")
		os.Exit(1)
	}
	for _, k := range []string{"HOME", "USERPROFILE"} {
		os.Setenv(k, root)
	}
	os.Setenv("APPDATA", filepath.Join(root, "AppData", "Roaming"))
	os.Setenv("LOCALAPPDATA", filepath.Join(root, "AppData", "Local"))
	code := m.Run()
	os.RemoveAll(root)
	os.Exit(code)
}

func TestTooltipHashKnownVectors(t *testing.T) {
	// FNV-1a 64 reference values.
	if got := TooltipHash(""); got != "cbf29ce484222325" {
		t.Errorf("hash of empty string = %s", got)
	}
	if got := TooltipHash("a"); got != "af63dc4c8601ec8c" {
		t.Errorf("hash of a = %s", got)
	}
	if got := TooltipHash("foobar"); got != "85944171f73967e8" {
		t.Errorf("hash of foobar = %s", got)
	}
}

// snippet mimics the shapes PrintConfig.cpp uses; the strings are invented.
const snippet = `
namespace Slic3r {
static t_config_enum_values s_keys_map_Mode {
    { "one", mOne },
    { "two", int(Mode::mTwo) }
};
CONFIG_OPTION_ENUM_DEFINE_STATIC_MAPS(Mode)

void PrintConfigDef::init_fff_params()
{
    ConfigOptionDef* def;
    def = this->add("widget_size", coFloatOrPercent);
    def->label = L("Widget size");
    def->category = L("Quality");
    def->tooltip = L("First line of\n"
                     "the \"tip\" text");
    def->sidetext = L("mm or %");
    def->min = 0.5;
    def->max = 100;
    def->mode = comAdvanced;
    def->set_default_value(new ConfigOptionFloatOrPercent(20, true));

    def = this->add("widget_mode", coEnum);
    def->label = L("Widget mode");
    def->enum_keys_map = &ConfigOptionEnum<Mode>::get_enum_values();
    def->enum_values.push_back("one");
    def->enum_values.push_back("two");
    def->enum_labels.push_back(L("One"));
    def->enum_labels.push_back(L("Two"));
    def->set_default_value(new ConfigOptionEnum<Mode>(Mode::mTwo));

    def = this->add("widget_list", coInts);
    def->label = L("List");
    def->set_default_value(new ConfigOptionInts{ 1, 2 });
}
}
`

func parseSnippet(t *testing.T) *DefParser {
	t.Helper()
	ct, err := loadClean("snippet.cpp", []byte(snippet))
	if err != nil {
		t.Fatal(err)
	}
	tree := buildTree(ct)
	dp := newDefParser(ct, parseEnumMaps(ct, tree))
	dp.walkDefs(tree)
	if len(dp.warnings) != 0 {
		t.Fatalf("warnings: %v", dp.warnings)
	}
	return dp
}

func TestDefinitionParsing(t *testing.T) {
	dp := parseSnippet(t)
	if len(dp.order) != 3 {
		t.Fatalf("parsed %d options, want 3", len(dp.order))
	}
	w := dp.classes["print"]["widget_size"]
	if w.Tooltip != "First line of\nthe \"tip\" text" {
		t.Errorf("tooltip msgid = %q", w.Tooltip)
	}
	if w.Mode != "advanced" || w.Sidetext != "mm or %" || w.Category != "Quality" {
		t.Errorf("props: %+v", w)
	}
	if w.Min == nil || *w.Min != 0.5 || w.Max == nil || *w.Max != 100 {
		t.Errorf("min/max: %v %v", w.Min, w.Max)
	}
	m, ok := w.Default.(map[string]interface{})
	if !ok || m["percent"] != true || m["value"] != float64(20) {
		t.Errorf("default = %#v", w.Default)
	}
	e := dp.classes["print"]["widget_mode"]
	if e.Default != "two" || len(e.EnumValues) != 2 || e.EnumLabels[1] != "Two" {
		t.Errorf("enum: default=%v values=%v labels=%v", e.Default, e.EnumValues, e.EnumLabels)
	}
	l := dp.classes["print"]["widget_list"]
	if list, ok := l.Default.([]interface{}); !ok || len(list) != 2 {
		t.Errorf("list default = %#v", l.Default)
	}
}

func TestSlimHasNoTooltipText(t *testing.T) {
	dp := parseSnippet(t)
	b := &Build{Ref: "vX", Commit: "abc", Defs: dp, EnumMaps: dp.enumMaps}
	var cat Catalog
	for _, o := range dp.order {
		cat.Options = append(cat.Options, Entry{Key: o.Key, DefClass: "print", Tooltip: o.Tooltip, Label: o.Label, ValueType: valueTypeName(o.TypeName), UILevel: "simple"})
	}
	sf := b.slim(&cat)
	if len(sf.Options) != 3 {
		t.Fatalf("slim options = %d", len(sf.Options))
	}
	if sf.Options[0].TooltipHash != TooltipHash("First line of\nthe \"tip\" text") {
		t.Errorf("tooltip hash does not cover the unescaped concatenated msgid")
	}
	if sf.Options[1].TooltipHash != "" {
		t.Errorf("option without tooltip has a hash")
	}
}

func TestExpandVarsSkipsStringLiterals(t *testing.T) {
	got := expandVars(`support_type == 1 && config->opt_int("support_type") > 0`, map[string]string{"support_type": "config->opt_enum<T>(\"support_type\")"})
	if strings.Count(got, `("support_type")`) != 2 || strings.Contains(got, `"(config`) {
		t.Errorf("expandVars corrupted a literal: %s", got)
	}
}

func TestParseCond(t *testing.T) {
	maps := map[string]*EnumMap{"Style": {Name: "Style", Pairs: [][2]string{{"grid", "sGrid"}, {"snug", "sSnug"}}}}
	c := parseCond(`(config->opt_bool("enable_support") || config->opt_int("raft_layers") > 0) && !is_belt_machine`, maps)
	if c.K != "and" || len(c.A) != 2 {
		t.Fatalf("top = %+v", c)
	}
	or := c.A[0]
	if or.K != "or" || or.A[0].K != "opt" || or.A[0].Key != "enable_support" || or.A[0].Kind != "bool" {
		t.Errorf("or = %+v", or)
	}
	if cmp := or.A[1]; cmp.K != "cmp" || cmp.Op != ">" || cmp.A[0].Key != "raft_layers" || cmp.A[1].K != "num" {
		t.Errorf("cmp = %+v", cmp)
	}
	if n := c.A[1]; n.K != "not" || n.A[0].K != "cap" || n.A[0].Name != "is_belt_machine" {
		t.Errorf("not = %+v", n)
	}
	e := parseCond(`config->opt_enum<Style>("support_style") == Style::sSnug`, maps)
	if e.K != "cmp" || e.A[1].K != "enum" || e.A[1].V != "snug" {
		t.Errorf("enum cmp = %+v %+v", e, e.A)
	}
	// a template argument list must not be read as a comparison
	o := parseCond(`config->option<ConfigOptionEnum<Style>>("support_style")->value != Style::sGrid`, maps)
	if o.K != "cmp" || o.Op != "!=" || o.A[0].Key != "support_style" || o.A[1].V != "grid" {
		t.Errorf("option<> cmp = %+v %+v", o, o.A)
	}
	// unknown atoms survive as text
	x := parseCond(`some_local_flag`, maps)
	if x.K != "text" {
		t.Errorf("unknown atom = %+v", x)
	}
}

func TestForcedValue(t *testing.T) {
	maps := map[string]*EnumMap{"Style": {Name: "Style", Pairs: [][2]string{{"grid", "sGrid"}}}}
	cases := map[string]string{
		"new ConfigOptionBool(false)":               "off",
		"new ConfigOptionFloat(0.2)":                "0.2",
		"new ConfigOptionInt(1)":                    "1",
		"new ConfigOptionEnum<Style>(Style::sGrid)": "grid",
		"new ConfigOptionFloatOrPercent(0, true)":   "0%",
	}
	for in, want := range cases {
		if got, ok := forcedValue(in, maps); !ok || got != want {
			t.Errorf("forcedValue(%s) = %q, %v, want %q", in, got, ok, want)
		}
	}
	// values computed in code are not expressible
	for _, in := range []string{
		"has_ai ? new ConfigOptionEnum<Style>(Style::sGrid) : x",
		"new ConfigOptionFloat(max_lh)",
		"static_cast<ConfigOptionBools*>(m_config->option(\"wipe\")->clone())",
		"opt",
	} {
		if got, ok := forcedValue(in, maps); ok {
			t.Errorf("forcedValue(%s) = %q, want not expressible", in, got)
		}
	}
}

func TestPreprocessorDeadCode(t *testing.T) {
	src := "int a;\n#if 0\nint dead;\n#endif\n#ifdef SLIC3R_ENABLE_TIME_ANALYTICS_EXPORT\nint tagged;\n#endif\nint b;\n"
	ct, err := loadClean("x.cpp", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	text := string(ct.Buf)
	if strings.Contains(text, "dead") {
		t.Error("dead code kept")
	}
	if !strings.Contains(text, "tagged") || ct.condAt(6) != macroTimeAnalytics {
		t.Errorf("macro-guarded code lost or untagged: cond=%q", ct.condAt(6))
	}
}

func TestParseCondMembershipHasAndDialogs(t *testing.T) {
	maps := map[string]*EnumMap{"Pat": {Name: "Pat", Pairs: [][2]string{{"grid", "pGrid"}, {"gyroid", "pGyroid"}, {"line", "pLine"}}}}
	c := parseCond(`std::set<Pat>{pGrid, pGyroid}.count((config->opt_enum<Pat>("sparse_infill_pattern"))) != 0`, maps)
	if c.K != "in" || len(c.A) != 1 || c.A[0].Key != "sparse_infill_pattern" {
		t.Fatalf("in = %+v", c)
	}
	if vals, _ := c.V.([]interface{}); len(vals) != 2 || vals[0] != "grid" || vals[1] != "gyroid" {
		t.Errorf("in values = %#v", c.V)
	}
	n := parseCond(`std::set<Pat>{pLine}.count(config->opt_enum<Pat>("sparse_infill_pattern")) == 0`, maps)
	if n.K != "not" || n.A[0].K != "in" {
		t.Errorf("== 0 = %+v", n)
	}
	// has(k) && opt(k) reads as opt(k)
	h := parseCond(`config->has("flush_into_infill") && config->opt_bool("flush_into_infill")`, maps)
	if h.K != "opt" || h.Key != "flush_into_infill" {
		t.Errorf("has && opt = %+v", h)
	}
	ctx := contextConds([]string{"if (answer == wxID_YES)", "if (m_active_page->title() == L(\"Cooling\"))", "if (config->opt_bool(\"spiral_mode\"))", "else"}, maps)
	if len(ctx) != 2 || ctx[0].K != "opt" || ctx[1].K != "text" {
		t.Errorf("context = %+v", ctx)
	}
}

func TestSanitizeTurnsCodeFragmentsIntoUnparsed(t *testing.T) {
	c := sanitize(parseCond("config->opt_bool(\"a\") && have_raft && (m_x->y() > 3)", nil))
	if c.K != "and" || len(c.A) != 3 || c.A[0].K != "opt" || c.A[1].K != "text" || c.A[2].K != "unparsed" {
		t.Fatalf("sanitize = %+v", c.A)
	}
	cmp := sanitize(parseCond("foo->bar == 3", nil))
	if cmp.K != "unparsed" {
		t.Errorf("comparison with a code operand = %+v", cmp)
	}
}
