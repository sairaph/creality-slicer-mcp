package threemf

import (
	"fmt"
	"math"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sairaph/creality-slicer-mcp/internal/mesh"
)

// NewOptions configures New.
type NewOptions struct {
	// AppVersion is the installed slicer's full version, "7.2.2.5483": the
	// 7.2 CLI recognises a project by the "Creality_Print V" prefix of the
	// Application metadata.
	AppVersion string
	// Stage is "Release" (default).
	Stage string
	// Now is the clock for the dates; default time.Now.
	Now func() time.Time
}

// New starts an empty project with one plate, written exactly like a project
// saved by Creality Print 7.2 (split objects under 3D/Objects, production
// extension UUIDs, creality.config, header only slice_info.config).
func New(o NewOptions) *Project {
	if o.Stage == "" {
		o.Stage = "Release"
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	date := o.Now().Format("2006-01-02")
	p := &Project{
		byName: map[string]*member{}, Now: o.Now, isNew: true, objectFiles: map[string]*modelFile{},
		rootTag: defaultRootTag, buildUUID: buildUUID,
		modelDirty: true, settingsDirty: true, gcodesDirty: true, IsSlicerProject: true,
	}
	p.Metadata = KVs{
		{"Application", "Creality_Print V" + o.AppVersion + " " + o.Stage},
		{"BambuStudio:3mfVersion", "1"},
		{"Copyright", ""},
		{"CreationDate", date},
		{"Description", ""},
		{"Designer", ""},
		{"DesignerCover", ""},
		{"DesignerUserId", ""},
		{"License", ""},
		{"ModificationDate", date},
		{"Origin", ""},
		{"Title", ""},
	}
	p.Creality = KVs{
		{"Company", "Creality"}, {"Application", "Creality_Print"}, {"AppVersion", o.AppVersion},
		{"AppStage", o.Stage}, {"FileVersion", "1.0"}, {"FileType", "Undefined"}, {"CreationDate", date},
	}
	p.Plates = []*Plate{{Index: 1}}
	p.GCodes = []PlateGCodes{{Plate: 1, Mode: ModeSingleExtruder}}
	return p
}

func (p *Project) requireSplit() error { return p.ensureSplit() }

// MarkModified forces every generated member to be rewritten on Save. Use it
// after changing exported fields directly instead of through the methods.
func (p *Project) MarkModified() {
	p.modelDirty, p.settingsDirty, p.gcodesDirty = true, true, true
	p.rangesDirty = p.hasRanges
}

// SetMetadata sets a <metadata> entry of 3dmodel.model (Title, Designer,
// Description, ...).
func (p *Project) SetMetadata(key, value string) error {
	if err := checkText("metadata", key, value); err != nil {
		return err
	}
	if key == "" || strings.ContainsAny(key, "\"<>&") {
		return fmt.Errorf("%w: metadata key %q", ErrInvalid, key)
	}
	if err := p.requireSplit(); err != nil {
		return err
	}
	p.Metadata.Set(key, value)
	p.modelDirty = true
	return nil
}

// SetSettings replaces the project_settings.config content.
func (p *Project) SetSettings(c *Config) {
	if c != nil {
		c.dirty = true
	}
	p.Settings = c
}

// maxID is the largest object or part id in use.
func (p *Project) maxID() int {
	max := 0
	for _, o := range p.Objects {
		if o.ID > max {
			max = o.ID
		}
		for _, part := range o.Parts {
			if part.ID > max {
				max = part.ID
			}
		}
	}
	return max
}

// nextBackupID is the number for the next sub model file.
func (p *Project) nextBackupID() int {
	max := 0
	for _, o := range p.Objects {
		if o.backupID > max {
			max = o.backupID
		}
		for _, part := range o.Parts {
			if n := fileNumber(part.Mesh.Path); n > max {
				max = n
			}
		}
	}
	for _, m := range p.members {
		if n := fileNumber(m.name); n > max {
			max = n
		}
	}
	return max + 1
}

func fileNumber(name string) int {
	base := path.Base(name)
	if !strings.HasPrefix(base, "object_") || !strings.HasSuffix(base, ".model") {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(base, "object_"), ".model"))
	return n
}

