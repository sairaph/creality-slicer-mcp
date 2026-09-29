package mcpserver

// Every tool's title, description, annotations and parameter descriptions
// live in this file, so the wording is reviewed in one place and the size
// limits in tooltext_test.go apply to all of it. addTool applies a tool's text
// to its schema at registration and panics on a mismatch, so a parameter never
// ships without a description.
//
// The rules the texts follow: a description is at most maxDescriptionBytes
// (some clients cut long ones), the first sentence says what the tool does,
// and every rule that changes how an argument is written sits in that
// argument's description, because the schema reaches every client whole.
// No em or en dashes and no markdown inside a description.

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	// maxDescriptionBytes is the longest tool description.
	maxDescriptionBytes = 1000
	// maxSchemaBytes is the largest input schema, parameter descriptions
	// included: some clients drop every parameter description above 5,000.
	maxSchemaBytes = 4000
	// maxParamDescriptionBytes is the longest parameter description.
	maxParamDescriptionBytes = 800
	// maxInstructionsChars is the longest set of server instructions.
	maxInstructionsChars = 1500
)

// serverInstructions are delivered once per session, before any tool text.
const serverInstructions = `Prepare, tune and slice Creality K2 prints with the Creality Print 7.2 or 7.3 installed on this computer (7.3 preferred; it needs nothing extra from the user), and explain every setting. Search for these tools when the user wants to slice a model, choose print settings, or asks what a slicer setting does.
Start: call get_slicer_status, then get_guide (topic start) for the workflow, then create_project or open_project.
Rules for every call:
- Paths are absolute paths on this computer. Source files are never modified: projects live in this server's own store.
- Slicing writes G-code into the project folder; uploading and printing go through the creality-k2-mcp server.
- Setting keys are Creality's own. Find them with search_settings and read them with describe_setting; never guess a key.
- Check spools with creality-k2-mcp get_filaments before a multi-colour print.
- Pictures: get_view draws a plate from a named camera; the tools that change a project attach a small screenshot of the plate unless include_screenshot is false; the preview parameters of open_project and slice_project are none, small or large.
- Filament slots are numbered from 1 in the project tools; the slice handoff uses 0-based tool indexes.`

// toolText is the text of one tool.
type toolText struct {
	Description string
	// Params maps a parameter name to its description; a parameter inside an
	// array of objects is "array[].name". A parameter missing here takes its
	// text from sharedParams.
	Params map[string]string
}

// toolAnnotation is the behaviour hints of one tool, for client approval and
// display.
type toolAnnotation struct {
	readOnly, destructive, openWorld, idempotent bool
}

var (
	// annReadOnly reads and changes nothing.
	annReadOnly = toolAnnotation{readOnly: true}
	// annAdditive creates things and destroys nothing.
	annAdditive = toolAnnotation{}
	// annChanging changes or removes something that exists.
	annChanging = toolAnnotation{destructive: true}
	// annIdempotent changes state, but repeating the same call leaves the
	// same state.
	annIdempotent = toolAnnotation{idempotent: true}
)

// toolAnnotations assigns every tool one of the sets above.
var toolAnnotations = map[string]toolAnnotation{
	"get_slicer_status": annReadOnly, "get_guide": annReadOnly,
	"search_settings": annReadOnly, "describe_setting": annReadOnly, "browse_settings": annReadOnly,
	"list_presets": annReadOnly, "get_preset": annReadOnly,
	"create_project": annAdditive, "open_project": annAdditive, "list_projects": annReadOnly, "get_project": annReadOnly, "get_view": annReadOnly,
	"add_model": annAdditive, "update_object": annChanging, "remove_object": annChanging, "remove_part": annChanging,
	"update_settings": annChanging, "set_presets": annChanging, "add_modifier": annAdditive,
	"set_height_ranges": annChanging, "set_layer_actions": annChanging, "manage_plates": annChanging,
	"export_project": annChanging, "delete_project": annChanging,
	"slice_project": annAdditive, "get_slice_status": annIdempotent, "get_slice_report": annReadOnly,
}

