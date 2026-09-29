package projects

import (
	"strconv"
	"strings"

	"github.com/sairaph/creality-slicer-mcp/internal/catalog"
)

// condValue is the result of evaluating one node: a bool, a float64 or a
// string, and whether it could be worked out at all.
type condValue struct {
	v     any
	known bool
}

// condEnv evaluates the small condition trees of the catalog (the GUI's
// gating and forcing rules) against the current settings. Nodes that depend on
// code the catalog cannot express (capabilities, helper calls, unparsed
// text) make the whole condition unknown, and an unknown condition is never
// treated as true or false.
type condEnv struct {
	get func(key string) (string, bool)
}

func (e condEnv) eval(c *catalog.Cond) condValue {
	if c == nil {
		return condValue{}
	}
	switch c.K {
	case "bool":
		b, ok := c.V.(bool)
		return condValue{b, ok}
	case "num":
		f, ok := c.V.(float64)
		return condValue{f, ok}
	case "enum":
		s, ok := c.V.(string)
		return condValue{s, ok}
	case "opt":
		s, ok := e.get(c.Key)
		if !ok {
			return condValue{}
		}
		switch c.Kind {
		case "bool":
			return condValue{s == "1" || strings.EqualFold(s, "true"), true}
		case "int", "float":
			f, err := strconv.ParseFloat(strings.TrimSuffix(s, "%"), 64)
			return condValue{f, err == nil}
		}
		return condValue{s, true}
	case "not":
		if len(c.A) != 1 {
			return condValue{}
		}
		if v := e.eval(c.A[0]); v.known {
			if b, ok := v.v.(bool); ok {
				return condValue{!b, true}
			}
		}
		return condValue{}
	case "and", "or":
		and := c.K == "and"
		unknown := false
		for _, a := range c.A {
			v := e.eval(a)
			b, isBool := v.v.(bool)
			if !v.known || !isBool {
				unknown = true
				continue
			}
			if and && !b {
				return condValue{false, true}
			}
			if !and && b {
				return condValue{true, true}
			}
		}
		if unknown {
			return condValue{}
		}
		return condValue{and, true}
	case "cmp":
		if len(c.A) != 2 {
			return condValue{}
		}
		l, r := e.eval(c.A[0]), e.eval(c.A[1])
		if !l.known || !r.known {
			return condValue{}
		}
		return compareConds(c.Op, l.v, r.v)
	case "in":
		if len(c.A) != 1 {
			return condValue{}
		}
		l := e.eval(c.A[0])
		s, ok := l.v.(string)
		if !l.known || !ok {
			return condValue{}
		}
		list, _ := c.V.([]any)
		for _, x := range list {
			if xs, ok := x.(string); ok && xs == s {
				return condValue{true, true}
			}
		}
		return condValue{false, true}
	case "has":
		_, ok := e.get(c.Key)
		return condValue{ok, true}
	}
	return condValue{} // cap, call, text, unparsed
}

func compareConds(op string, l, r any) condValue {
	switch a := l.(type) {
	case float64:
		b, ok := r.(float64)
		if !ok {
			return condValue{}
		}
		switch op {
		case "==":
			return condValue{a == b, true}
		case "!=":
			return condValue{a != b, true}
		case ">":
			return condValue{a > b, true}
		case "<":
			return condValue{a < b, true}
		case ">=":
			return condValue{a >= b, true}
		case "<=":
			return condValue{a <= b, true}
		}
	case string:
		b, ok := r.(string)
		if !ok {
			return condValue{}
		}
		switch op {
		case "==":
			return condValue{a == b, true}
		case "!=":
			return condValue{a != b, true}
		}
	case bool:
		b, ok := r.(bool)
		if !ok {
			return condValue{}
		}
		switch op {
		case "==":
			return condValue{a == b, true}
		case "!=":
			return condValue{a != b, true}
		}
	}
	return condValue{}
}

// holds reports whether every condition is definitely true.
func (e condEnv) holds(cs []*catalog.Cond) bool {
	if len(cs) == 0 {
		return false
	}
	for _, c := range cs {
		v := e.eval(c)
		b, ok := v.v.(bool)
		if !v.known || !ok || !b {
			return false
		}
	}
	return true
}

// mentions reports whether a condition reads the setting key.
func mentions(c *catalog.Cond, key string) bool {
	if c == nil {
		return false
	}
	if (c.K == "opt" || c.K == "has") && c.Key == key {
		return true
	}
	for _, a := range c.A {
		if mentions(a, key) {
			return true
		}
	}
	return false
}
