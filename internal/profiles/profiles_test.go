package profiles

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/sairaph/creality-slicer-mcp/internal/userhome/testhome"
)

func TestMain(m *testing.M) { os.Exit(testhome.Run(m)) }

const (
	printerA4 = "Test Printer A 0.4 nozzle"
	printerA6 = "Test Printer A 0.6 nozzle"
	printerB4 = "Test Printer B 0.4 nozzle"
	proc4     = "0.20mm Standard @Test A 0.4"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(Roots{InstallProfiles: filepath.Join("testdata", "bundle"), DataDir: filepath.Join("testdata", "data")})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func names(ds []Descriptor) []string {
	out := make([]string, len(ds))
	for i, d := range ds {
		out[i] = d.Name
	}
	return out
}

func mustGet(t *testing.T, s *Store, typ Type, name string) Preset {
	t.Helper()
	p, err := s.Get(typ, name)
	if err != nil {
		t.Fatalf("Get(%s, %q): %v", typ, name, err)
	}
	return p
}

func TestOpenInfo(t *testing.T) {
	s := open(t)
	info := s.Info()
	if info.Vendor != "Creality" || info.Version != "2.0.0.1" || info.InstallVersion != "2.0.0.1" || info.DataVersion != "1.0.0.0" {
		t.Errorf("%+v", info)
	}
	if info.Root != filepath.Join("testdata", "bundle") || info.DataRoot != filepath.Join("testdata", "data", "system") {
		t.Errorf("%+v", info)
	}
	want := []string{filepath.Join("testdata", "data", "user", "4242"), filepath.Join("testdata", "data", "user", "default")}
	if !reflect.DeepEqual(info.UserDirs, want) {
		t.Errorf("user dirs %v", info.UserDirs)
	}
}

func writeBundle(t *testing.T, root, version string, processes ...string) {
	t.Helper()
	var list []string
	if err := os.MkdirAll(filepath.Join(root, "Creality", "process"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, p := range processes {
		list = append(list, `{"name": "`+p+`", "sub_path": "process/`+p+`.json"}`)
		body := `{"type":"process","name":"` + p + `","from":"system","instantiation":"true","layer_height":"0.2"}`
		if err := os.WriteFile(filepath.Join(root, "Creality", "process", p+".json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	index := `{"name":"Creality","version":"` + version + `","machine_list":[],"filament_list":[],"process_list":[` + strings.Join(list, ",") + `]}`
	if err := os.WriteFile(filepath.Join(root, "Creality.json"), []byte(index), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestOpenPicksTheNewerBundle(t *testing.T) {
	data := t.TempDir()
	writeBundle(t, filepath.Join(data, "system"), "3.0.0.0", "Only In Data")
	s, err := Open(Roots{InstallProfiles: filepath.Join("testdata", "bundle"), DataDir: data})
	if err != nil {
		t.Fatal(err)
	}
	info := s.Info()
	if info.Root != filepath.Join(data, "system") || info.Version != "3.0.0.0" || info.InstallVersion != "2.0.0.1" || info.DataVersion != "3.0.0.0" {
		t.Errorf("%+v", info)
	}
	got, err := s.List(TypeProcess, Filter{})
	if err != nil || len(got) != 1 || got[0].Name != "Only In Data" {
		t.Errorf("%v %v", names(got), err)
	}
	if got[0].File != filepath.Join(data, "system", "Creality", "process", "Only In Data.json") {
		t.Errorf("file %q", got[0].File)
	}
}

func TestOpenTieKeepsTheInstall(t *testing.T) {
	data := t.TempDir()
	writeBundle(t, filepath.Join(data, "system"), "2.0.0.1", "Only In Data")
	s, err := Open(Roots{InstallProfiles: filepath.Join("testdata", "bundle"), DataDir: data})
	if err != nil {
		t.Fatal(err)
	}
	if s.Info().Root != filepath.Join("testdata", "bundle") {
		t.Errorf("%+v", s.Info())
	}
}

func TestOpenOnlyDataAndErrors(t *testing.T) {
	data := t.TempDir()
	writeBundle(t, filepath.Join(data, "system"), "1.0", "P")
	if s, err := Open(Roots{DataDir: data}); err != nil || s.Info().Root != filepath.Join(data, "system") || len(s.Info().UserDirs) != 0 {
		t.Errorf("%v %+v", err, s)
	}
	if _, err := Open(Roots{}); err == nil {
		t.Error("no roots must fail")
	}
	if _, err := Open(Roots{InstallProfiles: t.TempDir(), DataDir: t.TempDir()}); err == nil {
		t.Error("roots without a vendor index must fail")
	}
}

func TestListPrinters(t *testing.T) {
	s := open(t)
	all, err := s.List(TypePrinter, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{printerA4, "Test Printer A 0.4 nozzle - Copy(1)", printerA6, printerB4}
	if !reflect.DeepEqual(names(all), want) {
		t.Errorf("printers %q", names(all))
	}
	a := all[0]
	if a.PrinterModel != "Model A" || a.PrinterVariant != "0.4" || a.NozzleDiameter != "0.4" || a.BedX != 260 || a.BedY != 260 ||
		a.Source != SourceSystem || !a.Selectable || a.Inherits != "fdm_test_common" {
		t.Errorf("%+v", a)
	}
	b := all[3]
	if b.BedX != 300 || b.BedY != 250 {
		t.Errorf("bed %v x %v", b.BedX, b.BedY)
	}
	model, _ := s.List(TypePrinter, Filter{PrinterModel: "model a"})
	if !reflect.DeepEqual(names(model), want[:3]) {
		t.Errorf("by model %q", names(model))
	}
	one, _ := s.List(TypePrinter, Filter{Printer: printerB4})
	if !reflect.DeepEqual(names(one), []string{printerB4}) {
		t.Errorf("by name %q", names(one))
	}
	bases, _ := s.List(TypePrinter, Filter{IncludeBases: true, Source: "system"})
	if len(bases) != 5 || bases[0].Name != "fdm_machine_common" || bases[0].Selectable {
		t.Errorf("with bases %q", names(bases))
	}
	user, _ := s.List(TypePrinter, Filter{Source: "user"})
	if len(user) != 1 || user[0].Source != SourceUser {
		t.Errorf("user printers %q", names(user))
	}
}

func TestListProcessesByPrinterCompatibility(t *testing.T) {
	s := open(t)
	got, err := s.List(TypeProcess, Filter{Printer: printerA4})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"0.20mm BadCond @Test", "0.20mm Cond @Test", proc4, proc4 + " - Copy(1)", "Untitled"}
	if !reflect.DeepEqual(names(got), want) {
		t.Errorf("A 0.4: %q", names(got))
	}
	got, _ = s.List(TypeProcess, Filter{Printer: printerA6})
	want = []string{"0.20mm BadCond @Test", "0.30mm Standard @Test A 0.6", "Untitled"}
	if !reflect.DeepEqual(names(got), want) {
		t.Errorf("A 0.6 (the condition says nozzle 0.4): %q", names(got))
	}
	got, _ = s.List(TypeProcess, Filter{PrinterModel: "Model B"})
	if !reflect.DeepEqual(names(got), []string{"0.20mm BadCond @Test", "Untitled"}) {
		t.Errorf("model B: %q", names(got))
	}
	if got[0].LayerHeight != "0.2" || got[0].WallLoops != "2" || got[0].InfillDense != "15%" {
		t.Errorf("%+v", got[0])
	}
	// The broken presets are skipped with a warning, not fatal.
	warned := strings.Join(s.Warnings(), "\n")
	for _, w := range []string{"does-not-exist.json", "no such parent", "inherits chain loops"} {
		if !strings.Contains(warned, w) {
			t.Errorf("warnings miss %q:\n%s", w, warned)
		}
	}
}

func TestListFilaments(t *testing.T) {
	s := open(t)
	got, err := s.List(TypeFilament, Filter{Printer: printerA4})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Generic ABS", "My PLA", "TP-PETG @Test A 0.4", "TP-PLA @Test A 0.4", "TP-PLA Special @Test A 0.4"}
	if !reflect.DeepEqual(names(got), want) {
		t.Errorf("%q", names(got))
	}
	byName := map[string]Descriptor{}
	for _, d := range got {
		byName[d.Name] = d
	}
	pla := byName["TP-PLA @Test A 0.4"]
	if pla.FilamentType != "PLA" || pla.FilamentVendor != "Creality" || pla.FilamentID != "04001" || pla.NozzleTemp != "210" {
		t.Errorf("%+v", pla)
	}
	if byName["My PLA"].NozzleTemp != "215" || byName["My PLA"].Source != SourceUser || byName["Generic ABS"].FilamentType != "ABS" {
		t.Errorf("%+v %+v", byName["My PLA"], byName["Generic ABS"])
	}
	petg, _ := s.List(TypeFilament, Filter{Printer: printerA4, FilamentType: "petg"})
	if !reflect.DeepEqual(names(petg), []string{"TP-PETG @Test A 0.4"}) {
		t.Errorf("%q", names(petg))
	}
	model, _ := s.List(TypeFilament, Filter{PrinterModel: "Model A", Source: "system"})
	if len(model) != 5 || !contains(names(model), "TP-PLA @Test A 0.6") {
		t.Errorf("by model: %q", names(model))
	}
	user, _ := s.List(TypeFilament, Filter{Source: "user"})
	if !reflect.DeepEqual(names(user), []string{"My PLA"}) {
		t.Errorf("user filaments %q", names(user))
	}
	if !strings.Contains(strings.Join(s.Warnings(), "\n"), "Broken.json") {
		t.Errorf("the unparsable user file must produce a warning: %v", s.Warnings())
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func TestListErrors(t *testing.T) {
	s := open(t)
	if _, err := s.List(TypeProcess, Filter{Printer: "no such printer"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("%v", err)
	}
	if _, err := s.List(TypeProcess, Filter{Source: "cloud"}); err == nil {
		t.Error("unknown source")
	}
}

func TestFlattenPrinter(t *testing.T) {
	s := open(t)
	p := mustGet(t, s, TypePrinter, printerA4)
	if !reflect.DeepEqual(p.InheritsChain, []string{printerA4, "fdm_test_common", "fdm_machine_common"}) {
		t.Errorf("chain %v", p.InheritsChain)
	}
	if p.Parent() != "fdm_test_common" || p.Source != SourceSystem || !p.Selectable || p.Type != TypePrinter {
		t.Errorf("%+v", p)
	}
	for key, want := range map[string]struct{ value, origin string }{
		"gcode_flavor":        {"klipper", "fdm_test_common"},
		"nozzle_volume":       {"183", printerA4},
		"min_layer_height":    {"0.08", printerA4},
		"retract_length":      {"1", "fdm_machine_common"},
		"printer_settings_id": {"", "fdm_machine_common"},
		"printer_model":       {"Model A", printerA4},
	} {
		if got := p.String(key); got != want.value || p.Origin[key] != want.origin {
			t.Errorf("%s = %q from %q, want %q from %q", key, got, p.Origin[key], want.value, want.origin)
		}
	}
	if _, ok := p.Values["inherits"]; ok {
		t.Error("inherits must not survive flattening")
	}
	if got := p.Values["nozzle_diameter"]; !reflect.DeepEqual(got, []string{"0.4"}) {
		t.Errorf("vector value %#v", got)
	}
	if got := p.Values["machine_start_gcode"]; got != "M140 S0\nSTART_PRINT BED=[bed_temperature] IF {a < b && c > d}" {
		t.Errorf("gcode %q", got)
	}
	if !reflect.DeepEqual(p.List("nozzle_diameter"), []string{"0.4"}) || !reflect.DeepEqual(p.List("gcode_flavor"), []string{"klipper"}) || p.List("missing") != nil {
		t.Error("List helper")
	}
	base := mustGet(t, s, TypePrinter, "fdm_machine_common")
	if base.Selectable || len(base.InheritsChain) != 1 || base.Parent() != "" {
		t.Errorf("%+v", base)
	}
}

func TestFlattenProcessAndFilamentChains(t *testing.T) {
	s := open(t)
	p := mustGet(t, s, TypeProcess, proc4)
	if !reflect.DeepEqual(p.InheritsChain, []string{proc4, "fdm_process_test_common", "fdm_process_common"}) {
		t.Errorf("chain %v", p.InheritsChain)
	}
	for key, want := range map[string]struct{ value, origin string }{
		"top_layers":            {"4", "fdm_process_common"},
		"enable_prime_tower":    {"1", "fdm_process_test_common"},
		"exclude_object":        {"1", "fdm_process_test_common"},
		"wall_loops":            {"2", "fdm_process_common"},
		"layer_height":          {"0.2", proc4},
		"sparse_infill_density": {"15%", "fdm_process_common"},
		"setting_id":            {"GP101", proc4},
	} {
		if got := p.String(key); got != want.value || p.Origin[key] != want.origin {
			t.Errorf("%s = %q from %q, want %q from %q", key, got, p.Origin[key], want.value, want.origin)
		}
	}
	if !reflect.DeepEqual(p.Values["compatible_printers"], []string{printerA4}) || p.Origin["compatible_printers"] != proc4 {
		t.Errorf("child list must replace the base's empty list: %#v", p.Values["compatible_printers"])
	}

	f := mustGet(t, s, TypeFilament, "TP-PLA Special @Test A 0.4")
	wantChain := []string{"TP-PLA Special @Test A 0.4", "TP-PLA @Test A 0.4", "fdm_filament_pla", "fdm_filament_common"}
	if !reflect.DeepEqual(f.InheritsChain, wantChain) {
		t.Errorf("chain %v", f.InheritsChain)
	}
	if f.String("filament_id") != "04099" || f.String("setting_id") != "GFSA04" || f.Origin["setting_id"] != "TP-PLA @Test A 0.4" ||
		f.String("nozzle_temperature") != "210" || f.Origin["filament_type"] != "fdm_filament_pla" {
		t.Errorf("%+v", f.Origin)
	}
	abs := mustGet(t, s, TypeFilament, "Generic ABS")
	if len(abs.InheritsChain) != 2 || abs.String("filament_type") != "ABS" || abs.String("nozzle_temperature") != "200" {
		t.Errorf("two level chain: %+v", abs.InheritsChain)
	}
}

func TestGetErrors(t *testing.T) {
	s := open(t)
	if _, err := s.Get(TypeProcess, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("%v", err)
	}
	if _, err := s.Get(TypeProcess, "0.20mm Broken @Test"); !errors.Is(err, ErrInheritsNotFound) || !strings.Contains(err.Error(), "no such parent") {
		t.Errorf("%v", err)
	}
	if _, err := s.Get(TypeProcess, "Loop A"); !errors.Is(err, ErrInheritsCycle) {
		t.Errorf("%v", err)
	}
	// A name of another type is not found.
	if _, err := s.Get(TypeFilament, printerA4); !errors.Is(err, ErrNotFound) {
		t.Errorf("%v", err)
	}
}

func TestUserPresets(t *testing.T) {
	s := open(t)
	proc := mustGet(t, s, TypeProcess, proc4+" - Copy(1)")
	if proc.Source != SourceUser || proc.String("layer_height") != "0.28" || proc.Origin["layer_height"] != proc.Name ||
		proc.String("enable_prime_tower") != "1" || proc.Parent() != proc4 || len(proc.InheritsChain) != 4 {
		t.Errorf("%+v", proc)
	}
	if proc.Info["base_id"] != "GP101" || proc.Info["updated_time"] != "1790000001" || proc.Info["setting_id"] != "" {
		t.Errorf("info %v", proc.Info)
	}
	if !strings.HasSuffix(filepath.ToSlash(proc.File), "user/4242/process/0.20mm Standard @Test A 0.4 - Copy(1).json") {
		t.Errorf("file %q", proc.File)
	}
	base := mustGet(t, s, TypeProcess, "Untitled")
	if base.Source != SourceUser || len(base.InheritsChain) != 1 || base.String("wall_loops") != "5" || base.Info["updated_time"] != "1790000002" {
		t.Errorf("preset from the base folder: %+v", base)
	}
	printer := mustGet(t, s, TypePrinter, "Test Printer A 0.4 nozzle - Copy(1)")
	if printer.String("nozzle_volume") != "150" || printer.String("printer_model") != "Model A" || printer.Info["base_id"] != "70001" {
		t.Errorf("%+v", printer)
	}
	filament := mustGet(t, s, TypeFilament, "My PLA")
	if filament.String("nozzle_temperature") != "215" || filament.String("filament_id") != "04001" || filament.Source != SourceUser {
		t.Errorf("%+v", filament)
	}
	if system := mustGet(t, s, TypeProcess, proc4); system.Info != nil {
		t.Errorf("system presets have no sidecar: %v", system.Info)
	}
}

func TestGetReturnsIndependentCopies(t *testing.T) {
	s := open(t)
	a := mustGet(t, s, TypePrinter, printerA4)
	a.Values["nozzle_volume"] = "1"
	a.Values["nozzle_diameter"].([]string)[0] = "9"
	a.Origin["nozzle_volume"] = "x"
	a.InheritsChain[0] = "x"
	b := mustGet(t, s, TypePrinter, printerA4)
	if b.String("nozzle_volume") != "183" || b.String("nozzle_diameter") != "0.4" || b.Origin["nozzle_volume"] != printerA4 || b.InheritsChain[0] != printerA4 {
		t.Errorf("cache was modified through a returned preset: %+v", b)
	}
}

func TestCompatible(t *testing.T) {
	s := open(t)
	a4 := mustGet(t, s, TypePrinter, printerA4)
	a6 := mustGet(t, s, TypePrinter, printerA6)
	copyPrinter := mustGet(t, s, TypePrinter, "Test Printer A 0.4 nozzle - Copy(1)")
	std := mustGet(t, s, TypeProcess, proc4)
	cond := mustGet(t, s, TypeProcess, "0.20mm Cond @Test")
	bad := mustGet(t, s, TypeProcess, "0.20mm BadCond @Test")
	untitled := mustGet(t, s, TypeProcess, "Untitled")

	check := func(name string, printer, preset Preset, wantOK, wantEval bool) {
		t.Helper()
		if ok, ev := Compatible(printer, preset); ok != wantOK || ev != wantEval {
			t.Errorf("%s: got (%v, %v), want (%v, %v)", name, ok, ev, wantOK, wantEval)
		}
	}
	check("listed", a4, std, true, true)
	check("not listed", a6, std, false, true)
	check("user printer through its parent", copyPrinter, std, true, true)
	check("empty lists, no condition", a6, untitled, true, true)
	check("condition true", a4, cond, true, true)
	check("condition false", a6, cond, false, true)
	check("unparsable condition counts as compatible, flagged", a4, bad, true, false)

	special := mustGet(t, s, TypeFilament, "TP-PLA Special @Test A 0.4")
	std6 := mustGet(t, s, TypeProcess, "0.30mm Standard @Test A 0.6")
	if ok, ev := CompatiblePrint(std, special); !ok || !ev {
		t.Errorf("compatible_prints listed: %v %v", ok, ev)
	}
	if ok, ev := CompatiblePrint(std6, special); ok || !ev {
		t.Errorf("compatible_prints not listed: %v %v", ok, ev)
	}
	pla := mustGet(t, s, TypeFilament, "TP-PLA @Test A 0.4")
	if ok, ev := CompatiblePrint(std6, pla); !ok || !ev {
		t.Errorf("no compatible_prints: %v %v", ok, ev)
	}
}

func TestDiff(t *testing.T) {
	s := open(t)
	diffs, err := s.Diff(TypePrinter, printerA4, printerA6)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Difference{}
	var keys []string
	for _, d := range diffs {
		got[d.Key] = d
		keys = append(keys, d.Key)
	}
	want := []string{"min_layer_height", "name", "nozzle_diameter", "nozzle_volume", "printer_variant", "setting_id"}
	if !reflect.DeepEqual(keys, want) {
		t.Errorf("keys %v", keys)
	}
	if d := got["min_layer_height"]; !d.InA || d.InB || d.A != "0.08" || d.B != nil {
		t.Errorf("%+v", d)
	}
	if d := got["nozzle_volume"]; d.A != "183" || d.B != "100" {
		t.Errorf("%+v", d)
	}
	if d := got["nozzle_diameter"]; !reflect.DeepEqual(d.A, []string{"0.4"}) || !reflect.DeepEqual(d.B, []string{"0.6"}) {
		t.Errorf("%+v", d)
	}
	if same, _ := s.Diff(TypePrinter, printerA4, printerA4); len(same) != 0 {
		t.Errorf("%v", same)
	}
	if _, err := s.Diff(TypePrinter, printerA4, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("%v", err)
	}
}

func readBack(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, data)
	}
	return doc
}

func TestWriteFlatSystemPreset(t *testing.T) {
	s := open(t)
	p := mustGet(t, s, TypePrinter, printerA4)
	out := filepath.Join(t.TempDir(), "nested", "dir", "machine.json")
	if err := s.WriteFlat(p, out); err != nil {
		t.Fatal(err)
	}
	doc := readBack(t, out)
	if _, ok := doc["inherits"]; ok {
		t.Error("inherits must be removed")
	}
	if doc["type"] != "machine" || doc["from"] != "system" || doc["name"] != printerA4 || doc["instantiation"] != "true" {
		t.Errorf("%v %v %v %v", doc["type"], doc["from"], doc["name"], doc["instantiation"])
	}
	if doc["setting_id"] != "70001" || doc["gcode_flavor"] != "klipper" || doc["machine_start_gcode"] != p.Values["machine_start_gcode"] {
		t.Errorf("values lost: %v", doc)
	}
	if !reflect.DeepEqual(doc["nozzle_diameter"], []any{"0.4"}) {
		t.Errorf("vector %#v", doc["nozzle_diameter"])
	}
	raw, _ := os.ReadFile(out)
	if !strings.Contains(string(raw), "a < b && c > d") {
		t.Errorf("< > & must not be escaped:\n%s", raw)
	}
	if !strings.HasPrefix(string(raw), "{\n    \"type\": \"machine\",\n    \"from\": \"system\",\n    \"name\"") {
		t.Errorf("identity keys first:\n%.120s", raw)
	}
	// A base (instantiation false) is written as instantiation true.
	base := mustGet(t, s, TypeFilament, "fdm_filament_pla")
	out2 := filepath.Join(t.TempDir(), "f.json")
	if err := WriteFlat(base, out2); err != nil {
		t.Fatal(err)
	}
	if d := readBack(t, out2); d["instantiation"] != "true" || d["type"] != "filament" {
		t.Errorf("%v", d)
	}
}

func TestWriteFlatUserPresetAndOverwrite(t *testing.T) {
	s := open(t)
	p := mustGet(t, s, TypeProcess, proc4+" - Copy(1)")
	dir := t.TempDir()
	out := filepath.Join(dir, "process.json")
	if err := os.WriteFile(out, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteFlat(p, out); err != nil {
		t.Fatal(err)
	}
	doc := readBack(t, out)
	if doc["from"] != "User" || doc["type"] != "process" || doc["layer_height"] != "0.28" || doc["enable_prime_tower"] != "1" || doc["wall_loops"] != "2" {
		t.Errorf("%v", doc)
	}
	if _, ok := doc["inherits"]; ok {
		t.Error("inherits must be removed")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("temporary files left behind: %v", entries)
	}
	// An empty vector stays [] and not null.
	empty := mustGet(t, s, TypeProcess, proc4)
	empty.Values["compatible_printers"] = []string{}
	out3 := filepath.Join(dir, "p3.json")
	if err := WriteFlat(empty, out3); err != nil {
		t.Fatal(err)
	}
	if v := readBack(t, out3)["compatible_printers"]; !reflect.DeepEqual(v, []any{}) {
		t.Errorf("%#v", v)
	}
	// Writing to an impossible path fails cleanly.
	if err := WriteFlat(p, filepath.Join(out, "sub", "x.json")); err == nil {
		t.Error("expected an error")
	}
}

func TestRoundTripEqualsFlatten(t *testing.T) {
	s := open(t)
	for _, tc := range []struct {
		typ  Type
		name string
	}{{TypePrinter, printerA4}, {TypeProcess, proc4}, {TypeFilament, "TP-PLA Special @Test A 0.4"}} {
		p := mustGet(t, s, tc.typ, tc.name)
		out := filepath.Join(t.TempDir(), "x.json")
		if err := WriteFlat(p, out); err != nil {
			t.Fatal(err)
		}
		back, err := readJSONFile(out)
		if err != nil {
			t.Fatal(err)
		}
		for k, v := range p.Values {
			if k == "instantiation" {
				continue
			}
			if !reflect.DeepEqual(back[k], v) {
				t.Errorf("%s %s: %q = %#v, read back %#v", tc.typ, tc.name, k, v, back[k])
			}
		}
	}
}

func TestParseType(t *testing.T) {
	for in, want := range map[string]Type{"printer": TypePrinter, "Machine": TypePrinter, " process ": TypeProcess, "print": TypeProcess, "filament": TypeFilament} {
		if got, err := ParseType(in); err != nil || got != want {
			t.Errorf("ParseType(%q) = %q, %v", in, got, err)
		}
	}
	if _, err := ParseType("sla"); err == nil {
		t.Error("unknown type")
	}
}

func TestCompareVersions(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"26.08.29.19", "26.07.18.17", 1}, {"26.8.29.19", "26.08.29.19", 0}, {"2", "10", -1}, {"1.0", "1.0.0", 0}, {"", "0", 0}, {"1.2", "1.10", -1},
	} {
		if got := CompareVersions(c.a, c.b); got != c.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestConcurrentUse(t *testing.T) {
	s := open(t)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, typ := range Types {
				if _, err := s.List(typ, Filter{PrinterModel: "Model A"}); err != nil {
					t.Error(err)
				}
			}
			if _, err := s.Get(TypeProcess, proc4); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
}

func TestBundleRootIsUsedAsGivenWithoutChoosingAgain(t *testing.T) {
	data := t.TempDir()
	writeBundle(t, filepath.Join(data, "system"), "1.0.0.0", "Only In Data")
	// The install bundle is newer, but the caller (slicer.Install.ProfileRoot)
	// decided: Open must read exactly that folder.
	s, err := Open(Roots{InstallProfiles: filepath.Join("testdata", "bundle"), DataDir: data, Bundle: filepath.Join(data, "system")})
	if err != nil {
		t.Fatal(err)
	}
	info := s.Info()
	if info.Root != filepath.Join(data, "system") || info.Version != "1.0.0.0" || info.InstallVersion != "2.0.0.1" || info.DataVersion != "1.0.0.0" {
		t.Errorf("%+v", info)
	}
	got, _ := s.List(TypeProcess, Filter{})
	if len(got) != 1 || got[0].Name != "Only In Data" {
		t.Errorf("%v", names(got))
	}
	// Only the bundle, nothing else given.
	only, err := Open(Roots{Bundle: filepath.Join("testdata", "bundle")})
	if err != nil || only.Info().Version != "2.0.0.1" {
		t.Errorf("%v %+v", err, only)
	}
	if _, err := Open(Roots{Bundle: filepath.Join(t.TempDir(), "nothing")}); err == nil {
		t.Error("a bundle without a vendor index must fail")
	}
}

func TestWriteFlatUsesTheAtomicWriter(t *testing.T) {
	s := open(t)
	p := mustGet(t, s, TypePrinter, printerA4)
	dir := filepath.Join(t.TempDir(), "new", "folder")
	out := filepath.Join(dir, "m.json")
	if err := WriteFlat(p, out); err != nil {
		t.Fatal(err)
	}
	// Replacing an existing file works and leaves no temporary file behind.
	if err := WriteFlat(p, out); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("%v", entries)
	}
	if doc := readBack(t, out); doc["name"] != printerA4 {
		t.Errorf("%v", doc)
	}
}
