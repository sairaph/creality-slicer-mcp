package threemf

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// Value is one value of project_settings.config: a string, a vector of
// strings, or (rarely) any other JSON kept as it is.
type Value struct {
	Str    string
	List   []string
	IsList bool
	Raw    json.RawMessage // set when the value is neither string nor []string
}

// String makes a scalar value.
func String(s string) Value { return Value{Str: s} }

// List makes a vector value (possibly empty).
func List(items ...string) Value { return Value{List: append([]string{}, items...), IsList: true} }

// First returns the scalar, or the first element of a vector.
func (v Value) First() string {
	if v.IsList {
		if len(v.List) > 0 {
			return v.List[0]
		}
		return ""
	}
	return v.Str
}

// Config is the ordered JSON object of Metadata/project_settings.config. Key
// order is kept on a round trip; the app writes keys sorted, and new keys are
// inserted at their sorted place while the file is sorted (appended
// otherwise).
type Config struct {
	keys  []string
	vals  map[string]Value
	crlf  bool
	dirty bool
}

// NewConfig returns an empty config that writes like the app: CRLF line ends.
func NewConfig() *Config { return &Config{vals: map[string]Value{}, crlf: true, dirty: true} }

// ParseConfig reads project_settings.config.
func ParseConfig(data []byte) (*Config, error) {
	c := &Config{vals: map[string]Value{}, crlf: bytes.Contains(data, []byte("\r\n"))}
	dec := json.NewDecoder(bytes.NewReader(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, fmt.Errorf("project settings are not a JSON object")
	}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := kt.(string)
		if !ok {
			return nil, fmt.Errorf("bad key %v", kt)
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
		v := classify(raw)
		if _, dup := c.vals[key]; !dup {
			c.keys = append(c.keys, key)
		}
		c.vals[key] = v
	}
	if _, err := dec.Token(); err != nil && err != io.EOF {
		return nil, err
	}
	return c, nil
}

func classify(raw json.RawMessage) Value {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return Value{Str: s}
	}
	var l []string
	if json.Unmarshal(raw, &l) == nil {
		if l == nil {
			l = []string{}
		}
		return Value{List: l, IsList: true}
	}
	return Value{Raw: append(json.RawMessage(nil), raw...)}
}

// Keys returns the keys in file order.
func (c *Config) Keys() []string { return append([]string(nil), c.keys...) }

// Len is the number of keys.
func (c *Config) Len() int { return len(c.keys) }

// Get returns the value of key.
func (c *Config) Get(key string) (Value, bool) { v, ok := c.vals[key]; return v, ok }

// String returns the scalar value of key (the first element of a vector).
func (c *Config) String(key string) string { return c.vals[key].First() }

// List returns the vector of key (a scalar counts as one element).
func (c *Config) List(key string) []string {
	v, ok := c.vals[key]
	switch {
	case !ok:
		return nil
	case v.IsList:
		return append([]string{}, v.List...)
	case v.Raw != nil:
		return nil
	}
	return []string{v.Str}
}

// Set stores a value.
func (c *Config) Set(key string, v Value) {
	if _, ok := c.vals[key]; !ok {
		if sort.StringsAreSorted(c.keys) {
			i := sort.SearchStrings(c.keys, key)
			c.keys = append(c.keys, "")
			copy(c.keys[i+1:], c.keys[i:])
			c.keys[i] = key
		} else {
			c.keys = append(c.keys, key)
		}
	}
	c.vals[key] = v
	c.dirty = true
}

// SetString stores a scalar.
func (c *Config) SetString(key, value string) { c.Set(key, String(value)) }

// SetList stores a vector.
func (c *Config) SetList(key string, items ...string) { c.Set(key, List(items...)) }

// Delete removes a key.
func (c *Config) Delete(key string) bool {
	if _, ok := c.vals[key]; !ok {
		return false
	}
	delete(c.vals, key)
	for i, k := range c.keys {
		if k == key {
			c.keys = append(c.keys[:i], c.keys[i+1:]...)
			break
		}
	}
	c.dirty = true
	return true
}

// Marshal writes the config the way Creality Print does: four space indent,
// every vector element on its own line, "[]" for an empty vector, CRLF line
// ends, a line end after the closing brace.
func (c *Config) Marshal() []byte {
	nl := "\n"
	if c.crlf {
		nl = "\r\n"
	}
	var b strings.Builder
	b.WriteString("{" + nl)
	for i, k := range c.keys {
		v := c.vals[k]
		b.WriteString("    " + jsonString(k) + ": ")
		switch {
		case v.Raw != nil:
			b.Write(v.Raw)
		case v.IsList && len(v.List) == 0:
			b.WriteString("[]")
		case v.IsList:
			b.WriteString("[" + nl)
			for j, e := range v.List {
				b.WriteString("        " + jsonString(e))
				if j < len(v.List)-1 {
					b.WriteString(",")
				}
				b.WriteString(nl)
			}
			b.WriteString("    ]")
		default:
			b.WriteString(jsonString(v.Str))
		}
		if i < len(c.keys)-1 {
			b.WriteString(",")
		}
		b.WriteString(nl)
	}
	b.WriteString("}" + nl)
	return []byte(b.String())
}

// jsonString quotes a string like the slicer's writer: the usual escapes, no
// escaping of "/" or of non ASCII text.
func jsonString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		default:
			if r < 0x20 {
				fmt.Fprintf(&b, `\u%04x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}
