package projects

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/sairaph/creality-slicer-mcp/internal/catalog"
	"github.com/sairaph/creality-slicer-mcp/internal/mesh"
	"github.com/sairaph/creality-slicer-mcp/internal/threemf"
)

// Placement rules of add_model: objects keep this gap between them, this
// margin from the bed edge and stay clear of the wipe tower.
const (
	PlacementGap    = 5.0
	PlacementMargin = 10.0
)

// AddModelRequest is add_model.
type AddModelRequest struct {
	Path string // .stl, .obj or .3mf (absolute)
	Name string // default: the file name
	// Copies is how many objects to add (default 1).
	Copies int
	// Filament is the slot the object prints with (1 based, default 1).
	Filament int
	// Plate is the plate to put it on; 0 uses the first plate it fits on.
	Plate int
	// X and Y place the centre of the bounding box; nil places automatically.
	X, Y *float64
	// Z lifts the lowest point off the bed (default 0).
	Z *float64
	// Rotation is Euler degrees applied about X, then Y, then Z.
	Rotation [3]float64
	// Scale per axis; a zero axis means 1. ScaleAll multiplies all three.
	Scale    [3]float64
	ScaleAll float64
	// LayFlat puts the model on its largest flat face first.
	LayFlat bool
	// Overrides are per object settings.
	Overrides map[string]any
	// Objects names the objects of a .3mf input to take (case insensitive); empty
	// takes them all. An unknown name is an error that lists the names of the file.
	Objects []string
}

// AddModelResult reports the objects that were added.
type AddModelResult struct {
	Info     *Info
	Added    []ObjectInfo
	Warnings []string
}

// loadModel reads the meshes of a model file.
func loadModel(path string) ([]mesh.Placed, error) {
	if !filepath.IsAbs(path) {
		return nil, invalidf("pass an absolute path to an existing .stl, .obj or .3mf file", "%q is not an absolute path", path)
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, invalidf("pass an absolute path to an existing .stl, .obj or .3mf file", "the model file %q does not exist", path)
		}
		return nil, invalidf("pass an absolute path to an existing .stl, .obj or .3mf file", "the model file %q cannot be read: %v", path, err)
	}
	var (
		m   *mesh.Mesh
		err error
	)
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	switch strings.ToLower(filepath.Ext(path)) {
	case ".stl":
		m, err = mesh.ReadSTL(path)
	case ".obj":
		m, err = mesh.ReadOBJ(path)
	case ".3mf":
		items, e := mesh.Read3MF(path)
		if e != nil {
			return nil, invalidf("", "%q is not a readable 3MF: %v", path, e)
		}
		if len(items) == 0 {
			return nil, invalidf("", "%q holds no models", path)
		}
		// A slicer project keeps the object names in its own settings, not in the
		// model file: take them from there when the items line up.
		if sp, oerr := threemf.Open(path); oerr == nil {
			if sp.IsSlicerProject && len(sp.Items) == len(items) {
				for i, it := range sp.Items {
					if o := sp.Object(it.ObjectID); o != nil && o.Name != "" {
						items[i].Name = o.Name
					}
				}
			}
			sp.Close()
		}
		return items, nil
	default:
		return nil, invalidf("add_model reads .stl, .obj and .3mf files", "%q is not a supported model file", path)
	}
	if err != nil {
		return nil, invalidf("", "%q is not a readable model: %v", path, err)
	}
	return []mesh.Placed{{Name: name, Mesh: m}}, nil
}

// occupied returns the footprints of the instances on a plate, except the
// given instance.
func (h *handle) occupied(plate, skipObject, skipInstance int) []rect {
	pl := h.p.Plate(plate)
	if pl == nil {
		return nil
	}
	var out []rect
	var heights []float64
	for _, in := range pl.Instances {
		if in.ObjectID == skipObject && in.InstanceID == skipInstance {
			continue
		}
		o := h.p.Object(in.ObjectID)
		items := h.p.ItemsOf(in.ObjectID)
		if o == nil || in.InstanceID < 0 || in.InstanceID >= len(items) {
			continue
		}
		m, err := h.objectMesh(o)
		if err != nil {
			continue
		}
		if b, ok := bboxOf(m, h.itemT(in.ObjectID, in.InstanceID)); ok {
			out = append(out, footprint(b))
			heights = append(heights, float64(b.Max[2]))
		}
	}
	h.takenHeights = heights // aligned with the result, for findSpot
	return out
}

