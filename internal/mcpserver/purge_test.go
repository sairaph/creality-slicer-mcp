package mcpserver

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sairaph/creality-slicer-mcp/internal/projects"
)

func TestPurgeLine(t *testing.T) {
	if got := purgeLine(projects.PlateResult{}); got != "" {
		t.Fatalf("single colour: %q", got)
	}
	got := purgeLine(projects.PlateResult{PrimeTowerG: 2.2, PrimeTowerS: 90, FlushG: 22.4, FlushChanges: 30, FlushEstimated: true})
	for _, want := range []string{"prime tower 2.2 g", "flush about 22.4 g over 30 changes", "estimate"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q lacks %q", got, want)
		}
	}
}

func TestSlotMap(t *testing.T) {
	if got := slotMap([]toolFront{{Filament: 0, SpoolSlot: "T2C"}, {Filament: 1}}); got != nil {
		t.Fatalf("a tool without a slot: %+v", got)
	}
	got := slotMap([]toolFront{{Filament: 0, SpoolSlot: "T2C"}, {Filament: 1, SpoolSlot: "T1A"}})
	if len(got) != 2 || got[0] != (slotMapFront{0, "T2C"}) || got[1] != (slotMapFront{1, "T1A"}) {
		t.Fatalf("slot map %+v", got)
	}
}

func TestSpoolsThroughTheTools(t *testing.T) {
	pf := newProjFixture(t)
	out := pf.ok(t, "create_project", map[string]any{"name": "Spooled", "spools": []map[string]any{
		{"slot": "T2C", "catalog_id": "P001", "material": "PLA", "colour": "#FFFFFF", "status": "defined"},
		{"slot": "T1A", "catalog_id": "99999", "material": "PLA", "colour": "#FF0000", "status": "rfid", "name": "Red"},
	}})
	contains(t, "create_project", out, "spool_slot: T2C", "match: exact", "match: generic", "GENERIC preset", "Generic PLA @Creality K2 0.4 nozzle", "filament 2 <- spool T1A")
	id := frontOf(t, out)["project"].(string)
	pf.ok(t, "add_model", map[string]any{"project": id, "path": pf.stl})
	res := call(t, pf.cs, "slice_project", map[string]any{"project": id, "preview": "none", "background": false})
	sl := text(t, res)
	if res.IsError {
		t.Fatalf("slice_project failed:\n%s", sl)
	}
	contains(t, "slice_project", sl, "slot_map", `"slot": "T2C"`, `"slot": "T1A"`, "spool_slot: T2C", "made from those spools")
	// Both lists at once are refused.
	e := pf.errText(t, "create_project", map[string]any{"name": "x", "spools": []map[string]any{{"material": "PLA", "status": "defined"}},
		"filaments": []map[string]any{{"preset": tpPLA, "colour": "#FFFFFF"}}})
	contains(t, "both lists", e, "not both")
	// open_project into replaces the content and keeps the id.
	saved := filepath.Join(t.TempDir(), "saved.3mf")
	pf.ok(t, "export_project", map[string]any{"project": id, "path": saved})
	got := pf.ok(t, "open_project", map[string]any{"path": saved, "into": id})
	contains(t, "open_project into", got, "Replaced the content of project", id)
	if frontOf(t, got)["project"] != id {
		t.Errorf("into changed the id: %v", frontOf(t, got))
	}
}

