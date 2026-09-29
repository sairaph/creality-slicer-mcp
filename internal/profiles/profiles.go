// Package profiles reads the Creality Print preset bundles (machine, process
// and filament presets of the Creality vendor, plus the user's own presets),
// flattens their inheritance chains and writes CLI-ready preset files.
//
// A preset JSON is a delta plus "inherits"; neither system nor user files are
// flat, and the slicer's --load-settings never resolves "inherits", so the MCP
// flattens presets itself (dev_docs/11-presets-and-projects.md (a)).
//
// The package detects nothing: the caller passes the directories (from
// slicer.Install) in Roots.
package profiles

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Type is a preset kind, named as the tools name it.
type Type string

// Preset types. The bundle calls a printer preset "machine".
const (
	TypePrinter  Type = "printer"
	TypeProcess  Type = "process"
	TypeFilament Type = "filament"
)

// Types lists every type.
var Types = []Type{TypePrinter, TypeProcess, TypeFilament}

// jsonType is the value of the JSON "type" key and of the bundle folder name.
func (t Type) jsonType() string {
	if t == TypePrinter {
		return "machine"
	}
	return string(t)
}

// ParseType accepts printer, machine, process and filament.
func ParseType(s string) (Type, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "printer", "machine":
		return TypePrinter, nil
	case "process", "print":
		return TypeProcess, nil
	case "filament":
		return TypeFilament, nil
	}
	return "", fmt.Errorf("unknown preset type %q: use printer, process or filament", s)
}

// Sources of a preset.
const (
	SourceSystem = "system"
	SourceUser   = "user"
)

// DefaultVendor is the vendor bundle read.
const DefaultVendor = "Creality"

// Errors.
var (
	ErrNotFound         = errors.New("preset not found")
	ErrInheritsNotFound = errors.New("inherited preset not found")
	ErrInheritsCycle    = errors.New("inherits chain loops")
)

// Roots are the directories the bundles live in. The caller takes them from
// slicer.Install: InstallProfiles is <install>\resources\profiles and DataDir
// is the per-user data folder (its "system" folder is the GUI's copy of the
// bundle, its "user" folder holds the user's presets).
type Roots struct {
	InstallProfiles string // folder holding Creality.json and Creality\
	DataDir         string // folder holding system\, user\ and Creality.conf; may be empty
	Vendor          string // default "Creality"
	// Bundle, when set, is the bundle folder to read (slicer.Install.ProfileRoot,
	// which already applies the "higher version wins, install on a tie" rule
	// once). Open then reads that folder and does not choose again; the two
	// versions of InstallProfiles and DataDir are still reported in Info.
	Bundle string
}

// Info describes the bundles Open found.
type Info struct {
	Vendor         string
	InstallRoot    string // "" when not given or without a vendor index
	InstallVersion string
	DataRoot       string // <DataDir>\system, "" when absent
	DataVersion    string
	// Root is the bundle actually read: the one with the higher version (the
	// install's on a tie) and Version is that version.
	Root    string
	Version string
	// UserDirs are the user preset folders that exist (preset_folder from
	// Creality.conf, then "default").
	UserDirs []string
}

// Preset is one preset with its inheritance flattened.
type Preset struct {
	Type   Type
	Name   string
	Source string // system or user
	File   string // the preset's own JSON file
	// InheritsChain starts with the preset itself, then its parent, its
	// grandparent and so on up to the base.
	InheritsChain []string
	// Selectable is false for the fdm_* bases ("instantiation": "false").
	Selectable bool
	// Values is the flattened config: every JSON key of the chain, parent
	// first, child overriding, without "inherits". Values are string (scalar)
	// or []string (vector).
	Values map[string]any
	// Origin names, per key, the preset of the chain whose value won.
	Origin map[string]string
	// Info is the user preset's .info sidecar (setting_id, base_id,
	// updated_time, user_id, sync_info); nil for system presets.
	Info map[string]string
}

// String returns the scalar value of key ("" when absent; the first element
// of a vector).
func (p *Preset) String(key string) string {
	switch v := p.Values[key].(type) {
	case string:
		return v
	case []string:
		if len(v) > 0 {
			return v[0]
		}
	}
	return ""
}

// List returns the vector value of key (a scalar is a one element vector).
func (p *Preset) List(key string) []string {
	switch v := p.Values[key].(type) {
	case string:
		return []string{v}
	case []string:
		return v
	}
	return nil
}

// Parent is the immediate parent name, "" for a root preset.
func (p *Preset) Parent() string {
	if len(p.InheritsChain) > 1 {
		return p.InheritsChain[1]
	}
	return ""
}