// sharedParams are the descriptions of parameters that mean the same in every
// tool that has them.
var sharedParams = map[string]string{
	"include_screenshot": "attach an isometric picture of the affected plate, 512 px, with parts and labels, framed on what changed (default true; the server can be set to leave screenshots out, and a picture that cannot be drawn is left out without failing the call)",
	"page":               "page number, starting at 1 (default 1); the reply says how many pages there are and which to ask for next",
	"project":            "project id or exact project name, as list_projects shows it",
	"level":              "how many settings to show: beginner (the app's simple mode, default), advanced (adds advanced), or all (adds developer settings)",
}

// toolTexts is the text of every tool.
var toolTexts = map[string]toolText{
	// Status and guidance.
	"get_slicer_status": {
		Description: `Report whether Creality Print is installed and usable: version, build, supported dialect, profile version and where the presets come from, whether its window is open, the projects folder, the settings catalog version and how many settings have a description from the installed app. Call it first. When the version is neither 7.2 nor 7.3, settings, presets and guides still work but slicing does not. Pass refresh true to detect the install again after installing or updating it.`,
		Params:      map[string]string{"refresh": "detect Creality Print again and reload presets and descriptions (default false: use the result cached since the server started)"},
	},
	"get_guide": {
		Description: `Read the guide: our own written workflows for slicing on a K2 or K2 Combo. Without arguments it lists the topics. Pass topic for a page: start (the whole flow with calls), k2-combo, multicolor-cfs (spool matching, flush, handoff to the printer), supports, strength, surface-quality, speed-vs-quality, modifiers-and-ranges, multi-plate, calibration, troubleshooting (exit codes), gui-handoff (work only the app can do), glossary. Pass term alone to look a word up in the glossary. The same pages are installed as a skill.`,
		Params: map[string]string{
			"topic": "topic name from the index (default: list the topics)",
			"term":  "glossary word to look up; every entry whose term contains it is returned, ignoring case; use alone or with topic glossary",
		},
	},

	// Settings.
	"search_settings": {
		Description: `Find settings by words in their key, label, area, choices or description. Each row: key, label, area (where the app shows it), unit, the K2 default and level, with the first sentence of the app's description under it. The K2 default is the value in the flattened Creality K2 0.4 nozzle, 0.20mm Standard and CR-PLA presets; a setting they do not set shows the catalog default, marked (catalog). Every word must appear (in the key, label, area, choices or description), so use one or two words; exact key or label matches rank first. Results are paged. Use describe_setting for one key's meaning, range and dependencies, and browse_settings to walk the areas.`,
		Params: map[string]string{
			"query": "words to look for, such as wall loops or support angle (default: every setting the other filters allow)",
			"area":  "GUI path prefix to search under, such as process/Quality or filament/Filament; browse_settings shows the paths",
			"scope": "printer, process or filament: settings of that preset type; object, part, layer_range or plate: settings that can be overridden there (default: all)",
		},
	},
	"describe_setting": {
		Description: `Explain one setting: its label, type, unit, default, range or choices, level, area, where it can be set (project, object, part, layer range, plate) and whether Creality's presets lock it. The body has the app's description, its dependencies in plain words (only used when another setting is on, forced changes), related settings and an update_settings example. An unknown key returns up to five close ones. Pass preset to also show the value a preset has and which preset in its chain set it, or project for the value in a project.`,
		Params: map[string]string{
			"key":     "setting key, such as wall_loops or filament_max_volumetric_speed; search_settings finds keys",
			"preset":  "preset to read the current value from, as type:name, for example process:0.20mm Standard @Creality K2 0.4 nozzle (default: none)",
			"project": "project whose current project level value to show, with where it comes from: changed in the project or from its presets (default: none)",
		},
	},
	"browse_settings": {
		Description: `Walk the settings the way the app lays them out: tabs (process, filament, printer, plate), then pages, then groups. At the top and at tab or page level it lists the children with their setting counts; at group level it lists the settings with label, key, unit and default. Use it to discover what exists in an area, search_settings to find something by words, and describe_setting for the details of one key.`,
		Params:      map[string]string{"path": "area to open, such as process, process/Strength or process/Strength/Infill (default: the top level)"},
	},

	// Presets.
	"list_presets": {
		Description: `List presets of one type with their key facts: process (layer height, walls, infill), filament (type, vendor, filament_id, nozzle temperature) or printer (nozzle, bed). By default only presets that fit Creality K2 0.4 nozzle are shown (printer presets: the K2 family). Filter by filament_type such as PLA or PETG, and by source system or user. Results are paged. Use get_preset to see every value.`,
		Params: map[string]string{
			"type":          "printer, process or filament",
			"printer":       "printer preset the presets must fit (default Creality K2 0.4 nozzle); any lists presets of every Creality printer; all lists presets of every K2 nozzle size",
			"filament_type": "only filament presets of this material, such as PLA, PETG or ABS (default: all)",
			"source":        "system (shipped with the app), user (yours) or all (default)",
		},
	},
	"get_preset": {
		Description: `Show one preset's values, grouped by where the app shows them, with the inheritance chain and the printers it fits. With compare_to (a preset of the same type, or parent) only the keys that differ are shown as this -> other. The values are the flattened result: everything inherited applied. Without keys only beginner level settings are listed; pass level all or keys for more.`,
		Params: map[string]string{
			"type":       "printer, process or filament",
			"name":       "exact preset name, as list_presets shows it",
			"compare_to": "name of another preset of the same type, or parent for the preset it inherits from; shows only the differences",
			"keys":       "only these setting keys (default: every key the level allows)",
		},
	},
	// Projects.
	"create_project": {
		Description: `Create a project: an empty plate with a printer, a process and one preset per filament slot. Projects live in this server's own store and become Creality Print 3MF files on export. Each filament needs a preset and a colour; a multi-colour print needs one entry per colour. Presets must fit the printer: an unknown or incompatible one returns the compatible names. Next: add_model, then update_settings if needed, then slice_project. Match the filament types to the spools loaded in the printer (get_guide topic multicolor-cfs).`,
		Params: map[string]string{
			"name":               "project name, as shown in list_projects (does not have to be unique: the id is)",
			"printer":            "printer preset name (default Creality K2 0.4 nozzle)",
			"process":            "process preset name (default: the printer's default process, 0.20mm Standard for the K2)",
			"filaments":          "filament slots in order; slot 1 is tool T0. At least one",
			"filaments[].preset": "filament preset name, as list_presets shows it, for example Hyper PLA @Creality K2 0.4 nozzle",
			"filaments[].colour": "colour as #RRGGBB, for example #FF0000",
			"bed_type":           "bed surface for plate 1 (default: the printer's default), for example Textured PEI Plate",
		},
	},
	"open_project": {
		Description: `Import an existing Creality Print or Bambu-family 3MF project into the store as a new project. The source file is copied and never changed. Reports presets, filaments, plates, objects, objects with painted data (kept, but only the app can edit them) and warnings: another printer than the K2 family, a file from a newer app version, presets missing on this machine. A file with no project settings is refused: use create_project and add_model instead.`,
		Params: map[string]string{
			"path":    "absolute path of a .3mf project file",
			"name":    "name for the new project (default: the file name)",
			"preview": "none (default) or small: small attaches the thumbnail the app stored in the file, when it has one (there is no large one)",
		},
	},
	"list_projects": {
		Description: `List the projects in the store, newest first: id, name, printer, objects, plates and last slice. Results are paged. Use get_project for one project.`,
		Params:      map[string]string{},
	},
	"get_project": {
		Description: `Show one project as text: presets, filaments, plates and objects (size, plate-relative position, rotation, filament, overrides, painted), the parts of each object (modifier, negative part, support enforcer or blocker) with kind, size and position, the settings changed from the presets, the last slice and warnings such as objects outside the bed. Objects show their exclusion label once the project was sliced. For a picture call get_view.`,
		Params:      map[string]string{},
	},
	"get_view": {
		Description: `Draw a plate from a named camera: the bed with a grid and the plate axes (X red, Y green), the objects in their filament colours, their modifiers, negative parts, support enforcers and blockers as translucent coloured volumes with outlines, each object's id and name, and optionally its height ranges as bands. Use it to check placement, heights and modifier coverage: the Front and Right views show heights. Frame one object with focus, hide or isolate objects, and ask for a size (the frame is 4:3). Labels that would cover each other are moved or left out and listed. It reads the project and changes nothing.`,
		Params: map[string]string{
			"plate":       "plate to draw, starting at 1 (default 1)",
			"view_name":   "camera: Isometric (default, from the front left), Front (camera at the front, looking along Y), Top, Right, Back, Left, Bottom, Dimetric or Trimetric",
			"focus":       "object ids or names to frame the picture on (default: the whole bed)",
			"hide":        "object ids or names to leave out of the picture",
			"isolate":     "object ids or names to draw alone; every other object is left out",
			"show_parts":  "draw modifiers (yellow), negative parts (red), support enforcers (green) and support blockers (blue-grey) (default true)",
			"show_labels": "write each object's id and short name at its top (default true)",
			"show_ranges": "draw the height ranges of the objects as tinted bands (default false)",
			"width":       "image width in pixels, 1 to 2048; give one of width and height to get a 4:3 frame (default: 1024 wide, 768 high)",
			"height":      "image height in pixels, 1 to 2048",
		},
	},
	"add_model": {
		Description: `Add a model file to a plate. Without position it is placed automatically in free space; scale, rotation, copies and filament apply to every copy. Reports where each copy went, its size and whether it fits, and warns when the size looks like the wrong unit. A model that does not fit fails with the free area. Unit hint: STL files carry no units, and the slicer reads them as millimetres.`,
		Params: map[string]string{
			"path":     "absolute path of a .stl, .obj or .3mf file; a .3mf adds all of its objects unless objects names some",
			"objects":  "names of the objects to take from a .3mf (default: all of them); an unknown name lists the names in the file",
			"plate":    "plate to add to, starting at 1 (default 1)",
			"position": "[x, y] or [x, y, z] in mm of the model's centre, measured from the corner of its own plate (default: automatic placement)",
			"rotation": "[x, y, z] degrees (default none)",
			"scale":    "one number for every axis, or [x, y, z] (default 1); must be above 0",
			"filament": "filament slot for the object, starting at 1 (default 1)",
			"name":     "object name (default: the file name); copies get a number appended",
			"copies":   "how many copies to add (default 1)",
		},
	},
	"update_object": {
		Description: `Change one object: position, rotation, scale, filament, plate or name, or lay it flat on its largest face. Only the given fields change. The object is an id or a name that is unique in the project. Returns the object's new size and position.`,
		Params: map[string]string{
			"object":   "object id or unique name, as get_project shows it",
			"position": "[x, y] or [x, y, z] in mm of the object's centre, measured from the corner of its own plate",
			"rotation": "[x, y, z] degrees, replacing the current rotation",
			"scale":    "one number for every axis, or [x, y, z]; must be above 0; replaces the current scale",
			"filament": "filament slot, starting at 1",
			"plate":    "move the object to this plate, starting at 1",
			"name":     "new object name",
			"lay_flat": "true rotates the object so its largest flat face is down (default false)",
		},
	},
	"remove_object": {
		Description: `Remove one object, with its parts, modifiers and height ranges, from the project. Objects on other plates are not affected. Returns the remaining objects.`,
		Params:      map[string]string{"object": "object id or unique name, as get_project shows it"},
	},
	"remove_part": {
		Description: `Remove one part of an object: a modifier, a negative part, a support blocker or enforcer, or an extra model part. get_project lists the parts of each object with their names and ids. The last model part of an object cannot be removed: use remove_object for that. Returns the parts that are left and a screenshot of the plate.`,
		Params: map[string]string{
			"object": "object id or unique name, as get_project shows it",
			"part":   "part id or name, as get_project shows it under the object",
		},
	},
	"update_settings": {
		Description: `Change settings at one scope: the whole project (default), an object, a part, a layer range or a plate. Every key and value is checked against Creality's catalog first (unknown key, type, choice, range, scope, vendor lock) and nothing changes unless all pass. Reports each change as old to new, warns about settings that do nothing until another is on, and applies the side effects the app would apply. For a per-filament key (a filament preset setting) a list is one value per filament slot and a single value applies to every slot; a list key of the printer takes the whole list, as describe_setting shows. Find keys with search_settings.`,
		Params: map[string]string{
			"scope":        "project (default), object, part, layer_range or plate",
			"target":       "what to change: object id or name; part as object/part; plate number; not used for project scope",
			"values":       "setting key to value; a value is a string, number, boolean or list (vector keys); null removes the override and goes back to the preset value; positions such as wipe_tower_x are in mm from the corner of the plate",
			"allow_locked": "true allows keys that Creality's system presets lock (default false)",
		},
	},
	"set_presets": {
		Description: `Change the project's printer, process or filament presets, or its flush matrix. At least one of printer, process, filaments, flush_matrix or flush_multiplier. Changing a preset re-bases the project on it; keep_changes decides whether this project's own changed settings survive. Filaments is the full list in slot order; a list shorter than now is refused while objects use the removed slots. Omit flush_matrix for the automatic flush volumes.`,
		Params: map[string]string{
			"printer":            "printer preset name",
			"process":            "process preset name",
			"filaments":          "the full filament list in slot order, replacing the current one",
			"filaments[].preset": "filament preset name, as list_presets shows it",
			"filaments[].colour": "colour as #RRGGBB",
			"keep_changes":       "keep this project's changed settings when switching presets (default true)",
			"flush_matrix":       "manual flush volumes in mm3: N*N numbers for N filaments, row by row, from filament to filament (default: automatic)",
			"flush_multiplier":   "multiplier applied to the flush volumes, for example 0.8 (default: unchanged)",
		},
	},
	"add_modifier": {
		Description: `Add a shaped volume to an object: a modifier with its own settings, a negative part that cuts the object, or a support enforcer or blocker. The shape is a box, cylinder or sphere sized in mm, placed relative to the object's centre. A modifier needs values, checked at part scope. A support enforcer only works when enable_support is on.`,
		Params: map[string]string{
			"object":   "object id or unique name",
			"kind":     "modifier, negative_part, support_enforcer or support_blocker",
			"shape":    "box, cylinder or sphere",
			"size":     "[x, y, z] in mm",
			"position": "[x, y, z] in mm relative to the object's centre (default [0, 0, 0])",
			"rotation": "[x, y, z] degrees that turn the shape about its centre, applied about X, then Y, then Z of the bed (default none)",
			"values":   "settings for kind modifier, key to value, for example {\"sparse_infill_density\": \"40%\"}",
			"name":     "part name (default: the kind)",
		},
	},
	"set_height_ranges": {
		Description: `Replace an object's height ranges: layers between two heights get their own settings, for example denser infill or a different layer height in one band. The list replaces the current one and an empty list clears it. Ranges must not overlap and are checked at layer range scope.`,
		Params: map[string]string{
			"object":          "object id or unique name",
			"ranges":          "the full list of ranges, replacing the current one; empty clears it",
			"ranges[].from_z": "range start in mm above the bed, at or above 0",
			"ranges[].to_z":   "range end in mm above the bed, above from_z",
			"ranges[].values": "settings for the range, key to value",
		},
	},
	"set_layer_actions": {
		Description: `Replace the actions of a plate: a pause, a colour change to a filament slot, or custom G-code, each at a height. The list replaces the current one. Give z in mm or a layer number (starting at 1, converted with the project's layer heights). A colour change needs a second filament slot in the project.`,
		Params: map[string]string{
			"plate":              "plate, starting at 1 (default 1)",
			"actions":            "the full list of actions, replacing the current one",
			"actions[].z":        "height in mm where the action happens; give z or layer",
			"actions[].layer":    "layer number, starting at 1; give layer or z",
			"actions[].type":     "pause, color_change or custom",
			"actions[].filament": "filament slot to change to, starting at 1; for color_change",
			"actions[].gcode":    "G-code lines to insert; for custom",
		},
	},
	"manage_plates": {
		Description: `Add, remove, rename, lock or unlock a plate, or set plate settings (bed type, print sequence, spiral mode). Removing a plate that has objects is refused: move or remove them first. Locking sets the plate's lock flag, which the app honours when it arranges; the arrange option of slice_project ignores it.`,
		Params: map[string]string{
			"action": "add, remove, rename, lock, unlock or set",
			"plate":  "plate number, starting at 1 (not used for add)",
			"name":   "new name, for rename or add",
			"values": "for set: curr_bed_type, print_sequence, first_layer_print_sequence, other_layers_print_sequence or spiral_mode, key to value",
		},
	},
	"export_project": {
		Description: `Write the project as a Creality Print 3MF file, for painting, supports or checks in the app. The store keeps its copy and the file is not linked to it: after saving changes in the app, call open_project on the saved file. An existing file is only replaced with overwrite true.`,
		Params: map[string]string{
			"path":      "absolute path of the .3mf file to write; missing folders are created",
			"overwrite": "replace an existing file (default false)",
		},
	},
	"delete_project": {
		Description: `Delete a project from the store: its folder, including the G-code of its slices. Exported files and the files it was built from are not touched. This cannot be undone.`,
		Params:      map[string]string{"confirm": "the project id, typed again to confirm"},
	},

	// Slicing.
	"slice_project": {
		Description: `Slice a project with the installed Creality Print and write G-code into the project folder. Reports per plate the time, filament grams per tool, filament changes, layers and objects, warnings, and a handoff block for the creality-k2-mcp server: gcode_path, upload_name, the tools with their filament type and colour (0-based), and object names for exclusion. The call waits up to wait seconds (default 60, at most 600) for the result; a slice that takes longer carries on in the background and the reply gives its job_id: poll get_slice_status. background true returns the job_id at once. timeout is only the limit for a hung slicer. Failures return the slicer's exit code, its meaning and its output.`,
		Params: map[string]string{
			"plate":      "plate to slice, starting at 1; 0 slices every plate (default 0)",
			"arrange":    "pack the objects of the plate first (gap between objects, margin to the bed edge, wipe tower kept free); the new positions are saved in the project (default false)",
			"orient":     "lay every object on its largest flat face first; saved in the project (default false)",
			"overrides":  "settings for this slice only, key to value, checked like update_settings at project scope; the project is not changed",
			"wait":       "seconds to wait for the result before the job id is returned and the slice carries on, 1 to 600 (default 60)",
			"background": "true returns a job_id at once without waiting (default false)",
			"thumbnails": "put the plate pictures the printer preset asks for into the G-code (default true)",
			"timeout":    "seconds after which a hung slicer is stopped, up to 1800 (default 1800)",
			"preview":    "none (default), small or large: attach an isometric picture of the sliced toolpaths, layer upon layer in each filament's colour (512 or 1024 px wide), and the first layer of all objects (384 or 1024 px square)",
		},
	},
	"get_slice_status": {
		Description: `Check a slice started in the background: running, finished, failed or cancelled. A finished job returns the same result as slice_project, including the handoff block. Without job_id it lists the running and recent jobs. Pass wait to hold the call until the job ends (or the seconds are up) instead of polling. Pass cancel true with a job_id to stop a job.`,
		Params: map[string]string{
			"job_id": "job id from slice_project (default: list the jobs)",
			"wait":   "seconds to hold the call while the job runs, until it ends; 0 to 600 (default 0: answer at once)",
			"cancel": "true stops the job (default false)",
		},
	},
	"get_slice_report": {
		Description: `Read the result of the last slice of a plate: summary, filaments (grams, mm, cm3 per tool), objects (labels and boxes), layers (count and heights), one layer (extrusion by feature, with a picture) or settings (values that differ from the process preset). A report older than the project says so. Slice first with slice_project.`,
		Params: map[string]string{
			"plate":    "plate, starting at 1 (default 1)",
			"section":  "summary (default), filaments, objects, layers, layer or settings",
			"layer":    "layer number, starting at 1; for section layer",
			"z":        "height in mm; for section layer, the nearest layer is used when layer is not given",
			"color_by": "feature (default), filament or speed; how the layer picture is coloured",
			"preview":  "none, small (default for section layer) or large; the layer picture, 384 or 1024 px square. On a plate printed by object it shows every object at the layer's height",
		},
	},
}