// NextIdentifyID returns an identify id no instance uses yet (ids are
// unique and increasing; `--skip-objects` takes them).
func (p *Project) NextIdentifyID() int {
	max := 0
	for _, pl := range p.Plates {
		for _, in := range pl.Instances {
			if in.IdentifyID > max {
				max = in.IdentifyID
			}
		}
	}
	return max + 1
}

// ObjectSpec describes an object to add.
type ObjectSpec struct {
	Name string
	Mesh *mesh.Mesh
	// Transform places the mesh as given in bed coordinates; nil is the
	// identity. The mesh is stored centred on its bounding box (like the app
	// does) and the transform is adjusted so the placement is the same.
	Transform *mesh.Matrix
	// Extruder is the filament number (1 based); 0 leaves it unset.
	Extruder int
	// Plate is the plate index (1 based); 0 means plate 1.
	Plate int
	// Config holds further object overrides.
	Config KVs
}

func validateMesh(m *mesh.Mesh) error {
	if m == nil || len(m.Triangles) == 0 {
		return fmt.Errorf("%w: the mesh has no triangles", ErrInvalid)
	}
	if err := m.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	for _, v := range m.Vertices {
		for _, c := range v {
			if math.IsNaN(float64(c)) || math.IsInf(float64(c), 0) {
				return fmt.Errorf("%w: the mesh has a non finite coordinate", ErrInvalid)
			}
		}
	}
	return nil
}

func refOf(m *mesh.Mesh, filePath string, id int, typ string) MeshRef {
	bb, _ := m.BBox()
	return MeshRef{Path: filePath, ObjectID: id, Type: typ, Vertices: len(m.Vertices), Triangles: len(m.Triangles), Min: bb.Min, Max: bb.Max}
}

// AddObject adds an object with one normal part, one build item and one
// instance on a plate.
func (p *Project) AddObject(spec ObjectSpec) (*Object, error) {
	if err := checkText("object name", spec.Name); err != nil {
		return nil, err
	}
	for _, kv := range spec.Config {
		if err := checkText("setting", kv.Key, kv.Value); err != nil {
			return nil, err
		}
	}
	if err := validateMesh(spec.Mesh); err != nil {
		return nil, err
	}
	plateIdx := spec.Plate
	if plateIdx == 0 {
		plateIdx = 1
	}
	plate := p.Plate(plateIdx)
	if plate == nil {
		return nil, fmt.Errorf("%w: plate %d", ErrNotFound, plateIdx)
	}
	for _, kv := range spec.Config {
		if err := checkOverrideKey(kv.Key); err != nil {
			return nil, err
		}
	}
	if err := p.requireSplit(); err != nil {
		return nil, err
	}
	tr := mesh.Identity()
	if spec.Transform != nil {
		tr = *spec.Transform
	}
	bb, _ := spec.Mesh.BBox()
	c := bb.Center()
	centred := spec.Mesh.Transformed(mesh.Translate(-float64(c[0]), -float64(c[1]), -float64(c[2])))
	item := mesh.Translate(float64(c[0]), float64(c[1]), float64(c[2])).Then(tr)

	partID := p.maxID() + 1
	backup := p.nextBackupID()
	o := &Object{ID: partID + 1, Type: "model", Name: spec.Name, backupID: backup, inSettings: true}
	o.UUID = hex8(backup) + objectUUIDSuffix
	for _, kv := range spec.Config {
		o.Config.Set(kv.Key, kv.Value)
	}
	if spec.Extruder > 0 {
		o.Config.Set("extruder", strconv.Itoa(spec.Extruder))
	}
	filePath := "/3D/Objects/object_" + strconv.Itoa(backup) + ".model"
	o.Parts = []*Part{{
		ID: partID, Subtype: SubtypeNormal, Name: spec.Name, Matrix: mesh.Identity4(), HasMatrix: true,
		ComponentTransform: mesh.Identity(), Mesh: refOf(centred, filePath, partID, "model"), fresh: centred, hasComponent: true, inSettings: true,
	}}
	p.Objects = append(p.Objects, o)
	p.Items = append(p.Items, &BuildItem{ObjectID: o.ID, UUID: hex8(o.ID) + buildUUIDSuffix, Transform: item, Printable: true})
	plate.Instances = append(plate.Instances, Instance{ObjectID: o.ID, InstanceID: 0, IdentifyID: p.NextIdentifyID()})
	p.ResetSliceResult(plateIdx)
	p.modelDirty, p.settingsDirty = true, true
	return o, nil
}

