package mcpserver

import (
	"context"
	"encoding/json"
	"github.com/sairaph/creality-slicer-mcp/internal/profiles"
	"github.com/sairaph/creality-slicer-mcp/internal/slicer"
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

// newInstallWithExtraPreset is a second install whose filament list has one
// more preset than the fixture's default.
func newInstallWithExtraPreset(t *testing.T) (in slicer.Install, extra string) {
	t.Helper()
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
	extra = "Extra PLA @Creality K2 0.4 nozzle"
	writeJSON(t, filepath.Join(newInstall.ProfileRoot, "Creality", "filament", extra+".json"), preset{
		"type": "filament", "name": extra, "from": "system", "instantiation": "true", "inherits": "fdm_filament_common", "filament_id": "09999",
		"filament_type": []string{"PLA"}, "compatible_printers": []string{k2Printer}})
	idx["filament_list"] = append(idx["filament_list"].([]any), map[string]any{"name": extra, "sub_path": "filament/" + extra + ".json"})
	writeJSON(t, idxPath, idx)
	return newInstall, extra
}

// MC3: a load that read the install before a refresh must not fill the cache
// the refresh emptied with what it built from the old install. The interleaving
// is forced on the loaders themselves: a load reads the old install and is held
// there while the refresh runs to the end, then it is let go. Each kind of lazy
// value is tried: the preset store, and the projects backend built from it.
func TestRefreshDuringALoadKeepsTheNewInstall(t *testing.T) {
	newInstall, extra := newInstallWithExtraPreset(t)
	for name, load := range map[string]func(e *env) (hasExtra bool){
		"profile store": func(e *env) bool {
			st, err := e.profileStore(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			_, gerr := st.Get(profiles.TypeFilament, extra)
			return gerr == nil
		},
		"projects backend": func(e *env) bool {
			be, err := e.projectBackend(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			_ = be
			st, err := e.profileStore(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			_, gerr := st.Get(profiles.TypeFilament, extra)
			return gerr == nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newProjFixture(t)
			f.install.mu.Lock()
			f.install.after = &newInstall
			f.install.mu.Unlock()
			e := newEnv(f.cfg.Deps.withDefaults(f.cfg))

			readOld := make(chan struct{})
			release := make(chan struct{})
			var once sync.Once
			f.install.mu.Lock()
			f.install.afterGet = func(read slicer.Install) {
				if read.ProfileRoot == newInstall.ProfileRoot {
					return // only a read of the old install is held
				}
				once.Do(func() {
					close(readOld)
					<-release
				})
			}
			f.install.mu.Unlock()

			done := make(chan struct{})
			go func() {
				defer close(done)
				load(e) // the held load: whatever it stores must not survive
			}()
			<-readOld // the load holds the old install in its hand
			if _, err := e.install(context.Background(), true); err != nil {
				t.Fatal(err)
			}
			close(release)
			<-done
			if !load(e) {
				t.Errorf("after the refresh the %s still belongs to the old install", name)
			}
		})
	}
}

// The refresh with calls in parallel, without forced timing, as a wide net.
func TestRefreshWithParallelCallsKeepsTheNewInstall(t *testing.T) {
	newInstall, extra := newInstallWithExtraPreset(t)
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
