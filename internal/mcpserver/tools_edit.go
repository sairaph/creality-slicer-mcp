package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sairaph/creality-slicer-mcp/internal/projects"
)

// scaleValue is the scale argument: one number for every axis, or [x, y, z].
type scaleValue struct {
	all  float64
	axes *[3]float64
}

func (v *scaleValue) UnmarshalJSON(data []byte) error {
	var one float64
	if err := json.Unmarshal(data, &one); err == nil {
		*v = scaleValue{all: one}
		return nil
	}
	var three [3]float64
	if err := json.Unmarshal(data, &three); err != nil {
		return fmt.Errorf("scale must be a number or a list of three numbers")
	}
	*v = scaleValue{axes: &three}
	return nil
}

func (v scaleValue) MarshalJSON() ([]byte, error) {
	if v.axes != nil {
		return json.Marshal(*v.axes)
	}
	return json.Marshal(v.all)
}

func (v scaleValue) set() bool { return v.all != 0 || v.axes != nil }

func init() {
	three := jsonschema.Ptr(3)
	typeSchemas[reflect.TypeFor[scaleValue]()] = &jsonschema.Schema{AnyOf: []*jsonschema.Schema{
		{Type: "number", ExclusiveMinimum: jsonschema.Ptr(0.0)},
		{Type: "array", Items: &jsonschema.Schema{Type: "number", ExclusiveMinimum: jsonschema.Ptr(0.0)}, MinItems: three, MaxItems: three},
	}}
}

func (s *Server) registerEditTools() {
	addTool(s.mcpServer, "add_model", withRange(withItemRange(withItemRange(inputSchema[addModelInput](map[string]string{"copies": "1", "include_screenshot": "true"}), "position", 2, 3), "rotation", 3, 3), 1, 1e6, "copies"), s.addModel)
	addTool(s.mcpServer, "update_object", withItemRange(withItemRange(inputSchema[updateObjectInput](map[string]string{"include_screenshot": "true"}), "position", 2, 3), "rotation", 3, 3), s.updateObject)
	addTool(s.mcpServer, "group_objects", withMinItems(inputSchema[groupObjectsInput](map[string]string{"include_screenshot": "true"}), "objects", 2), s.groupObjects)
	addTool(s.mcpServer, "remove_object", inputSchema[removeObjectInput](map[string]string{"include_screenshot": "true"}), s.removeObject)
	addTool(s.mcpServer, "remove_part", inputSchema[removePartInput](map[string]string{"include_screenshot": "true"}), s.removePart)
	addTool(s.mcpServer, "update_settings", withEnum(inputSchema[updateSettingsInput](map[string]string{"scope": `"project"`, "allow_locked": "false"}), "scope", "project", "object", "part", "layer_range", "plate"), s.updateSettings)
	addTool(s.mcpServer, "set_presets", inputSchema[setPresetsInput](map[string]string{"keep_changes": "true", "include_screenshot": "true"}), s.setPresets)
	addTool(s.mcpServer, "add_modifier", withItemRange(withItemRange(withEnum(withEnum(withItemRange(inputSchema[addModifierInput](map[string]string{"include_screenshot": "true"}), "size", 3, 3), "kind", "modifier", "negative_part", "support_enforcer", "support_blocker"), "shape", "box", "cylinder", "sphere"), "position", 3, 3), "rotation", 3, 3), s.addModifier)
	addTool(s.mcpServer, "set_height_ranges", inputSchema[heightRangesInput](map[string]string{"include_screenshot": "true"}), s.setHeightRanges)
	addTool(s.mcpServer, "set_layer_actions", withRange(inputSchema[layerActionsInput](map[string]string{"plate": "1"}), 1, 1e6, "plate"), s.setLayerActions)
	addTool(s.mcpServer, "manage_plates", withEnum(inputSchema[managePlatesInput](map[string]string{"include_screenshot": "true"}), "action", "add", "remove", "rename", "lock", "unlock", "set"), s.managePlates)
}

// xyz splits a position list into pointers (z is optional).
func xyz(p []float64) (x, y, z *float64) {
	if len(p) >= 2 {
		x, y = &p[0], &p[1]
	}
	if len(p) >= 3 {
		z = &p[2]
	}
	return
}

func rot3(r []float64) [3]float64 {
	var out [3]float64
	copy(out[:], r)
	return out
}

func warningLines(ws []string) string {
	if len(ws) == 0 {
		return ""
	}
	return "\n\nWarnings:\n- " + strings.Join(ws, "\n- ")
}

// --- add_model ---

