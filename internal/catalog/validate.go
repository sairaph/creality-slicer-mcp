package catalog

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// ValidateOptions tunes Validate.
type ValidateOptions struct {
	// AllowLocked lets a write through to settings that Creality's vendor
	// policy fixes in its system presets.
	AllowLocked bool
	// IsCrealityPreset is true when the value is written into a preset that
	// belongs to the Creality vendor (system presets and presets derived from
	// them): the vendor policy of the setting then applies.
	IsCrealityPreset bool
}

// Error codes of ValidationError.
const (
	CodeUnknownKey = "unknown_key"
	CodeScope      = "wrong_scope"
	CodeLocked     = "locked"
	CodeType       = "wrong_type"
	CodeRange      = "out_of_range"
	CodeEnum       = "not_a_choice"
	CodeLength     = "wrong_length"
)

// ValidationError is returned by Validate.
type ValidationError struct {
	Key     string
	Code    string
	Message string
}

func (e *ValidationError) Error() string { return e.Message }

func fail(key, code, format string, a ...any) error {
	return &ValidationError{Key: key, Code: code, Message: fmt.Sprintf(format, a...)}
}

// Validate checks that value can be written for key at the given scope: the
// setting exists, may be set at that scope, is not vendor-locked, has the right
// type and length, and is inside its range or choices. The messages name the
// valid range or the valid choices. It knows nothing about other settings
// (extruder count, current preset), so vector length is only checked for being
// non-empty.
func (c *Catalog) Validate(key string, value any, scope Scope, opts ValidateOptions) error {
	o, ok := c.byKey[key]
	if !ok {
		msg := fmt.Sprintf("unknown setting %q", key)
		if near := c.Closest(key, 5); len(near) > 0 {
			msg += "; closest: " + strings.Join(near, ", ")
		}
		return &ValidationError{Key: key, Code: CodeUnknownKey, Message: msg}
	}
	if scope == "" {
		scope = ScopePreset
	}
	if !o.HasScope(scope) {
		return fail(key, CodeScope, "%s cannot be set at scope %q; valid scopes: %s", key, scope, strings.Join(o.scopeNames(), ", "))
	}
	if scope == ScopePreset && opts.IsCrealityPreset && !opts.AllowLocked && o.VendorLock != "" {
		if o.VendorLock == "hidden" {
			return fail(key, CodeLocked, "%s is hidden in Creality presets (the vendor fixes it); it cannot be changed unless locked settings are allowed", key)
		}
		return fail(key, CodeLocked, "%s is read-only in Creality presets (the vendor fixes it); it cannot be changed unless locked settings are allowed", key)
	}
	return o.checkValue(value)
}

func (o *Option) scopeNames() []string {
	var out []string
	if o.HasScope(ScopePreset) {
		out = append(out, string(ScopePreset))
	}
	return append(out, o.Scopes...)
}

func (o *Option) checkValue(value any) error {
	if o.ValueType == "point" && o.IsVector {
		if value == nil || !isPoint(value) {
			return fail(o.Key, CodeType, "%s expects a list of points written \"XxY\" or [x, y], got %s", o.Key, show(value))
		}
		return nil
	}
	if o.IsVector {
		elems, isSlice := asSlice(value)
		if !isSlice {
			if value == nil {
				return fail(o.Key, CodeType, "%s expects a list with one value per extruder or nozzle, got nothing", o.Key)
			}
			elems = []any{value} // a single value is a list of one
		}
		if len(elems) == 0 {
			return fail(o.Key, CodeLength, "%s needs at least one value (one per extruder or nozzle)", o.Key)
		}
		for i, e := range elems {
			if e == nil {
				if o.Nullable {
					continue // unset entry: inherit
				}
				return fail(o.Key, CodeType, "%s value %d is empty; this setting does not accept unset entries", o.Key, i+1)
			}
			if err := o.checkScalar(e, i+1); err != nil {
				return err
			}
		}
		return nil
	}
	if value == nil {
		if o.Nullable {
			return nil
		}
		return fail(o.Key, CodeType, "%s needs a value", o.Key)
	}
	if _, isSlice := asSlice(value); isSlice && o.ValueType != "point" {
		return fail(o.Key, CodeType, "%s takes a single value, not a list", o.Key)
	}
	return o.checkScalar(value, 0)
}

// where names the element for messages ("" for scalars).
func where(i int) string {
	if i == 0 {
		return ""
	}
	return fmt.Sprintf(" (value %d)", i)
}

