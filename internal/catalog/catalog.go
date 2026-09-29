// Package catalog is the machine-readable inventory of every Creality Print
// setting: type, default, limits, enum choices, where the GUI shows it, who may
// override it, what enables it, and its command-line flag. The data is
// extracted from the application source by tools/catalog-gen and embedded here
// (data/catalog-<version>.json). It holds no tooltip text: each setting carries
// only the hash of its tooltip message id, and WithTexts attaches the wording
// read from the installed application (package motext).
package catalog

import (
	"embed"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

//go:embed data/catalog-*.json
var dataFS embed.FS

// SupportedFormat is the slim catalog layout version this package reads.
const SupportedFormat = 1

// Scope is where a setting value is written.
type Scope string

const (
	ScopePreset     Scope = "preset"      // the process, filament or printer preset (or project global value)
	ScopeObject     Scope = "object"      // per-object override
	ScopePart       Scope = "part"        // per-part or modifier override
	ScopeLayerRange Scope = "layer_range" // per height-range override
	ScopePlate      Scope = "plate"       // per-plate override
)

// Level selects how many settings are shown, following the application's
// simple, advanced and develop modes.
type Level string

const (
	LevelBeginner Level = "beginner" // ui_level simple
	LevelAdvanced Level = "advanced" // simple and advanced
	LevelAll      Level = "all"      // adds develop; the zero value means all
)

func levelRank(uiLevel string) int {
	switch uiLevel {
	case "advanced":
		return 1
	case "develop":
		return 2
	}
	return 0
}

func (l Level) maxRank() int {
	switch l {
	case LevelBeginner:
		return 0
	case LevelAdvanced:
		return 1
	}
	return 2
}

// Enum lists the choices of an enum setting. Labels is empty or parallel to Values.
type Enum struct {
	Values []string `json:"values"`
	Labels []string `json:"labels,omitempty"`
}

// Label returns the display label of a value ("" when unknown).
func (e *Enum) Label(value string) string {
	if e == nil {
		return ""
	}
	for i, v := range e.Values {
		if v == value && i < len(e.Labels) {
			return e.Labels[i]
		}
	}
	return ""
}

// GUI is the first place the application shows the setting.
type GUI struct {
	Tab   string `json:"tab"`
	Page  string `json:"page"`
	Group string `json:"group"`
}

// Cond is one node of a condition tree (see tools/catalog-gen cond.go).
type Cond struct {
	K    string  `json:"k"`
	Op   string  `json:"op,omitempty"`
	A    []*Cond `json:"a,omitempty"`
	Key  string  `json:"key,omitempty"`
	Kind string  `json:"kind,omitempty"`
	T    string  `json:"t,omitempty"`
	Name string  `json:"name,omitempty"`
	V    any     `json:"v,omitempty"`
}

// Gate is a rule that shows/hides the setting's row (Effect "line") or
// enables/disables its input (Effect "field").
type Gate struct {
	Effect   string   `json:"effect"`
	Constant string   `json:"constant,omitempty"`
	When     *Cond    `json:"when,omitempty"`
	Context  []*Cond  `json:"context,omitempty"`
	Drivers  []string `json:"drivers,omitempty"`
}

// Forced is code that overwrites the setting's value.
type Forced struct {
	Set  string  `json:"set"`
	When []*Cond `json:"when,omitempty"`
}

// Restriction limits the enum choices offered while a condition holds.
type Restriction struct {
	When   string   `json:"when"`
	Values []string `json:"values"`
}

// Option is one setting.
type Option struct {
	Key         string        `json:"key"`
	Owner       string        `json:"owner"`
	PresetTypes []string      `json:"preset_types"`
	ValueType   string        `json:"value_type"`
	IsVector    bool          `json:"is_vector"`
	Nullable    bool          `json:"nullable"`
	Default     any           `json:"default"`
	Min         *float64      `json:"min,omitempty"`
	Max         *float64      `json:"max,omitempty"`
	Enum        *Enum         `json:"enum,omitempty"`
	Label       string        `json:"label,omitempty"`
	FullLabel   string        `json:"full_label,omitempty"`
	Sidetext    string        `json:"sidetext,omitempty"`
	Category    string        `json:"category,omitempty"`
	UILevel     string        `json:"ui_level"`
	GUI         *GUI          `json:"gui,omitempty"`
	Scopes      []string      `json:"scopes,omitempty"`
	GatedBy     []Gate        `json:"gated_by,omitempty"`
	ForcedBy    []Forced      `json:"forced_by,omitempty"`
	EnumRestr   []Restriction `json:"gui_enum_restrictions,omitempty"`
	VendorLock  string        `json:"creality_vendor_policy,omitempty"`
	CLIFlags    []string      `json:"cli_flags,omitempty"`
	NoCLI       bool          `json:"nocli,omitempty"`
	TooltipHash string        `json:"tooltip_hash,omitempty"`

	hash uint64 // parsed TooltipHash
	seq  int    // position in the embedded order (GUI order, then the rest)
}

// GUIPath is "tab/page/group" ("" when the setting has no GUI line).
func (o *Option) GUIPath() string {
	if o.GUI == nil {
		return ""
	}
	if o.GUI.Group == "" {
		return o.GUI.Tab + "/" + o.GUI.Page
	}
	return o.GUI.Tab + "/" + o.GUI.Page + "/" + o.GUI.Group
}

// HasScope reports whether the setting can be overridden at s (ScopePreset is
// always true for settings that belong to a preset or the project).
func (o *Option) HasScope(s Scope) bool {
	if s == ScopePreset {
		return len(o.PresetTypes) > 0 || o.Owner == "project"
	}
	for _, x := range o.Scopes {
		if x == string(s) {
			return true
		}
	}
	return false
}

// Title is the full label when there is one, else the label, else the key.
func (o *Option) Title() string {
	switch {
	case o.FullLabel != "":
		return o.FullLabel
	case o.Label != "":
		return o.Label
	}
	return o.Key
}

// Catalog is an immutable set of options (WithTexts returns a copy).
type Catalog struct {
	Version string
	Ref     string
	Commit  string

	opts  []*Option
	byKey map[string]*Option
	texts map[uint64]string // tooltip wording by hash, when attached
	loadd bool              // WithTexts was applied
}

type file struct {
	Format int `json:"format"`
	Source struct {
		Ref    string `json:"ref"`
		Commit string `json:"commit"`
	} `json:"source"`
	Options []*Option `json:"options"`
}

// Versions lists the embedded catalog versions, ascending.
func Versions() []string {
	ents, _ := dataFS.ReadDir("data")
	var out []string
	for _, e := range ents {
		n := e.Name()
		if strings.HasPrefix(n, "catalog-") && strings.HasSuffix(n, ".json") {
			out = append(out, strings.TrimSuffix(strings.TrimPrefix(n, "catalog-"), ".json"))
		}
	}
	sort.Slice(out, func(i, j int) bool { return versionLess(out[i], out[j]) })
	return out
}

func versionParts(v string) []int {
	var out []int
	for _, p := range strings.Split(v, ".") {
		n, _ := strconv.Atoi(p)
		out = append(out, n)
	}
	return out
}

func versionLess(a, b string) bool {
	pa, pb := versionParts(a), versionParts(b)
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		if x != y {
			return x < y
		}
	}
	return false
}