// toolTitle is a tool's name in words: "create_project" is "Create project".
func toolTitle(name string) string {
	title := strings.Join(strings.Split(name, "_"), " ")
	return strings.ToUpper(title[:1]) + title[1:]
}

// annotationsFor builds the MCP annotations of a tool.
func annotationsFor(name string) *mcp.ToolAnnotations {
	a, ok := toolAnnotations[name]
	if !ok {
		panic(fmt.Sprintf("tool %q has no annotations", name))
	}
	open, destructive := a.openWorld, a.destructive
	ann := &mcp.ToolAnnotations{
		Title:          toolTitle(name),
		ReadOnlyHint:   a.readOnly,
		IdempotentHint: a.readOnly || a.idempotent,
		OpenWorldHint:  &open,
	}
	if !a.readOnly {
		ann.DestructiveHint = &destructive
	}
	return ann
}

// describeSchema sets the description of every parameter of schema from the
// tool's own text, else sharedParams. It panics when a parameter has none, or
// when the tool's text names a parameter the schema does not have.
func describeSchema(name string, schema *jsonschema.Schema, own map[string]string) {
	used := map[string]bool{}
	var walk func(prefix string, s *jsonschema.Schema)
	walk = func(prefix string, s *jsonschema.Schema) {
		for prop, sub := range s.Properties {
			key := prefix + prop
			text, ok := own[key]
			if ok {
				used[key] = true
			} else if text, ok = sharedParams[prop]; !ok || prefix != "" {
				panic(fmt.Sprintf("tool %q: parameter %q has no description", name, key))
			}
			sub.Description = text
			if sub.Items != nil && len(sub.Items.Properties) > 0 {
				walk(key+"[].", sub.Items)
			}
		}
	}
	walk("", schema)
	for key := range own {
		if !used[key] {
			panic(fmt.Sprintf("tool %q: text for unknown parameter %q", name, key))
		}
	}
}

// addTool registers a tool with its text, title and annotations.
func addTool[In any](srv *mcp.Server, name string, schema *jsonschema.Schema,
	handler func(context.Context, *mcp.CallToolRequest, In) (*mcp.CallToolResult, any, error)) {
	text, ok := toolTexts[name]
	if !ok {
		panic(fmt.Sprintf("tool %q has no text", name))
	}
	describeSchema(name, schema, text.Params)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        name,
		Title:       toolTitle(name),
		Description: text.Description,
		Annotations: annotationsFor(name),
		InputSchema: schema,
	}, recovering(name, handler))
}

// toolTextNames lists the tools that have text, sorted (for tests).
func toolTextNames() []string {
	names := make([]string, 0, len(toolTexts))
	for name := range toolTexts {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
