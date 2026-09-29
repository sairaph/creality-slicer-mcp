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
