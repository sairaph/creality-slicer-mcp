package mcpserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// MC2: in compare mode the body says how many of the differing keys the level
// cuts, and the front keeps the full count.
func TestCompareHeaderSaysWhatTheLevelHides(t *testing.T) {
	f := newFixture(t)
	beginner := f.ok(t, "get_preset", map[string]any{"type": "process", "name": k2Process, "compare_to": "parent"})
	front := frontOf(t, beginner)
	total, shown := front["differences"].(int), front["values_shown"].(int)
	if shown >= total {
		t.Fatalf("the fixture shows every difference at beginner level: %d of %d", shown, total)
	}
	contains(t, "cut header", bodyOf(beginner), "shows", "of", "differing key(s) at level beginner", "pass level all or keys for the rest")
	all := f.ok(t, "get_preset", map[string]any{"type": "process", "name": k2Process, "compare_to": "parent", "level": "all"})
	notContains(t, "full header", bodyOf(all), "pass level all")
	if frontOf(t, all)["differences"] != total {
		t.Errorf("differences changed with the level: %v vs %d", frontOf(t, all)["differences"], total)
	}
}

// MC3: a tool call in parallel with a refresh never leaves the presets of the
// old install cached after the refresh.
func TestRefreshWithParallelCallsKeepsTheNewInstall(t *testing.T) {
	newInstall := testInstall(t)
	idxPath := filepath.Join(newInstall.ProfileRoot, "Creality.json")
	raw, err := os.ReadFile(idxPath)
	if err != nil {
		t.Fatal(err)
	}
	var idx map[string]any
	if err := json.Unmarshal(raw, &idx); err != nil {
		t.Fatal(err)
	}
	const extra = "Extra PLA @Creality K2 0.4 nozzle"
	writeJSON(t, filepath.Join(newInstall.ProfileRoot, "Creality", "filament", extra+".json"), preset{
		"type": "filament", "name": extra, "from": "system", "instantiation": "true", "inherits": "fdm_filament_common", "filament_id": "09999",
		"filament_type": []string{"PLA"}, "compatible_printers": []string{k2Printer}})
	idx["filament_list"] = append(idx["filament_list"].([]any), map[string]any{"name": extra, "sub_path": "filament/" + extra + ".json"})
	writeJSON(t, idxPath, idx)

	for round := 0; round < 5; round++ {
		f := newFixture(t, func(fi *fakeInstall, _ *Deps) { fi.after = &newInstall })
		var wg sync.WaitGroup
		stop := make(chan struct{})
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					select {
					case <-stop:
						return
					default:
						call(t, f.cs, "list_presets", map[string]any{"type": "filament"})
					}
				}
			}()
		}
		f.ok(t, "get_slicer_status", map[string]any{"refresh": true})
		close(stop)
		wg.Wait()
		contains(t, "presets after the refresh", f.ok(t, "list_presets", map[string]any{"type": "filament"}), extra)
	}
}

// MC5: 15 and 15% are the same percentage; a percentage always shows its sign.
func TestPercentValuesShowTheirSign(t *testing.T) {
	if percentText("15") != "15%" || percentText("15%") != "15%" || percentText("auto") != "auto" {
		t.Errorf("percentText = %q %q %q", percentText("15"), percentText("15%"), percentText("auto"))
	}
}

// MC6: a dependency sentence loses the app's editor context and is cut at a
// whole condition.
func TestShortDependency(t *testing.T) {
	in := "forced to 1 when a per-plate override is not being edited and spiral_mode is on"
	if got := shortDependency(in); got != "forced to 1 when spiral_mode is on" {
		t.Errorf("shortDependency = %q", got)
	}
	long := "shown when a is on"
	for i := 0; i < 30; i++ {
		long += " and setting_" + string(rune('a'+i%26)) + " is not 1"
	}
	got := shortDependency(long)
	if len([]rune(got)) > maxDependencyChars+30 || !strings.HasSuffix(got, "... and more conditions") {
		t.Errorf("long sentence = %q", got)
	}
	if strings.Contains(got, "  ") {
		t.Errorf("double space in %q", got)
	}
}