func (o *Option) checkScalar(v any, idx int) error {
	switch o.ValueType {
	case "bool":
		if _, ok := toBool(v); !ok {
			return fail(o.Key, CodeType, "%s%s expects true or false, got %s", o.Key, where(idx), show(v))
		}
	case "int":
		f, ok := toFloat(v)
		if !ok || f != math.Trunc(f) {
			return fail(o.Key, CodeType, "%s%s expects a whole number, got %s", o.Key, where(idx), show(v))
		}
		return o.checkRange(f, idx)
	case "float":
		f, ok := toFloat(v)
		if !ok {
			return fail(o.Key, CodeType, "%s%s expects a number%s, got %s", o.Key, where(idx), o.unit(), show(v))
		}
		return o.checkRange(f, idx)
	case "percent":
		f, _, ok := toPercent(v)
		if !ok {
			return fail(o.Key, CodeType, "%s%s expects a percentage (a number or \"NN%%\"), got %s", o.Key, where(idx), show(v))
		}
		return o.checkRange(f, idx)
	case "float_or_percent":
		f, isPct, ok := toPercent(v)
		if !ok {
			return fail(o.Key, CodeType, "%s%s expects a number%s or a percentage like \"120%%\", got %s", o.Key, where(idx), o.unit(), show(v))
		}
		if isPct {
			if o.Min != nil && *o.Min >= 0 && f < 0 {
				return fail(o.Key, CodeRange, "%s%s: %s%% is negative; percentages must be 0 or more", o.Key, where(idx), fmtNum(f))
			}
			return nil
		}
		return o.checkRange(f, idx)
	case "enum":
		s, ok := v.(string)
		if !ok {
			return fail(o.Key, CodeType, "%s%s expects one of the choices %s, got %s", o.Key, where(idx), o.choices(), show(v))
		}
		if o.Enum != nil && len(o.Enum.Values) > 0 {
			for _, ev := range o.Enum.Values {
				if ev == s {
					return nil
				}
			}
			hint := ""
			for i, l := range o.Enum.Labels {
				if i < len(o.Enum.Values) && strings.EqualFold(l, s) {
					hint = fmt.Sprintf("; did you mean %q (the label %q)?", o.Enum.Values[i], l)
				}
			}
			return fail(o.Key, CodeEnum, "%s%s: %q is not a valid choice; valid values: %s%s", o.Key, where(idx), s, o.choices(), hint)
		}
	case "point":
		if !isPoint(v) {
			return fail(o.Key, CodeType, "%s%s expects a point written \"XxY\" or [x, y], got %s", o.Key, where(idx), show(v))
		}
	case "string":
		if _, ok := v.(string); !ok {
			return fail(o.Key, CodeType, "%s%s expects text, got %s", o.Key, where(idx), show(v))
		}
	}
	return nil
}

func (o *Option) unit() string {
	if o.Sidetext == "" {
		return ""
	}
	return " (" + o.Sidetext + ")"
}

func (o *Option) choices() string {
	if o.Enum == nil {
		return "(none listed)"
	}
	parts := make([]string, len(o.Enum.Values))
	for i, v := range o.Enum.Values {
		parts[i] = v
		if i < len(o.Enum.Labels) && o.Enum.Labels[i] != "" && o.Enum.Labels[i] != v {
			parts[i] = fmt.Sprintf("%s (%s)", v, o.Enum.Labels[i])
		}
	}
	return strings.Join(parts, ", ")
}

func (o *Option) checkRange(f float64, idx int) error {
	if (o.Min != nil && f < *o.Min) || (o.Max != nil && f > *o.Max) {
		return fail(o.Key, CodeRange, "%s%s: %s is outside the valid range %s%s", o.Key, where(idx), fmtNum(f), o.rangeText(), o.unit())
	}
	return nil
}

func (o *Option) rangeText() string {
	switch {
	case o.Min != nil && o.Max != nil:
		return fmtNum(*o.Min) + " to " + fmtNum(*o.Max)
	case o.Min != nil:
		return fmtNum(*o.Min) + " or more"
	case o.Max != nil:
		return fmtNum(*o.Max) + " or less"
	}
	return "(unbounded)"
}

func fmtNum(f float64) string { return strconv.FormatFloat(f, 'g', -1, 64) }

func show(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	s := string(b)
	if len(s) > 60 {
		s = s[:60] + "..."
	}
	return s
}

func asSlice(v any) ([]any, bool) {
	switch x := v.(type) {
	case []any:
		return x, true
	case []string:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = e
		}
		return out, true
	case []float64:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = e
		}
		return out, true
	case []int:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = e
		}
		return out, true
	case []bool:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = e
		}
		return out, true
	}
	return nil, false
}

func toBool(v any) (bool, bool) {
	switch x := v.(type) {
	case bool:
		return x, true
	case string:
		switch strings.ToLower(strings.TrimSpace(x)) {
		case "true", "1", "on", "yes":
			return true, true
		case "false", "0", "off", "no":
			return false, true
		}
	default:
		if f, ok := toFloat(v); ok && (f == 0 || f == 1) {
			return f == 1, true
		}
	}
	return false, false
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, !math.IsNaN(x) && !math.IsInf(x, 0)
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int8:
		return float64(x), true
	case int16:
		return float64(x), true
	case int32:
		return float64(x), true
	case int64:
		return float64(x), true
	case uint:
		return float64(x), true
	case uint8:
		return float64(x), true
	case uint16:
		return float64(x), true
	case uint32:
		return float64(x), true
	case uint64:
		return float64(x), true
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f, err == nil && !math.IsNaN(f) && !math.IsInf(f, 0)
	}
	return 0, false
}

// toPercent accepts a number or a string like "120%" (the flag reports the "%").
func toPercent(v any) (f float64, isPct, ok bool) {
	if s, isStr := v.(string); isStr {
		s = strings.TrimSpace(s)
		if strings.HasSuffix(s, "%") {
			f, ok = toFloat(strings.TrimSuffix(s, "%"))
			return f, true, ok
		}
	}
	f, ok = toFloat(v)
	return f, false, ok
}

var rePoint = regexp.MustCompile(`^\s*-?\d+(\.\d+)?\s*[xX,]\s*-?\d+(\.\d+)?\s*$`)

func isPoint(v any) bool {
	if s, ok := v.(string); ok {
		return rePoint.MatchString(s)
	}
	if xs, ok := asSlice(v); ok {
		if len(xs) == 2 {
			if _, ok1 := toFloat(xs[0]); ok1 {
				if _, ok2 := toFloat(xs[1]); ok2 {
					return true
				}
			}
		}
		// a list of points, for settings that hold several
		if len(xs) > 0 {
			for _, e := range xs {
				if !isPoint(e) {
					return false
				}
			}
			return true
		}
	}
	return false
}
