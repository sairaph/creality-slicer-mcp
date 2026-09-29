package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gopkg.in/yaml.v3"

	"github.com/sairaph/creality-slicer-mcp/internal/catalog"
	"github.com/sairaph/creality-slicer-mcp/internal/slicer"
)

// fakeInstall is an InstallSource that returns a canned install and counts
// how often it was asked to detect.
type fakeInstall struct {
	mu        sync.Mutex
	install   slicer.Install
	after     *slicer.Install // returned once Refresh has been called
	gets      int
	refreshes int
}

func (f *fakeInstall) Get(context.Context) (slicer.Install, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gets++
	return f.install, nil
}

func (f *fakeInstall) Refresh(context.Context) (slicer.Install, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refreshes++
	if f.after != nil {
		f.install = *f.after
	}
	return f.install, nil
}

// fakeTexts gives every setting the same two sentence description.
type fakeTexts struct{}

func (fakeTexts) Text(uint64) (string, bool) {
	return "A synthetic description of the setting. It has a second sentence.", true
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

type preset = map[string]any

// writeBundle writes a small Creality profile bundle under dir/resources/profiles
// with K2 presets, and returns the profiles folder.
func writeBundle(t *testing.T, dir string) string {
	t.Helper()
	root := filepath.Join(dir, "resources", "profiles")
	vendor := filepath.Join(root, "Creality")
	compat04 := []string{k2Printer}
	machines := []preset{
		{"type": "machine", "name": "fdm_machine_common", "instantiation": "false", "gcode_flavor": "klipper"},
		{"type": "machine", "name": k2Printer, "from": "system", "instantiation": "true", "inherits": "fdm_machine_common",
			"setting_id": "71187", "printer_model": "Creality K2", "printer_variant": "0.4", "nozzle_diameter": []string{"0.4"},
			"printable_area": "0x0,260x0,260x260,0x260", "default_print_profile": k2Process, "default_filament_profile": k2Filament},
		{"type": "machine", "name": "Creality K2 0.6 nozzle", "from": "system", "instantiation": "true", "inherits": "fdm_machine_common",
			"setting_id": "79206", "printer_model": "Creality K2", "printer_variant": "0.6", "nozzle_diameter": []string{"0.6"},
			"printable_area": "0x0,260x0,260x260,0x260"},
		{"type": "machine", "name": "Creality K1 0.4 nozzle", "from": "system", "instantiation": "true", "inherits": "fdm_machine_common",
			"printer_model": "Creality K1", "printer_variant": "0.4", "nozzle_diameter": []string{"0.4"}, "printable_area": "0x0,220x0,220x220,0x220"},
	}
	processes := []preset{
		{"type": "process", "name": "fdm_process_common", "instantiation": "false", "layer_height": "0.3", "wall_loops": "3", "sparse_infill_density": "20%", "enable_support": "1"},
		{"type": "process", "name": k2Process, "from": "system", "instantiation": "true", "inherits": "fdm_process_common",
			"layer_height": "0.2", "wall_loops": "2", "sparse_infill_density": "15%", "enable_support": "0", "compatible_printers": compat04},
		{"type": "process", "name": "0.28mm Standard @Creality K2 0.4 nozzle", "from": "system", "instantiation": "true", "inherits": "fdm_process_common",
			"layer_height": "0.28", "compatible_printers": compat04},
	}
	filaments := []preset{
		{"type": "filament", "name": "fdm_filament_common", "instantiation": "false", "filament_type": []string{"PLA"}, "filament_vendor": []string{"Generic"}, "nozzle_temperature": []string{"200"}},
		{"type": "filament", "name": k2Filament, "from": "system", "instantiation": "true", "inherits": "fdm_filament_common", "filament_id": "04001",
			"filament_type": []string{"PLA"}, "filament_vendor": []string{"Creality"}, "nozzle_temperature": []string{"220"},
			"filament_max_volumetric_speed": []string{"18"}, "compatible_printers": compat04},
		{"type": "filament", "name": "Generic PETG @Creality K2 0.4 nozzle", "from": "system", "instantiation": "true", "inherits": "fdm_filament_common", "filament_id": "00003",
			"filament_type": []string{"PETG"}, "filament_vendor": []string{"Generic"}, "nozzle_temperature": []string{"240"}, "compatible_printers": compat04},
		{"type": "filament", "name": "Generic PLA @Creality K1 0.4 nozzle", "from": "system", "instantiation": "true", "inherits": "fdm_filament_common", "filament_id": "00001",
			"filament_type": []string{"PLA"}, "filament_vendor": []string{"Generic"}, "compatible_printers": []string{"Creality K1 0.4 nozzle"}},
	}
	index := map[string]any{"name": "Creality", "version": "26.08.29.19"}
	for key, list := range map[string]struct {
		dir     string
		presets []preset
	}{"machine_list": {"machine", machines}, "process_list": {"process", processes}, "filament_list": {"filament", filaments}} {
		var entries []map[string]string
		for _, p := range list.presets {
			name := p["name"].(string)
			writeJSON(t, filepath.Join(vendor, list.dir, name+".json"), p)
			entries = append(entries, map[string]string{"name": name, "sub_path": list.dir + "/" + name + ".json"})
		}
		index[key] = entries
	}
	writeJSON(t, filepath.Join(root, "Creality.json"), index)
	return root
}

// testInstall is a found, supported install whose bundle is written under a
// temporary folder.
func testInstall(t *testing.T) slicer.Install {
	t.Helper()
	dir := t.TempDir()
	root := writeBundle(t, dir)
	return slicer.Install{
		Found: true, Supported: true, Version: "7.2.2", Build: "5483", Dialect: "v72",
		Dir: dir, Exe: filepath.Join(dir, "CrealityPrint.exe"), DataDir: filepath.Join(dir, "data"),
		ProfileVersion: "26.08.29.19", ProfileRoot: root,
	}
}

// fixture is a server built on fakes plus the fakes themselves.
type fixture struct {
	install *fakeInstall
	cs      *mcp.ClientSession
	cfg     Config
}

// newFixture serves the seven tools over an in-memory client, on a fake
// install with a real small profile bundle and synthetic descriptions.
func newFixture(t *testing.T, mutate ...func(*fakeInstall, *Deps)) *fixture {
	t.Helper()
	f := &fixture{install: &fakeInstall{install: testInstall(t)}}
	projectsDir := filepath.Join(t.TempDir(), "projects")
	deps := Deps{
		Install:     f.install,
		Texts:       func(string) (catalog.TextSource, error) { return fakeTexts{}, nil },
		ProjectsDir: func() (string, error) { return projectsDir, nil },
	}
	for _, m := range mutate {
		m(f.install, &deps)
	}
	f.cfg = Config{Version: "1.2.3", Deps: deps}
	f.cs = sessionConfig(t, f.cfg)
	return f
}

// sessionConfig serves a server built from cfg to an in-process client.
func sessionConfig(t *testing.T, cfg Config, extra ...func(*Server)) *mcp.ClientSession {
	t.Helper()
	srv := newServer(cfg, extra...)
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	if _, err := srv.MCPServer().Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

// call runs a tool and returns its first text.
func (f *fixture) call(t *testing.T, name string, args map[string]any) (string, bool) {
	t.Helper()
	res := call(t, f.cs, name, args)
	return text(t, res), res.IsError
}

// ok runs a tool that must succeed.
func (f *fixture) ok(t *testing.T, name string, args map[string]any) string {
	t.Helper()
	out, isErr := f.call(t, name, args)
	if isErr {
		t.Fatalf("%s %v failed:\n%s", name, args, out)
	}
	return out
}

// errText runs a tool that must fail and returns its text.
func (f *fixture) errText(t *testing.T, name string, args map[string]any) string {
	t.Helper()
	out, isErr := f.call(t, name, args)
	if !isErr {
		t.Fatalf("%s %v succeeded:\n%s", name, args, out)
	}
	return out
}

// frontOf parses the YAML frontmatter of a reply.
func frontOf(t *testing.T, reply string) map[string]any {
	t.Helper()
	rest, ok := strings.CutPrefix(reply, "---\n")
	if !ok {
		t.Fatalf("no frontmatter:\n%.200s", reply)
	}
	front, _, ok := strings.Cut(rest, "\n---\n")
	if !ok {
		t.Fatalf("unterminated frontmatter:\n%.200s", reply)
	}
	var m map[string]any
	if err := yaml.Unmarshal([]byte(front), &m); err != nil {
		t.Fatalf("frontmatter is not YAML: %v\n%s", err, front)
	}
	return m
}

// bodyOf is the reply after its frontmatter.
func bodyOf(reply string) string {
	_, body, _ := strings.Cut(strings.TrimPrefix(reply, "---\n"), "\n---\n")
	return body
}

// addKey sets one more key in a preset file of the bundle at root.
func addKey(t *testing.T, root, dir, name, key, value string) {
	t.Helper()
	path := filepath.Join(root, "Creality", dir, name+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var p map[string]any
	if err := json.Unmarshal(data, &p); err != nil {
		t.Fatal(err)
	}
	p[key] = value
	writeJSON(t, path, p)
}