func TestPurgeNoteFirstInTheSliceReplyAndInTheReport(t *testing.T) {
	pf := newProjFixture(t)
	id := pf.withModel(t, "Waste")
	res := call(t, pf.cs, "slice_project", map[string]any{"project": id, "preview": "none", "background": false})
	out := text(t, res)
	if res.IsError {
		t.Fatalf("slice_project failed:\n%s", out)
	}
	body := out[strings.LastIndex(out, "---\n")+4:]
	lines := strings.Split(body, "\n")
	if !strings.HasPrefix(lines[0], "Sliced 1 plate(s)") || !strings.HasPrefix(lines[1], "Purge waste ") {
		t.Fatalf("the purge note is not right after the Sliced line:\n%s", body)
	}
	if strings.Count(body, "Purge waste ") != 1 {
		t.Errorf("the note appears more than once in the body:\n%s", body)
	}
	front := frontOf(t, out)
	ws, _ := front["warnings"].([]any)
	if len(ws) == 0 || !strings.HasPrefix(ws[0].(string), "Purge waste ") {
		t.Errorf("front warnings %v", front["warnings"])
	}
	contains(t, "summary", pf.ok(t, "get_slice_report", map[string]any{"project": id}), "Purge waste ", "printing by object")
}

func TestBedTemperaturesShownWithTheFilaments(t *testing.T) {
	pf := newProjFixture(t)
	id := pf.create(t, "Bed")
	contains(t, "get_project", pf.ok(t, "get_project", map[string]any{"project": id}), "first layer bed temperature", "filament 1: 35 C", "filament 2: 35 C")
}

// A plate type the filament does not support (0 in the preset) is said so, not shown as 0 C.
func TestBedBlockSaysUnsupported(t *testing.T) {
	in := &projects.Info{
		Filaments: []projects.FilamentInfo{{Index: 1}, {Index: 2}},
		Plates:    []projects.PlateInfo{{Index: 1, BedType: "Cool Plate", BedTemps: []string{"60", "0"}}},
	}
	got := bedLines(in)
	if !strings.Contains(got, "filament 1: 60 C") || !strings.Contains(got, "filament 2: this plate type is not supported by its preset") || strings.Contains(got, "filament 2: 0 C") {
		t.Fatalf("bed block:\n%s", got)
	}
}

func TestSetLayerActionsToolChangeAndColourChange(t *testing.T) {
	pf := newProjFixture(t)
	id := pf.withModel(t, "Stripes")
	out := pf.ok(t, "set_layer_actions", map[string]any{"project": id, "actions": []map[string]any{
		{"layer": 2, "type": "tool_change", "filament": 2},
	}})
	contains(t, "tool_change", out, "tool_change to filament 2 (#000000)")
	if strings.Contains(out, "CFS filament change") {
		t.Errorf("the colour change line appears for a plain tool change:\n%s", out)
	}
	out = pf.ok(t, "set_layer_actions", map[string]any{"project": id, "actions": []map[string]any{
		{"layer": 2, "type": "color_change", "filament": 2},
	}})
	contains(t, "color_change", out, "tool_change to filament 2 (#000000)",
		"On this printer a colour change is a CFS filament change: it is stored as tool_change and the printer switches spools by itself.")
	// A tool change needs its filament, within the project.
	e := pf.errText(t, "set_layer_actions", map[string]any{"project": id, "actions": []map[string]any{{"layer": 2, "type": "tool_change"}}})
	contains(t, "no filament", e, "needs filament")
	e = pf.errText(t, "set_layer_actions", map[string]any{"project": id, "actions": []map[string]any{{"layer": 2, "type": "tool_change", "filament": 5}}})
	contains(t, "filament 5", e, "filament 5 does not exist")
}

func TestOpenProjectReportsRestoredSlots(t *testing.T) {
	pf := newProjFixture(t)
	out := pf.ok(t, "create_project", map[string]any{"name": "Slots", "spools": []map[string]any{
		{"slot": "T2C", "catalog_id": "P001", "material": "PLA", "colour": "#FFFFFF", "status": "defined"},
		{"slot": "T1A", "catalog_id": "P002", "material": "PETG", "colour": "#000000", "status": "defined"},
	}})
	id := frontOf(t, out)["project"].(string)
	file := filepath.Join(t.TempDir(), "slots.3mf")
	pf.ok(t, "export_project", map[string]any{"project": id, "path": file})
	got := pf.ok(t, "open_project", map[string]any{"path": file, "name": "Again"})
	contains(t, "open_project", got, "CFS slots restored from the file: filament 1 -> T2C, filament 2 -> T1A", "spool_slot: T2C")
}