type addModelInput struct {
	Project           string     `json:"project"`
	Path              string     `json:"path"`
	Plate             *int       `json:"plate,omitempty"`
	Position          []float64  `json:"position,omitempty"`
	Rotation          []float64  `json:"rotation,omitempty"`
	Scale             scaleValue `json:"scale,omitempty"`
	Filament          *int       `json:"filament,omitempty"`
	Name              *string    `json:"name,omitempty"`
	Copies            *int       `json:"copies,omitempty"`
	Objects           []string   `json:"objects,omitempty"`
	Names             []string   `json:"names,omitempty"`
	KeepPositions     *bool      `json:"keep_positions,omitempty"`
	IncludeScreenshot *bool      `json:"include_screenshot,omitempty"`
}

type objectFront struct {
	ID       int        `yaml:"id"`
	Name     string     `yaml:"name"`
	Plate    int        `yaml:"plate"`
	Size     [3]float64 `yaml:"size"`
	Position [3]float64 `yaml:"position"`
	Rotation [3]float64 `yaml:"rotation,omitempty"`
	Filament int        `yaml:"filament"`
}

func objectFrontOf(o projects.ObjectInfo) objectFront {
	return objectFront{ID: o.ID, Name: o.Name, Plate: o.Plate, Size: round2v(o.Size), Position: round2v(o.Position), Rotation: round1v(o.Rotation), Filament: o.Filament}
}

type addModelFront struct {
	baseFront `yaml:",inline"`
	Added     []objectFront `yaml:"added"`
}

func (s *Server) addModel(ctx context.Context, _ *mcp.CallToolRequest, in addModelInput) (*mcp.CallToolResult, any, error) {
	be, fail := s.projectsOrFail(ctx)
	if fail != nil {
		return fail, nil, nil
	}
	before := warnCodesBefore(be, in.Project)
	req := projects.AddModelRequest{Path: in.Path, Name: deref(in.Name), Rotation: rot3(in.Rotation), LayFlat: false, Objects: in.Objects, Names: in.Names, KeepPositions: boolOr(in.KeepPositions, false)}
	req.X, req.Y, req.Z = xyz(in.Position)
	if in.Plate != nil {
		req.Plate = *in.Plate
	}
	if in.Filament != nil {
		req.Filament = *in.Filament
	}
	if in.Copies != nil {
		req.Copies = *in.Copies
	}
	if in.Scale.axes != nil {
		req.Scale = *in.Scale.axes
	} else if in.Scale.all != 0 {
		req.ScaleAll = in.Scale.all
	}
	res, err := be.Store.AddModel(in.Project, req)
	if err != nil {
		return projFailure(err), nil, nil
	}
	front := addModelFront{baseFront: base(res.Info)}
	var b strings.Builder
	fmt.Fprintf(&b, "Added %d object(s):\n", len(res.Added))
	for _, o := range res.Added {
		front.Added = append(front.Added, objectFrontOf(o))
		fmt.Fprintf(&b, "- %s\n", objectLine(o))
	}
	b.WriteString(warningLines(withIntroduced(res.Warnings, before, res.Info)))
	b.WriteString("\nNext: get_view to look at the plate from another side, update_settings to change settings, or slice_project.")
	var focus []string
	for _, o := range res.Added {
		focus = append(focus, strconv.Itoa(o.ID))
	}
	plate := req.Plate
	if len(res.Added) > 0 {
		plate = res.Added[0].Plate
	}
	return s.withScreenshot(be, successResult(front, strings.TrimRight(b.String(), "\n")), in.IncludeScreenshot, in.Project, plate, focus, false), nil, nil
}

// --- update_object ---

type updateObjectInput struct {
	Project           string     `json:"project"`
	Object            string     `json:"object"`
	Position          []float64  `json:"position,omitempty"`
	Rotation          []float64  `json:"rotation,omitempty"`
	Scale             scaleValue `json:"scale,omitempty"`
	Filament          *int       `json:"filament,omitempty"`
	Plate             *int       `json:"plate,omitempty"`
	Name              *string    `json:"name,omitempty"`
	LayFlat           *bool      `json:"lay_flat,omitempty"`
	IncludeScreenshot *bool      `json:"include_screenshot,omitempty"`
}

type updateObjectFront struct {
	baseFront `yaml:",inline"`
	Object    objectFront `yaml:"object"`
}

