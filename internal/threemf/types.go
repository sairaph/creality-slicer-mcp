// Package threemf reads and writes Creality Print 3MF projects.
//
// A project is a ZIP package (dev_docs/11-presets-and-projects.md section c):
// the Bambu Studio 3MF with Creality additions. Open reads everything the
// app writes; Save copies every member that was not touched byte for byte
// (meshes with painted triangles, thumbnails, unknown members) and regenerates
// only the members whose content changed, in the exact formatting of a
// project saved by Creality Print 7.2 (the golden sample of dev_docs/10-cli.md
// section 20.1).
//
// project_settings.config is kept as ordered JSON (Config); this package
// stores and writes it but never composes it: the full config of a project
// (printer, process and filament presets, flush matrix) is built by the layer
// above.
package threemf

import (
	"errors"
	"sort"
	"strings"

	"github.com/sairaph/creality-slicer-mcp/internal/mesh"
)

// Errors.
var (
	// ErrUnsupportedLayout is returned by mutations of a project that keeps
	// its meshes inside 3D/3dmodel.model (files written by other tools or by
	// old versions). Such projects can be read, but not changed: open and save
	// them once in Creality Print 7.2.
	ErrUnsupportedLayout = errors.New("the project keeps its meshes inline (not the split layout of Creality Print 7.2); save it once in Creality Print first")
	ErrNotFound          = errors.New("not found")
	ErrInvalid           = errors.New("invalid argument")
)

// Part subtypes (ModelVolume types).
const (
	SubtypeNormal          = "normal_part"
	SubtypeNegative        = "negative_part"
	SubtypeModifier        = "modifier_part"
	SubtypeSupportEnforcer = "support_enforcer"
	SubtypeSupportBlocker  = "support_blocker"
)

// Custom G-code item types (CustomGCode.hpp).
const (
	GCodeColorChange = 0
	GCodePausePrint  = 1
	GCodeToolChange  = 2
	GCodeTemplate    = 3
	GCodeCustom      = 4
)

// Custom G-code modes.
const (
	ModeSingleExtruder = "SingleExtruder"
	ModeMultiAsSingle  = "MultiAsSingle"
	ModeMultiExtruder  = "MultiExtruder"
)

// KV is one key and value; KVs keep the order of the file.
type KV struct{ Key, Value string }

// KVs is an ordered list of key value pairs.
type KVs []KV

// Get returns the value of key.
func (k KVs) Get(key string) (string, bool) {
	for _, e := range k {
		if e.Key == key {
			return e.Value, true
		}
	}
	return "", false
}

// Value returns the value of key or "".
func (k KVs) Value(key string) string { v, _ := k.Get(key); return v }

// Set replaces the value of key in place, or inserts a new key at its sorted
// position when the list is sorted (the app writes sorted keys) and appends it
// otherwise.
func (k *KVs) Set(key, value string) {
	for i := range *k {
		if (*k)[i].Key == key {
			(*k)[i].Value = value
			return
		}
	}
	if sort.SliceIsSorted(*k, func(i, j int) bool { return (*k)[i].Key < (*k)[j].Key }) {
		i := sort.Search(len(*k), func(i int) bool { return (*k)[i].Key > key })
		*k = append(*k, KV{})
		copy((*k)[i+1:], (*k)[i:])
		(*k)[i] = KV{key, value}
		return
	}
	*k = append(*k, KV{key, value})
}

// Delete removes key; it reports whether it was there.
func (k *KVs) Delete(key string) bool {
	for i := range *k {
		if (*k)[i].Key == key {
			*k = append((*k)[:i], (*k)[i+1:]...)
			return true
		}
	}
	return false
}

func (k KVs) clone() KVs { return append(KVs(nil), k...) }

// Painted reports which kinds of painted triangle data a mesh carries. The
// data itself is never parsed or modified.
type Painted struct {
	Supports     bool // paint_supports
	Seam         bool // paint_seam
	Color        bool // paint_color (multi material painting)
	FuzzySkin    bool // paint_fuzzy_skin
	FaceProperty bool // face_property
}

// Any reports whether any painting is present.
func (p Painted) Any() bool {
	return p.Supports || p.Seam || p.Color || p.FuzzySkin || p.FaceProperty
}

func (p Painted) or(o Painted) Painted {
	return Painted{p.Supports || o.Supports, p.Seam || o.Seam, p.Color || o.Color, p.FuzzySkin || o.FuzzySkin, p.FaceProperty || o.FaceProperty}
}

// MeshRef locates and summarises a mesh object of the package.
type MeshRef struct {
	// Path is the model file inside the package ("/3D/Objects/object_1.model",
	// or "/3D/3dmodel.model" for an inline mesh).
	Path      string
	ObjectID  int
	Type      string // "model" or "other"
	Vertices  int
	Triangles int
	Painted   Painted
	Min, Max  [3]float32 // bounds of the mesh in its own coordinates
	// Shared is true when another part of the project uses the same mesh.
	Shared bool
}

