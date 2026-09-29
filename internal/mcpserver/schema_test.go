package mcpserver

import (
	"encoding/json"
	"testing"
)

type schemaInput struct {
	Name    string   `json:"name"`
	Limit   *int     `json:"limit,omitempty"`
	Timeout *float64 `json:"timeout,omitempty"`
	Kind    *string  `json:"kind,omitempty"`
	Tags    []string `json:"tags,omitempty"`
	Items   []string `json:"items,omitempty"`
}

func TestInputSchemaHelpers(t *testing.T) {
	s := inputSchema[schemaInput](mergeDefaults(map[string]string{"limit": "5"}, map[string]string{"limit": "7", "kind": `"a"`}))
	if got := string(s.Properties["limit"].Default); got != "7" {
		t.Errorf("limit default = %s, later maps must win", got)
	}
	if got := string(s.Properties["kind"].Default); got != `"a"` {
		t.Errorf("kind default = %s", got)
	}

	withRange(s, 1, 10, "limit")
	if p := s.Properties["limit"]; *p.Minimum != 1 || *p.Maximum != 10 {
		t.Errorf("limit range = %v..%v", *p.Minimum, *p.Maximum)
	}
	withPositiveMax(s, "timeout", 1800)
	if p := s.Properties["timeout"]; *p.ExclusiveMinimum != 0 || *p.Maximum != 1800 {
		t.Errorf("timeout range = >%v..%v", *p.ExclusiveMinimum, *p.Maximum)
	}
	withEnum(s, "kind", "a", "b")
	if p := s.Properties["kind"]; len(p.Enum) != 2 || p.Type != "string" || len(p.Types) != 0 {
		t.Errorf("kind = type %q types %v enum %v", p.Type, p.Types, p.Enum)
	}
	withEnum(s, "tags", "x", "y")
	if p := s.Properties["tags"]; len(p.Items.Enum) != 2 {
		t.Errorf("tags items enum = %v", p.Items.Enum)
	}
	withItemRange(s, "items", 1, 3)
	if p := s.Properties["items"]; *p.MinItems != 1 || *p.MaxItems != 3 || p.Type != "array" {
		t.Errorf("items = min %v max %v type %q", *p.MinItems, *p.MaxItems, p.Type)
	}
	withMinItems(s, "tags", 2)
	if p := s.Properties["tags"]; *p.MinItems != 2 {
		t.Errorf("tags min items = %v", *p.MinItems)
	}
	if _, err := json.Marshal(s); err != nil {
		t.Errorf("schema does not marshal: %v", err)
	}
}

func TestSchemaHelpersPanicOnUnknownProperty(t *testing.T) {
	s := inputSchema[schemaInput](nil)
	for name, f := range map[string]func(){
		"defaults":        func() { inputSchema[schemaInput](map[string]string{"nope": "1"}) },
		"withRange":       func() { withRange(s, 0, 1, "nope") },
		"withPositiveMax": func() { withPositiveMax(s, "nope", 1) },
		"withEnum":        func() { withEnum(s, "nope", "a") },
		"withItemRange":   func() { withItemRange(s, "nope", 0, 1) },
		"withMinItems":    func() { withMinItems(s, "nope", 1) },
	} {
		t.Run(name, func(t *testing.T) { mustPanic(t, f) })
	}
}

func TestBoolOr(t *testing.T) {
	yes, no := true, false
	if !boolOr(nil, true) || boolOr(nil, false) || !boolOr(&yes, false) || boolOr(&no, true) {
		t.Error("boolOr is wrong")
	}
}