// Load returns the embedded catalog for an application version. An empty
// version selects the newest embedded one. A version with no catalog of its
// own uses the newest embedded catalog of the same major.minor that is not
// newer (the installed 7.2.2 is described by the 7.2.1 catalog); Catalog.Version
// tells which one was used.
func Load(version string) (*Catalog, error) {
	all := Versions()
	if len(all) == 0 {
		return nil, fmt.Errorf("catalog: no embedded catalog")
	}
	chosen := ""
	switch {
	case version == "":
		chosen = all[len(all)-1]
	default:
		for _, v := range all {
			if v == version {
				chosen = v
			}
		}
		if chosen == "" {
			want := versionParts(version)
			for _, v := range all {
				got := versionParts(v)
				if len(want) >= 2 && len(got) >= 2 && want[0] == got[0] && want[1] == got[1] && !versionLess(version, v) {
					chosen = v
				}
			}
		}
		if chosen == "" {
			return nil, fmt.Errorf("catalog: no settings catalog for version %s (available: %s)", version, strings.Join(all, ", "))
		}
	}
	data, err := dataFS.ReadFile("data/catalog-" + chosen + ".json")
	if err != nil {
		return nil, err
	}
	c, err := FromJSON(data)
	if err != nil {
		return nil, fmt.Errorf("catalog %s: %w", chosen, err)
	}
	c.Version = chosen
	return c, nil
}

