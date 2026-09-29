package catalog

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestLoadEmbedded(t *testing.T) {
	c, err := Load("7.2.1")
	if err != nil {
		t.Fatal(err)
	}
	if c.Version != "7.2.1" || c.Ref != "v7.2.1" {
		t.Errorf("version=%s ref=%s", c.Version, c.Ref)
	}
	st := c.Stats()
	if st.Options < 600 || st.WithGUI < 400 || st.TooltipHashes < 550 {
		t.Errorf("implausible stats: %+v", st)
	}
	lh, ok := c.Get("layer_height")
	if !ok || lh.ValueType != "float" || lh.Owner != "process" || !lh.HasScope(ScopeObject) || !lh.HasScope(ScopeLayerRange) {
		t.Fatalf("layer_height = %+v", lh)
	}
	if flag, isBool, ok := c.CLIFlag("layer_height"); !ok || flag != "--layer-height" || isBool {
		t.Errorf("CLIFlag(layer_height) = %q %v %v", flag, isBool, ok)
	}
	if _, isBool, ok := c.CLIFlag("enable_support"); !ok || !isBool {
		t.Error("enable_support must be a boolean flag")
	}
	if _, _, ok := c.CLIFlag("print_host"); ok {
		t.Error("print_host is nocli and must have no flag")
	}
	if d, ok := c.Deps("support_threshold_angle"); !ok || d.Empty() {
		t.Error("support_threshold_angle should have dependency sentences")
	}
}

func TestLoadVersionResolution(t *testing.T) {
	if got := Versions(); len(got) == 0 {
		t.Fatal("no embedded versions")
	}
	c, err := Load("7.2.2")
	if err != nil || c.Version != "7.2.1" {
		t.Errorf("7.2.2 should resolve to the 7.2.1 catalog: %v %v", c, err)
	}
	if c2, err := Load(""); err != nil || c2.Version == "" {
		t.Errorf("empty version: %v", err)
	}
	if _, err := Load("6.0.0"); err == nil || !strings.Contains(err.Error(), "available") {
		t.Errorf("unknown version error = %v", err)
	}
}

func TestEmbeddedDataHasNoTooltipTextOrDashes(t *testing.T) {
	for _, v := range Versions() {
		data, err := dataFS.ReadFile("data/catalog-" + v + ".json")
		if err != nil {
			t.Fatal(err)
		}
		s := string(data)
		if strings.Contains(s, `"tooltip"`) {
			t.Errorf("%s: a tooltip field is embedded", v)
		}
		if !utf8.Valid(data) || strings.ContainsRune(s, rune(0x2013)) || strings.ContainsRune(s, rune(0x2014)) {
			t.Errorf("%s: invalid UTF-8 or contains an em or en dash", v)
		}
	}
}