func (s *Server) updateObject(ctx context.Context, _ *mcp.CallToolRequest, in updateObjectInput) (*mcp.CallToolResult, any, error) {
	be, fail := s.projectsOrFail(ctx)
	if fail != nil {
		return fail, nil, nil
	}
	before := warnCodesBefore(be, in.Project)
	req := projects.UpdateObjectRequest{Object: in.Object, Name: in.Name, Filament: in.Filament, Plate: in.Plate, LayFlat: boolOr(in.LayFlat, false)}
	req.X, req.Y, req.Z = xyz(in.Position)
	if len(in.Rotation) == 3 {
		r := rot3(in.Rotation)
		req.Rotation = &r
	}
	if in.Scale.axes != nil {
		req.Scale = in.Scale.axes
	} else if in.Scale.all != 0 {
		v := in.Scale.all
		req.ScaleAll = &v
	}
	res, err := be.Store.UpdateObject(in.Project, req)
	if err != nil {
		return projFailure(err), nil, nil
	}
	body := "Updated: " + objectLine(res.Object) + "." + warningLines(withIntroduced(res.Warnings, before, res.Info))
	out := successResult(updateObjectFront{baseFront: base(res.Info), Object: objectFrontOf(res.Object)}, nextLine(body, "get_view to check the plate, or slice_project."))
	return s.withObjectScreenshot(be, out, in.IncludeScreenshot, in.Project, res.Object.Plate, strconv.Itoa(res.Object.ID), false), nil, nil
}

// --- remove_object ---

type removeObjectInput struct {
	Project           string `json:"project"`
	Object            string `json:"object"`
	IncludeScreenshot *bool  `json:"include_screenshot,omitempty"`
}

type removeObjectFront struct {
	baseFront `yaml:",inline"`
	Removed   string `yaml:"removed"`
}

func (s *Server) removeObject(ctx context.Context, _ *mcp.CallToolRequest, in removeObjectInput) (*mcp.CallToolResult, any, error) {
	be, fail := s.projectsOrFail(ctx)
	if fail != nil {
		return fail, nil, nil
	}
	plate := 1
	before := warnCodesBefore(be, in.Project)
	if before, err := be.Store.GetProject(in.Project); err == nil {
		plate = plateOfObject(before, in.Object, plate)
	}
	info, err := be.Store.RemoveObject(in.Project, in.Object)
	if err != nil {
		return projFailure(err), nil, nil
	}
	out := successResult(removeObjectFront{baseFront: base(info), Removed: in.Object},
		nextLine(fmt.Sprintf("Removed object `%s`. The project now has %d object(s).", in.Object, len(info.Objects))+warningLines(withIntroduced(nil, before, info)), "get_view to check the plate, add_model, or slice_project."))
	return s.withScreenshot(be, out, in.IncludeScreenshot, in.Project, plate, nil, false), nil, nil
}

// --- update_settings ---

type updateSettingsInput struct {
	Project     string         `json:"project"`
	Scope       *string        `json:"scope,omitempty"`
	Target      *string        `json:"target,omitempty"`
	Targets     []string       `json:"targets,omitempty"`
	Values      map[string]any `json:"values"`
	AllowLocked *bool          `json:"allow_locked,omitempty"`
}

type updateSettingsFront struct {
	baseFront `yaml:",inline"`
	Scope     string   `yaml:"scope"`
	Changed   []string `yaml:"changed"`
	Warnings  int      `yaml:"warnings"`
}

func settingChangeText(c projects.Change) string {
	if c.Removed {
		return fmt.Sprintf("%s: %s -> (override removed)", c.Key, orDash(plainVector(c.Old)))
	}
	return fmt.Sprintf("%s: %s -> %s", c.Key, orDash(plainVector(c.Old)), orDash(plainVector(c.New)))
}

// plainVector shows a vector value whose entries are all equal (a single
// filament's value, or the same value for every filament) as that value:
// "[60]" and "[60,60]" read "60".
func plainVector(v string) string {
	t := strings.TrimSpace(v)
	if len(t) < 3 || t[0] != '[' || t[len(t)-1] != ']' {
		return v
	}
	parts := strings.Split(t[1:len(t)-1], ",")
	first := strings.TrimSpace(parts[0])
	for _, p := range parts[1:] {
		if strings.TrimSpace(p) != first {
			return v
		}
	}
	return first
}

