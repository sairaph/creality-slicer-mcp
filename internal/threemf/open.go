package threemf

import (
	"archive/zip"
	"fmt"
	"io"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sairaph/creality-slicer-mcp/internal/mesh"
)

// Member names.
const (
	memberModel         = "3D/3dmodel.model"
	memberModelRels     = "3D/_rels/3dmodel.model.rels"
	memberContentTypes  = "[Content_Types].xml"
	memberRels          = "_rels/.rels"
	memberModelSettings = "Metadata/model_settings.config"
	memberProjectConfig = "Metadata/project_settings.config"
	memberSliceInfo     = "Metadata/slice_info.config"
	memberCreality      = "Metadata/creality.config"
	memberLayerRanges   = "Metadata/layer_config_ranges.xml"
	memberCustomGCode   = "Metadata/custom_gcode_per_layer.xml"
)

type layout int

const (
	layoutSplit  layout = iota // meshes in 3D/Objects/*.model, objects made of components
	layoutInline               // meshes inside 3D/3dmodel.model
)

// member is one file of the package: either an entry of the open ZIP
// (untouched, copied raw on save) or new content.
type member struct {
	name string
	zf   *zip.File // the source entry, nil for generated members
	data []byte    // generated content (used when zf is nil or the member changed)
	// touched marks a member whose content this session replaced.
	touched bool
	// store writes the member uncompressed (images are already compressed).
	store bool
}

// Project is an open Creality Print project.
type Project struct {
	path string
	zr   *zip.ReadCloser

	members []*member
	byName  map[string]*member

	// Now is the clock used for dates (CreationDate of a new project,
	// ModificationDate on changes); tests set it.
	Now func() time.Time

	layout    layout
	rootTag   string // the <model ...> start tag, reused when 3dmodel.model is regenerated
	buildUUID string
	// Metadata is the <metadata> list of 3dmodel.model (Application, Title, ...).
	Metadata KVs
	Objects  []*Object
	Items    []*BuildItem
	Plates   []*Plate
	Assemble []AssembleItem
	// GCodes are the custom G-code items per plate.
	GCodes []PlateGCodes
	// Creality is Metadata/creality.config.
	Creality KVs
	// Settings is project_settings.config as ordered JSON; nil when the project
	// has none. Changes are written on Save.
	Settings *Config
	// IsSlicerProject is true when the package has Metadata/model_settings.config,
	// that is, when a slicer saved it. A plain 3MF (FreeCAD, other CAD tools)
	// can be read but not edited as a project: import its meshes into a new
	// project (add_model) instead. Mutating it returns ErrNotSlicerProject.
	IsSlicerProject bool
	// Warnings lists members that could not be understood and are kept as they are.
	Warnings []string

	// Presets lists the embedded preset members (process_settings_N.config, ...).
	Presets []string

	settingsOrder []int // object ids in the order of model_settings.config
	modelDirty    bool
	settingsDirty bool
	rangesDirty   bool
	gcodesDirty   bool
	hasRanges     bool
	hasGCodes     bool
	isNew         bool
	objectFiles   map[string]*modelFile // scanned model files by member name
}

// Open reads a project. Every ZIP entry is kept for a byte exact Save.
func Open(path string) (*Project, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	p := &Project{path: path, zr: zr, byName: map[string]*member{}, Now: time.Now, objectFiles: map[string]*modelFile{}}
	for _, f := range zr.File {
		m := &member{name: f.Name, zf: f}
		p.members = append(p.members, m)
		p.byName[f.Name] = m
	}
	if err := p.load(); err != nil {
		zr.Close()
		return nil, err
	}
	return p, nil
}

// Close releases the file.
func (p *Project) Close() error {
	if p.zr == nil {
		return nil
	}
	err := p.zr.Close()
	p.zr = nil
	return err
}

// Path is the file the project was opened from or last saved to.
func (p *Project) Path() string { return p.path }

// Members lists the files of the package in archive order.
func (p *Project) Members() []Member {
	out := make([]Member, 0, len(p.members))
	for _, m := range p.members {
		size := int64(len(m.data))
		if m.zf != nil && !m.touched {
			size = int64(m.zf.UncompressedSize64)
		}
		out = append(out, Member{Name: m.name, Size: size, Known: isKnownMember(m.name) || strings.HasSuffix(m.name, "/")})
	}
	return out
}