// findSpot looks for a place for a w by d footprint on a plate: the free spot
// nearest the bed centre, spiralling outward (margin from the edge, gap to
// the other objects, wipe tower kept free). It returns the centre.
func (h *handle) findSpot(plate int, w, d float64, taken []rect) (cx, cy float64, ok bool) {
	usable := h.geometry().rect()
	usable = rect{usable.x0 + PlacementMargin, usable.y0 + PlacementMargin, usable.x1 - PlacementMargin, usable.y1 - PlacementMargin}
	obstacles := append([]rect(nil), taken...)
	if t, has := h.towerRect(plate); has {
		obstacles = append(obstacles, t)
	}
	// Objects printed one after another keep the printer's clearance between them.
	gap := math.Max(PlacementGap, h.seqGap(plate))
	// By object, an object taller than the rod height cannot share a Y band with
	// another one (the slicer's too-tall rule): keep the bands apart.
	byObject := h.plateSequence(plate) == "by object"
	rod := cfgFloat(h, "extruder_clearance_height_to_rod", 1e9)
	half := math.Max(0, (gap-1)/2)
	tallNew := byObject && h.placeHeight > rod
	heights := h.takenHeights
	rodOK := func(c rect) bool {
		if !byObject {
			return true
		}
		for i, t := range taken {
			tall := tallNew || (i < len(heights) && len(heights) == len(taken) && heights[i] > rod)
			if tall && c.y0 < t.y1+half && t.y0 < c.y1+half {
				return false
			}
		}
		return true
	}
	// Candidate left and bottom edges: the bed centre, the usable edges, and
	// the spots touching each obstacle's clearance zone on either side. Every
	// combination is tried nearest to the bed centre first, so the first object
	// sits at the centre and later ones spiral outward around it.
	ccx, ccy := (usable.x0+usable.x1)/2, (usable.y0+usable.y1)/2
	xs := []float64{ccx - w/2, usable.x0, usable.x1 - w}
	ys := []float64{ccy - d/2, usable.y0, usable.y1 - d}
	for _, r := range obstacles {
		xs = append(xs, r.x1+gap, r.x0-gap-w)
		ys = append(ys, r.y1+gap, r.y0-gap-d)
	}
	type cand struct{ x, y, dist float64 }
	var cands []cand
	for _, y := range ys {
		for _, x := range xs {
			c := rect{x, y, x + w, y + d}
			if !usable.contains(c) {
				continue
			}
			cands = append(cands, cand{x, y, math.Hypot(x+w/2-ccx, y+d/2-ccy)})
		}
	}
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].dist < cands[j].dist })
	for _, cd := range cands {
		c := rect{cd.x, cd.y, cd.x + w, cd.y + d}
		clear := rodOK(c)
		for _, o := range obstacles {
			if c.overlaps(o.inflate(gap - 1e-6)) {
				clear = false
				break
			}
		}
		if clear {
			return cd.x + w/2, cd.y + d/2, true
		}
	}
	return 0, 0, false
}

func linearFor(scale [3]float64, rot [3]float64) mesh.Matrix {
	return mesh.Compose(mesh.Scale(scale[0], scale[1], scale[2]), eulerMatrix(rot[0], rot[1], rot[2]))
}

func orOne(v float64) float64 {
	if v == 0 {
		return 1
	}
	return v
}