func (s *Server) updateSettings(ctx context.Context, _ *mcp.CallToolRequest, in updateSettingsInput) (*mcp.CallToolResult, any, error) {
	be, fail := s.projectsOrFail(ctx)
	if fail != nil {
		return fail, nil, nil
	}
	before := warnCodesBefore(be, in.Project)
	scope := strings.TrimSpace(deref(in.Scope))
	if scope == "" {
		scope = "project"
	}
	res, err := be.Store.UpdateSettings(in.Project, projects.SettingsRequest{
		Scope: scope, Target: strings.TrimSpace(deref(in.Target)), Targets: in.Targets, Values: in.Values, AllowLocked: boolOr(in.AllowLocked, false),
	})
	if err != nil {
		return projFailure(err), nil, nil
	}
	front := updateSettingsFront{baseFront: base(res.Info), Scope: scope, Warnings: len(res.Warnings)}
	var b strings.Builder
	// A value written as it already was is reported as unchanged, not as a
	// change from 15% to 15%.
	var changed, same []projects.Change
	for _, c := range res.Changed {
		if !c.Removed && c.Old != "" && sameSetting(c.Old, c.New) {
			same = append(same, c)
		} else {
			changed = append(changed, c)
		}
	}
	if len(changed) == 0 {
		b.WriteString("Nothing changed: every value was already set.\n")
	} else {
		fmt.Fprintf(&b, "Changed %d setting(s) at %s scope:\n", len(changed), scope)
		// With several targets the changes are grouped under their target.
		last := "\x00"
		var order []string
		byTarget := map[string][]projects.Change{}
		for _, c := range changed {
			if _, ok := byTarget[c.Target]; !ok {
				order = append(order, c.Target)
			}
			byTarget[c.Target] = append(byTarget[c.Target], c)
		}
		for _, t := range order {
			if len(order) > 1 && t != last {
				fmt.Fprintf(&b, "%s:\n", orDash(t))
			}
			last = t
			for _, c := range byTarget[t] {
				front.Changed = append(front.Changed, changeLine(c, len(in.Targets) > 1))
				label := ""
				if c.Label != "" && c.Label != c.Key {
					label = " (" + c.Label + ")"
				}
				fmt.Fprintf(&b, "- %s%s\n", settingChangeText(c), label)
			}
		}
	}
	if len(same) > 0 {
		var names []string
		for _, c := range same {
			names = append(names, c.Key+" ("+orDash(c.New)+")")
		}
		fmt.Fprintf(&b, "\nUnchanged, already at that value: %s.\n", strings.Join(names, ", "))
	}
	if len(res.Forced) > 0 {
		b.WriteString("\nThe app would also set these as a consequence, so they were applied too:\n")
		for _, c := range res.Forced {
			front.Changed = append(front.Changed, settingChangeText(c))
			note := ""
			if c.Note != "" {
				note = " (" + c.Note + ")"
			}
			fmt.Fprintf(&b, "- %s%s\n", settingChangeText(c), note)
		}
	}
	b.WriteString(warningLines(withIntroduced(res.Warnings, before, res.Info)))
	return successResult(front, nextLine(strings.TrimRight(b.String(), "\n"), "update_settings for more changes, add_model, or slice_project.")), nil, nil
}

// --- set_presets ---

type setPresetsInput struct {
	Project           string          `json:"project"`
	Printer           *string         `json:"printer,omitempty"`
	Process           *string         `json:"process,omitempty"`
	Filaments         []filamentInput `json:"filaments,omitempty"`
	Spools            []spoolInput    `json:"spools,omitempty"`
	KeepChanges       *bool           `json:"keep_changes,omitempty"`
	FlushMatrix       []int           `json:"flush_matrix,omitempty"`
	FlushMultiplier   *float64        `json:"flush_multiplier,omitempty"`
	AutoFlush         *bool           `json:"auto_flush,omitempty"`
	IncludeScreenshot *bool           `json:"include_screenshot,omitempty"`
}

type setPresetsFront struct {
	baseFront    `yaml:",inline"`
	Printer      string          `yaml:"printer"`
	Process      string          `yaml:"process"`
	Filaments    []filamentFront `yaml:"filaments"`
	FlushMatrix  string          `yaml:"flush_matrix"`
	FlushVolumes []string        `yaml:"flush_volumes,omitempty"`
}