// UnknownMembers lists the members this package does not interpret; Save
// keeps them untouched.
func (p *Project) UnknownMembers() []string {
	var out []string
	for _, m := range p.Members() {
		if !m.Known {
			out = append(out, m.Name)
		}
	}
	return out
}

// Read returns the content of a member (decompressed).
func (p *Project) Read(name string) ([]byte, error) {
	m := p.byName[name]
	if m == nil {
		return nil, fmt.Errorf("%w: member %q", ErrNotFound, name)
	}
	return m.content()
}

func (m *member) content() ([]byte, error) {
	if m.zf == nil || m.touched {
		return m.data, nil
	}
	rc, err := m.zf.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

func (p *Project) open(name string) (io.ReadCloser, error) {
	m := p.byName[name]
	if m == nil {
		return nil, fmt.Errorf("%w: member %q", ErrNotFound, name)
	}
	if m.zf == nil || m.touched {
		return io.NopCloser(strings.NewReader(string(m.data))), nil
	}
	return m.zf.Open()
}

// load reads the structure of the package.
func (p *Project) load() error {
	main, err := p.scanMember(memberModel)
	if err != nil {
		return fmt.Errorf("%s: %w", memberModel, err)
	}
	p.rootTag = main.Root
	p.buildUUID = main.BuildUUID
	p.Metadata = main.Metadata

	// Mesh objects that other objects reference as components of the same file
	// (Bambu Studio style inline layout) are resources, not objects.
	sameFile := map[int]bool{}
	for _, fo := range main.Objects {
		for _, c := range fo.Components {
			if c.Path == "" {
				sameFile[c.ObjectID] = true
			}
		}
	}
	p.IsSlicerProject = p.byName[memberModelSettings] != nil
	shared := map[[2]string]int{} // (file, object) -> users
	nextBackup := 0
	for _, fo := range main.Objects {
		if fo.Mesh != nil {
			p.layout = layoutInline
			if sameFile[fo.ID] {
				continue
			}
		}
		o := &Object{ID: fo.ID, UUID: fo.UUID, Type: fo.Type, Name: fo.Name}
		if fo.Mesh != nil {
			p.layout = layoutInline
			part := &Part{ID: fo.ID, Subtype: SubtypeNormal, Name: fo.Name, ComponentTransform: mesh.Identity(),
				Mesh: MeshRef{Path: "/" + memberModel, ObjectID: fo.ID, Type: fo.Type, Vertices: fo.Mesh.Vertices, Triangles: fo.Mesh.Triangles,
					Painted: fo.Mesh.Painted, Min: fo.Mesh.Min, Max: fo.Mesh.Max}}
			o.Parts = append(o.Parts, part)
		}
		for _, c := range fo.Components {
			file := c.Path
			if file == "" {
				file = "/" + memberModel
			}
			part := &Part{ID: c.ObjectID, Subtype: SubtypeNormal, ComponentTransform: c.Transform, componentUUID: c.UUID, hasComponent: true,
				Mesh: MeshRef{Path: file, ObjectID: c.ObjectID}}
			if sub, err := p.scanMember(strings.TrimPrefix(file, "/")); err == nil {
				if so := sub.object(c.ObjectID); so != nil {
					part.Mesh.Type = so.Type
					part.Name = so.Name
					if so.Mesh != nil {
						part.Mesh.Vertices, part.Mesh.Triangles = so.Mesh.Vertices, so.Mesh.Triangles
						part.Mesh.Painted, part.Mesh.Min, part.Mesh.Max = so.Mesh.Painted, so.Mesh.Min, so.Mesh.Max
					}
				}
			} else {
				p.Warnings = append(p.Warnings, fmt.Sprintf("%s: %v", file, err))
			}
			shared[[2]string{file, strconv.Itoa(c.ObjectID)}]++
			o.Parts = append(o.Parts, part)
		}
		o.backupID = backupIDOf(o)
		if o.backupID > nextBackup {
			nextBackup = o.backupID
		}
		p.Objects = append(p.Objects, o)
	}
	for _, o := range p.Objects {
		for _, part := range o.Parts {
			part.Mesh.Shared = shared[[2]string{part.Mesh.Path, strconv.Itoa(part.Mesh.ObjectID)}] > 1
		}
	}
	for _, it := range main.Items {
		p.Items = append(p.Items, &BuildItem{ObjectID: it.ObjectID, UUID: it.UUID, Path: it.Path, Transform: it.Transform, Printable: it.Printable})
	}

	if m := p.byName[memberModelSettings]; m != nil {
		if err := p.loadModelSettings(); err != nil {
			p.Warnings = append(p.Warnings, fmt.Sprintf("%s: %v (kept as it is)", memberModelSettings, err))
		}
	}
	if data, err := p.Read(memberProjectConfig); err == nil {
		if cfg, err := ParseConfig(data); err == nil {
			p.Settings = cfg
		} else {
			p.Warnings = append(p.Warnings, fmt.Sprintf("%s: %v (kept as it is)", memberProjectConfig, err))
		}
	}
	if data, err := p.Read(memberCreality); err == nil {
		if root, err := parseTree(data); err == nil {
			for _, c := range root.Children {
				if c.Name == "metadata" {
					p.Creality = append(p.Creality, KV{c.attrValue("key"), c.attrValue("value")})
				}
			}
		}
	}
	if data, err := p.Read(memberLayerRanges); err == nil {
		p.hasRanges = true
		if err := p.loadLayerRanges(data); err != nil {
			p.Warnings = append(p.Warnings, fmt.Sprintf("%s: %v (kept as it is)", memberLayerRanges, err))
		}
	}
	if data, err := p.Read(memberCustomGCode); err == nil {
		p.hasGCodes = true
		if err := p.loadGCodes(data); err != nil {
			p.Warnings = append(p.Warnings, fmt.Sprintf("%s: %v (kept as it is)", memberCustomGCode, err))
		}
	}
	for _, m := range p.members {
		base := path.Base(m.name)
		if strings.HasPrefix(m.name, "Metadata/") && strings.HasSuffix(base, ".config") &&
			(strings.HasPrefix(base, "process_settings_") || strings.HasPrefix(base, "filament_settings_") || strings.HasPrefix(base, "machine_settings_")) {
			p.Presets = append(p.Presets, m.name)
		}
	}
	// A project without model_settings (a plain 3MF) still has one instance
	// per build item and one plate.
	if len(p.Plates) == 0 && len(p.Items) > 0 {
		pl := &Plate{Index: 1}
		for i, it := range p.Items {
			pl.Instances = append(pl.Instances, Instance{ObjectID: it.ObjectID, InstanceID: p.instanceIndex(i)})
		}
		p.Plates = append(p.Plates, pl)
	}
	return nil
}

// instanceIndex is the instance number of the build item at index i within
// its object.
func (p *Project) instanceIndex(i int) int {
	n := 0
	for j := 0; j < i; j++ {
		if p.Items[j].ObjectID == p.Items[i].ObjectID {
			n++
		}
	}
	return n
}

// backupIDOf recovers the object's number from the sub model file name of its
// first component, falling back to the id.
func backupIDOf(o *Object) int {
	for _, part := range o.Parts {
		base := path.Base(part.Mesh.Path)
		if strings.HasPrefix(base, "object_") && strings.HasSuffix(base, ".model") {
			if n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(base, "object_"), ".model")); err == nil {
				return n
			}
		}
	}
	return 0
}

