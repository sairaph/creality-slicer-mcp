package mcpserver

import (
	"regexp"
	"strings"
	"testing"

	"github.com/sairaph/creality-slicer-mcp/internal/profiles"
)

// D8 (a): a list key of the printer is not described as a per-filament one.
func TestDescribeVectorKeysByOwner(t *testing.T) {
	f := newFixture(t)
	printer := bodyOf(f.ok(t, "describe_setting", map[string]any{"key": "machine_max_speed_x"}))
	notContains(t, "printer key", printer, "per-filament")
	contains(t, "printer key", printer, "printer setting holds a list", "No other setting controls when this one is shown", "Locked")
	notContains(t, "printer key", printer, "None: this setting is always shown") // D8 (b)
	filament := bodyOf(f.ok(t, "describe_setting", map[string]any{"key": "filament_max_volumetric_speed"}))
	contains(t, "filament key", filament, "per-filament setting")
}

// D8 (c): a restriction that offers every value is not stated.
func TestNoRestrictionSentenceThatOffersEveryValue(t *testing.T) {
	f := newFixture(t)
	body := bodyOf(f.ok(t, "describe_setting", map[string]any{"key": "sparse_infill_pattern"}))
	if regexp.MustCompile(`(\d+) of the (\d+) values`).MatchString(body) {
		m := regexp.MustCompile(`(\d+) of the (\d+) values`).FindStringSubmatch(body)
		if m[1] == m[2] {
			t.Errorf("a restriction to all values: %s", body)
		}
	}
}

// D8 (d): a validation error names the key once.
func TestValidationErrorsDoNotRepeatTheKey(t *testing.T) {
	if got := dedupeKeyPrefix("sparse_infill_density: sparse_infill_density: 150 is outside 0 to 100; wall_loops: wall_loops: bad"); got != "sparse_infill_density: 150 is outside 0 to 100; wall_loops: bad" {
		t.Errorf("dedupe = %q", got)
	}
	if got := dedupeKeyPrefix("layer_height: wall_loops: keeps its two different words"); got != "layer_height: wall_loops: keeps its two different words" {
		t.Errorf("dedupe changed different words: %q", got)
	}
	pf := newProjFixture(t)
	id := pf.withModel(t, "E")
	e := pf.errText(t, "update_settings", map[string]any{"project": id, "values": map[string]any{"wall_loops": 5000}})
	if strings.Contains(e, "wall_loops: wall_loops") {
		t.Errorf("the key is repeated:\n%s", e)
	}
}

// D8 (e): a count whose catalog maximum is the entry field's stop shows none.
func TestNoUpperLimitIsNotShownAsANumber(t *testing.T) {
	f := newFixture(t)
	out := f.ok(t, "describe_setting", map[string]any{"key": "wall_loops"})
	if frontOf(t, out)["max"] != nil {
		t.Errorf("front = %v", frontOf(t, out))
	}
	contains(t, "wall_loops", bodyOf(out), "no upper limit")
	// A setting with a real maximum keeps it.
	if got := frontOf(t, f.ok(t, "describe_setting", map[string]any{"key": "sparse_infill_density"}))["max"]; got == nil {
		t.Error("the maximum of sparse_infill_density was dropped")
	}
}

// D8 (f): writing a value that is already there is reported as unchanged.
func TestNoOpChangesAreReportedAsUnchanged(t *testing.T) {
	pf := newProjFixture(t)
	id := pf.withModel(t, "Same")
	pf.ok(t, "update_settings", map[string]any{"project": id, "values": map[string]any{"wall_loops": 4}})
	again := pf.ok(t, "update_settings", map[string]any{"project": id, "values": map[string]any{"wall_loops": 4}})
	contains(t, "second update", bodyOf(again), "Nothing changed", "Unchanged, already at that value: wall_loops (4)")
	notContains(t, "second update", bodyOf(again), "wall_loops: 4 -> 4")
	if fr, _ := frontOf(t, again)["changed"].([]any); len(fr) != 0 {
		t.Errorf("changed = %v", fr)
	}
	mixed := pf.ok(t, "update_settings", map[string]any{"project": id, "values": map[string]any{"wall_loops": 4, "layer_height": 0.3}})
	contains(t, "mixed", bodyOf(mixed), "Changed 1 setting(s)", "layer_height", "Unchanged, already at that value: wall_loops")
}

// D13: infill is written with its percent sign in every row.
func TestPresetRowsWritePercentConsistently(t *testing.T) {
	rows := presetRows(profiles.TypeProcess, []profiles.Descriptor{
		{Name: "a", Source: "system", LayerHeight: "0.2", WallLoops: "3", InfillDense: "15"},
		{Name: "b", Source: "system", LayerHeight: "0.2", WallLoops: "3", InfillDense: "15%"},
		{Name: "c", Source: "system", LayerHeight: "0.2", WallLoops: "3"},
	})
	for _, line := range strings.Split(rows, "\n")[:2] {
		if !strings.HasSuffix(line, "| 15%") {
			t.Errorf("row %q", line)
		}
	}
	if !strings.HasSuffix(strings.Split(rows, "\n")[2], "| -") {
		t.Errorf("row without infill: %q", rows)
	}
}

// D14: a comparison names both sides.
func TestCompareHeaderNamesBothSides(t *testing.T) {
	f := newFixture(t)
	body := bodyOf(f.ok(t, "get_preset", map[string]any{"type": "process", "name": k2Process, "compare_to": "parent", "level": "all"}))
	contains(t, "compare header", body, "Comparing process `"+k2Process+"` (this) with `fdm_process_common` (other)", "this value -> other value")
}