func (s *Server) setPresets(ctx context.Context, _ *mcp.CallToolRequest, in setPresetsInput) (*mcp.CallToolResult, any, error) {
	be, fail := s.projectsOrFail(ctx)
	if fail != nil {
		return fail, nil, nil
	}
	before := warnCodesBefore(be, in.Project)
	if deref(in.Printer) == "" && deref(in.Process) == "" && len(in.Filaments) == 0 && len(in.Spools) == 0 && len(in.FlushMatrix) == 0 && in.FlushMultiplier == nil && !boolOr(in.AutoFlush, false) {
		return invalidInput("Nothing to change: give at least one of printer, process, filaments, spools, flush_matrix, auto_flush or flush_multiplier",
			"Call set_presets with the presets to change, for example {\"project\": \"<id>\", \"process\": \"0.28mm Standard @Creality K2 0.4 nozzle\"}."), nil, nil
	}
	req := projects.PresetsRequest{
		Printer: strings.TrimSpace(deref(in.Printer)), Process: strings.TrimSpace(deref(in.Process)), Filaments: filamentSpecs(in.Filaments), Spools: spoolSpecs(in.Spools),
		KeepChanges: boolOr(in.KeepChanges, true), FlushMatrix: in.FlushMatrix, AutoFlush: boolOr(in.AutoFlush, false),
	}
	if in.FlushMultiplier != nil {
		req.FlushMultiplier = strconv.FormatFloat(*in.FlushMultiplier, 'g', -1, 64)
	}
	res, err := be.Store.SetPresets(in.Project, req)
	if err != nil {
		return projFailure(err), nil, nil
	}
	info := res.Info
	front := setPresetsFront{baseFront: base(info), Printer: info.Printer, Process: info.Process, Filaments: filamentsFront(info),
		FlushMatrix: info.FlushMode, FlushVolumes: info.FlushMatrix}
	var b strings.Builder
	if len(res.Changed) == 0 {
		b.WriteString("Nothing changed.\n")
	} else {
		b.WriteString("Changed:\n")
		for _, c := range res.Changed {
			fmt.Fprintf(&b, "- %s\n", c)
		}
	}
	if len(res.Dropped) > 0 {
		fmt.Fprintf(&b, "\nSettings changed in this project that went away with the replaced preset: %s.\n", strings.Join(res.Dropped, ", "))
	}
	b.WriteString("\nFilaments (slot | preset | type | colour):\n")
	for _, f := range info.Filaments {
		fmt.Fprintf(&b, "%d | %s | %s | %s\n", f.Index, pipeSafe(f.Preset), f.Type, f.Colour)
	}
	b.WriteString(spoolLines(info))
	b.WriteString(bedLines(info))
	if len(info.Filaments) > 1 {
		fmt.Fprintf(&b, "\nFlush matrix: %s (multiplier %s). It is the purge volume in mm3 from each filament (row) to each other (column); dark to light needs the most. Give flush_matrix to set it by hand.\n", info.FlushMode, orDash(info.FlushMultiplier))
		b.WriteString("For the K2 CFS, each filament's type must match a loaded spool: call get_guide with {\"topic\": \"multicolor-cfs\"}.\n")
	}
	b.WriteString(warningLines(withIntroduced(res.Warnings, before, info)))
	out := successResult(front, nextLine(strings.TrimRight(b.String(), "\n"), "update_settings or add_model, or slice_project."))
	if len(in.Filaments) > 0 || len(in.Spools) > 0 {
		out = s.withScreenshot(be, out, in.IncludeScreenshot, in.Project, 1, nil, false)
	}
	return out, nil, nil
}

// --- add_modifier ---

type addModifierInput struct {
	Project           string         `json:"project"`
	Object            string         `json:"object"`
	Kind              string         `json:"kind"`
	Shape             string         `json:"shape"`
	Size              []float64      `json:"size"`
	Position          []float64      `json:"position,omitempty"`
	Rotation          []float64      `json:"rotation,omitempty"`
	Values            map[string]any `json:"values,omitempty"`
	Name              *string        `json:"name,omitempty"`
	IncludeScreenshot *bool          `json:"include_screenshot,omitempty"`
}

type addModifierFront struct {
	baseFront `yaml:",inline"`
	Part      int    `yaml:"part"`
	Kind      string `yaml:"kind"`
}

func (s *Server) addModifier(ctx context.Context, _ *mcp.CallToolRequest, in addModifierInput) (*mcp.CallToolResult, any, error) {
	be, fail := s.projectsOrFail(ctx)
	if fail != nil {
		return fail, nil, nil
	}
	before := warnCodesBefore(be, in.Project)
	req := projects.ModifierRequest{Object: in.Object, Subtype: in.Kind, Name: deref(in.Name), Shape: in.Shape, Settings: in.Values}
	copy(req.Size[:], in.Size)
	copy(req.Rotation[:], in.Rotation)
	if len(in.Position) == 3 {
		// Relative to the centre of the object, resolved by the store under the
		// project lock (no race with a move, same name rule as everywhere).
		req.Relative = &[3]float64{in.Position[0], in.Position[1], in.Position[2]}
	}
	res, err := be.Store.AddModifier(in.Project, req)
	if err != nil {
		return projFailure(err), nil, nil
	}
	body := fmt.Sprintf("Added a %s (%s, part id %d) to object `%s`.", in.Kind, in.Shape, res.PartID, in.Object)
	switch in.Kind {
	case "support_enforcer":
		body += " It only forces supports when enable_support is on: check with describe_setting."
	case "support_blocker":
		body += " Supports are not built inside it."
	case "negative_part":
		body += " The shape is subtracted from the object when slicing; the model file is unchanged."
	default:
		if len(in.Values) > 0 {
			body += " Its settings apply where it overlaps the object."
		}
	}
	body += warningLines(withIntroduced(nil, before, res.Info))
	out := successResult(addModifierFront{baseFront: base(res.Info), Part: res.PartID, Kind: in.Kind}, nextLine(body, "get_view with the object as focus and the Front or Right view to check it, or slice_project."))
	return s.withPartScreenshot(be, out, in.IncludeScreenshot, in.Project, plateOfObject(res.Info, in.Object, 1), in.Object), nil, nil
}