func TestSetLayerActionsRepliesWithTheIgnoredWarning(t *testing.T) {
	pf := newProjFixture(t)
	id := pf.withModel(t, "Two")
	pf.ok(t, "add_model", map[string]any{"project": id, "path": pf.stl, "filament": 2})
	out := pf.ok(t, "set_layer_actions", map[string]any{"project": id, "actions": []map[string]any{{"layer": 2, "type": "tool_change", "filament": 2}}})
	contains(t, "mixed", out, "Warnings:", "have no effect", "every object on the plate prints with one filament")
}

func namedModel(t *testing.T, pf *projFixture, project, name string, filament int) string {
	t.Helper()
	args := map[string]any{"path": pf.stl, "name": name, "filament": filament}
	if project == "" {
		project = pf.create(t, "Proj")
	}
	args["project"] = project
	pf.ok(t, "add_model", args)
	return project
}

func TestSettingsReportListsOverrides(t *testing.T) {
	pf := newProjFixture(t)
	id := namedModel(t, pf, "", "Over", 1)
	pf.ok(t, "update_settings", map[string]any{"project": id, "scope": "object", "target": "Over", "values": map[string]any{"wall_loops": 6}})
	pf.ok(t, "slice_project", map[string]any{"project": id, "preview": "none", "background": false})
	out := pf.ok(t, "get_slice_report", map[string]any{"project": id, "section": "settings"})
	contains(t, "settings", out, "Settings below the project level on plate 1", "Object `Over`", "wall_loops: 6")
}

func TestGroupObjectsThroughTheTool(t *testing.T) {
	pf := newProjFixture(t)
	id := namedModel(t, pf, "", "First", 1)
	namedModel(t, pf, id, "Second", 2)
	out := pf.ok(t, "group_objects", map[string]any{"project": id, "objects": []string{"First", "Second"}, "name": "Pair", "include_screenshot": false})
	contains(t, "group_objects", out, "Grouped 2 objects into `Pair` (id 2) on plate 1", "Parts (name | kind | filament):\nFirst | normal_part | 1\nSecond | normal_part | 2\n")
	e := pf.errText(t, "group_objects", map[string]any{"project": id, "objects": []string{"Pair"}})
	if e == "" {
		t.Fatal("one object accepted")
	}
}

func TestUpdateSettingsTargetsThroughTheTool(t *testing.T) {
	pf := newProjFixture(t)
	id := namedModel(t, pf, "", "One", 1)
	namedModel(t, pf, id, "Two", 1)
	out := pf.ok(t, "update_settings", map[string]any{"project": id, "scope": "object", "targets": []string{"One", "Two"}, "values": map[string]any{"wall_loops": 5}})
	contains(t, "targets", out, "One:", "Two:", "wall_loops")
}

// analyzeG is a small plate G-code: one object printed on three layers, the
// inner wall of layer 2 starting in mid air at (80, 80).
const analyzeG = `; filament_diameter: 1.75
M83
;LAYER_CHANGE
;Z:0.2
;HEIGHT:0.2
; OBJECT_ID: 1
EXCLUDE_OBJECT_START NAME=Cube_id_0_copy_0
;TYPE:Outer wall
;WIDTH:0.45
G1 X10 Y10 F30000
G1 X20 Y10 E.4 F3600
G1 X20 Y20 E.4
EXCLUDE_OBJECT_END NAME=Cube_id_0_copy_0
;LAYER_CHANGE
;Z:0.4
;HEIGHT:0.2
EXCLUDE_OBJECT_START NAME=Cube_id_0_copy_0
;TYPE:Outer wall
;WIDTH:0.45
G1 X10 Y10 F30000
G1 X20 Y10 E.4 F3600
;TYPE:Inner wall
G1 X80 Y80 F30000
G1 X83 Y80 E.12 F3600
EXCLUDE_OBJECT_END NAME=Cube_id_0_copy_0
`

