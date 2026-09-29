package mcpserver

import (
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/google/jsonschema-go/jsonschema"
)

// typeSchemas maps custom Go types to the schema they take. It is empty until
// a tool needs an enum type or a free-form object.
var typeSchemas = map[reflect.Type]*jsonschema.Schema{}

// inputSchema infers the schema of T, applying the custom types above and
// the given property defaults (JSON literals). An optional enum property
// accepts only its listed values: the inferred "null" alternative is dropped,
// since null is not one of them.
func inputSchema[T any](defaults map[string]string) *jsonschema.Schema {
	s, err := jsonschema.For[T](&jsonschema.ForOptions{TypeSchemas: typeSchemas})
	if err != nil {
		panic(err)
	}
	for _, prop := range s.Properties {
		if len(prop.Enum) > 0 {
			dropNullAlt(prop)
		}
	}
	for name, value := range defaults {
		prop, ok := s.Properties[name]
		if !ok {
			panic(fmt.Sprintf("inputSchema: no property %q", name))
		}
		prop.Default = json.RawMessage(value)
	}
	return s
}

// withRange sets an inclusive minimum and maximum on the named numeric
// properties of s, which struct tags cannot express.
func withRange(s *jsonschema.Schema, lo, hi float64, names ...string) *jsonschema.Schema {
	for _, name := range names {
		prop, ok := s.Properties[name]
		if !ok {
			panic(fmt.Sprintf("withRange: no property %q", name))
		}
		prop.Minimum, prop.Maximum = jsonschema.Ptr(lo), jsonschema.Ptr(hi)
	}
	return s
}

// withPositiveMax requires the named numeric property of s to be greater
// than 0 and at most hi.
func withPositiveMax(s *jsonschema.Schema, name string, hi float64) *jsonschema.Schema {
	prop, ok := s.Properties[name]
	if !ok {
		panic(fmt.Sprintf("withPositiveMax: no property %q", name))
	}
	prop.ExclusiveMinimum, prop.Maximum = jsonschema.Ptr(0.0), jsonschema.Ptr(hi)
	return s
}

// withEnum restricts the named property of s to values: a string property
// directly, an array property through its items. As in inputSchema, the
// inferred "null" alternative of an optional property is dropped, since null
// is not one of the values.
func withEnum(s *jsonschema.Schema, name string, values ...string) *jsonschema.Schema {
	prop, ok := s.Properties[name]
	if !ok {
		panic(fmt.Sprintf("withEnum: no property %q", name))
	}
	enum := make([]any, len(values))
	for i, v := range values {
		enum[i] = v
	}
	dropNullAlt(prop)
	target := prop
	if prop.Type == "array" {
		if prop.Items == nil {
			panic(fmt.Sprintf("withEnum: array property %q has no items schema", name))
		}
		target = prop.Items
	}
	target.Enum = enum
	return s
}

func boolOr(b *bool, def bool) bool {
	if b == nil {
		return def
	}
	return *b
}

// mergeDefaults combines several property-default maps (as inputSchema
// takes) into one, later maps overriding earlier ones for the same key.
func mergeDefaults(maps ...map[string]string) map[string]string {
	out := map[string]string{}
	for _, m := range maps {
		for k, v := range m {
			out[k] = v
		}
	}
	return out
}

// withItemRange requires the named array property of s to have between lo
// and hi items, inclusive. Struct tags cannot express this.
func withItemRange(s *jsonschema.Schema, name string, lo, hi int) *jsonschema.Schema {
	prop, ok := s.Properties[name]
	if !ok {
		panic(fmt.Sprintf("withItemRange: no property %q", name))
	}
	prop.MinItems, prop.MaxItems = jsonschema.Ptr(lo), jsonschema.Ptr(hi)
	dropNullAlt(prop)
	return s
}

// withMinItems requires the named array property of s to have at least
// atLeast items, dropping the inferred null alternative an optional-looking
// slice property gets (as withEnum drops it for an enum), since the property
// is only ever a JSON array.
func withMinItems(s *jsonschema.Schema, name string, atLeast int) *jsonschema.Schema {
	prop, ok := s.Properties[name]
	if !ok {
		panic(fmt.Sprintf("withMinItems: no property %q", name))
	}
	prop.MinItems = jsonschema.Ptr(atLeast)
	dropNullAlt(prop)
	return s
}

// dropNullAlt removes prop's inferred "null" alternative, a no-op when it has
// none: a slice or enum property built from a Go pointer type infers
// ["null", "<type>"], but the property is only ever present with a real
// value, never JSON null, once its minimum length or its enum is set.
func dropNullAlt(prop *jsonschema.Schema) {
	if len(prop.Types) == 2 && prop.Types[0] == "null" {
		prop.Type, prop.Types = prop.Types[1], nil
	}
}