// --- set_height_ranges ---

type rangeInput struct {
	FromZ  float64        `json:"from_z"`
	ToZ    float64        `json:"to_z"`
	Values map[string]any `json:"values"`
}

type heightRangesInput struct {
	Project           string       `json:"project"`
	Object            string       `json:"object"`
	Ranges            []rangeInput `json:"ranges"`
	IncludeScreenshot *bool        `json:"include_screenshot,omitempty"`
}

type heightRangesFront struct {
	baseFront `yaml:",inline"`
	Object    string `yaml:"object"`
	Ranges    int    `yaml:"ranges"`
}

func (s *Server) setHeightRanges(ctx context.Context, _ *mcp.CallToolRequest, in heightRangesInput) (*mcp.CallToolResult, any, error) {
	be, fail := s.projectsOrFail(ctx)
	if fail != nil {
		return fail, nil, nil
	}
	before := warnCodesBefore(be, in.Project)
	specs := make([]projects.RangeSpec, len(in.Ranges))
	for i, r := range in.Ranges {
		specs[i] = projects.RangeSpec{From: r.FromZ, To: r.ToZ, Settings: r.Values}
	}
	hr, err := be.Store.SetHeightRangesDetailed(in.Project, in.Object, specs)
	if err != nil {
		return projFailure(err), nil, nil
	}
	info := hr.Info
	var b strings.Builder
	for _, o := range info.Objects {
		if strconv.Itoa(o.ID) != in.Object && o.Name != in.Object {
			continue
		}
		if len(o.HeightRanges) == 0 {
			b.WriteString("The object has no height ranges now.")
			break
		}
		fmt.Fprintf(&b, "Object `%s` has %d height range(s):\n", o.Name, len(o.HeightRanges))
		for _, r := range o.HeightRanges {
			keys := make([]string, 0, len(r.Settings))
			for k, v := range r.Settings {
				keys = append(keys, k+" = "+v)
			}
			fmt.Fprintf(&b, "- %s to %s mm: %s\n", num(r.From), num(r.To), strings.Join(sortedStrings(keys), ", "))
		}
		break
	}
	if b.Len() == 0 {
		b.WriteString("Height ranges updated.")
	}
	for _, n := range hr.Notes {
		b.WriteString("\nNote: " + n + ".")
	}
	b.WriteString(warningLines(withIntroduced(nil, before, info)))
	out := successResult(heightRangesFront{baseFront: base(info), Object: in.Object, Ranges: len(in.Ranges)}, nextLine(strings.TrimRight(b.String(), "\n"), "get_view with show_ranges true to check the bands, or slice_project."))
	return s.withObjectScreenshot(be, out, in.IncludeScreenshot, in.Project, plateOfObject(info, in.Object, 1), in.Object, true), nil, nil
}

// --- set_layer_actions ---

type actionInput struct {
	Z        *float64 `json:"z,omitempty"`
	Layer    *int     `json:"layer,omitempty"`
	Type     string   `json:"type"`
	Filament *int     `json:"filament,omitempty"`
	GCode    *string  `json:"gcode,omitempty"`
}

type layerActionsInput struct {
	Project string        `json:"project"`
	Plate   *int          `json:"plate,omitempty"`
	Actions []actionInput `json:"actions"`
}

type layerActionsFront struct {
	baseFront `yaml:",inline"`
	Plate     int `yaml:"plate"`
	Actions   int `yaml:"actions"`
}