// Source is the origin of a part's mesh as recorded by the slicer.
type Source struct {
	File                      string
	ObjectID, VolumeID        string
	OffsetX, OffsetY, OffsetZ string
	InInches, InMeters        bool
}

// Part is one volume of an object: the model itself, a modifier, a support
// blocker or enforcer, a negative part.
type Part struct {
	ID      int // the mesh object id, also the id in model_settings.config
	Subtype string
	Name    string
	// Matrix is the part matrix of model_settings.config (volume matrix times
	// source transform), 4x4, row major.
	Matrix    mesh.Matrix4
	HasMatrix bool
	Source    *Source
	// Config holds the part's setting overrides (a modifier's settings,
	// "extruder", ...).
	Config KVs
	// MeshStat is the mesh_stat element (edges_fixed, degenerate_facets, ...).
	MeshStat KVs
	Mesh     MeshRef
	// ComponentTransform is the volume's transform in 3D/3dmodel.model.
	ComponentTransform mesh.Matrix
	componentUUID      string
	hasComponent       bool
	extra              []*node    // unknown children (text, emboss shapes)
	fresh              *mesh.Mesh // mesh of a part added in this session, not yet saved
	inSettings         bool       // the part has an entry in model_settings.config
}

// Object is one object of the project (a wrapper object of 3D/3dmodel.model
// with its model_settings.config entry).
type Object struct {
	ID     int // the object id used by build items and plates
	UUID   string
	Type   string
	Name   string
	Module string
	// Config holds the per object setting overrides, "extruder" among them
	// (1 based filament number).
	Config KVs
	Parts  []*Part
	// LayerRanges are the height range modifiers (layer_config_ranges.xml).
	LayerRanges []LayerRange
	extra       []*node
	inSettings  bool
	backupID    int // index used in the object file name and the uuids
}

// Painted aggregates the painting of all parts.
func (o *Object) Painted() Painted {
	var p Painted
	for _, part := range o.Parts {
		p = p.or(part.Mesh.Painted)
	}
	return p
}

// Extruder returns the object's filament number (1 based), 0 when not set.
func (o *Object) Extruder() int {
	n := 0
	for _, c := range o.Config.Value("extruder") {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// Part returns the part with the given id.
func (o *Object) Part(id int) *Part {
	for _, p := range o.Parts {
		if p.ID == id {
			return p
		}
	}
	return nil
}

// BuildItem is one instance of an object on the bed (<item> of <build>).
type BuildItem struct {
	ObjectID  int
	UUID      string
	Path      string
	Transform mesh.Matrix
	Printable bool
}

// Instance is one model instance of a plate.
type Instance struct {
	ObjectID   int
	InstanceID int
	// IdentifyID is the id `--skip-objects` takes.
	IdentifyID int
}

// Plate is one plate (<plate> of model_settings.config).
type Plate struct {
	Index  int // plater_id, 1 based
	Name   string
	Locked bool
	// Config holds the other plate keys in file order: bed_type,
	// print_sequence, first_layer_print_sequence, spiral_mode, the thumbnail
	// references, ...
	Config    KVs
	Instances []Instance
	extra     []*node
}

// AssembleItem is an <assemble_item> of the assembly view.
type AssembleItem struct {
	ObjectID   int
	InstanceID int
	Attrs      KVs // all attributes in file order
}

// LayerRange is a height range modifier of an object.
type LayerRange struct {
	MinZ, MaxZ float64
	Options    KVs // opt_key -> value
}

// GCodeItem is a custom G-code item at a layer height.
type GCodeItem struct {
	TopZ     float64
	Type     int // GCodeColorChange ... GCodeCustom
	Extruder int
	Color    string
	Extra    string
	// GCode overrides the g-code text; empty derives it from the type.
	GCode string
}

// PlateGCodes are the custom G-code items of one plate.
type PlateGCodes struct {
	Plate int // 1 based
	Mode  string
	Items []GCodeItem
}

// Member describes one file of the package.
type Member struct {
	Name string
	Size int64
	// Known is true for members this package understands; others are carried
	// along untouched.
	Known bool
}

func isKnownMember(name string) bool {
	switch {
	case name == "[Content_Types].xml", name == "_rels/.rels", name == "3D/3dmodel.model",
		name == "3D/_rels/3dmodel.model.rels":
		return true
	case strings.HasPrefix(name, "3D/Objects/") && strings.HasSuffix(name, ".model"):
		return true
	}
	if !strings.HasPrefix(name, "Metadata/") {
		return false
	}
	base := strings.TrimPrefix(name, "Metadata/")
	switch base {
	case "model_settings.config", "project_settings.config", "slice_info.config", "creality.config",
		"layer_heights_profile.txt", "layer_config_ranges.xml", "brim_ear_points.txt",
		"custom_gcode_per_layer.xml", "cut_information.xml":
		return true
	}
	for _, prefix := range []string{"process_settings_", "filament_settings_", "machine_settings_", "plate_", "plate_no_light_", "top_", "pick_", "calibration_p"} {
		if strings.HasPrefix(base, prefix) {
			return true
		}
	}
	return false
}