// raw is one preset file as read, before flattening.
type raw struct {
	name   string
	file   string
	source string
	values map[string]any // as read, including inherits
	info   map[string]string
}

// Store reads and caches presets. It is safe for concurrent use.
type Store struct {
	roots Roots
	info  Info

	mu       sync.Mutex
	loaded   map[string]bool            // "type/source"
	index    map[string]map[string]*raw // "type/source" -> name -> raw
	order    map[string][]string        // listing order per "type/source"
	warnings []string
	flat     map[string]*Preset // "type/source/name"
	vendor   vendorIndex
}

func bucket(t Type, source string) string { return string(t) + "/" + source }

// Open checks the roots and reads the vendor index of the newer bundle. It
// fails when no bundle with a vendor index exists.
func Open(r Roots) (*Store, error) {
	if r.Vendor == "" {
		r.Vendor = DefaultVendor
	}
	s := &Store{
		roots:  r,
		loaded: map[string]bool{},
		index:  map[string]map[string]*raw{},
		order:  map[string][]string{},
		flat:   map[string]*Preset{},
	}
	s.info.Vendor = r.Vendor
	type cand struct {
		root, version string
		idx           vendorIndex
	}
	var cands []cand
	for i, root := range []string{r.InstallProfiles, dataSystem(r.DataDir)} {
		if root == "" {
			continue
		}
		idx, err := readVendorIndex(root, r.Vendor)
		if err != nil {
			continue
		}
		cands = append(cands, cand{root, idx.Version, idx})
		if i == 0 {
			s.info.InstallRoot, s.info.InstallVersion = root, idx.Version
		} else {
			s.info.DataRoot, s.info.DataVersion = root, idx.Version
		}
	}
	if len(cands) == 0 && r.Bundle == "" {
		return nil, fmt.Errorf("no %s.json vendor index under %q or %q", r.Vendor, r.InstallProfiles, dataSystem(r.DataDir))
	}
	var best cand
	if len(cands) > 0 {
		best = cands[0]
	}
	if r.Bundle != "" {
		idx, err := readVendorIndex(r.Bundle, r.Vendor)
		if err != nil {
			return nil, fmt.Errorf("no %s.json vendor index under the given bundle %q: %w", r.Vendor, r.Bundle, err)
		}
		best = cand{r.Bundle, idx.Version, idx}
	} else {
		for _, c := range cands[1:] {
			if CompareVersions(c.version, best.version) > 0 {
				best = c
			}
		}
	}
	s.info.Root, s.info.Version, s.vendor = best.root, best.version, best.idx
	s.info.UserDirs = userDirs(r.DataDir)
	return s, nil
}

func dataSystem(dataDir string) string {
	if dataDir == "" {
		return ""
	}
	return filepath.Join(dataDir, "system")
}

// Info returns what Open found.
func (s *Store) Info() Info { return s.info }

// Warnings lists files that could not be read (unparsable JSON, index entries
// without a file). It grows as presets are loaded.
func (s *Store) Warnings() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.warnings...)
}

// vendorIndex is <Vendor>.json.
type vendorIndex struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Lists   map[Type][]indexEntry
}

type indexEntry struct {
	Name    string `json:"name"`
	SubPath string `json:"sub_path"`
}