func (s *Server) setLayerActions(ctx context.Context, _ *mcp.CallToolRequest, in layerActionsInput) (*mcp.CallToolResult, any, error) {
	be, fail := s.projectsOrFail(ctx)
	if fail != nil {
		return fail, nil, nil
	}
	before := warnCodesBefore(be, in.Project)
	plate := 1
	if in.Plate != nil {
		plate = *in.Plate
	}
	var info *projects.Info
	var actions []projects.LayerAction
	for i, a := range in.Actions {
		act := projects.LayerAction{Kind: a.Type, GCode: deref(a.GCode)}
		if a.Z != nil {
			act.Z = *a.Z
		}
		if a.Layer != nil {
			act.Layer = *a.Layer
		}
		if a.Filament != nil {
			act.Filament = *a.Filament
		}
		switch a.Type {
		case projects.ActionPause, projects.ActionColorChange, projects.ActionToolChange, projects.ActionCustom:
		default:
			return invalidInput(fmt.Sprintf("Action %d: unknown type %q", i+1, a.Type), "Use type pause, color_change, tool_change or custom."), nil, nil
		}
		if (a.Type == projects.ActionToolChange || a.Type == projects.ActionColorChange) && a.Filament == nil {
			return invalidInput(fmt.Sprintf("Action %d: a %s needs filament", i+1, a.Type), "Give filament, the slot to change to (1 to the number of filaments)."), nil, nil
		}
		if a.Type == projects.ActionColorChange || a.Type == projects.ActionToolChange {
			// The new colour is the colour of the filament the print changes to.
			if info == nil {
				var err error
				if info, err = be.Store.GetProject(in.Project); err != nil {
					return projFailure(err), nil, nil
				}
			}
			slot := max(act.Filament, 1)
			if slot > len(info.Filaments) || act.Filament < 1 {
				return invalidInput(fmt.Sprintf("Action %d: filament %d does not exist", i+1, act.Filament), fmt.Sprintf("Use a filament from 1 to %d.", len(info.Filaments))), nil, nil
			}
			act.Filament, act.Colour = slot, info.Filaments[slot-1].Colour
		}
		actions = append(actions, act)
	}
	res, err := be.Store.SetLayerActions(in.Project, plate, actions)
	if err != nil {
		return projFailure(err), nil, nil
	}
	var b strings.Builder
	for _, p := range res.Plates {
		if p.Index != plate {
			continue
		}
		if len(p.Actions) == 0 {
			b.WriteString("Plate has no layer actions now.")
			break
		}
		fmt.Fprintf(&b, "Plate %d has %d layer action(s):\n", plate, len(p.Actions))
		for _, a := range p.Actions {
			extra := ""
			switch a.Kind {
			case projects.ActionColorChange, projects.ActionToolChange:
				extra = fmt.Sprintf(" to filament %d (%s)", a.Filament, a.Colour)
			case projects.ActionCustom:
				extra = " with your G-code"
			}
			fmt.Fprintf(&b, "- layer %d (z %s mm): %s%s\n", a.Layer, num(a.Z), a.Kind, extra)
		}
		break
	}
	if b.Len() == 0 {
		b.WriteString("Layer actions updated.")
	}
	// A colour change the printer cannot do with G-code comes back stored as a
	// tool change: more tool changes than the request asked for.
	asked, stored := 0, 0
	for _, a := range in.Actions {
		if a.Type == projects.ActionToolChange {
			asked++
		}
	}
	for _, p := range res.Plates {
		if p.Index == plate {
			for _, a := range p.Actions {
				if a.Kind == projects.ActionToolChange {
					stored++
				}
			}
		}
	}
	if stored > asked {
		b.WriteString("\nOn this printer a colour change is a CFS filament change: it is stored as tool_change and the printer switches spools by itself.")
	}
	// Nothing is refused for these: the warnings say what would not work.
	var ws []string
	for _, w := range res.Warnings {
		switch w.Code {
		case "layer_tool_change_ignored", "action_above_model", "color_change_no_gcode":
			ws = append(ws, w.Message)
		}
	}
	b.WriteString(warningLines(withIntroduced(ws, before, res)))
	return successResult(layerActionsFront{baseFront: base(res), Plate: plate, Actions: len(actions)}, nextLine(strings.TrimRight(b.String(), "\n"), "get_project to check the plate, or slice_project.")), nil, nil
}

// --- manage_plates ---

type managePlatesInput struct {
	Project           string         `json:"project"`
	Action            string         `json:"action"`
	Plate             *int           `json:"plate,omitempty"`
	Name              *string        `json:"name,omitempty"`
	Values            map[string]any `json:"values,omitempty"`
	IncludeScreenshot *bool          `json:"include_screenshot,omitempty"`
}

type managePlatesFront struct {
	baseFront `yaml:",inline"`
	Action    string       `yaml:"action"`
	Plates    []plateFront `yaml:"plates"`
}

