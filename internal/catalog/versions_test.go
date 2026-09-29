package catalog

import (
	"errors"
	"strings"
	"testing"
)

func TestVersionsAndLoadByMajorMinor(t *testing.T) {
	vs := Versions()
	if len(vs) != 2 || vs[0] != "7.2.1" || vs[1] != "7.3.0" {
		t.Fatalf("Versions = %v", vs)
	}
	cases := map[string]string{
		"7.3.0": "7.3.0", "7.3.1": "7.3.0", "7.3.9.1234": "7.3.0",
		"7.2.1": "7.2.1", "7.2.2": "7.2.1", "7.2.2.5483": "7.2.1",
		"": "7.3.0", // newest
	}
	for in, want := range cases {
		c, err := Load(in)
		if err != nil || c.Version != want {
			t.Errorf("Load(%q) = %v, %v; want version %s", in, c, err, want)
		}
	}
	for _, in := range []string{"7.1.0", "7.4.0", "8.0.0", "6.3.0"} {
		_, err := Load(in)
		if err == nil || !strings.Contains(err.Error(), "7.2.1, 7.3.0") {
			t.Errorf("Load(%q) error = %v; want a list of the available versions", in, err)
		}
	}
	c73, _ := Load("7.3.0")
	if c73.Ref != "v7.3.0" || c73.Stats().Options < 650 || c73.Stats().Options <= mustLoad(t, "7.2.1").Stats().Options {
		t.Errorf("7.3.0 stats: %+v", c73.Stats())
	}
}

func mustLoad(t *testing.T, v string) *Catalog {
	t.Helper()
	c, err := Load(v)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// The 43 process and printer settings that became nullable per-nozzle-variant
// lists in 7.3 must be validated as lists there and as single values in 7.2.
func TestVectorShapeFollowsCatalogVersion(t *testing.T) {
	c72, c73 := mustLoad(t, "7.2.1"), mustLoad(t, "7.3.0")
	converted := 0
	for _, o72 := range c72.Options() {
		o73, ok := c73.Get(o72.Key)
		if !ok {
			t.Errorf("%s exists in 7.2.1 but not in 7.3.0", o72.Key)
			continue
		}
		if !o72.IsVector && o73.IsVector {
			converted++
			if !o73.Nullable {
				t.Errorf("%s became a vector in 7.3 but is not nullable", o72.Key)
			}
			if c72.IsVector(o72.Key) || !c73.IsVector(o72.Key) || !c73.Nullable(o72.Key) {
				t.Errorf("IsVector/Nullable disagree for %s", o72.Key)
			}
		}
	}
	if converted != 43 {
		t.Errorf("%d scalar-to-vector conversions, expected 43", converted)
	}

	const key = "outer_wall_speed"
	if err := c72.Validate(key, 60, ScopePreset, ValidateOptions{}); err != nil {
		t.Errorf("7.2 scalar: %v", err)
	}
	if err := c72.Validate(key, []any{60, 70}, ScopePreset, ValidateOptions{}); err == nil || !strings.Contains(err.Error(), "single value") {
		t.Errorf("7.2 must refuse a list: %v", err)
	}
	if err := c73.Validate(key, []any{60, nil}, ScopePreset, ValidateOptions{}); err != nil {
		t.Errorf("7.3 list with an unset entry: %v", err)
	}
	if err := c73.Validate(key, 60, ScopePreset, ValidateOptions{}); err != nil {
		t.Errorf("7.3 single value is a list of one: %v", err)
	}
	if err := c73.Validate(key, []any{}, ScopePreset, ValidateOptions{}); err == nil {
		t.Error("7.3 empty list accepted")
	}
	var ve *ValidationError
	if err := c73.Validate(key, []any{60, -5}, ScopePreset, ValidateOptions{}); !errors.As(err, &ve) || ve.Code != CodeRange || !strings.Contains(err.Error(), "value 2") {
		t.Errorf("7.3 per-entry range: %v", err)
	}
	// percent-or-float vector: line width entries
	if err := c73.Validate("line_width", []any{"120%", 0.4, nil}, ScopePreset, ValidateOptions{}); err != nil {
		t.Errorf("7.3 line_width list: %v", err)
	}
}

func TestExpand(t *testing.T) {
	c73, c72 := mustLoad(t, "7.3.0"), mustLoad(t, "7.2.1")
	got, err := c73.Expand("outer_wall_speed", 60, 3)
	if err != nil {
		t.Fatal(err)
	}
	l, ok := got.([]any)
	if !ok || len(l) != 3 || l[0] != 60 || l[2] != 60 {
		t.Errorf("scalar expanded = %#v", got)
	}
	if got, _ := c73.Expand("outer_wall_speed", []any{60}, 2); len(got.([]any)) != 2 {
		t.Errorf("one-element list expanded = %#v", got)
	}
	keep := []any{60, 70}
	if got, _ := c73.Expand("outer_wall_speed", keep, 4); len(got.([]any)) != 2 {
		t.Errorf("a longer list must be kept: %#v", got)
	}
	if got, _ := c73.Expand("outer_wall_speed", 60, 0); len(got.([]any)) != 1 {
		t.Errorf("n < 1 means 1: %#v", got)
	}
	if got, err := c72.Expand("outer_wall_speed", 60, 3); err != nil || got != 60 {
		t.Errorf("7.2 scalar setting must be unchanged: %#v %v", got, err)
	}
	if _, err := c73.Expand("outer_wall_speed", nil, 2); err == nil {
		t.Error("nil value")
	}
	if _, err := c73.Expand("no_such_key", 1, 2); err == nil {
		t.Error("unknown key")
	}
	if c73.IsVector("no_such_key") || c73.Nullable("no_such_key") {
		t.Error("unknown keys are neither vector nor nullable")
	}
}

func TestEmbedded73Facts(t *testing.T) {
	c := mustLoad(t, "7.3.0")
	for _, k := range []string{"zaa_enabled", "enable_mixed_color_sublayer", "cell_type", "prime_tower_corner_rib_length", "print_nozzle_variant"} {
		if _, ok := c.Get(k); !ok {
			t.Errorf("%s missing from the 7.3.0 catalog", k)
		}
	}
	if o, _ := c.Get("sparse_infill_pattern"); o == nil || !containsStr(o.Enum.Values, "field") {
		t.Error("sparse_infill_pattern lacks the 7.3 value field")
	}
	if _, ok := mustLoad(t, "7.2.1").Get("zaa_enabled"); ok {
		t.Error("zaa_enabled must not exist in 7.2.1")
	}
	if flag, isBool, ok := c.CLIFlag("zaa_enabled"); !ok || flag != "--zaa-enabled" || !isBool {
		t.Errorf("zaa_enabled flag: %q %v %v", flag, isBool, ok)
	}
}

func containsStr(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