func TestAnalyzeToolpathsTool(t *testing.T) {
	pf := newProjFixture(t)
	pf.exec.gcode = analyzeG
	id := namedModel(t, pf, "", "Cube", 1)
	pf.ok(t, "slice_project", map[string]any{"project": id, "preview": "none", "background": false})
	out := pf.ok(t, "analyze_toolpaths", map[string]any{"project": id})
	contains(t, "default", out, "first_layers (object", "Cube | Outer wall | 1 | 2", "Cube | Inner wall | 2 | 2", "bounds in mm", "Cube | Outer wall | all")
	out = pf.ok(t, "analyze_toolpaths", map[string]any{"project": id, "measure": []string{"unsupported_starts", "flow"}, "objects": []string{"cube"}, "features": []string{"inner wall"}})
	contains(t, "findings", out, "unsupported_starts", "Cube | 2 | 0.40 | Inner wall", "flow (object")
	fm := frontOf(t, out)
	if fm["unsupported_starts"] != 1 {
		t.Errorf("front unsupported_starts = %v", fm["unsupported_starts"])
	}
	// parameter errors are results with hints
	for name, args := range map[string]map[string]any{
		"bad measure":    {"project": id, "measure": []string{"speed"}},
		"layers and z":   {"project": id, "layers": []int{1, 2}, "z": []float64{0, 1}},
		"radius":         {"project": id, "measure": []string{"radius"}},
		"layers reverse": {"project": id, "layers": []int{3, 1}},
		"bad detail":     {"project": id, "detail": "all"},
	} {
		if pf.errText(t, "analyze_toolpaths", args) == "" {
			t.Errorf("%s accepted", name)
		}
	}
	// a changed project marks the answer stale
	pf.ok(t, "update_settings", map[string]any{"project": id, "scope": "object", "target": "Cube", "values": map[string]any{"wall_loops": 5}})
	stale := pf.ok(t, "analyze_toolpaths", map[string]any{"project": id, "detail": "per_layer", "layers": []int{2, 2}})
	contains(t, "stale", stale, "this is the old toolpath", "stale: true")
}

func TestAnalyzeToolpathsUnknownObjectIsAnError(t *testing.T) {
	pf := newProjFixture(t)
	pf.exec.gcode = analyzeG
	id := namedModel(t, pf, "", "Cube", 1)
	pf.ok(t, "slice_project", map[string]any{"project": id, "preview": "none", "background": false})
	for name, objs := range map[string][]string{"none matches": {"nothing"}, "some match": {"cube", "nothing", "other"}} {
		e := pf.errText(t, "analyze_toolpaths", map[string]any{"project": id, "objects": objs})
		contains(t, name, e, "not_found", "No object named ", "Names as get_project shows them: Cube.")
		if strings.Contains(e, "_id_0_copy_0") || strings.Contains(e, "cube,") {
			t.Errorf("%s: %s", name, e)
		}
	}
	// a known name still works, whatever its spelling
	pf.ok(t, "analyze_toolpaths", map[string]any{"project": id, "objects": []string{"CUBE"}})
}