// RemoveObject removes an object with all its instances, its mesh files
// (when nothing else uses them), its height ranges and its assembly items.
func (p *Project) RemoveObject(id int) error {
	idx := -1
	for i, o := range p.Objects {
		if o.ID == id {
			idx = i
		}
	}
	if idx < 0 {
		return fmt.Errorf("%w: object %d", ErrNotFound, id)
	}
	if err := p.requireSplit(); err != nil {
		return err
	}
	id = p.Objects[idx].ID
	reset := p.platesOf(id)
	gone := p.Objects[idx]
	p.Objects = append(p.Objects[:idx], p.Objects[idx+1:]...)
	items := p.Items[:0]
	for _, it := range p.Items {
		if it.ObjectID != id {
			items = append(items, it)
		}
	}
	p.Items = items
	for _, pl := range p.Plates {
		kept := pl.Instances[:0]
		for _, in := range pl.Instances {
			if in.ObjectID != id {
				kept = append(kept, in)
			}
		}
		pl.Instances = kept
	}
	asm := p.Assemble[:0]
	for _, a := range p.Assemble {
		if a.ObjectID != id {
			asm = append(asm, a)
		}
	}
	p.Assemble = asm
	p.pruneMeshFiles(gone)
	p.modelDirty, p.settingsDirty = true, true
	for _, idx := range reset {
		p.ResetSliceResult(idx)
	}
	if len(gone.LayerRanges) > 0 || p.hasRanges {
		p.rangesDirty = true
	}
	return nil
}

// pruneUnreferenced drops from a sub model file the mesh objects that no part
// points at any more (a removed part leaves its mesh behind in a file other parts
// still use).
func (p *Project) pruneUnreferenced(name string) {
	ref := map[int]bool{}
	for _, o := range p.Objects {
		for _, part := range o.Parts {
			if strings.TrimPrefix(part.Mesh.Path, "/") == name {
				ref[part.Mesh.ObjectID] = true
			}
		}
	}
	data, err := p.Read(name)
	if err != nil {
		return
	}
	text := string(data)
	changed := false
	for {
		i := objectBlockStart(text, ref)
		if i < 0 {
			break
		}
		end := strings.Index(text[i:], "</object>")
		if end < 0 {
			break
		}
		end += i + len("</object>")
		for end < len(text) && (text[end] == '\n' || text[end] == '\r') {
			end++
			break
		}
		// take the indentation before the tag too
		start := i
		for start > 0 && (text[start-1] == ' ' || text[start-1] == '\t') {
			start--
		}
		text = text[:start] + text[end:]
		changed = true
	}
	if changed {
		p.setMember(name, []byte(text))
		delete(p.objectFiles, name)
	}
}

// objectBlockStart finds the next <object ...> start tag in a model file whose
// id is not in keep, or -1.
func objectBlockStart(text string, keep map[int]bool) int {
	pos := 0
	for {
		i := strings.Index(text[pos:], "<object ")
		if i < 0 {
			return -1
		}
		i += pos
		tagEnd := strings.Index(text[i:], ">")
		if tagEnd < 0 {
			return -1
		}
		tag := text[i : i+tagEnd]
		id := -1
		if j := strings.Index(tag, " id=\""); j >= 0 {
			rest := tag[j+5:]
			if k := strings.Index(rest, "\""); k >= 0 {
				fmt.Sscanf(rest[:k], "%d", &id)
			}
		}
		if !keep[id] {
			return i
		}
		pos = i + tagEnd
	}
}