func readVendorIndex(root, vendor string) (vendorIndex, error) {
	data, err := os.ReadFile(filepath.Join(root, vendor+".json"))
	if err != nil {
		return vendorIndex{}, err
	}
	var doc struct {
		Name         string       `json:"name"`
		Version      string       `json:"version"`
		MachineList  []indexEntry `json:"machine_list"`
		ProcessList  []indexEntry `json:"process_list"`
		FilamentList []indexEntry `json:"filament_list"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return vendorIndex{}, fmt.Errorf("%s.json: %w", vendor, err)
	}
	return vendorIndex{
		Name: doc.Name, Version: strings.TrimSpace(doc.Version),
		Lists: map[Type][]indexEntry{TypePrinter: doc.MachineList, TypeProcess: doc.ProcessList, TypeFilament: doc.FilamentList},
	}, nil
}

// userDirs are the existing user preset folders: preset_folder of
// Creality.conf, then default.
func userDirs(dataDir string) []string {
	if dataDir == "" {
		return nil
	}
	var names []string
	if folder := presetFolder(filepath.Join(dataDir, "Creality.conf")); folder != "" {
		names = append(names, folder)
	}
	names = append(names, "default")
	var out []string
	seen := map[string]bool{}
	for _, n := range names {
		if seen[n] {
			continue
		}
		seen[n] = true
		dir := filepath.Join(dataDir, "user", n)
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			out = append(out, dir)
		}
	}
	return out
}

// presetFolder reads app.preset_folder from Creality.conf (a JSON file).
func presetFolder(conf string) string {
	data, err := os.ReadFile(conf)
	if err != nil {
		return ""
	}
	var doc struct {
		App struct {
			PresetFolder any `json:"preset_folder"`
		} `json:"app"`
	}
	if json.NewDecoder(bytes.NewReader(data)).Decode(&doc) != nil {
		return ""
	}
	switch v := doc.App.PresetFolder.(type) {
	case string:
		return strings.TrimSpace(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return ""
}

// load reads every preset file of one type and source once.
func (s *Store) load(t Type, source string) {
	b := bucket(t, source)
	if s.loaded[b] {
		return
	}
	s.loaded[b] = true
	s.index[b] = map[string]*raw{}
	add := func(r *raw) {
		if _, dup := s.index[b][r.name]; dup {
			return
		}
		s.index[b][r.name] = r
		s.order[b] = append(s.order[b], r.name)
	}
	if source == SourceSystem {
		for _, e := range s.vendor.Lists[t] {
			file := filepath.Join(s.info.Root, s.roots.Vendor, filepath.FromSlash(e.SubPath))
			v, err := readJSONFile(file)
			if err != nil {
				s.warn(fmt.Sprintf("%s: %v", file, err))
				continue
			}
			name := stringOf(v["name"])
			if name == "" {
				name = e.Name
			}
			r := &raw{name: name, file: file, source: source, values: v}
			add(r)
			if e.Name != "" && e.Name != name {
				if _, dup := s.index[b][e.Name]; !dup {
					s.index[b][e.Name] = r
				}
			}
		}
		return
	}
	for _, dir := range s.info.UserDirs {
		base := filepath.Join(dir, t.jsonType())
		for _, d := range []string{base, filepath.Join(base, "base")} {
			entries, err := os.ReadDir(d)
			if err != nil {
				continue
			}
			names := make([]string, 0, len(entries))
			for _, e := range entries {
				if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ".json") {
					names = append(names, e.Name())
				}
			}
			sort.Strings(names)
			for _, fn := range names {
				file := filepath.Join(d, fn)
				v, err := readJSONFile(file)
				if err != nil {
					s.warn(fmt.Sprintf("%s: %v", file, err))
					continue
				}
				name := stringOf(v["name"])
				if name == "" {
					name = strings.TrimSuffix(fn, filepath.Ext(fn))
				}
				add(&raw{name: name, file: file, source: source, values: v, info: readInfo(strings.TrimSuffix(file, filepath.Ext(file)) + ".info")})
			}
		}
	}
}

// readInfo parses a .info sidecar: "key = value" lines.
func readInfo(path string) map[string]string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	out := map[string]string{}
	for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		if i := strings.Index(line, "="); i > 0 {
			out[strings.TrimSpace(line[:i])] = strings.TrimSpace(line[i+1:])
		}
	}
	return out
}

// readJSONFile parses a preset file into string and []string values. Numbers
// and booleans become their JSON text; nested objects keep compact JSON.
func readJSONFile(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var doc map[string]any
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	out := make(map[string]any, len(doc))
	for k, v := range doc {
		switch t := v.(type) {
		case []any:
			list := make([]string, len(t))
			for i, e := range t {
				list[i] = stringOf(e)
			}
			out[k] = list
		default:
			out[k] = stringOf(v)
		}
	}
	return out, nil
}

func stringOf(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case json.Number:
		return t.String()
	case bool:
		return strconv.FormatBool(t)
	default:
		b, _ := json.Marshal(t)
		return string(b)
	}
}

// CompareVersions compares dotted numeric versions ("26.08.29.19"); missing
// parts count as 0 and a non numeric part ends the comparison.
func CompareVersions(a, b string) int {
	x, y := versionNumbers(a), versionNumbers(b)
	for i := 0; i < len(x) || i < len(y); i++ {
		var p, q int
		if i < len(x) {
			p = x[i]
		}
		if i < len(y) {
			q = y[i]
		}
		if p != q {
			if p > q {
				return 1
			}
			return -1
		}
	}
	return 0
}

func versionNumbers(v string) []int {
	var nums []int
	for _, part := range strings.Split(strings.TrimSpace(v), ".") {
		n, err := strconv.Atoi(part)
		if err != nil {
			break
		}
		nums = append(nums, n)
	}
	return nums
}

// warn records a warning once.
func (s *Store) warn(msg string) {
	for _, w := range s.warnings {
		if w == msg {
			return
		}
	}
	s.warnings = append(s.warnings, msg)
}