// Every page shows every measure that still has rows, findings first, and says
// which rows it holds.
func TestPageOfSectionsSharesTheBudget(t *testing.T) {
	long := func(prefix string, n int) []string {
		var rows []string
		for i := 0; i < n; i++ {
			rows = append(rows, fmt.Sprintf("%s row %d %s", prefix, i, strings.Repeat("x", 80)))
		}
		return rows
	}
	secs := []section{
		{measure: "unsupported_starts", heading: "unsupported_starts", rows: long("find", 120)},
		{measure: "first_layers", heading: "first_layers", rows: long("first", 400)},
		{measure: "bounds", heading: "bounds", rows: long("bounds", 60)},
	}
	text, pages := pageOfSections(secs, 1)
	if pages < 2 {
		t.Fatalf("pages %d", pages)
	}
	for _, want := range []string{"## unsupported_starts", "## first_layers", "## bounds", "find row 0", "first row 0", "bounds row 0", "rows 1-", "of 120", "of 400", "of 60"} {
		if !strings.Contains(text, want) {
			t.Errorf("page 1 lacks %q", want)
		}
	}
	if strings.Index(text, "## unsupported_starts") > strings.Index(text, "## first_layers") {
		t.Error("findings are not first")
	}
	// the last page still has the long measure and has no heading of a finished one
	last, n := pageOfSections(secs, pages)
	if n != pages || !strings.Contains(last, "## first_layers") || strings.Contains(last, "## bounds") {
		t.Errorf("last page:\n%.300s", last)
	}
	// every row is shown exactly once over the pages
	seen := 0
	for p := 1; p <= pages; p++ {
		tx, _ := pageOfSections(secs, p)
		seen += strings.Count(tx, " row ")
	}
	if seen != 580 {
		t.Errorf("%d rows over %d pages, want 580", seen, pages)
	}
}

// v0.3.1: a change that introduces a warning says so in its own reply, once.
func TestChangeRepliesListTheWarningsTheyIntroduce(t *testing.T) {
	pf := newProjFixture(t)
	id := namedModel(t, pf, "", "A", 1)
	pf.ok(t, "set_layer_actions", map[string]any{"project": id, "actions": []map[string]any{{"layer": 2, "type": "tool_change", "filament": 2}}})
	// A second object on filament 2 makes the tool change ignored: add_model says so.
	out := pf.ok(t, "add_model", map[string]any{"project": id, "path": pf.stl, "name": "B", "filament": 2, "include_screenshot": false})
	contains(t, "add_model", out, "Warnings:", "have no effect")
	// The next change does not introduce it again.
	again := pf.ok(t, "add_model", map[string]any{"project": id, "path": pf.stl, "name": "C", "filament": 2, "include_screenshot": false})
	if strings.Contains(again, "have no effect") {
		t.Errorf("the old warning is repeated:\n%s", again)
	}
	// Removing the cause and changing a setting does not list it either; update_object
	// moving B back to filament 1 clears it, and putting it back introduces it again.
	pf.ok(t, "update_object", map[string]any{"project": id, "object": "B", "filament": 1, "include_screenshot": false})
	pf.ok(t, "update_object", map[string]any{"project": id, "object": "C", "filament": 1, "include_screenshot": false})
	back := pf.ok(t, "update_object", map[string]any{"project": id, "object": "C", "filament": 2, "include_screenshot": false})
	contains(t, "update_object", back, "have no effect")
}

func TestSliceReplySaysIgnoredToolChanges(t *testing.T) {
	pf := newProjFixture(t)
	id := namedModel(t, pf, "", "A", 1)
	namedModel(t, pf, id, "B", 2)
	pf.ok(t, "set_layer_actions", map[string]any{"project": id, "actions": []map[string]any{{"layer": 2, "type": "tool_change", "filament": 2}}})
	res := call(t, pf.cs, "slice_project", map[string]any{"project": id, "preview": "none", "background": false})
	out := text(t, res)
	if res.IsError {
		t.Fatalf("slice failed:\n%s", out)
	}
	contains(t, "slice_project", out, "ignored by Creality Print: the plate's objects use several filaments", "have no effect")
	if strings.Contains(out, "found in the G-code") && strings.Contains(out, "tool change at layer 2") && !strings.Contains(out, "ignored by Creality Print") {
		t.Errorf("found: %s", out)
	}
	rep := pf.ok(t, "get_slice_report", map[string]any{"project": id})
	contains(t, "report", rep, "is ignored by Creality Print: the plate's objects use several filaments")
}