func (s *Server) managePlates(ctx context.Context, _ *mcp.CallToolRequest, in managePlatesInput) (*mcp.CallToolResult, any, error) {
	be, fail := s.projectsOrFail(ctx)
	if fail != nil {
		return fail, nil, nil
	}
	before := warnCodesBefore(be, in.Project)
	plate := 0
	if in.Plate != nil {
		plate = *in.Plate
	}
	var info *projects.Info
	var warnings []string
	if in.Action == "set" {
		if plate == 0 {
			plate = 1
		}
		res, err := be.Store.UpdateSettings(in.Project, projects.SettingsRequest{Scope: "plate", Target: strconv.Itoa(plate), Values: in.Values})
		if err != nil {
			return projFailure(err), nil, nil
		}
		info, warnings = res.Info, res.Warnings
	} else {
		res, err := be.Store.ManagePlates(in.Project, projects.PlatesRequest{Action: in.Action, Plate: plate, Name: deref(in.Name)})
		if err != nil {
			return projFailure(err), nil, nil
		}
		info, warnings = res.Info, res.Warnings
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Done (%s). The project has %d plate(s):\n", in.Action, len(info.Plates))
	for _, p := range info.Plates {
		lock := ""
		if p.Locked {
			lock = ", locked"
		}
		fmt.Fprintf(&b, "- plate %d `%s`: %d object(s), bed %s, sequence %s%s\n", p.Index, orDash(p.Name), p.Objects, orDash(p.BedType), orDash(p.PrintSequence), lock)
	}
	b.WriteString(warningLines(withIntroduced(warnings, before, info)))
	out := successResult(managePlatesFront{baseFront: base(info), Action: in.Action, Plates: platesFront(info)}, nextLine(strings.TrimRight(b.String(), "\n"), "add_model with a plate, get_view to look at a plate, or slice_project."))
	switch in.Action {
	case "add", "remove", "set":
		shot := plate
		if in.Action == "add" {
			shot = len(info.Plates)
		}
		if shot < 1 || shot > len(info.Plates) {
			shot = 1
		}
		out = s.withScreenshot(be, out, in.IncludeScreenshot, in.Project, shot, nil, false)
	}
	return out, nil, nil
}

// --- remove_part ---

type removePartInput struct {
	Project           string `json:"project"`
	Object            string `json:"object"`
	Part              string `json:"part"`
	IncludeScreenshot *bool  `json:"include_screenshot,omitempty"`
}

type removePartFront struct {
	baseFront `yaml:",inline"`
	Object    string `yaml:"object"`
	Removed   string `yaml:"removed"`
	Kind      string `yaml:"kind"`
	Parts     int    `yaml:"parts"`
}

func (s *Server) removePart(ctx context.Context, _ *mcp.CallToolRequest, in removePartInput) (*mcp.CallToolResult, any, error) {
	be, fail := s.projectsOrFail(ctx)
	if fail != nil {
		return fail, nil, nil
	}
	before := warnCodesBefore(be, in.Project)
	res, err := be.Store.RemovePart(in.Project, in.Object, in.Part)
	if err != nil {
		return projFailure(err), nil, nil
	}
	parts := 0
	for _, o := range res.Info.Objects {
		if o.Name == res.Object {
			parts = len(o.Parts)
		}
	}
	kind := strings.ReplaceAll(res.Part.Subtype, "_", " ")
	out := successResult(removePartFront{baseFront: base(res.Info), Object: res.Object, Removed: res.Removed, Kind: res.Part.Subtype, Parts: parts},
		nextLine(fmt.Sprintf("Removed the %s `%s` from object `%s`. The object has %d part(s) left.", kind, res.Removed, res.Object, parts)+noteLine(res.Note)+warningLines(withIntroduced(nil, before, res.Info)), "get_project to see the parts, add_modifier to add another, or slice_project."))
	return s.withPartScreenshot(be, out, in.IncludeScreenshot, in.Project, plateOfObject(res.Info, in.Object, 1), in.Object), nil, nil
}

// warnCodesBefore is the set of warning codes a project has before a change, so
// the reply can name the ones the change introduced.
func warnCodesBefore(be ProjectBackend, ref string) map[string]bool {
	info, err := be.Store.GetProject(ref)
	if err != nil || info == nil {
		return nil
	}
	codes := map[string]bool{}
	for _, w := range info.Warnings {
		codes[w.Code] = true
	}
	return codes
}

// withIntroduced adds to a reply's own warnings the project warnings this call
// introduced: present after, absent before, by code. A message the reply shows
// already is not repeated. Without a "before" (the project could not be read)
// nothing is added.
func withIntroduced(own []string, before map[string]bool, after *projects.Info) []string {
	if before == nil || after == nil {
		return own
	}
	out := append([]string(nil), own...)
	shown := map[string]bool{}
	for _, w := range own {
		shown[w] = true
	}
	for _, w := range after.Warnings {
		if !before[w.Code] && !shown[w.Message] {
			shown[w.Message] = true
			out = append(out, w.Message)
		}
	}
	return out
}

// changeLine is the front matter line of a change; with several targets it names
// the target.
func changeLine(c projects.Change, withTarget bool) string {
	if withTarget && c.Target != "" {
		return c.Target + ": " + settingChangeText(c)
	}
	return settingChangeText(c)
}

// noteLine is a note on its own line after a reply's first sentence, or nothing.
func noteLine(n string) string {
	if n == "" {
		return ""
	}
	return "\n\nNote: " + n + "."
}