// pruneMeshFiles removes the sub model files a removed object used, unless a
// remaining part still uses them (shared meshes), and its empty stub file.
func (p *Project) pruneMeshFiles(gone *Object) {
	used := map[string]bool{}
	for _, o := range p.Objects {
		for _, part := range o.Parts {
			used[strings.TrimPrefix(part.Mesh.Path, "/")] = true
		}
	}
	candidates := map[string]bool{}
	for _, part := range gone.Parts {
		candidates[strings.TrimPrefix(part.Mesh.Path, "/")] = true
	}
	if gone.backupID > 0 {
		candidates["3D/Objects/object_"+strconv.Itoa(gone.backupID)+".model"] = true
	}
	for name := range candidates {
		if !strings.HasPrefix(name, "3D/Objects/") {
			continue
		}
		if used[name] {
			p.pruneUnreferenced(name) // a shared file keeps only the meshes some part still names
			continue
		}
		p.removeMember(name)
	}
	// Empty stub files (the app writes one per object, objects that share a
	// mesh get an empty one) that no remaining object could own go too.
	owners := map[int]bool{}
	for _, o := range p.Objects {
		owners[o.backupID] = true
	}
	for _, name := range p.objectFileNames() {
		if used[name] || owners[fileNumber(name)] {
			continue
		}
		if mf, err := p.scanMember(name); err == nil {
			stub := true
			for _, fo := range mf.Objects {
				if fo.Mesh != nil {
					stub = false
				}
			}
			if stub {
				p.removeMember(name)
			}
		}
	}
}

// PartSpec describes a part to add to an object.
type PartSpec struct {
	Subtype string // SubtypeNormal ... SubtypeSupportBlocker; default modifier
	Name    string
	Mesh    *mesh.Mesh
	// Transform places the part in the object's frame (the object's own,
	// centred coordinates); nil is the identity.
	Transform *mesh.Matrix
	// Config holds the part's overrides (a modifier's settings, "extruder").
	Config KVs
}

func validSubtype(s string) bool {
	switch s {
	case SubtypeNormal, SubtypeNegative, SubtypeModifier, SubtypeSupportEnforcer, SubtypeSupportBlocker:
		return true
	}
	return false
}

// AddPart adds a part (modifier, blocker, enforcer, negative or normal part)
// to an object.
func (p *Project) AddPart(objectID int, spec PartSpec) (*Part, error) {
	if err := checkText("part name", spec.Name); err != nil {
		return nil, err
	}
	for _, kv := range spec.Config {
		if err := checkText("setting", kv.Key, kv.Value); err != nil {
			return nil, err
		}
	}
	o := p.findObject(objectID)
	if o == nil {
		return nil, fmt.Errorf("%w: object %d", ErrNotFound, objectID)
	}
	if spec.Subtype == "" {
		spec.Subtype = SubtypeModifier
	}
	if !validSubtype(spec.Subtype) {
		return nil, fmt.Errorf("%w: part subtype %q", ErrInvalid, spec.Subtype)
	}
	if err := validateMesh(spec.Mesh); err != nil {
		return nil, err
	}
	for _, kv := range spec.Config {
		if err := checkOverrideKey(kv.Key); err != nil {
			return nil, err
		}
	}
	if err := p.requireSplit(); err != nil {
		return nil, err
	}
	tr := mesh.Identity()
	if spec.Transform != nil {
		tr = *spec.Transform
	}
	id := p.maxID() + 1
	backup := p.nextBackupID()
	if o.backupID == 0 {
		o.backupID = backup
	}
	typ := "model"
	if spec.Subtype != SubtypeNormal {
		typ = "other"
	}
	filePath := "/3D/Objects/object_" + strconv.Itoa(backup) + ".model"
	part := &Part{
		ID: id, Subtype: spec.Subtype, Name: spec.Name, Matrix: tr.To4(), HasMatrix: true, ComponentTransform: tr,
		Mesh: refOf(spec.Mesh, filePath, id, typ), fresh: spec.Mesh.Clone(), hasComponent: true, inSettings: true,
	}
	for _, kv := range spec.Config {
		part.Config.Set(kv.Key, kv.Value)
	}
	o.Parts = append(o.Parts, part)
	p.modelDirty, p.settingsDirty = true, true
	p.resetPlatesOf(o.ID)
	return part, nil
}