func TestPlainVectorAndTargetLines(t *testing.T) {
	for in, want := range map[string]string{"[60]": "60", "[60,60]": "60", "[60, 40]": "[60, 40]", "60": "60", "[]": "[]", "[a]": "a"} {
		if got := plainVector(in); got != want {
			t.Errorf("plainVector(%q) = %q, want %q", in, got, want)
		}
	}
	pf := newProjFixture(t)
	id := namedModel(t, pf, "", "One", 1)
	namedModel(t, pf, id, "Two", 1)
	out := pf.ok(t, "update_settings", map[string]any{"project": id, "scope": "object", "targets": []string{"One", "Two"}, "values": map[string]any{"wall_loops": 6}})
	fm := frontOf(t, out)
	changed, _ := fm["changed"].([]any)
	if len(changed) != 2 || !strings.HasPrefix(changed[0].(string), "One: wall_loops") || !strings.HasPrefix(changed[1].(string), "Two: wall_loops") {
		t.Errorf("front changed %v", fm["changed"])
	}
}

func TestLayerReportShowsFilamentPerTool(t *testing.T) {
	pf := newProjFixture(t)
	pf.exec.gcode = analyzeG
	id := namedModel(t, pf, "", "Cube", 1)
	pf.ok(t, "slice_project", map[string]any{"project": id, "preview": "none", "background": false})
	out := pf.ok(t, "get_slice_report", map[string]any{"project": id, "section": "layer", "layer": 1, "preview": "none"})
	contains(t, "layer", out, "Filament in this layer, path extrusion only; flush and purge are not in it (tool | mm of filament | grams):", "T0 | 0.8")
}

func TestAnalyzeNoFindingsLine(t *testing.T) {
	pf := newProjFixture(t)
	pf.exec.gcode = analyzeG
	id := namedModel(t, pf, "", "Cube", 1)
	pf.ok(t, "slice_project", map[string]any{"project": id, "preview": "none", "background": false})
	out := pf.ok(t, "analyze_toolpaths", map[string]any{"project": id, "measure": []string{"unsupported_starts", "support_contacts", "short_runs", "first_layers"}})
	contains(t, "findings first", out, "no findings: support_contacts, short_runs", "rows 1-")
	if strings.Index(out, "## unsupported_starts") > strings.Index(out, "## first_layers") {
		t.Error("findings are not first")
	}
	if strings.Contains(out, "## support_contacts") || strings.Contains(out, "## short_runs") {
		t.Errorf("empty sections are shown:\n%s", out)
	}
}

// Later pages carry no per object summary; page 1 has it.
func TestAnalyzeToolpathsSummaryOnPageOneOnly(t *testing.T) {
	pf := newProjFixture(t)
	pf.exec.gcode = analyzeG
	id := namedModel(t, pf, "", "Cube", 1)
	pf.ok(t, "slice_project", map[string]any{"project": id, "preview": "none", "background": false})
	one := pf.ok(t, "analyze_toolpaths", map[string]any{"project": id})
	if !strings.Contains(one, "objects:") || !strings.Contains(one, "first_layer: 1") {
		t.Errorf("page 1 lacks the object summary:\n%s", one)
	}
	// a page past the end is an empty page, as in the other paged tools
	e := pf.ok(t, "analyze_toolpaths", map[string]any{"project": id, "page": 2})
	contains(t, "page 2", e, "past the end")
}

func TestHandoffDescribesTheTwoStepStartPrint(t *testing.T) {
	text := handoffText(nil)
	for _, want := range []string{"two calls", "confirm_token", "no confirm_token", "sends nothing", "every warning", "spools are in place and the bed is clear"} {
		if !strings.Contains(text, want) {
			t.Errorf("handoff lacks %q:\n%s", want, text)
		}
	}
}