// FromJSON builds a catalog from slim JSON (used by Load and by tests).
func FromJSON(data []byte) (*Catalog, error) {
	var f file
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	if f.Format != SupportedFormat {
		return nil, fmt.Errorf("unsupported slim catalog format %d (this build reads %d)", f.Format, SupportedFormat)
	}
	c := &Catalog{Ref: f.Source.Ref, Commit: f.Source.Commit, opts: f.Options, byKey: make(map[string]*Option, len(f.Options))}
	for i, o := range c.opts {
		if o.Key == "" {
			return nil, fmt.Errorf("option %d has no key", i)
		}
		if _, dup := c.byKey[o.Key]; dup {
			return nil, fmt.Errorf("duplicate key %q", o.Key)
		}
		o.seq = i
		if o.TooltipHash != "" {
			h, err := strconv.ParseUint(o.TooltipHash, 16, 64)
			if err != nil {
				return nil, fmt.Errorf("option %s: bad tooltip_hash %q", o.Key, o.TooltipHash)
			}
			o.hash = h
		}
		c.byKey[o.Key] = o
	}
	return c, nil
}

// TextSource supplies wording by message id hash (motext.Index implements it).
type TextSource interface {
	Text(hash uint64) (string, bool)
}

// WithTexts returns a copy of the catalog with tooltips attached from ts.
func (c *Catalog) WithTexts(ts TextSource) *Catalog {
	n := *c
	n.texts = map[uint64]string{}
	n.loadd = true
	if ts != nil {
		for _, o := range c.opts {
			if o.hash == 0 {
				continue
			}
			if s, ok := ts.Text(o.hash); ok {
				n.texts[o.hash] = s
			}
		}
	}
	return &n
}

// Get returns the setting with the given key.
func (c *Catalog) Get(key string) (*Option, bool) {
	o, ok := c.byKey[key]
	return o, ok
}

// Options returns every setting in catalog order (GUI order first).
func (c *Catalog) Options() []*Option { return c.opts }

// Tooltip returns the installed application's description of a setting.
func (c *Catalog) Tooltip(key string) (string, bool) {
	o, ok := c.byKey[key]
	if !ok || o.hash == 0 {
		return "", false
	}
	s, ok := c.texts[o.hash]
	return s, ok
}

// Excerpt returns the first sentence of a tooltip on one line.
func Excerpt(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	for i := 0; i < len(text); i++ {
		if text[i] == '.' && (i+1 == len(text) || text[i+1] == ' ') {
			return text[:i+1]
		}
	}
	return text
}