// RemovePart removes a part; an object keeps at least one part.
func (p *Project) RemovePart(objectID, partID int) error {
	o := p.findObject(objectID)
	if o == nil {
		return fmt.Errorf("%w: object %d", ErrNotFound, objectID)
	}
	idx := -1
	for i, part := range o.Parts {
		if part.ID == partID {
			idx = i
		}
	}
	if idx < 0 {
		return fmt.Errorf("%w: part %d of object %d", ErrNotFound, partID, objectID)
	}
	if len(o.Parts) == 1 {
		return fmt.Errorf("%w: an object needs at least one part; remove the object instead", ErrInvalid)
	}
	if err := p.requireSplit(); err != nil {
		return err
	}
	part := o.Parts[idx]
	o.Parts = append(o.Parts[:idx], o.Parts[idx+1:]...)
	p.pruneMeshFiles(&Object{Parts: []*Part{part}})
	p.modelDirty, p.settingsDirty = true, true
	p.resetPlatesOf(o.ID)
	return nil
}

func checkOverrideKey(key string) error {
	if key == "" || strings.ContainsAny(key, "\"<>&' \t\r\n") {
		return fmt.Errorf("%w: setting key %q", ErrInvalid, key)
	}
	switch key {
	case "name", "module", "matrix", "subtype", "id":
		return fmt.Errorf("%w: %q is not a setting override", ErrInvalid, key)
	}
	return nil
}

// SetObjectOverride sets a per object setting ("extruder", "wall_loops", ...).
func (p *Project) SetObjectOverride(objectID int, key, value string) error {
	if err := checkText("setting", key, value); err != nil {
		return err
	}
	o := p.findObject(objectID)
	if o == nil {
		return fmt.Errorf("%w: object %d", ErrNotFound, objectID)
	}
	if err := checkOverrideKey(key); err != nil {
		return err
	}
	if err := p.requireSplit(); err != nil {
		return err
	}
	o.Config.Set(key, value)
	p.settingsDirty = true
	p.resetPlatesOf(o.ID)
	return nil
}

// DeleteObjectOverride removes a per object setting; it reports whether it existed.
func (p *Project) DeleteObjectOverride(objectID int, key string) (bool, error) {
	o := p.findObject(objectID)
	if o == nil {
		return false, fmt.Errorf("%w: object %d", ErrNotFound, objectID)
	}
	if _, ok := o.Config.Get(key); !ok {
		return false, nil
	}
	if err := p.requireSplit(); err != nil {
		return false, err
	}
	o.Config.Delete(key)
	p.settingsDirty = true
	p.resetPlatesOf(o.ID)
	return true, nil
}

// RenameObject changes the object's name. A rename does not change the slice.
func (p *Project) RenameObject(objectID int, name string) error {
	if err := checkText("object name", name); err != nil {
		return err
	}
	o := p.findObject(objectID)
	if o == nil {
		return fmt.Errorf("%w: object %d", ErrNotFound, objectID)
	}
	if err := p.requireSplit(); err != nil {
		return err
	}
	o.Name = name
	p.settingsDirty = true
	return nil
}

// SetPartOverride sets a setting of one part (a modifier's settings).
func (p *Project) SetPartOverride(objectID, partID int, key, value string) error {
	if err := checkText("setting", key, value); err != nil {
		return err
	}
	o, part, err := p.partOf(objectID, partID)
	if err != nil {
		return err
	}
	if err := checkOverrideKey(key); err != nil {
		return err
	}
	if err := p.requireSplit(); err != nil {
		return err
	}
	part.Config.Set(key, value)
	p.settingsDirty = true
	p.resetPlatesOf(o.ID)
	return nil
}