// AddModel adds one or more objects from a model file.
func (s *Store) AddModel(ref string, req AddModelRequest) (*AddModelResult, error) {
	items, err := loadModel(req.Path)
	if err != nil {
		return nil, err
	}
	if len(req.Objects) > 0 {
		if items, err = pickObjects(items, req.Objects, req.Path); err != nil {
			return nil, err
		}
	}
	copies := req.Copies
	if copies == 0 {
		copies = 1
	}
	if copies < 1 {
		return nil, invalidf("", "copies must be at least 1")
	}
	if (req.X != nil || req.Y != nil) && (copies > 1 || len(items) > 1) {
		return nil, invalidf("add the models one at a time, or leave x and y out for automatic placement", "x and y place one object; %d objects were requested", copies*len(items))
	}
	if (req.X == nil) != (req.Y == nil) {
		return nil, invalidf("give both x and y, or neither", "x and y go together")
	}
	// Factors are above 0: ScaleAll 0 means "not given", and an array with any
	// axis given needs every axis above 0 (a zero axis would flatten the model).
	if req.ScaleAll < 0 {
		return nil, invalidf("scale factors must be above 0", "scale %g is not valid", req.ScaleAll)
	}
	if req.Scale != [3]float64{} {
		for _, v := range req.Scale {
			if v <= 0 {
				return nil, invalidf("scale factors must be above 0", "scale %g is not valid", v)
			}
		}
	}
	res := &AddModelResult{}
	var addedIDs []int
	err = s.write(ref, func(h *handle) error {
		cfg, err := h.cfg()
		if err != nil {
			return err
		}
		nfil := len(cfg.List("filament_settings_id"))
		slot := req.Filament
		if slot == 0 {
			slot = 1
		}
		if slot < 1 || slot > nfil {
			return invalidf(fmt.Sprintf("use a filament from 1 to %d", nfil), "the project has %d filament(s); filament %d does not exist", nfil, slot)
		}
		var errs keyErrors
		ov := map[string]string{}
		for _, key := range sortedValueKeys(req.Overrides) {
			v := req.Overrides[key]
			if v == nil {
				errs.add(key, "needs a value")
				continue
			}
			if o, ok := h.validate(key, v, catalog.ScopeObject, false, &errs); ok {
				ov[key] = objectValue(o, v)
			}
		}
		if err := errs.err("call describe_setting for the valid values of a setting"); err != nil {
			return err
		}
		plateFor := req.Plate
		if plateFor != 0 && h.p.Plate(plateFor) == nil {
			return notFoundf("call get_project to see the plates", "project %s has no plate %d", h.id, plateFor)
		}
		for _, it := range items {
			base := req.Name
			if base == "" {
				base = it.Name
			}
			if base == "" {
				base = strings.TrimSuffix(filepath.Base(req.Path), filepath.Ext(req.Path))
			}
			L := linearFor([3]float64{orOne(req.Scale[0]) * orOne(req.ScaleAll), orOne(req.Scale[1]) * orOne(req.ScaleAll), orOne(req.Scale[2]) * orOne(req.ScaleAll)}, req.Rotation)
			if req.LayFlat {
				if rot, ok := mesh.LayFlat(it.Mesh.Transformed(L)); ok {
					L = L.Then(rot)
				} else {
					res.Warnings = append(res.Warnings, fmt.Sprintf("%s: no flat face was found to lay it on", base))
				}
			}
			bb, ok := bboxOf(it.Mesh, L)
			if !ok {
				return invalidf("", "%q holds no triangles", base)
			}
			if g := mesh.GuessUnits(bb); g.Suspicious {
				res.Warnings = append(res.Warnings, fmt.Sprintf("%s: %s", base, g.Reason))
			}
			size := size3(bb)
			c := center3(bb)
			for k := 0; k < copies; k++ {
				name := base
				if copies > 1 {
					name = fmt.Sprintf("%s_%d", base, k+1)
				}
				plate := plateFor
				var cx, cy float64
				if req.X != nil {
					cx, cy = *req.X, *req.Y
					if plate == 0 {
						plate = 1
					}
					if r := (rect{cx - size[0]/2, cy - size[1]/2, cx + size[0]/2, cy + size[1]/2}); !h.geometry().rect().contains(r) {
						res.Warnings = append(res.Warnings, fmt.Sprintf("%s is not fully inside the printable area at (%g, %g)", name, cx, cy))
					}
				} else {
					plates := []int{plate}
					if plate == 0 {
						plates = plates[:0]
						for _, pl := range h.p.Plates {
							plates = append(plates, pl.Index)
						}
					}
					placed := false
					for _, pi := range plates {
						h.placeHeight = size[2]
						if x, y, ok := h.findSpot(pi, size[0], size[1], h.occupied(pi, -1, -1)); ok {
							plate, cx, cy, placed = pi, x, y, true
							break
						}
					}
					if !placed {
						where := "any plate"
						if plateFor != 0 {
							where = fmt.Sprintf("plate %d", plateFor)
						}
						return conflictf("add a plate with manage_plates, remove objects, or scale the model down",
							"%s (%.1f x %.1f mm) does not fit on %s with a %g mm gap between objects and a %g mm margin to the bed edge", name, size[0], size[1], where, PlacementGap, PlacementMargin)
					}
				}
				z := 0.0
				if req.Z != nil {
					z = *req.Z
				}
				T := L.Then(mesh.Translate(cx-c[0], cy-c[1], z-float64(bb.Min[2])))
				abs := h.absolute(T, plate)
				o, err := h.p.AddObject(threemf.ObjectSpec{Name: name, Mesh: it.Mesh, Transform: &abs, Extruder: slot, Plate: plate})
				if err != nil {
					return invalidf("", "adding %q failed: %v", name, err)
				}
				for _, key := range sortedKeys(keysOf(ov)) {
					if err := h.p.SetObjectOverride(o.ID, key, ov[key]); err != nil {
						return errf(CodeInternal, "", "%v", err)
					}
				}
				addedIDs = append(addedIDs, o.ID)
				h.touchPlate(plate)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	err = s.read(ref, func(h *handle) error {
		var ierr error
		res.Info, ierr = h.info()
		for _, id := range addedIDs {
			for _, oi := range res.Info.Objects {
				if oi.ID == id {
					res.Added = append(res.Added, oi)
				}
			}
		}
		return ierr
	})
	return res, err
}

func keysOf(m map[string]string) map[string]bool {
	out := make(map[string]bool, len(m))
	for k := range m {
		out[k] = true
	}
	return out
}

// UpdateObjectRequest is update_object. Nil fields are left as they are.
type UpdateObjectRequest struct {
	Object   string
	Instance int // 0 based, default the first
	Name     *string
	// X, Y move the centre of the bounding box, Z the lowest point.
	X, Y, Z *float64
	// Rotation is absolute Euler degrees (X, then Y, then Z); Scale absolute
	// per axis factors; ScaleAll sets all three axes to one factor.
	Rotation *[3]float64
	Scale    *[3]float64
	ScaleAll *float64
	LayFlat  bool
	Filament *int
	// Plate moves the instance to another plate (placed automatically unless
	// x and y are given).
	Plate *int
}

// UpdateObjectResult reports an object change.
type UpdateObjectResult struct {
	Info     *Info
	Object   ObjectInfo
	Warnings []string
}

// UpdateObject renames, moves, rotates, scales, lays flat, re-assigns the
// filament of, or moves to another plate one instance of an object.
func (s *Store) UpdateObject(ref string, req UpdateObjectRequest) (*UpdateObjectResult, error) {
	res := &UpdateObjectResult{}
	var objectID int
	err := s.write(ref, func(h *handle) error {
		o, err := h.objectByRef(req.Object)
		if err != nil {
			return err
		}
		objectID = o.ID
		items := h.p.ItemsOf(o.ID)
		if req.Instance < 0 || req.Instance >= len(items) {
			return notFoundf("", "object %q has %d instance(s); instance %d does not exist", o.Name, len(items), req.Instance)
		}
		m, err := h.objectMesh(o)
		if err != nil {
			return invalidf("", "object %q has no usable geometry: %v", o.Name, err)
		}
		cur := h.itemT(o.ID, req.Instance)
		if req.Name != nil {
			if strings.TrimSpace(*req.Name) == "" {
				return invalidf("", "the name cannot be empty")
			}
			if err := h.p.RenameObject(o.ID, *req.Name); err != nil {
				return threemfError(err)
			}
		}
		if req.Filament != nil {
			cfg, cerr := h.cfg()
			if cerr != nil {
				return cerr
			}
			n := len(cfg.List("filament_settings_id"))
			if *req.Filament < 1 || *req.Filament > n {
				return invalidf(fmt.Sprintf("use a filament from 1 to %d", n), "the project has %d filament(s); filament %d does not exist", n, *req.Filament)
			}
			if err := h.p.SetObjectOverride(o.ID, "extruder", strconv.Itoa(*req.Filament)); err != nil {
				return errf(CodeInternal, "", "%v", err)
			}
			h.touchObject(o.ID)
		}
		scale, rot := decompose(cur)
		linearChanged := req.Rotation != nil || req.Scale != nil || req.ScaleAll != nil || req.LayFlat
		if req.Rotation != nil {
			rot = *req.Rotation
		}
		if req.Scale != nil {
			for i, v := range req.Scale {
				if v <= 0 {
					return invalidf("scale factors must be above 0", "scale %g is not valid", v)
				}
				scale[i] = v
			}
		}
		if req.ScaleAll != nil {
			if *req.ScaleAll <= 0 {
				return invalidf("scale factors must be above 0", "scale %g is not valid", *req.ScaleAll)
			}
			scale = [3]float64{*req.ScaleAll, *req.ScaleAll, *req.ScaleAll}
		}
		L := linear(cur)
		if linearChanged {
			L = linearFor(scale, rot)
			if req.LayFlat {
				if r, ok := mesh.LayFlat(m.Transformed(L)); ok {
					L = L.Then(r)
				} else {
					res.Warnings = append(res.Warnings, "no flat face was found to lay the object on")
				}
			}
		}
		oldB, ok := bboxOf(m, cur)
		if !ok {
			return invalidf("", "object %q has no geometry", o.Name)
		}
		newB, _ := bboxOf(m, L)
		oc := center3(oldB)
		cx, cy := oc[0], oc[1]
		z := float64(oldB.Min[2])
		if linearChanged && req.LayFlat && req.Z == nil {
			z = 0
		}
		if req.X != nil {
			cx = *req.X
		}
		if req.Y != nil {
			cy = *req.Y
		}
		if req.Z != nil {
			z = *req.Z
		}
		curPlate := 0
		if pl := h.p.PlateOf(o.ID, req.Instance); pl != nil {
			curPlate = pl.Index
		}
		target := curPlate
		if req.Plate != nil && *req.Plate != curPlate {
			if h.p.Plate(*req.Plate) == nil {
				return notFoundf("call get_project to see the plates", "project %s has no plate %d", h.id, *req.Plate)
			}
			target = *req.Plate
			if req.X == nil && req.Y == nil {
				sz := size3(newB)
				h.placeHeight = sz[2]
				x, y, fits := h.findSpot(target, sz[0], sz[1], h.occupied(target, o.ID, req.Instance))
				if !fits {
					return conflictf("make room on that plate, or give x and y", "%q (%.1f x %.1f mm) does not fit on plate %d", o.Name, sz[0], sz[1], target)
				}
				cx, cy = x, y
			}
			if err := h.p.MoveInstance(o.ID, req.Instance, target); err != nil {
				return errf(CodeInternal, "", "%v", err)
			}
			h.touchPlate(curPlate)
			h.touchPlate(target)
		}
		nc := center3(newB)
		T := L
		T[9], T[10], T[11] = cx-nc[0], cy-nc[1], z-float64(newB.Min[2])
		if req.X != nil || req.Y != nil || req.Z != nil || linearChanged || target != curPlate {
			if T != cur || target != curPlate { // a moved instance always gets the origin of its new plate
				if err := h.setItemT(o.ID, req.Instance, T); err != nil {
					return errf(CodeInternal, "", "%v", err)
				}
			}
		}
		h.touchObject(o.ID)
		return nil
	})
	if err != nil {
		return nil, err
	}
	err = s.read(ref, func(h *handle) error {
		var ierr error
		res.Info, ierr = h.info()
		for _, oi := range res.Info.Objects {
			if oi.ID == objectID {
				res.Object = oi
			}
		}
		return ierr
	})
	return res, err
}

// RemoveObject removes an object with all its instances.
func (s *Store) RemoveObject(ref, object string) (*Info, error) {
	err := s.write(ref, func(h *handle) error {
		o, err := h.objectByRef(object)
		if err != nil {
			return err
		}
		h.touchObject(o.ID)
		if err := h.p.RemoveObject(o.ID); err != nil {
			return errf(CodeInternal, "", "%v", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	var info *Info
	err = s.read(ref, func(h *handle) error {
		var ierr error
		info, ierr = h.info()
		return ierr
	})
	return info, err
}

// Modifier shapes.
const (
	ShapeBox      = "box"
	ShapeCylinder = "cylinder"
	ShapeSphere   = "sphere"
)

// ModifierRequest is add_modifier: a shaped volume inside an object that
// changes the settings there (or blocks or forces supports, or cuts).
type ModifierRequest struct {
	Object string
	// Subtype is modifier (default), negative, support_blocker or support_enforcer.
	Subtype string
	Name    string
	// Shape is box, cylinder or sphere. Size is the extent in mm along bed X, Y
	// and Z (a cylinder uses X as its diameter and Z as its height, a sphere X
	// as its diameter).
	Shape string
	Size  [3]float64
	// X, Y, Z place the centre of the shape on the bed (default: the centre of
	// the object).
	X, Y, Z *float64
	// Settings are the part overrides of a modifier.
	Settings map[string]any
	// Rotation turns the shape about its centre before it is placed: Euler degrees
	// applied about X, then Y, then Z of the bed.
	Rotation [3]float64
	// Relative places the shape relative to the centre of the object's bounding box
	// (mm, in the frame of its plate), resolved under the project lock (a concurrent move cannot race), with the
	// object found by the same case insensitive name rule as everywhere. It wins
	// over X, Y and Z.
	Relative *[3]float64
}

// ModifierResult reports the new part.
type ModifierResult struct {
	Info   *Info
	PartID int
}

var subtypeNames = map[string]string{
	"modifier": threemf.SubtypeModifier, "negative": threemf.SubtypeNegative, "negative_part": threemf.SubtypeNegative,
	"support_blocker": threemf.SubtypeSupportBlocker, "support_enforcer": threemf.SubtypeSupportEnforcer,
}

// AddModifier adds a modifier volume to an object.
func (s *Store) AddModifier(ref string, req ModifierRequest) (*ModifierResult, error) {
	res := &ModifierResult{}
	err := s.write(ref, func(h *handle) error {
		o, err := h.objectByRef(req.Object)
		if err != nil {
			return err
		}
		subtype := req.Subtype
		if subtype == "" {
			subtype = "modifier"
		}
		st, ok := subtypeNames[subtype]
		if !ok {
			return invalidf("subtypes: modifier, negative, support_blocker, support_enforcer", "%q is not a part subtype", req.Subtype)
		}
		if req.Size[0] <= 0 || (req.Shape != ShapeSphere && req.Size[2] <= 0) || (req.Shape != ShapeCylinder && req.Shape != ShapeSphere && req.Size[1] <= 0) {
			return invalidf("give size as [x, y, z] in mm; a cylinder uses [diameter, -, height], a sphere [diameter]", "the modifier size %v is not valid", req.Size)
		}
		var shape *mesh.Mesh
		switch req.Shape {
		case ShapeBox, "":
			shape = mesh.Box(req.Size[0], req.Size[1], req.Size[2])
		case ShapeCylinder:
			shape = mesh.Cylinder(req.Size[0]/2, req.Size[2])
		case ShapeSphere:
			shape = mesh.Sphere(req.Size[0] / 2)
		default:
			return invalidf("shapes: box, cylinder, sphere", "%q is not a modifier shape", req.Shape)
		}
		if st == threemf.SubtypeModifier && len(req.Settings) == 0 {
			return invalidf("give settings: {key: value} the modifier should apply", "a modifier without settings changes nothing")
		}
		if st != threemf.SubtypeModifier && len(req.Settings) > 0 {
			return invalidf("only a modifier carries settings", "a %s part takes no settings", subtype)
		}
		var errs keyErrors
		cfgKV := threemf.KVs{}
		for _, key := range sortedValueKeys(req.Settings) {
			v := req.Settings[key]
			if v == nil {
				errs.add(key, "needs a value")
				continue
			}
			if opt, ok := h.validate(key, v, catalog.ScopePart, false, &errs); ok {
				cfgKV.Set(key, objectValue(opt, v))
			}
		}
		if err := errs.err("call describe_setting for the valid values of a setting"); err != nil {
			return err
		}
		items := h.p.ItemsOf(o.ID)
		if len(items) == 0 {
			return invalidf("", "object %q has no instance", o.Name)
		}
		m, err := h.objectMesh(o)
		if err != nil {
			return invalidf("", "object %q has no usable geometry: %v", o.Name, err)
		}
		itemT := items[0].Transform // scene coordinates: the part lives in the frame of this transform
		b, _ := bboxOf(m, h.itemT(o.ID, 0))
		c := center3(b)
		px, py, pz := c[0], c[1], c[2]
		if req.Relative != nil {
			px, py, pz = c[0]+req.Relative[0], c[1]+req.Relative[1], c[2]+req.Relative[2]
		}
		if req.X != nil {
			px = *req.X
		}
		if req.Y != nil {
			py = *req.Y
		}
		if req.Z != nil {
			pz = *req.Z
		}
		inv, ok := itemT.Inverse()
		if !ok {
			return invalidf("", "object %q has a degenerate transform", o.Name)
		}
		// The shape is axis aligned on the bed; its transform in the object frame
		// undoes the object's own scale and rotation.
		var org [2]float64
		if pl := h.p.PlateOf(o.ID, 0); pl != nil {
			org = h.origin(pl.Index)
		}
		pt := eulerMatrix(req.Rotation[0], req.Rotation[1], req.Rotation[2]).Then(mesh.Translate(px+org[0], py+org[1], pz)).Then(inv)
		name := req.Name
		if name == "" {
			name = subtype
		}
		part, err := h.p.AddPart(o.ID, threemf.PartSpec{Subtype: st, Name: name, Mesh: shape, Transform: &pt, Config: cfgKV})
		if err != nil {
			return invalidf("", "adding the modifier failed: %v", err)
		}
		res.PartID = part.ID
		h.touchObject(o.ID)
		return nil
	})
	if err != nil {
		return nil, err
	}
	err = s.read(ref, func(h *handle) error {
		var ierr error
		res.Info, ierr = h.info()
		return ierr
	})
	return res, err
}

// pickObjects keeps the objects of a model file that the request names.
func pickObjects(items []mesh.Placed, names []string, path string) ([]mesh.Placed, error) {
	if !strings.EqualFold(filepath.Ext(path), ".3mf") {
		return nil, invalidf("objects picks objects out of a .3mf file; leave it out for an STL or OBJ", "%q holds a single model, objects does not apply", path)
	}
	var out []mesh.Placed
	for _, want := range names {
		found := false
		for _, it := range items {
			if strings.EqualFold(strings.TrimSpace(it.Name), strings.TrimSpace(want)) {
				out = append(out, it)
				found = true
			}
		}
		if !found {
			var have []string
			for _, it := range items {
				have = append(have, it.Name)
			}
			return nil, invalidf("the file holds: "+strings.Join(have, "; "), "the 3MF has no object named %q", want)
		}
	}
	return out, nil
}

// RemovePartResult reports a removed part.
type RemovePartResult struct {
	Info    *Info
	Part    PartInfo
	Object  string
	Removed string
}

// RemovePart removes one part (a modifier, negative part, support blocker or
// enforcer, or an extra model part) from an object. The last model part cannot
// go: remove the object instead.
func (s *Store) RemovePart(ref, object, part string) (*RemovePartResult, error) {
	res := &RemovePartResult{}
	err := s.write(ref, func(h *handle) error {
		o, err := h.objectByRef(object)
		if err != nil {
			return err
		}
		pt, err := h.partByRef(o, part)
		if err != nil {
			return err
		}
		if pt.Subtype == threemf.SubtypeNormal {
			normal := 0
			for _, p := range o.Parts {
				if p.Subtype == threemf.SubtypeNormal {
					normal++
				}
			}
			if normal <= 1 {
				return invalidf("remove the whole object with remove_object", "part %q is the only model part of object %q", pt.Name, o.Name)
			}
		}
		res.Part = PartInfo{ID: pt.ID, Name: pt.Name, Subtype: pt.Subtype}
		res.Object, res.Removed = o.Name, pt.Name
		h.touchObject(o.ID)
		if h.meshes != nil {
			delete(h.meshes, o.ID)
		}
		if err := h.p.RemovePart(o.ID, pt.ID); err != nil {
			return threemfError(err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	res.Info, err = s.info(ref)
	return res, err
}