func TestUpdateSettingsRouteReplyListsTheIgnoredToolChange(t *testing.T) {
	pf := newProjFixture(t)
	id := namedModel(t, pf, "", "A", 1)
	namedModel(t, pf, id, "B", 1)
	pf.ok(t, "set_layer_actions", map[string]any{"project": id, "actions": []map[string]any{{"layer": 2, "type": "tool_change", "filament": 2}}})
	out := pf.ok(t, "update_settings", map[string]any{"project": id, "scope": "object", "target": "B", "values": map[string]any{"extruder": 2}})
	contains(t, "update_settings", out, "Warnings:", "have no effect")
}

// A client that gives up (its request context ends) stops the wait at once and
// the job it started is cancelled.
func TestSliceProjectStopsWhenTheClientCancels(t *testing.T) {
	pf := newProjFixture(t)
	id := namedModel(t, pf, "", "Cube", 1)
	pf.exec.block = make(chan struct{})
	pf.exec.killed = make(chan struct{})
	pf.exec.started = make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	wait := 600.0
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		_, _, _ = pf.srv.sliceProject(ctx, nil, sliceInput{Project: id, Wait: &wait})
	}()
	select {
	case <-pf.exec.started:
	case <-time.After(30 * time.Second):
		t.Fatal("the slicer run did not start")
	}
	cancel()
	select {
	case <-finished:
	case <-time.After(30 * time.Second):
		t.Fatal("the call kept waiting after the client cancelled")
	}
	select {
	case <-pf.exec.killed:
	case <-time.After(30 * time.Second):
		t.Fatal("the job was not cancelled")
	}
}

// The slice reply is stale only for the plates it shows: a change on another
// plate does not make it stale.
func TestSliceReplyStaleIsPerPlate(t *testing.T) {
	pf := newProjFixture(t)
	id := namedModel(t, pf, "", "A", 1)
	pf.ok(t, "manage_plates", map[string]any{"project": id, "action": "add", "include_screenshot": false})
	pf.ok(t, "add_model", map[string]any{"project": id, "path": pf.stl, "name": "B", "plate": 2, "include_screenshot": false})
	pf.ok(t, "slice_project", map[string]any{"project": id, "preview": "none", "background": false})
	// plate 2 changes after the slice; plate 1 does not
	pf.ok(t, "update_object", map[string]any{"project": id, "object": "B", "position": []float64{130, 130}, "include_screenshot": false})
	out := pf.ok(t, "slice_project", map[string]any{"project": id, "plate": 1, "preview": "none", "background": false})
	if strings.Contains(out, "stale: true") || strings.Contains(out, "The project changed after this slice") {
		t.Errorf("a plate 1 slice was called stale because of plate 2:\n%.400s", out)
	}
}

func TestColourChangeWithoutFilamentIsRefusedThroughTheTool(t *testing.T) {
	pf := newProjFixture(t)
	id := pf.withModel(t, "NoFilament")
	e := pf.errText(t, "set_layer_actions", map[string]any{"project": id, "actions": []map[string]any{{"layer": 2, "type": "color_change"}}})
	contains(t, "color_change", e, "needs filament", "hint: Give filament")
}

// Plate 0 with an empty plate slices only the plates that have objects, one
// slicer call per plate; the all-in-one call (which the slicer fails with -50
// on an empty plate) is never made.
func TestSliceAllPlatesSkipsAnEmptyPlateThroughTheTool(t *testing.T) {
	pf := newProjFixture(t)
	pf.exec.failAll = true
	id := pf.withModel(t, "Empty2")
	pf.ok(t, "manage_plates", map[string]any{"project": id, "action": "add"})
	out := pf.ok(t, "slice_project", map[string]any{"project": id, "preview": "none", "background": false})
	contains(t, "slice", out, "Sliced 1 plate(s)")
	if got := fmt.Sprint(pf.exec.slices); got != "[1]" {
		t.Fatalf("slicer calls %s, want [1]", got)
	}
}

