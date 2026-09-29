package profiles

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/sairaph/creality-slicer-mcp/internal/domain"
	"reflect"
	"sort"
	"strings"
)

// maxChain guards against runaway inheritance (a real chain is 3 levels).
const maxChain = 32

// Get returns the preset called name with its inheritance flattened. A system
// preset wins over a user preset of the same name.
func (s *Store) Get(t Type, name string) (Preset, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.getLocked(t, name)
	if err != nil {
		return Preset{}, err
	}
	return p.clone(), nil
}

func (s *Store) getLocked(t Type, name string) (*Preset, error) {
	for _, source := range []string{SourceSystem, SourceUser} {
		s.load(t, source)
		if r, ok := s.index[bucket(t, source)][name]; ok {
			return s.flattenLocked(t, r)
		}
	}
	return nil, fmt.Errorf("%w: %s preset %q", ErrNotFound, t, name)
}

func (p *Preset) clone() Preset {
	c := *p
	c.InheritsChain = append([]string(nil), p.InheritsChain...)
	c.Values = make(map[string]any, len(p.Values))
	for k, v := range p.Values {
		if list, ok := v.([]string); ok {
			v = append([]string{}, list...)
		}
		c.Values[k] = v
	}
	c.Origin = make(map[string]string, len(p.Origin))
	for k, v := range p.Origin {
		c.Origin[k] = v
	}
	if p.Info != nil {
		c.Info = make(map[string]string, len(p.Info))
		for k, v := range p.Info {
			c.Info[k] = v
		}
	}
	return c
}

// lookupParent finds the preset a child inherits: system presets first, then
// (for a user child) user presets.
func (s *Store) lookupParent(t Type, name, childSource string) *raw {
	sources := []string{SourceSystem}
	if childSource == SourceUser {
		sources = append(sources, SourceUser)
	}
	for _, source := range sources {
		s.load(t, source)
		if r, ok := s.index[bucket(t, source)][name]; ok {
			return r
		}
	}
	return nil
}

func (s *Store) flattenLocked(t Type, r *raw) (*Preset, error) {
	key := bucket(t, r.source) + "/" + r.name
	if p, ok := s.flat[key]; ok {
		return p, nil
	}
	chain := []*raw{r}
	seen := map[*raw]bool{r: true}
	for cur := r; ; {
		parentName := strings.TrimSpace(stringOf(cur.values["inherits"]))
		if parentName == "" {
			break
		}
		parent := s.lookupParent(t, parentName, cur.source)
		if parent == nil {
			return nil, fmt.Errorf("%w: %q inherits %q", ErrInheritsNotFound, cur.name, parentName)
		}
		if seen[parent] || len(chain) >= maxChain {
			return nil, fmt.Errorf("%w: %q", ErrInheritsCycle, r.name)
		}
		seen[parent] = true
		chain = append(chain, parent)
		cur = parent
	}
	p := &Preset{
		Type: t, Name: r.name, Source: r.source, File: r.file, Info: r.info,
		Values: map[string]any{}, Origin: map[string]string{},
	}
	for i := len(chain) - 1; i >= 0; i-- {
		link := chain[i]
		for k, v := range link.values {
			if k == "inherits" {
				continue
			}
			p.Values[k] = v
			p.Origin[k] = link.name
		}
	}
	for _, link := range chain {
		p.InheritsChain = append(p.InheritsChain, link.name)
	}
	p.Selectable = !strings.EqualFold(strings.TrimSpace(p.String("instantiation")), "false")
	s.flat[key] = p
	return p, nil
}

// Difference is one key that differs between two presets.
type Difference struct {
	Key  string
	A, B any // string, []string or nil when the key is absent
	InA  bool
	InB  bool
}

// Diff lists the keys whose flattened values differ, sorted by key. The
// identity keys (name, setting_id, filament_id, version, from) are included
// like any other.
func Diff(a, b Preset) []Difference {
	keys := map[string]bool{}
	for k := range a.Values {
		keys[k] = true
	}
	for k := range b.Values {
		keys[k] = true
	}
	var out []Difference
	for k := range keys {
		av, inA := a.Values[k]
		bv, inB := b.Values[k]
		if inA && inB && reflect.DeepEqual(av, bv) {
			continue
		}
		out = append(out, Difference{Key: k, A: av, B: bv, InA: inA, InB: inB})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// Diff compares two presets of the store by name.
func (s *Store) Diff(t Type, a, b string) ([]Difference, error) {
	pa, err := s.Get(t, a)
	if err != nil {
		return nil, err
	}
	pb, err := s.Get(t, b)
	if err != nil {
		return nil, err
	}
	return Diff(pa, pb), nil
}

// WriteFlat writes p as a file the slicer accepts for --load-settings and
// --load-filaments: "inherits" removed, "instantiation" "true", "from" system
// or User, "type" machine, process or filament, "name" kept. The file is
// written to a temporary name and renamed; missing parent folders are created.
func WriteFlat(p Preset, path string) error {
	doc := make(map[string]any, len(p.Values)+4)
	for k, v := range p.Values {
		doc[k] = v
	}
	delete(doc, "inherits")
	doc["type"] = p.Type.jsonType()
	doc["name"] = p.Name
	doc["instantiation"] = "true"
	if p.Source == SourceUser {
		doc["from"] = "User"
	} else {
		doc["from"] = "system"
	}
	data, err := marshalOrdered(doc)
	if err != nil {
		return err
	}
	return domain.WriteFileAtomic(path, data, 0o600)
}

// WriteFlat is the package function WriteFlat for a preset of the store.
func (s *Store) WriteFlat(p Preset, path string) error { return WriteFlat(p, path) }

// marshalOrdered writes a JSON object with the identity keys first and the
// rest sorted, four space indent like the bundle's own files.
func marshalOrdered(doc map[string]any) ([]byte, error) {
	first := []string{"type", "from", "name", "instantiation"}
	var keys []string
	for _, k := range first {
		if _, ok := doc[k]; ok {
			keys = append(keys, k)
		}
	}
	var rest []string
	for k := range doc {
		if k != "type" && k != "from" && k != "name" && k != "instantiation" {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	keys = append(keys, rest...)
	var buf bytes.Buffer
	buf.WriteString("{\n")
	for i, k := range keys {
		kb, _ := json.Marshal(k)
		vb, err := marshalNoEscape(doc[k])
		if err != nil {
			return nil, err
		}
		buf.WriteString("    ")
		buf.Write(kb)
		buf.WriteString(": ")
		buf.Write(vb)
		if i < len(keys)-1 {
			buf.WriteByte(',')
		}
		buf.WriteByte('\n')
	}
	buf.WriteString("}\n")
	return buf.Bytes(), nil
}

// marshalNoEscape is json.Marshal without HTML escaping (G-code templates
// contain < and >).
func marshalNoEscape(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