// DeletePartOverride removes a setting of one part.
func (p *Project) DeletePartOverride(objectID, partID int, key string) (bool, error) {
	o, part, err := p.partOf(objectID, partID)
	if err != nil {
		return false, err
	}
	if _, ok := part.Config.Get(key); !ok {
		return false, nil
	}
	if err := p.requireSplit(); err != nil {
		return false, err
	}
	part.Config.Delete(key)
	p.settingsDirty = true
	p.resetPlatesOf(o.ID)
	return true, nil
}

func (p *Project) partOf(objectID, partID int) (*Object, *Part, error) {
	o := p.findObject(objectID)
	if o == nil {
		return nil, nil, fmt.Errorf("%w: object %d", ErrNotFound, objectID)
	}
	part := o.Part(partID)
	if part == nil {
		return nil, nil, fmt.Errorf("%w: part %d of object %d", ErrNotFound, partID, objectID)
	}
	return o, part, nil
}

// SetTransform moves an instance: the build item transform of the object's
// instance (0 based) in bed coordinates.
func (p *Project) SetTransform(objectID, instanceID int, t mesh.Matrix) error {
	items := p.ItemsOf(objectID)
	if instanceID < 0 || instanceID >= len(items) {
		return fmt.Errorf("%w: instance %d of object %d", ErrNotFound, instanceID, objectID)
	}
	item := items[instanceID]
	plate := p.PlateOf(objectID, instanceID)
	if err := p.requireSplit(); err != nil {
		return err
	}
	item.Transform = t
	p.modelDirty = true
	if plate != nil {
		p.ResetSliceResult(plate.Index)
	}
	return nil
}

// SetLayerRanges replaces the height ranges of an object. A range is
// [MinZ, MaxZ) with option overrides (layer_height, ...).
func (p *Project) SetLayerRanges(objectID int, ranges []LayerRange) error {
	for _, r := range ranges {
		for _, kv := range r.Options {
			if err := checkText("setting", kv.Key, kv.Value); err != nil {
				return err
			}
		}
	}
	o := p.findObject(objectID)
	if o == nil {
		return fmt.Errorf("%w: object %d", ErrNotFound, objectID)
	}
	sorted := append([]LayerRange(nil), ranges...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].MinZ < sorted[j].MinZ })
	for i, r := range sorted {
		if !(r.MinZ >= 0) || !(r.MaxZ > r.MinZ) || math.IsInf(r.MaxZ, 0) {
			return fmt.Errorf("%w: height range %g to %g", ErrInvalid, r.MinZ, r.MaxZ)
		}
		if i > 0 && r.MinZ < sorted[i-1].MaxZ {
			return fmt.Errorf("%w: height ranges %g-%g and %g-%g overlap", ErrInvalid, sorted[i-1].MinZ, sorted[i-1].MaxZ, r.MinZ, r.MaxZ)
		}
		if len(r.Options) == 0 {
			return fmt.Errorf("%w: height range %g to %g has no settings", ErrInvalid, r.MinZ, r.MaxZ)
		}
		for _, opt := range r.Options {
			if err := checkOverrideKey(opt.Key); err != nil {
				return err
			}
		}
	}
	if err := p.requireSplit(); err != nil {
		return err
	}
	o.LayerRanges = sorted
	p.rangesDirty = true
	p.hasRanges = true
	p.resetPlatesOf(o.ID)
	return nil
}