// Every error of the projects layer says what to call next.
func TestEveryProjectErrorHasAHint(t *testing.T) {
	for _, code := range []string{projects.CodeInvalidInput, projects.CodeNotFound, projects.CodeConflict, projects.CodeUnavailable, projects.CodeInternal, projects.CodeSlicerError} {
		res := projFailure(&projects.Error{Code: code, Message: "x"})
		if text := fmt.Sprint(res.Content[0].(*mcp.TextContent).Text); !strings.Contains(text, "hint:") {
			t.Errorf("%s has no hint:\n%s", code, text)
		}
	}
	pf := newProjFixture(t)
	for name, call := range map[string]func() string{
		"unknown project": func() string { return pf.errText(t, "get_project", map[string]any{"project": "nope"}) },
		"unknown job": func() string {
			return pf.errText(t, "get_slice_status", map[string]any{"project": pf.withModel(t, "H"), "job_id": "nope"})
		},
		"bad model": func() string {
			return pf.errText(t, "add_model", map[string]any{"project": pf.create(t, "H2"), "path": filepath.Join(t.TempDir(), "x.stl")})
		},
	} {
		if text := call(); !strings.Contains(text, "hint:") {
			t.Errorf("%s has no hint:\n%s", name, text)
		}
	}
}

func TestSetPresetsAutoFlushThroughTheTool(t *testing.T) {
	pf := newProjFixture(t)
	id := pf.create(t, "Flush")
	out := pf.ok(t, "set_presets", map[string]any{"project": id, "flush_matrix": []int{0, 111, 222, 0}})
	contains(t, "manual", out, "flush_matrix: manual")
	out = pf.ok(t, "set_presets", map[string]any{"project": id, "auto_flush": true})
	contains(t, "auto", out, "flush_matrix: auto", "flush matrix back to automatic")
	e := pf.errText(t, "set_presets", map[string]any{"project": id, "auto_flush": true, "flush_matrix": []int{0, 1, 2, 0}})
	contains(t, "both", e, "contradict")
}

// The settings digest: no slice, an empty list, and the plate that was asked for.
func TestOverridesDigestCases(t *testing.T) {
	pf := newProjFixture(t)
	pf.exec.plates = 2
	id := namedModel(t, pf, "", "Plain", 1)
	be := ProjectBackend{Store: pf.store}
	if got := overridesDigest(be, id, 1); !strings.Contains(got, "cannot be read") || !strings.Contains(got, "has not been sliced") {
		t.Errorf("no slice: %q", got)
	}
	pf.ok(t, "manage_plates", map[string]any{"project": id, "action": "add"})
	namedModelOnPlate(t, pf, id, "Second", 2)
	pf.ok(t, "update_settings", map[string]any{"project": id, "scope": "object", "target": "Second", "values": map[string]any{"wall_loops": 5}})
	pf.ok(t, "slice_project", map[string]any{"project": id, "preview": "none", "background": false})
	// Nothing overrides the project on plate 1: the empty list is said so.
	one := overridesDigest(be, id, 1)
	if !strings.Contains(one, "plate 1") || !strings.Contains(one, "None: no plate, object, part or height range overrides") {
		t.Errorf("plate 1: %q", one)
	}
	// Plate 2 shows its own object, and plate 0 means the first sliced plate.
	two := overridesDigest(be, id, 2)
	if !strings.Contains(two, "plate 2") || !strings.Contains(two, "Object `Second`") || !strings.Contains(two, "wall_loops: 5") || strings.Contains(two, "Plain") {
		t.Errorf("plate 2: %q", two)
	}
	if zero := overridesDigest(be, id, 0); !strings.Contains(zero, "plate 1") {
		t.Errorf("plate 0: %q", zero)
	}
	if bad := overridesDigest(be, id, 7); !strings.Contains(bad, "plate 7 has not been sliced") {
		t.Errorf("plate 7: %q", bad)
	}
}

func namedModelOnPlate(t *testing.T, pf *projFixture, project, name string, plate int) {
	t.Helper()
	pf.ok(t, "add_model", map[string]any{"project": project, "path": pf.stl, "name": name, "plate": plate})
}
