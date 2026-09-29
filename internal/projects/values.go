package projects

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/sairaph/creality-slicer-mcp/internal/catalog"
	"github.com/sairaph/creality-slicer-mcp/internal/threemf"
)

// val is a project_settings.config value: a string or a list of strings.
type val = threemf.Value

func sval(s string) val        { return threemf.String(s) }
func lval(items ...string) val { return threemf.List(items...) }
func sameVal(a, b val) bool    { return valEqual(a, b) }
func valEqual(a, b val) bool {
	if a.IsList != b.IsList {
		return false
	}
	if !a.IsList {
		return a.Str == b.Str && string(a.Raw) == string(b.Raw)
	}
	if len(a.List) != len(b.List) {
		return false
	}
	for i := range a.List {
		if a.List[i] != b.List[i] {
			return false
		}
	}
	return true
}

// valElems returns the elements of a value (a scalar is one element).
func valElems(v val) []string {
	if v.IsList {
		return v.List
	}
	return []string{v.Str}
}

// formatNumber prints a number the way the presets do: no exponent, no
// trailing zeros.
func formatNumber(f float64) string {
	if f == math.Trunc(f) && math.Abs(f) < 1e15 {
		return strconv.FormatFloat(f, 'f', 0, 64)
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// formatDefault renders one catalog default element in project format:
// booleans 1 and 0, numbers plain, percentages with the sign, points "XxY".
func formatDefault(valueType string, v any) string {
	switch t := v.(type) {
	case nil:
		if valueType == "string" || valueType == "enum" {
			return ""
		}
		return "0"
	case bool:
		if t {
			return "1"
		}
		return "0"
	case float64:
		s := formatNumber(t)
		if valueType == "percent" {
			s += "%"
		}
		return s
	case int:
		return strconv.Itoa(t)
	case string:
		return t
	case map[string]any:
		if val, ok := t["value"]; ok {
			s := formatDefault("float", val)
			if pct, _ := t["percent"].(bool); pct {
				s += "%"
			}
			return s
		}
	case []any:
		if len(t) == 2 && valueType == "point" {
			return formatDefault("float", t[0]) + "x" + formatDefault("float", t[1])
		}
		parts := make([]string, len(t))
		for i, e := range t {
			parts[i] = formatDefault(valueType, e)
		}
		return strings.Join(parts, ",")
	}
	return fmt.Sprint(v)
}

// defaultValue is the catalog default of an option in project format.
func defaultValue(o *catalog.Option) val {
	if !o.IsVector {
		if list, ok := o.Default.([]any); ok && o.ValueType != "point" {
			// A default given as a list for a scalar option: its first element.
			if len(list) == 0 {
				return sval("")
			}
			return sval(formatDefault(o.ValueType, list[0]))
		}
		return sval(formatDefault(o.ValueType, o.Default))
	}
	list, ok := o.Default.([]any)
	if !ok {
		if o.Default == nil {
			return lval()
		}
		list = []any{o.Default}
	}
	if o.ValueType == "point" && len(list) == 2 {
		if _, first := list[0].([]any); !first {
			list = []any{list} // one point given flat
		}
	}
	items := make([]string, len(list))
	for i, e := range list {
		if e == nil && o.Nullable {
			items[i] = "nil"
			continue
		}
		items[i] = formatDefault(o.ValueType, e)
	}
	return lval(items...)
}

// shape converts a value read from a preset to the shape the option has in a
// project. Presets write vectors as one string the way the command line does
// ("0,0" for numbers, "a;b" with C style quoting for strings, "0x0,260x0" for
// points) and the project stores a list; percentages lose their sign in some
// presets ("70") but not in the project ("70%").
func shape(o *catalog.Option, v val) val {
	if v.Raw != nil {
		return v
	}
	if o.IsVector {
		var items []string
		if v.IsList {
			items = v.List
		} else {
			s := unquoteEmpty(v.Str)
			if s == "" {
				return lval()
			}
			items = deserializeVector(o.ValueType, s)
		}
		out := make([]string, len(items))
		for i, e := range items {
			out[i] = fixElement(o, e)
		}
		return lval(out...)
	}
	if v.IsList {
		if len(v.List) == 0 {
			return sval("")
		}
		return sval(fixElement(o, unquoteEmpty(v.List[0])))
	}
	return sval(fixElement(o, unquoteEmpty(v.Str)))
}

// fixElement adds the percent sign a percent setting carries in a project.
func fixElement(o *catalog.Option, e string) string {
	if o.ValueType == "percent" && e != "" && e != "nil" && !strings.HasSuffix(e, "%") {
		return e + "%"
	}
	return e
}

// deserializeVector splits a vector written as one string.
func deserializeVector(valueType, s string) []string {
	if valueType == "string" {
		return splitQuoted(s, ';')
	}
	return strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' })
}

// splitQuoted splits on sep; an element may be wrapped in double quotes with
// the C escapes (backslash n, t, backslash and quote) inside.
func splitQuoted(s string, sep byte) []string {
	var out []string
	i := 0
	for i <= len(s) {
		if i < len(s) && s[i] == '"' {
			var b strings.Builder
			j := i + 1
			for j < len(s) && s[j] != '"' {
				if s[j] == '\\' && j+1 < len(s) {
					j++
					switch s[j] {
					case 'n':
						b.WriteByte('\n')
					case 't':
						b.WriteByte('\t')
					default:
						b.WriteByte(s[j])
					}
				} else {
					b.WriteByte(s[j])
				}
				j++
			}
			out = append(out, b.String())
			i = j + 1
			if i < len(s) && s[i] == sep {
				i++
			} else {
				break
			}
			continue
		}
		j := strings.IndexByte(s[i:], sep)
		if j < 0 {
			out = append(out, s[i:])
			break
		}
		out = append(out, s[i:i+j])
		i += j + 1
	}
	return out
}

// unquoteEmpty maps the literal two character string "" (how some Creality
// presets write an empty value) to the empty string.
func unquoteEmpty(s string) string {
	if s == `""` {
		return ""
	}
	return s
}

// normalizeThumbnails writes the printer's thumbnails value the way the app
// keeps it in a project: "96x96/PNG, 300x300/PNG".
func normalizeThumbnails(s string) string {
	fields := strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' || r == ' ' })
	for i, f := range fields {
		if !strings.Contains(f, "/") {
			fields[i] = f + "/PNG"
		}
	}
	return strings.Join(fields, ", ")
}

// anyToVal converts a value given by a caller (string, number, bool, list) to
// project format for an option: numbers plain, booleans 1 and 0, lists to
// string lists. null is not handled here.
func anyToVal(o *catalog.Option, v any) val {
	elem := func(e any) string {
		switch t := e.(type) {
		case bool:
			if t {
				return "1"
			}
			return "0"
		case float64:
			return formatNumber(t)
		case float32:
			return formatNumber(float64(t))
		case int:
			return strconv.Itoa(t)
		case int64:
			return strconv.FormatInt(t, 10)
		case string:
			if o.ValueType == "bool" {
				switch strings.ToLower(strings.TrimSpace(t)) {
				case "true", "yes", "on":
					return "1"
				case "false", "no", "off":
					return "0"
				}
			}
			return t
		case []any:
			if len(t) == 2 {
				return elemPoint(t)
			}
		}
		return fmt.Sprint(e)
	}
	list, isList := toAnySlice(v)
	if o.IsVector {
		if !isList {
			list = []any{v}
		}
		items := make([]string, len(list))
		for i, e := range list {
			items[i] = elem(e)
		}
		return lval(items...)
	}
	if isList {
		if len(list) == 0 {
			return sval("")
		}
		if o.ValueType == "point" {
			return sval(elemPoint(list))
		}
		return sval(elem(list[0]))
	}
	return sval(elem(v))
}

func elemPoint(l []any) string {
	return formatDefault("float", l[0]) + "x" + formatDefault("float", l[1])
}

func toAnySlice(v any) ([]any, bool) {
	switch t := v.(type) {
	case []any:
		return t, true
	case []string:
		out := make([]any, len(t))
		for i, s := range t {
			out[i] = s
		}
		return out, true
	case []float64:
		out := make([]any, len(t))
		for i, s := range t {
			out[i] = s
		}
		return out, true
	case []int:
		out := make([]any, len(t))
		for i, s := range t {
			out[i] = s
		}
		return out, true
	case []bool:
		out := make([]any, len(t))
		for i, s := range t {
			out[i] = s
		}
		return out, true
	}
	return nil, false
}

// valString renders a value for messages.
func valString(v val) string {
	if v.Raw != nil {
		return string(v.Raw)
	}
	if v.IsList {
		return "[" + strings.Join(v.List, ", ") + "]"
	}
	return v.Str
}