// SetCustomGCodes replaces the custom G-code items (color changes, pauses,
// tool changes, template and custom G-code) and the mode of a plate.
func (p *Project) SetCustomGCodes(plate int, mode string, items []GCodeItem) error {
	for _, it := range items {
		if err := checkText("custom G-code", it.Color, it.Extra, it.GCode); err != nil {
			return err
		}
	}
	if p.Plate(plate) == nil {
		return fmt.Errorf("%w: plate %d", ErrNotFound, plate)
	}
	switch mode {
	case ModeSingleExtruder, ModeMultiAsSingle, ModeMultiExtruder:
	default:
		return fmt.Errorf("%w: custom G-code mode %q", ErrInvalid, mode)
	}
	for _, it := range items {
		if it.Type < GCodeColorChange || it.Type > GCodeCustom {
			return fmt.Errorf("%w: custom G-code type %d", ErrInvalid, it.Type)
		}
		if !(it.TopZ > 0) || math.IsInf(it.TopZ, 0) {
			return fmt.Errorf("%w: custom G-code height %g", ErrInvalid, it.TopZ)
		}
	}
	sorted := append([]GCodeItem(nil), items...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].TopZ < sorted[j].TopZ })
	p.syncGCodes()
	for i := range p.GCodes {
		if p.GCodes[i].Plate == plate {
			p.GCodes[i].Mode, p.GCodes[i].Items = mode, sorted
		}
	}
	p.gcodesDirty, p.hasGCodes = true, true
	p.ResetSliceResult(plate)
	return nil
}

// CustomGCodes returns the custom G-code state of a plate.
func (p *Project) CustomGCodes(plate int) PlateGCodes {
	for _, g := range p.GCodes {
		if g.Plate == plate {
			return g
		}
	}
	return PlateGCodes{Plate: plate, Mode: ModeMultiExtruder}
}

// syncGCodes makes sure every plate has an entry and no removed plate does.
func (p *Project) syncGCodes() {
	have := map[int]PlateGCodes{}
	for _, g := range p.GCodes {
		have[g.Plate] = g
	}
	var out []PlateGCodes
	for _, pl := range p.Plates {
		g, ok := have[pl.Index]
		if !ok {
			g = PlateGCodes{Plate: pl.Index, Mode: ModeSingleExtruder}
		}
		out = append(out, g)
	}
	p.GCodes = out
}

// AddPlate appends an empty plate and returns it.
func (p *Project) AddPlate(name string) *Plate {
	name = stripIllegalXML(name) // no error to return here: what XML cannot hold is dropped
	max := 0
	for _, pl := range p.Plates {
		if pl.Index > max {
			max = pl.Index
		}
	}
	pl := &Plate{Index: max + 1, Name: name}
	p.Plates = append(p.Plates, pl)
	p.syncGCodes()
	p.settingsDirty, p.gcodesDirty = true, true
	return pl
}

// RemovePlate removes an empty plate and renumbers the later ones so plate
// numbers stay 1..n. The removed plate's images and slice result go with it;
// the later plates keep their images (renamed) but lose their slice result,
// whose file names carry the plate number.
func (p *Project) RemovePlate(index int) error {
	pl := p.Plate(index)
	if pl == nil {
		return fmt.Errorf("%w: plate %d", ErrNotFound, index)
	}
	if len(pl.Instances) > 0 {
		return fmt.Errorf("%w: plate %d still holds %d object(s); move or remove them first", ErrInvalid, index, len(pl.Instances))
	}
	if len(p.Plates) == 1 {
		return fmt.Errorf("%w: a project needs at least one plate", ErrInvalid)
	}
	p.syncGCodes()
	// Results and images of the removed plate and of every plate after it.
	for _, q := range p.Plates {
		if q.Index > index {
			p.ResetSliceResult(q.Index)
		}
	}
	p.removePlateMembers(index)
	var plates []*Plate
	var codes []PlateGCodes
	byPlate := map[int]PlateGCodes{}
	for _, g := range p.GCodes {
		byPlate[g.Plate] = g
	}
	for _, q := range p.Plates {
		if q == pl {
			continue
		}
		g := byPlate[q.Index]
		newIndex := len(plates) + 1
		if q.Index != newIndex {
			p.renamePlateMembers(q.Index, newIndex)
			for i := range q.Config {
				if strings.Contains(q.Config[i].Value, "_"+strconv.Itoa(q.Index)+".") {
					q.Config[i].Value = strings.Replace(q.Config[i].Value, "_"+strconv.Itoa(q.Index)+".", "_"+strconv.Itoa(newIndex)+".", 1)
				} else if strings.Contains(q.Config[i].Value, "plate_"+strconv.Itoa(q.Index)) {
					q.Config[i].Value = strings.Replace(q.Config[i].Value, "plate_"+strconv.Itoa(q.Index), "plate_"+strconv.Itoa(newIndex), 1)
				}
			}
		}
		q.Index, g.Plate = newIndex, newIndex
		plates = append(plates, q)
		codes = append(codes, g)
	}
	p.Plates, p.GCodes = plates, codes
	p.settingsDirty, p.gcodesDirty = true, true
	return nil
}