func (p *Project) scanMember(name string) (*modelFile, error) {
	if mf, ok := p.objectFiles[name]; ok {
		return mf, nil
	}
	data, err := p.Read(name)
	if err != nil {
		return nil, err
	}
	mf, err := scanModel(data)
	if err != nil {
		return nil, err
	}
	p.objectFiles[name] = mf
	return mf, nil
}

func (p *Project) findObject(id int) *Object {
	for _, o := range p.Objects {
		if o.ID == id {
			return o
		}
	}
	return nil
}

// Object returns the object with the given id, or nil.
func (p *Project) Object(id int) *Object { return p.findObject(id) }

// Plate returns the plate with the given plater_id, or nil.
func (p *Project) Plate(index int) *Plate {
	for _, pl := range p.Plates {
		if pl.Index == index {
			return pl
		}
	}
	return nil
}

// ItemsOf returns the build items of an object in instance order.
func (p *Project) ItemsOf(objectID int) []*BuildItem {
	var out []*BuildItem
	for _, it := range p.Items {
		if it.ObjectID == objectID {
			out = append(out, it)
		}
	}
	return out
}

// loadModelSettings interprets Metadata/model_settings.config.
func (p *Project) loadModelSettings() error {
	data, err := p.Read(memberModelSettings)
	if err != nil {
		return err
	}
	root, err := parseTree(data)
	if err != nil {
		return err
	}
	if root.Name != "config" {
		return fmt.Errorf("root element is %q, not config", root.Name)
	}
	for _, n := range root.Children {
		switch n.Name {
		case "object":
			id, err := strconv.Atoi(n.attrValue("id"))
			if err != nil {
				return fmt.Errorf("object without a numeric id")
			}
			o := p.findObject(id)
			if o == nil {
				// An entry without a resource object: keep it as an extra of the
				// project by adding an object shell so it is not lost.
				o = &Object{ID: id, Type: "model"}
				p.Objects = append(p.Objects, o)
			}
			p.settingsOrder = append(p.settingsOrder, id)
			p.applyObjectSettings(o, n)
		case "plate":
			p.Plates = append(p.Plates, parsePlate(n))
		case "assemble":
			for _, it := range n.Children {
				if it.Name != "assemble_item" {
					continue
				}
				oid, _ := strconv.Atoi(it.attrValue("object_id"))
				iid, _ := strconv.Atoi(it.attrValue("instance_id"))
				p.Assemble = append(p.Assemble, AssembleItem{ObjectID: oid, InstanceID: iid, Attrs: it.Attrs.clone()})
			}
		}
	}
	return nil
}