// Closest returns up to n keys that look most like key (for "did you mean").
func (c *Catalog) Closest(key string, n int) []string {
	type cand struct {
		key   string
		score int
	}
	q := strings.ToLower(key)
	var cs []cand
	for _, o := range c.opts {
		k := strings.ToLower(o.Key)
		s := 0
		switch {
		case strings.Contains(k, q) || strings.Contains(q, k):
			s = 1000 - abs(len(k)-len(q))
			if strings.HasPrefix(k, q) {
				s += 50
			}
		default:
			s = 100 - editDistance(q, k)*3
			// shared word bonus
			for _, w := range strings.Split(q, "_") {
				if w != "" && strings.Contains(k, w) {
					s += 15
				}
			}
		}
		if s > 0 {
			cs = append(cs, cand{o.Key, s})
		}
	}
	sort.SliceStable(cs, func(i, j int) bool { return cs[i].score > cs[j].score })
	var out []string
	for i := 0; i < len(cs) && i < n; i++ {
		out = append(out, cs[i].key)
	}
	return out
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func editDistance(a, b string) int {
	if len(a) > 64 || len(b) > 64 {
		return abs(len(a) - len(b))
	}
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

// CLIFlag returns the command-line flag that sets the setting ("--layer-height"),
// whether it is a boolean flag, and whether it has a flag at all (false for
// settings marked nocli). Boolean means the CLI never reads a value token after
// the flag (Config.cpp read_cli: neither coBool nor coBools takes one), so the
// value must be attached as --flag=1, or --flag=1,0,1 for the per-slot vector
// booleans; isBool is true for both.
func (c *Catalog) CLIFlag(key string) (flag string, isBool bool, ok bool) {
	o, found := c.byKey[key]
	if !found || o.NoCLI || len(o.CLIFlags) == 0 {
		return "", false, false
	}
	flag = o.CLIFlags[0]
	for _, f := range o.CLIFlags {
		if strings.HasPrefix(f, "--") {
			flag = f
			break
		}
	}
	return flag, o.ValueType == "bool", true
}

// UnknownKeys returns, sorted and without duplicates, the keys the catalog does
// not define. It is the drift check between the catalog (built from one source
// version) and the presets of the installed application: a key present in the
// installed presets but unknown here is a setting this catalog cannot describe
// or validate.
func (c *Catalog) UnknownKeys(keys []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, k := range keys {
		if _, ok := c.byKey[k]; !ok && !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// Stats summarises the catalog.
type Stats struct {
	Version        string
	Ref            string
	Options        int
	ByOwner        map[string]int
	ByValueType    map[string]int
	ByLevel        map[string]int
	Vector         int
	Nullable       int
	WithGUI        int
	ByScope        map[string]int
	TooltipHashes  int  // settings that have a tooltip (hash)
	TextsAttached  bool // WithTexts was applied
	TooltipsFound  int  // hashes resolved to wording
	TooltipsMissed int  // hashes with no wording in the installed application's catalogs
}

// Coverage is found/total of tooltips (0 when none or texts not attached).
func (s Stats) Coverage() float64 {
	if !s.TextsAttached || s.TooltipHashes == 0 {
		return 0
	}
	return float64(s.TooltipsFound) / float64(s.TooltipHashes)
}

// Stats counts the catalog and, when texts are attached, tooltip coverage.
func (c *Catalog) Stats() Stats {
	s := Stats{Version: c.Version, Ref: c.Ref, Options: len(c.opts), ByOwner: map[string]int{}, ByValueType: map[string]int{},
		ByLevel: map[string]int{}, ByScope: map[string]int{}, TextsAttached: c.loadd}
	for _, o := range c.opts {
		s.ByOwner[o.Owner]++
		s.ByValueType[o.ValueType]++
		s.ByLevel[o.UILevel]++
		if o.IsVector {
			s.Vector++
		}
		if o.Nullable {
			s.Nullable++
		}
		if o.GUI != nil {
			s.WithGUI++
		}
		for _, sc := range o.Scopes {
			s.ByScope[sc]++
		}
		if o.hash != 0 {
			s.TooltipHashes++
			if c.loadd {
				if _, ok := c.texts[o.hash]; ok {
					s.TooltipsFound++
				} else {
					s.TooltipsMissed++
				}
			}
		}
	}
	return s
}

// MissingTooltips lists the keys whose tooltip wording was not found (empty
// unless WithTexts was applied). Useful to explain gaps, for example settings
// the application never translates.
func (c *Catalog) MissingTooltips() []string {
	var out []string
	if !c.loadd {
		return out
	}
	for _, o := range c.opts {
		if o.hash != 0 {
			if _, ok := c.texts[o.hash]; !ok {
				out = append(out, o.Key)
			}
		}
	}
	return out
}

// IsVector reports whether the setting holds one value per extruder or nozzle
// variant in this catalog version (false for unknown keys). The answer differs
// between versions: 43 process and printer settings such as outer_wall_speed
// are single values in 7.2 and nullable lists in 7.3.
func (c *Catalog) IsVector(key string) bool {
	o, ok := c.byKey[key]
	return ok && o.IsVector
}

// Nullable reports whether entries of the setting may be unset (meaning
// "inherit"); false for unknown keys.
func (c *Catalog) Nullable(key string) bool {
	o, ok := c.byKey[key]
	return ok && o.Nullable
}

// Expand adapts a caller's value to the shape this catalog version stores: for
// a vector setting a single value, or a list of one, becomes a list of n copies
// (n is the number of extruders or nozzle variants, at least 1); a longer list
// is returned as it is. For a scalar setting the value is returned unchanged.
// Validate accepts a single value for a vector setting as a list of one, so
// Expand is for callers that must write the full-length list.
func (c *Catalog) Expand(key string, value any, n int) (any, error) {
	o, ok := c.byKey[key]
	if !ok {
		return nil, &ValidationError{Key: key, Code: CodeUnknownKey, Message: fmt.Sprintf("unknown setting %q", key)}
	}
	if !o.IsVector {
		return value, nil
	}
	if n < 1 {
		n = 1
	}
	elems, isList := asSlice(value)
	switch {
	case value == nil:
		return nil, fail(key, CodeType, "%s expects a value to repeat for each of %d entries, got nothing", key, n)
	case !isList:
		elems = []any{value}
	case len(elems) != 1:
		return value, nil
	}
	out := make([]any, n)
	for i := range out {
		out[i] = elems[0]
	}
	return out, nil
}