// RenamePlate sets the plate name.
func (p *Project) RenamePlate(index int, name string) error {
	if err := checkText("plate name", name); err != nil {
		return err
	}
	pl := p.Plate(index)
	if pl == nil {
		return fmt.Errorf("%w: plate %d", ErrNotFound, index)
	}
	pl.Name = name
	p.settingsDirty = true
	return nil
}

// LockPlate locks or unlocks a plate.
func (p *Project) LockPlate(index int, locked bool) error {
	pl := p.Plate(index)
	if pl == nil {
		return fmt.Errorf("%w: plate %d", ErrNotFound, index)
	}
	pl.Locked = locked
	p.settingsDirty = true
	return nil
}

// SetPlateKey sets a per plate key (bed_type, print_sequence,
// first_layer_print_sequence, other_layers_print_sequence,
// other_layers_print_sequence_nums, spiral_mode). The identity keys have their
// own methods and the image keys are written by SetPlateThumbnails.
func (p *Project) SetPlateKey(index int, key, value string) error {
	if err := checkText("setting", key, value); err != nil {
		return err
	}
	pl := p.Plate(index)
	if pl == nil {
		return fmt.Errorf("%w: plate %d", ErrNotFound, index)
	}
	switch key {
	case "", "plater_id", "plater_name", "locked":
		return fmt.Errorf("%w: %q is not a plate setting", ErrInvalid, key)
	}
	if strings.ContainsAny(key, "\"<>&' \t\r\n") {
		return fmt.Errorf("%w: plate key %q", ErrInvalid, key)
	}
	pl.Config.Set(key, value)
	p.settingsDirty = true
	if !plateKeysThatDoNotChangeTheSlice[key] {
		p.ResetSliceResult(index)
	}
	return nil
}

// DeletePlateKey removes a per plate key.
func (p *Project) DeletePlateKey(index int, key string) (bool, error) {
	pl := p.Plate(index)
	if pl == nil {
		return false, fmt.Errorf("%w: plate %d", ErrNotFound, index)
	}
	ok := pl.Config.Delete(key)
	if ok {
		p.settingsDirty = true
		if !plateKeysThatDoNotChangeTheSlice[key] {
			p.ResetSliceResult(index)
		}
	}
	return ok, nil
}

// MoveInstance moves an object instance to another plate.
func (p *Project) MoveInstance(objectID, instanceID, toPlate int) error {
	dst := p.Plate(toPlate)
	if dst == nil {
		return fmt.Errorf("%w: plate %d", ErrNotFound, toPlate)
	}
	for _, pl := range p.Plates {
		for i, in := range pl.Instances {
			if in.ObjectID == objectID && in.InstanceID == instanceID {
				if pl == dst {
					return nil
				}
				pl.Instances = append(pl.Instances[:i], pl.Instances[i+1:]...)
				dst.Instances = append(dst.Instances, in)
				p.settingsDirty = true
				p.ResetSliceResult(pl.Index)
				p.ResetSliceResult(dst.Index)
				return nil
			}
		}
	}
	return fmt.Errorf("%w: instance %d of object %d", ErrNotFound, instanceID, objectID)
}

// PlateOf returns the plate an instance is on, or nil.
func (p *Project) PlateOf(objectID, instanceID int) *Plate {
	for _, pl := range p.Plates {
		for _, in := range pl.Instances {
			if in.ObjectID == objectID && in.InstanceID == instanceID {
				return pl
			}
		}
	}
	return nil
}