func (p *Project) applyObjectSettings(o *Object, n *node) {
	o.inSettings = true
	used := map[*Part]bool{}
	for _, c := range n.Children {
		switch c.Name {
		case "metadata":
			k, v := c.attrValue("key"), c.attrValue("value")
			switch k {
			case "name":
				o.Name = v
			case "module":
				o.Module = v
			default:
				o.Config = append(o.Config, KV{k, v})
			}
		case "part":
			id, _ := strconv.Atoi(c.attrValue("id"))
			var part *Part
			for _, cand := range o.Parts {
				if cand.ID == id && !used[cand] {
					part = cand
					break
				}
			}
			if part == nil {
				part = &Part{ID: id, ComponentTransform: mesh.Identity(), Mesh: MeshRef{ObjectID: id}}
				o.Parts = append(o.Parts, part)
			}
			used[part] = true
			part.inSettings = true
			applyPartSettings(part, c)
		default:
			o.extra = append(o.extra, c)
		}
	}
}

func applyPartSettings(part *Part, n *node) {
	part.Subtype = n.attrValue("subtype")
	if part.Subtype == "" {
		part.Subtype = SubtypeNormal
	}
	var src Source
	hasSrc := false
	for _, c := range n.Children {
		switch c.Name {
		case "metadata":
			k, v := c.attrValue("key"), c.attrValue("value")
			switch k {
			case "name":
				part.Name = v
			case "matrix":
				if m, err := mesh.ParseMatrix4(v); err == nil {
					part.Matrix, part.HasMatrix = m, true
				} else {
					part.Config = append(part.Config, KV{k, v})
				}
			case "source_file":
				src.File, hasSrc = v, true
			case "source_object_id":
				src.ObjectID, hasSrc = v, true
			case "source_volume_id":
				src.VolumeID, hasSrc = v, true
			case "source_offset_x":
				src.OffsetX, hasSrc = v, true
			case "source_offset_y":
				src.OffsetY, hasSrc = v, true
			case "source_offset_z":
				src.OffsetZ, hasSrc = v, true
			case "source_in_inches":
				src.InInches, hasSrc = v == "1", true
			case "source_in_meters":
				src.InMeters, hasSrc = v == "1", true
			default:
				part.Config = append(part.Config, KV{k, v})
			}
		case "mesh_stat":
			part.MeshStat = c.Attrs.clone()
		default:
			part.extra = append(part.extra, c)
		}
	}
	if hasSrc {
		part.Source = &src
	}
}

