package mcpserver

import (
	"path/filepath"
	"strings"
	"testing"

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