func TestFromJSONRejects(t *testing.T) {
	for name, in := range map[string]string{
		"format":    `{"format":99,"options":[]}`,
		"duplicate": `{"format":1,"options":[{"key":"a"},{"key":"a"}]}`,
		"nokey":     `{"format":1,"options":[{"label":"x"}]}`,
		"hash":      `{"format":1,"options":[{"key":"a","tooltip_hash":"zz"}]}`,
		"json":      `{`,
	} {
		if _, err := FromJSON([]byte(in)); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestWithTextsAndStats(t *testing.T) {
	c := fixture(t)
	if s := c.Stats(); s.TextsAttached || s.Coverage() != 0 || s.TooltipHashes != 3 {
		t.Errorf("before texts: %+v", s)
	}
	if _, ok := c.Tooltip("layer_height"); ok {
		t.Error("tooltip available before WithTexts")
	}
	c2 := c.WithTexts(fakeTexts{1: "Invented layer text. Second sentence.", 3: "Invented support text"})
	if _, ok := c.Tooltip("layer_height"); ok {
		t.Error("WithTexts mutated the original")
	}
	tip, ok := c2.Tooltip("layer_height")
	if !ok || Excerpt(tip) != "Invented layer text." {
		t.Errorf("tooltip = %q %v", tip, ok)
	}
	if _, ok := c2.Tooltip("sparse_infill_density"); ok {
		t.Error("tooltip without wording reported")
	}
	s := c2.Stats()
	if !s.TextsAttached || s.TooltipsFound != 2 || s.TooltipsMissed != 1 || s.Coverage() < 0.66 || s.Coverage() > 0.67 {
		t.Errorf("stats: %+v", s)
	}
	if got := c2.MissingTooltips(); len(got) != 1 || got[0] != "sparse_infill_density" {
		t.Errorf("missing = %v", got)
	}
	if got := c.MissingTooltips(); len(got) != 0 {
		t.Errorf("missing before texts = %v", got)
	}
	if c.WithTexts(nil).Stats().TooltipsFound != 0 {
		t.Error("nil source found tooltips")
	}
}

func TestStatsCounts(t *testing.T) {
	s := fixture(t).Stats()
	if s.Options != 18 || s.ByOwner["process"] != 11 || s.ByOwner["printer"] != 5 || s.ByLevel["develop"] != 1 || s.Vector != 4 || s.Nullable != 1 {
		t.Errorf("stats: %+v", s)
	}
	if s.ByScope["object"] != 9 || s.ByValueType["enum"] != 4 {
		t.Errorf("stats scopes/types: %+v", s)
	}
}

func TestSearch(t *testing.T) {
	c := fixture(t)
	keys := func(hits []Hit) string {
		var k []string
		for _, h := range hits {
			k = append(k, h.Option.Key)
		}
		return strings.Join(k, ",")
	}
	// exact key first, then label word matches
	if got := keys(c.Search("enable_support", Filter{})); got != "enable_support" {
		t.Errorf("exact key: %s", got)
	}
	if got := keys(c.Search("support", Filter{Level: LevelBeginner})); !strings.HasPrefix(got, "enable_support") || !strings.Contains(got, "support_type") {
		t.Errorf("word search: %s", got)
	}
	// every token must match
	if got := keys(c.Search("support angle", Filter{})); got != "support_threshold_angle" {
		t.Errorf("AND tokens: %s", got)
	}
	if got := keys(c.Search("zzz nothing", Filter{})); got != "" {
		t.Errorf("no match: %s", got)
	}
	// levels
	all := len(c.Search("", Filter{Level: LevelAll}))
	adv := len(c.Search("", Filter{Level: LevelAdvanced}))
	beg := len(c.Search("", Filter{Level: LevelBeginner}))
	if !(beg < adv && adv < all) || all != 18 || adv != 17 {
		t.Errorf("levels beginner=%d advanced=%d all=%d", beg, adv, all)
	}
	if got := len(c.Search("", Filter{})); got != all {
		t.Errorf("zero Level must mean all: %d", got)
	}
	for _, h := range c.Search("", Filter{Level: LevelBeginner}) {
		if h.Option.UILevel != "simple" {
			t.Errorf("beginner returned %s (%s)", h.Option.Key, h.Option.UILevel)
		}
	}
	// owner matches owner or preset type; scope; path
	if got := keys(c.Search("", Filter{Owner: "filament"})); got != "nozzle_temperature" {
		t.Errorf("owner filter: %s", got)
	}
	if got := keys(c.Search("", Filter{Scope: ScopePlate})); got != "spiral_mode,curr_bed_type" && got != "curr_bed_type,spiral_mode" {
		t.Errorf("plate scope: %s", got)
	}
	if got := keys(c.Search("", Filter{Scope: ScopePart})); !strings.Contains(got, "sparse_infill_density") || strings.Contains(got, "layer_height") {
		t.Errorf("part scope: %s", got)
	}
	if got := keys(c.Search("", Filter{PathPrefix: "process/support/raft"})); got != "raft_layers" {
		t.Errorf("path prefix (case-insensitive): %s", got)
	}
	if got := keys(c.Search("", Filter{PathPrefix: "process/Qual", Level: LevelAll})); !strings.Contains(got, "layer_height") || !strings.Contains(got, "line_width") || strings.Contains(got, "raft") {
		t.Errorf("path prefix of last segment: %s", got)
	}
	if got := keys(c.Search("", Filter{PathPrefix: "proc/Quality"})); got != "" {
		t.Errorf("non-last segments must match whole: %s", got)
	}
	// enum labels are searchable, tooltips only once loaded
	if got := keys(c.Search("gyroid", Filter{})); got != "sparse_infill_pattern" {
		t.Errorf("enum label search: %s", got)
	}
	if got := keys(c.Search("invented", Filter{})); got != "" {
		t.Errorf("tooltip search before texts: %s", got)
	}
	c2 := c.WithTexts(fakeTexts{1: "Invented layer text"})
	if got := keys(c2.Search("invented", Filter{})); got != "layer_height" {
		t.Errorf("tooltip search after texts: %s", got)
	}
	// stable ranking: repeated calls give the same order; ties keep catalog order
	a, b := keys(c.Search("speed", Filter{})), keys(c.Search("speed", Filter{}))
	if a != b {
		t.Error("unstable ranking")
	}
	tie := c.Search("", Filter{Level: LevelBeginner})
	for i := 1; i < len(tie); i++ {
		if tie[i-1].Option.seq > tie[i].Option.seq {
			t.Error("empty query must keep catalog order")
		}
	}
}

func TestTree(t *testing.T) {
	c := fixture(t)
	root := c.Tree("", 1)
	if root.Kind != "root" || len(root.Children) != 4 || root.Children[0].Name != "process" || root.Children[1].Name != "filament" {
		t.Fatalf("root: %+v", root)
	}
	if root.Children[0].Children != nil || root.Children[0].Options != nil {
		t.Error("maxLevel 1 must not expand tabs")
	}
	proc := c.Tree("process", 2)
	if proc.Kind != "tab" || len(proc.Children) < 4 || proc.Children[0].Name != "Quality" {
		t.Fatalf("process: %+v", proc)
	}
	var support *Node
	for _, p := range proc.Children {
		if p.Name == "Support" {
			support = p
		}
	}
	if support == nil || len(support.Children) != 2 || support.Children[0].Name != "Support" || support.Children[0].Count != 3 {
		t.Fatalf("support page: %+v", support)
	}
	grp := c.Tree("process/Support/Support", 1)
	if grp.Kind != "group" || len(grp.Options) != 3 {
		t.Errorf("group: %+v", grp)
	}
	if got := c.Tree("process/Support/Support", 0); got.Options != nil || got.Count != 3 {
		t.Errorf("maxLevel 0: %+v", got)
	}
	if c.Tree("process/Nope", 1) != nil {
		t.Error("unknown path must be nil")
	}
	// page without groups keeps its options on the page node
	pl := c.Tree("plate/Plate Settings", 1)
	if pl == nil || len(pl.Options) != 1 || pl.Options[0].Key != "curr_bed_type" {
		t.Errorf("plate page: %+v", pl)
	}
	// filter by level drops advanced and develop options and empty groups
	f := c.TreeFiltered("process", 3, Filter{Level: LevelBeginner})
	for _, p := range f.Children {
		for _, g := range p.Children {
			for _, o := range g.Options {
				if o.UILevel != "simple" {
					t.Errorf("filtered tree kept %s", o.Key)
				}
			}
		}
	}
	if f.Count >= proc.Count {
		t.Errorf("filter did not reduce the count: %d vs %d", f.Count, proc.Count)
	}
}

func TestValidate(t *testing.T) {
	c := fixture(t)
	code := func(err error) string {
		var ve *ValidationError
		if errors.As(err, &ve) {
			return ve.Code
		}
		if err == nil {
			return ""
		}
		return "?" + err.Error()
	}
	type tc struct {
		key   string
		value any
		scope Scope
		opts  ValidateOptions
		code  string
		msg   string // substring the message must contain
	}
	cases := []tc{
		{"layer_height", 0.2, ScopePreset, ValidateOptions{}, "", ""},
		{"layer_height", "0.16", ScopePreset, ValidateOptions{}, "", ""},
		{"layer_height", -1, ScopePreset, ValidateOptions{}, CodeRange, "0 to 100"},
		{"layer_height", 101, ScopePreset, ValidateOptions{}, CodeRange, "(mm)"},
		{"layer_height", "thin", ScopePreset, ValidateOptions{}, CodeType, "expects a number"},
		{"layer_height", nil, ScopePreset, ValidateOptions{}, CodeType, "needs a value"},
		{"layer_height", []any{1}, ScopePreset, ValidateOptions{}, CodeType, "single value"},
		{"layer_height", 0.1, ScopeObject, ValidateOptions{}, "", ""},
		{"layer_height", 0.1, ScopePart, ValidateOptions{}, CodeScope, "valid scopes: preset, object, layer_range"},
		{"", 1, ScopePreset, ValidateOptions{}, CodeUnknownKey, ""},
		{"layr_height", 1, ScopePreset, ValidateOptions{}, CodeUnknownKey, "layer_height"},
		{"sparse_infill_density", "15%", ScopePreset, ValidateOptions{}, "", ""},
		{"sparse_infill_density", 150, ScopePreset, ValidateOptions{}, CodeRange, "0 to 100"},
		{"sparse_infill_density", true, ScopePreset, ValidateOptions{}, CodeType, "percentage"},
		{"line_width", "120%", ScopePreset, ValidateOptions{}, "", ""},
		{"line_width", -3, ScopePreset, ValidateOptions{}, CodeRange, "0 to 1000"},
		{"line_width", "-5%", ScopePreset, ValidateOptions{}, CodeRange, "negative"},
		{"enable_support", true, ScopePreset, ValidateOptions{}, "", ""},
		{"enable_support", "on", ScopePreset, ValidateOptions{}, "", ""},
		{"enable_support", 2, ScopePreset, ValidateOptions{}, CodeType, "true or false"},
		{"support_type", "tree_auto", ScopePreset, ValidateOptions{}, "", ""},
		{"support_type", "tree", ScopePreset, ValidateOptions{}, CodeEnum, "normal_auto (Normal (auto)), tree_auto (Tree (auto))"},
		{"support_type", "Tree (auto)", ScopePreset, ValidateOptions{}, CodeEnum, `did you mean "tree_auto"`},
		{"support_type", 3, ScopePreset, ValidateOptions{}, CodeType, "one of the choices"},
		{"support_threshold_angle", 45, ScopePreset, ValidateOptions{}, "", ""},
		{"support_threshold_angle", 45.5, ScopePreset, ValidateOptions{}, CodeType, "whole number"},
		{"support_threshold_angle", 0, ScopePreset, ValidateOptions{}, CodeRange, "1 to 90"},
		{"outer_wall_speed", []any{60, nil}, ScopePreset, ValidateOptions{}, "", ""}, // nullable: unset entry allowed
		{"outer_wall_speed", 60, ScopePreset, ValidateOptions{}, "", ""},             // single value is a list of one
		{"outer_wall_speed", []any{}, ScopePreset, ValidateOptions{}, CodeLength, "at least one value"},
		{"outer_wall_speed", []float64{60, 0}, ScopePreset, ValidateOptions{}, CodeRange, "value 2"},
		{"nozzle_temperature", []any{200, nil}, ScopePreset, ValidateOptions{}, CodeType, "unset entries"},
		{"nozzle_temperature", []int{200, 210}, ScopePreset, ValidateOptions{}, "", ""},
		{"nozzle_temperature", nil, ScopePreset, ValidateOptions{}, CodeType, "list"},
		{"printable_area", []any{"0x0", "260x0", "260x260", "0x260"}, ScopePreset, ValidateOptions{}, "", ""},
		{"printable_area", []any{[]any{0, 0}, []any{260, 0}}, ScopePreset, ValidateOptions{}, "", ""},
		{"printable_area", []any{0, 0}, ScopePreset, ValidateOptions{}, "", ""},
		{"printable_area", []any{"big"}, ScopePreset, ValidateOptions{}, CodeType, "points"},
		{"spiral_mode", true, ScopePlate, ValidateOptions{}, "", ""},
		{"spiral_mode", true, ScopePreset, ValidateOptions{}, "", ""},
		{"curr_bed_type", "Cool Plate", ScopePlate, ValidateOptions{}, "", ""},
		{"curr_bed_type", "Cool Plate", ScopePreset, ValidateOptions{}, CodeScope, "valid scopes: plate"},
		{"machine_max_speed_x", []any{500, 200}, ScopePreset, ValidateOptions{}, "", ""},
		{"machine_max_speed_x", []any{500, 200}, ScopePreset, ValidateOptions{IsCrealityPreset: true}, CodeLocked, "read-only"},
		{"machine_max_speed_x", []any{500, 200}, ScopePreset, ValidateOptions{IsCrealityPreset: true, AllowLocked: true}, "", ""},
		{"thumbnails", "48x48/PNG", ScopePreset, ValidateOptions{IsCrealityPreset: true}, CodeLocked, "hidden"},
		{"thumbnails", 5, ScopePreset, ValidateOptions{}, CodeType, "text"},
		{"gcode_flavor", "klipper", ScopePreset, ValidateOptions{IsCrealityPreset: true}, "", ""},
	}
	for _, x := range cases {
		err := c.Validate(x.key, x.value, x.scope, x.opts)
		if got := code(err); got != x.code {
			t.Errorf("Validate(%s, %#v, %s, %+v): code %q (%v), want %q", x.key, x.value, x.scope, x.opts, got, err, x.code)
			continue
		}
		if x.msg != "" && (err == nil || !strings.Contains(err.Error(), x.msg)) {
			t.Errorf("Validate(%s, %#v): message %q lacks %q", x.key, x.value, err, x.msg)
		}
	}
	// the empty scope means the preset
	if err := c.Validate("layer_height", 0.2, "", ValidateOptions{}); err != nil {
		t.Errorf("empty scope: %v", err)
	}
	// a plate-only key has no preset scope but a per-object override of a preset key is fine
	if err := c.Validate("enable_support", true, ScopeObject, ValidateOptions{IsCrealityPreset: true}); err != nil {
		t.Errorf("object override: %v", err)
	}
	// vendor lock only concerns preset scope: an object override of a locked key is a scope error, not a lock error
	if err := c.Validate("machine_max_speed_x", []any{1}, ScopeObject, ValidateOptions{IsCrealityPreset: true}); code(err) != CodeScope {
		t.Errorf("locked key at object scope: %v", err)
	}
}

func TestDeps(t *testing.T) {
	c := fixture(t)
	d, ok := c.Deps("support_threshold_angle")
	if !ok || len(d.Gates) != 1 || d.Gates[0] != "Editable only when enable_support is on and support_type is an automatic type." {
		t.Errorf("gates = %v", d.Gates)
	}
	if len(d.Drivers) != 2 || d.Drivers[0] != "enable_support" {
		t.Errorf("drivers = %v", d.Drivers)
	}
	d, _ = c.Deps("support_type")
	want := []string{
		"Editable only when enable_support is on or raft_layers is above 0.",
		"Shown only when the printer is not a belt printer.",
	}
	if strings.Join(d.Gates, "|") != strings.Join(want, "|") {
		t.Errorf("gates = %q", d.Gates)
	}
	d, _ = c.Deps("enable_support")
	if len(d.Forced) != 1 || d.Forced[0] != "Set to off automatically when spiral_mode is on and a per-plate override is not being edited." {
		t.Errorf("forced = %q", d.Forced)
	}
	d, _ = c.Deps("sparse_infill_pattern")
	if len(d.Gates) != 1 || d.Gates[0] != "Always hidden by the GUI code." {
		t.Errorf("constant gate = %q", d.Gates)
	}
	if len(d.Restrictions) != 1 || !strings.Contains(d.Restrictions[0], "ai_infill is enabled") || !strings.Contains(d.Restrictions[0], "zig-zag (Rectilinear)") {
		t.Errorf("restrictions = %q", d.Restrictions)
	}
	if got := d.Lines(); len(got) != 2 {
		t.Errorf("Lines = %q", got)
	}
	if d, ok := c.Deps("layer_height"); !ok || !d.Empty() {
		t.Errorf("layer_height should have no dependencies: %+v", d)
	}
	if _, ok := c.Deps("nope"); ok {
		t.Error("unknown key")
	}
}

func TestPhraseNegationAndEnums(t *testing.T) {
	c := fixture(t)
	notAnd := &Cond{K: "not", A: []*Cond{{K: "and", A: []*Cond{
		{K: "opt", Key: "enable_support", Kind: "bool"},
		{K: "cmp", Op: "==", A: []*Cond{{K: "opt", Key: "gcode_flavor", Kind: "enum"}, {K: "enum", V: "klipper"}}},
	}}}}
	got := c.phrase(notAnd, false)
	if got != `enable_support is off or gcode_flavor is not "klipper" (Klipper)` {
		t.Errorf("De Morgan phrase = %q", got)
	}
	mixed := &Cond{K: "and", A: []*Cond{
		{K: "or", A: []*Cond{{K: "opt", Key: "a", Kind: "bool"}, {K: "opt", Key: "b", Kind: "bool"}}},
		{K: "opt", Key: "c", Kind: "int"},
	}}
	if got := c.phrase(mixed, false); got != "(a is on or b is on) and c is not 0" {
		t.Errorf("parenthesised = %q", got)
	}
	if got := c.phrase(&Cond{K: "text", V: "have_raft"}, true); got != "raft_layers is 0" {
		t.Errorf("known text = %q", got)
	}
	if got := c.phrase(&Cond{K: "text", V: "some_var + 1"}, false); got != "(some_var + 1)" {
		t.Errorf("unknown text = %q", got)
	}
}

func TestClosestAndExcerpt(t *testing.T) {
	c := fixture(t)
	near := c.Closest("suport_type", 3)
	if len(near) == 0 || near[0] != "support_type" {
		t.Errorf("closest = %v", near)
	}
	if got := c.Closest("layer", 2); len(got) == 0 || got[0] != "layer_height" {
		t.Errorf("closest by substring = %v", got)
	}
	if Excerpt("One. Two.") != "One." || Excerpt("no stop") != "no stop" || Excerpt("a.b c. d") != "a.b c." {
		t.Error("Excerpt")
	}
	if Excerpt("Line one\nstill one. Next") != "Line one still one." {
		t.Errorf("Excerpt whitespace: %q", Excerpt("Line one\nstill one. Next"))
	}
}

func TestCLIFlagAndGUIPath(t *testing.T) {
	c := fixture(t)
	if f, b, ok := c.CLIFlag("spiral_mode"); !ok || f != "--spiral-mode" || !b {
		t.Errorf("spiral_mode: %q %v %v", f, b, ok)
	}
	if _, _, ok := c.CLIFlag("print_host"); ok {
		t.Error("nocli")
	}
	if _, _, ok := c.CLIFlag("curr_bed_type"); ok {
		t.Error("no flags")
	}
	if _, _, ok := c.CLIFlag("nope"); ok {
		t.Error("unknown")
	}
	o, _ := c.Get("curr_bed_type")
	if o.GUIPath() != "plate/Plate Settings" {
		t.Errorf("GUIPath = %q", o.GUIPath())
	}
	o, _ = c.Get("print_host")
	if o.GUIPath() != "" {
		t.Errorf("GUIPath without GUI = %q", o.GUIPath())
	}
	o, _ = c.Get("machine_max_speed_x")
	if o.Title() != "Maximum speed X" {
		t.Errorf("Title = %q", o.Title())
	}
}

func TestPhraseMembershipAndNestedNegation(t *testing.T) {
	c := fixture(t)
	in := &Cond{K: "in", A: []*Cond{{K: "opt", Key: "sparse_infill_pattern", Kind: "enum"}}, V: []any{"grid", "gyroid"}}
	if got := c.phrase(in, false); got != `sparse_infill_pattern is one of "grid" (Grid), "gyroid" (Gyroid)` {
		t.Errorf("in = %q", got)
	}
	if got := c.phrase(in, true); !strings.Contains(got, "is none of") {
		t.Errorf("not in = %q", got)
	}
	// a negated conjunction inside a conjunction must be parenthesised after De Morgan
	n := &Cond{K: "and", A: []*Cond{
		{K: "opt", Key: "a", Kind: "bool"},
		{K: "not", A: []*Cond{{K: "and", A: []*Cond{{K: "opt", Key: "b", Kind: "bool"}, {K: "opt", Key: "c", Kind: "bool"}}}}},
	}}
	if got := c.phrase(n, false); got != "a is on and (b is off or c is off)" {
		t.Errorf("nested = %q", got)
	}
	if got := c.phrase(&Cond{K: "has", Key: "x"}, false); got != "x is defined" {
		t.Errorf("has = %q", got)
	}
}