func parsePlate(n *node) *Plate {
	pl := &Plate{}
	for _, c := range n.Children {
		switch c.Name {
		case "metadata":
			k, v := c.attrValue("key"), c.attrValue("value")
			switch k {
			case "plater_id":
				pl.Index, _ = strconv.Atoi(v)
			case "plater_name":
				pl.Name = v
			case "locked":
				pl.Locked = v == "true"
			default:
				pl.Config = append(pl.Config, KV{k, v})
			}
		case "model_instance":
			var in Instance
			for _, m := range c.Children {
				if m.Name != "metadata" {
					continue
				}
				v, _ := strconv.Atoi(m.attrValue("value"))
				switch m.attrValue("key") {
				case "object_id":
					in.ObjectID = v
				case "instance_id":
					in.InstanceID = v
				case "identify_id":
					in.IdentifyID = v
				}
			}
			pl.Instances = append(pl.Instances, in)
		default:
			pl.extra = append(pl.extra, c)
		}
	}
	return pl
}

// loadLayerRanges reads layer_config_ranges.xml. Object ids in the file are
// 1 based positions in the object list.
func (p *Project) loadLayerRanges(data []byte) error {
	root, err := parseTree(data)
	if err != nil {
		return err
	}
	for _, on := range root.Children {
		if on.Name != "object" {
			continue
		}
		idx, err := strconv.Atoi(on.attrValue("id"))
		if err != nil || idx < 1 || idx > len(p.Objects) {
			return fmt.Errorf("layer ranges name object %q, which does not exist", on.attrValue("id"))
		}
		o := p.Objects[idx-1]
		for _, rn := range on.Children {
			if rn.Name != "range" {
				continue
			}
			lo, err1 := strconv.ParseFloat(rn.attrValue("min_z"), 64)
			hi, err2 := strconv.ParseFloat(rn.attrValue("max_z"), 64)
			if err1 != nil || err2 != nil {
				return fmt.Errorf("bad layer range height")
			}
			r := LayerRange{MinZ: lo, MaxZ: hi}
			for _, opt := range rn.Children {
				if opt.Name == "option" {
					r.Options = append(r.Options, KV{opt.attrValue("opt_key"), opt.Text})
				}
			}
			o.LayerRanges = append(o.LayerRanges, r)
		}
	}
	return nil
}

// loadGCodes reads custom_gcode_per_layer.xml.
func (p *Project) loadGCodes(data []byte) error {
	root, err := parseTree(data)
	if err != nil {
		return err
	}
	for _, pn := range root.Children {
		if pn.Name != "plate" {
			continue
		}
		pg := PlateGCodes{Mode: ModeMultiExtruder}
		for _, c := range pn.Children {
			switch c.Name {
			case "plate_info":
				pg.Plate, _ = strconv.Atoi(c.attrValue("id"))
			case "mode":
				pg.Mode = c.attrValue("value")
			case "layer":
				z, err := strconv.ParseFloat(c.attrValue("top_z"), 64)
				if err != nil {
					return fmt.Errorf("bad custom G-code height %q", c.attrValue("top_z"))
				}
				typ, _ := strconv.Atoi(c.attrValue("type"))
				ext, _ := strconv.Atoi(c.attrValue("extruder"))
				pg.Items = append(pg.Items, GCodeItem{TopZ: z, Type: typ, Extruder: ext, Color: c.attrValue("color"), Extra: c.attrValue("extra"), GCode: c.attrValue("gcode")})
			}
		}
		p.GCodes = append(p.GCodes, pg)
	}
	sort.SliceStable(p.GCodes, func(i, j int) bool { return p.GCodes[i].Plate < p.GCodes[j].Plate })
	return nil
}

// LoadMesh reads the geometry of a part from the package. Coordinates are in
// the part's own frame: apply Part.ComponentTransform and the item transform
// to place it.
func (p *Project) LoadMesh(part *Part) (*mesh.Mesh, error) {
	if part.fresh != nil {
		return part.fresh.Clone(), nil
	}
	name := strings.TrimPrefix(part.Mesh.Path, "/")
	rc, err := p.open(name)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	model, err := mesh.ParseModel(rc)
	if err != nil {
		return nil, err
	}
	o := model.Objects[part.Mesh.ObjectID]
	if o == nil || o.Mesh == nil {
		return nil, fmt.Errorf("%w: mesh object %d in %s", ErrNotFound, part.Mesh.ObjectID, name)
	}
	if err := o.Mesh.Validate(); err != nil {
		return nil, err
	}
	return o.Mesh, nil
}
