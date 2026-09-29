package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sairaph/creality-slicer-mcp/internal/catalog"
	"github.com/sairaph/creality-slicer-mcp/internal/guide"
	"github.com/sairaph/creality-slicer-mcp/internal/slicer"
)

func contains(t *testing.T, what, text string, parts ...string) {
	t.Helper()
	for _, p := range parts {
		if !strings.Contains(text, p) {
			t.Errorf("%s lacks %q:\n%s", what, p, text)
		}
	}
}

func notContains(t *testing.T, what, text string, parts ...string) {
	t.Helper()
	for _, p := range parts {
		if strings.Contains(text, p) {
			t.Errorf("%s contains %q:\n%s", what, p, text)
		}
	}
}

// --- get_slicer_status ---

func TestStatusOfASupportedInstall(t *testing.T) {
	f := newFixture(t)
	out := f.ok(t, "get_slicer_status", nil)
	front := frontOf(t, out)
	for k, want := range map[string]any{
		"installed": true, "supported": true, "version": "7.2.2", "build": "5483", "dialect": "v72",
		"profile_version": "26.08.29.19", "profile_source": "install", "gui_running": false, "catalog_version": "7.2.1",
	} {
		if front[k] != want {
			t.Errorf("front[%s] = %v, want %v", k, front[k], want)
		}
	}
	if cov, _ := front["tooltip_coverage"].(string); !strings.Contains(cov, "/") || strings.HasPrefix(cov, "0/") {
		t.Errorf("tooltip_coverage = %q", cov)
	}
	if front["projects_dir"] == "" || front["exe"] == "" || front["data_dir"] == "" {
		t.Errorf("front lacks paths: %v", front)
	}
	if _, has := front["reason"]; has {
		t.Errorf("a usable install has a reason: %v", front["reason"])
	}
	contains(t, "body", bodyOf(out), "installed and supported", "Slicing is available", "get_guide")
}

func TestStatusProfileSourceIsTheDataFolderWhenItsBundleWins(t *testing.T) {
	f := newFixture(t, func(fi *fakeInstall, _ *Deps) {
		fi.install.ProfileRoot = filepath.Join(fi.install.DataDir, "system")
	})
	if got := frontOf(t, f.ok(t, "get_slicer_status", nil))["profile_source"]; got != "data_dir" {
		t.Errorf("profile_source = %v", got)
	}
}

func TestStatusWithoutInstallAndUnsupported(t *testing.T) {
	f := newFixture(t, func(fi *fakeInstall, _ *Deps) {
		fi.install = slicer.Install{Reason: "Creality Print is not installed", ReasonCode: slicer.ReasonNotInstalled}
	})
	out := f.ok(t, "get_slicer_status", nil)
	front := frontOf(t, out)
	if front["installed"] != false || front["reason"] != "Creality Print is not installed" {
		t.Errorf("front = %v", front)
	}
	contains(t, "body", bodyOf(out), "not available", "not installed", "CREALITY_SLICER_MCP_CMD", "get_guide")

	f = newFixture(t, func(fi *fakeInstall, _ *Deps) {
		fi.install.Supported, fi.install.Dialect, fi.install.Version = false, "", "7.3.0"
		fi.install.Reason = "version 7.3.0 is not supported yet; supported: 7.2.x"
	})
	out = f.ok(t, "get_slicer_status", nil)
	contains(t, "body", bodyOf(out), "not supported", "7.3.0", "What works", "slicing (slice_project)")
	if frontOf(t, out)["supported"] != false {
		t.Error("an unsupported version reads as supported")
	}
}

func TestStatusRefreshDetectsAgainAndReloads(t *testing.T) {
	f := newFixture(t)
	f.ok(t, "get_slicer_status", nil)
	f.ok(t, "get_slicer_status", nil)
	if f.install.refreshes != 0 {
		t.Fatalf("%d refreshes without asking", f.install.refreshes)
	}
	newer := f.install.install
	newer.Version = "7.2.3"
	f.install.after = &newer
	out := f.ok(t, "get_slicer_status", map[string]any{"refresh": true})
	if f.install.refreshes != 1 || frontOf(t, out)["version"] != "7.2.3" {
		t.Errorf("refreshes = %d, version %v", f.install.refreshes, frontOf(t, out)["version"])
	}
	contains(t, "body", bodyOf(out), "detected again")
}

func TestStatusWithoutDescriptionsSaysSo(t *testing.T) {
	f := newFixture(t, func(_ *fakeInstall, d *Deps) {
		d.Texts = func(string) (catalog.TextSource, error) { return nil, fmt.Errorf("no catalogs here") }
	})
	out := f.ok(t, "get_slicer_status", nil)
	if _, has := frontOf(t, out)["tooltip_coverage"]; has {
		t.Error("tooltip_coverage reported without texts")
	}
	contains(t, "body", bodyOf(out), "descriptions are not available", "no catalogs here")
}

// --- get_guide ---

func TestGuideIndexTopicAndTerm(t *testing.T) {
	f := newFixture(t)
	idx := f.ok(t, "get_guide", nil)
	topics, _ := frontOf(t, idx)["topics"].([]any)
	if len(topics) != len(guide.Topics()) {
		t.Errorf("index lists %d topics, want %d", len(topics), len(guide.Topics()))
	}
	for _, name := range guide.Topics() {
		contains(t, "index", idx, "`"+name+"`")
		if topicSummaries[name] == "" {
			t.Errorf("topic %q has no summary line", name)
		}
	}

	page := f.ok(t, "get_guide", map[string]any{"topic": "Start"})
	want, _ := guide.Topic("start")
	if frontOf(t, page)["topic"] != "start" || strings.TrimSpace(bodyOf(page)) != strings.TrimSpace(want) {
		t.Error("the start page is not the guide's start topic")
	}

	term := f.ok(t, "get_guide", map[string]any{"term": "flush"})
	contains(t, "term reply", term, "**Flush**", "**Flush matrix**")
	notContains(t, "term reply", term, "**Seam**")
	if frontOf(t, term)["topic"] != "glossary" {
		t.Error("a term lookup is not a glossary reply")
	}
	both := f.ok(t, "get_guide", map[string]any{"topic": "glossary", "term": "seam"})
	contains(t, "glossary term", both, "**Seam**")
}

func TestGuideErrors(t *testing.T) {
	f := newFixture(t)
	out := f.errText(t, "get_guide", map[string]any{"topic": "nope"})
	contains(t, "unknown topic", out, "code: not_found", "start", "get_guide")
	out = f.errText(t, "get_guide", map[string]any{"term": "zzzz-not-a-term"})
	contains(t, "unknown term", out, "code: not_found", "glossary")
	out = f.errText(t, "get_guide", map[string]any{"topic": "supports", "term": "seam"})
	contains(t, "term with topic", out, "code: invalid_input", "glossary")
}

// --- search_settings ---

func TestSearchFindsAndShowsK2Defaults(t *testing.T) {
	f := newFixture(t)
	out := f.ok(t, "search_settings", map[string]any{"query": "wall loops"})
	front := frontOf(t, out)
	if front["query"] != "wall loops" || front["catalog_version"] != "7.2.1" {
		t.Errorf("front = %v", front)
	}
	body := bodyOf(out)
	contains(t, "search", body, "key | label | area | unit | K2 default | level", "wall_loops | ", "process/Strength/Walls")
	// The K2 process preset sets wall_loops to 2: taken from the preset, not the catalog.
	var row string
	for _, l := range strings.Split(body, "\n") {
		if strings.HasPrefix(l, "wall_loops | ") {
			row = l
		}
	}
	if !strings.Contains(row, "| 2 |") || strings.Contains(row, "(catalog)") {
		t.Errorf("wall_loops row = %q, want the preset value 2", row)
	}
	// The description excerpt (first sentence only) sits under the row.
	contains(t, "search", body, "  A synthetic description of the setting.")
	notContains(t, "search", body, "second sentence")
	// A key the K2 presets do not set falls back to the catalog and says so.
	out = f.ok(t, "search_settings", map[string]any{"query": "elefant foot compensation", "level": "all"})
	contains(t, "search", bodyOf(out), "(catalog)", "marks a setting those presets do not set")
	if frontOf(t, out)["defaults"] != "catalog" && frontOf(t, out)["defaults"] != "mixed" {
		t.Errorf("defaults = %v", frontOf(t, out)["defaults"])
	}
}

func TestSearchDefaultsComeFromTheCatalogWithoutAStore(t *testing.T) {
	f := newFixture(t, func(fi *fakeInstall, _ *Deps) {
		fi.install = slicer.Install{Reason: "Creality Print is not installed"}
	})
	out := f.ok(t, "search_settings", map[string]any{"query": "layer height"})
	if frontOf(t, out)["defaults"] != "catalog" {
		t.Errorf("defaults = %v", frontOf(t, out)["defaults"])
	}
	contains(t, "search", bodyOf(out), "could not be read", "layer_height | ", "(catalog)")
}

func TestSearchLevelsScopesAndAreas(t *testing.T) {
	f := newFixture(t)
	count := func(args map[string]any) int {
		n, _ := frontOf(t, f.ok(t, "search_settings", args))["count"].(int)
		return n
	}
	beginner, advanced, all := count(nil), count(map[string]any{"level": "advanced"}), count(map[string]any{"level": "all"})
	if !(beginner > 0 && beginner < advanced && advanced < all) {
		t.Errorf("counts beginner %d, advanced %d, all %d: want them to grow", beginner, advanced, all)
	}
	object := count(map[string]any{"scope": "object", "level": "all"})
	process := count(map[string]any{"scope": "process", "level": "all"})
	if object == 0 || object >= all || process == 0 || process >= all {
		t.Errorf("object %d, process %d of %d", object, process, all)
	}
	quality := count(map[string]any{"area": "process/Quality", "level": "all"})
	if quality == 0 || quality >= process {
		t.Errorf("area process/Quality found %d of %d process settings", quality, process)
	}
	out := f.ok(t, "search_settings", map[string]any{"area": "process/Quality", "level": "all", "query": "layer height"})
	for _, l := range strings.Split(bodyOf(out), "\n") {
		if strings.Contains(l, " | ") && !strings.HasPrefix(l, "key |") && !strings.Contains(l, "Columns:") && !strings.Contains(l, "process/Quality") {
			t.Errorf("a row is outside the area: %q", l)
		}
	}
}

func TestSearchPagesAndEmptyResults(t *testing.T) {
	f := newFixture(t)
	first := f.ok(t, "search_settings", map[string]any{"level": "all"})
	front := frontOf(t, first)
	pages, _ := front["total_pages"].(int)
	if pages < 3 || front["page"] != 1 {
		t.Fatalf("front = %v: want several pages", front)
	}
	contains(t, "page 1", bodyOf(first), "Next: page=2.")
	last := f.ok(t, "search_settings", map[string]any{"level": "all", "page": pages})
	notContains(t, "last page", bodyOf(last), "Next: page=")
	past := f.ok(t, "search_settings", map[string]any{"level": "all", "page": pages + 5})
	contains(t, "past the end", bodyOf(past), "past the end")

	none := f.ok(t, "search_settings", map[string]any{"query": "qqqzzzxxx"})
	contains(t, "no match", bodyOf(none), "No setting matches", "level \"all\"")
	if frontOf(t, none)["count"] != 0 {
		t.Error("count of an empty search is not 0")
	}
}

func TestSearchRejectsBadArguments(t *testing.T) {
	f := newFixture(t)
	for _, args := range []map[string]any{{"level": "expert"}, {"scope": "galaxy"}, {"page": 0}} {
		out := f.errText(t, "search_settings", args)
		contains(t, fmt.Sprint(args), out, "code: invalid_input")
	}
}

// --- describe_setting ---

func TestDescribeSettingFrontAndBody(t *testing.T) {
	f := newFixture(t)
	out := f.ok(t, "describe_setting", map[string]any{"key": "wall_loops"})
	front := frontOf(t, out)
	for k, want := range map[string]any{"key": "wall_loops", "type": "int", "level": "beginner", "area": "process/Strength/Walls"} {
		if front[k] != want {
			t.Errorf("front[%s] = %v, want %v", k, front[k], want)
		}
	}
	scopes, _ := front["scopes"].([]any)
	if len(scopes) == 0 || scopes[0] != "project" {
		t.Errorf("scopes = %v", front["scopes"])
	}
	if front["label"] == "" || front["default"] == nil {
		t.Errorf("front = %v", front)
	}
	body := bodyOf(out)
	contains(t, "body", body, "## Description", "A synthetic description of the setting. It has a second sentence.",
		"## Dependencies", "## Related settings", "## How to change it", "update_settings", "\"wall_loops\"", "\"project\"")
	// The example value is valid for the setting.
	cat, _ := catalog.Load(catalogVersion)
	if err := cat.Validate("wall_loops", 3, catalog.ScopePreset, catalog.ValidateOptions{}); err != nil {
		t.Fatalf("test premise: %v", err)
	}
}

func TestDescribeSettingEnumsRangesAndVectors(t *testing.T) {
	f := newFixture(t)
	out := f.ok(t, "describe_setting", map[string]any{"key": "support_type"})
	enum, _ := frontOf(t, out)["enum"].([]any)
	if len(enum) < 4 {
		t.Errorf("enum = %v", enum)
	}
	out = f.ok(t, "describe_setting", map[string]any{"key": "support_threshold_angle"})
	front := frontOf(t, out)
	if front["min"] == nil || front["max"] == nil || front["unit"] == nil {
		t.Errorf("range/unit missing: %v", front)
	}
	out = f.ok(t, "describe_setting", map[string]any{"key": "nozzle_temperature"})
	contains(t, "vector", bodyOf(out), "per-filament", "one value per filament slot")
	if frontOf(t, out)["vector"] != true {
		t.Error("vector flag missing")
	}
}

func TestDescribeSettingUnknownKeySuggestsClosest(t *testing.T) {
	f := newFixture(t)
	out := f.errText(t, "describe_setting", map[string]any{"key": "wall_loop"})
	contains(t, "unknown key", out, "code: not_found", "Did you mean", "wall_loops", "search_settings")
	out = f.errText(t, "describe_setting", map[string]any{"key": "zzzzqqqq"})
	contains(t, "unknown key", out, "code: not_found", "search_settings")
}

func TestDescribeSettingWithoutDescriptionText(t *testing.T) {
	f := newFixture(t, func(_ *fakeInstall, d *Deps) {
		d.Texts = func(string) (catalog.TextSource, error) { return nil, fmt.Errorf("i18n missing") }
	})
	out := f.ok(t, "describe_setting", map[string]any{"key": "wall_loops"})
	contains(t, "body", bodyOf(out), "No description in this Creality Print build", "i18n missing")
}

func TestDescribeSettingLockedByVendorPolicy(t *testing.T) {
	cat, _ := catalog.Load(catalogVersion)
	var locked *catalog.Option
	for _, o := range cat.Options() {
		if o.VendorLock == "read_only" {
			locked = o
			break
		}
	}
	if locked == nil {
		t.Skip("the catalog has no read-only key")
	}
	f := newFixture(t)
	out := f.ok(t, "describe_setting", map[string]any{"key": locked.Key})
	if frontOf(t, out)["locked"] != "read_only" {
		t.Errorf("locked = %v", frontOf(t, out)["locked"])
	}
	contains(t, "body", bodyOf(out), "read-only", "allow_locked")
}

func TestDescribeSettingWithPresetShowsValueAndOrigin(t *testing.T) {
	f := newFixture(t)
	ref := "process:" + k2Process
	out := f.ok(t, "describe_setting", map[string]any{"key": "layer_height", "preset": ref})
	front := frontOf(t, out)
	if front["current"] != "0.2" || front["origin"] != k2Process {
		t.Errorf("current %v origin %v", front["current"], front["origin"])
	}
	contains(t, "body", bodyOf(out), "the value is `0.2`", k2Process)
	// A value inherited from the base names the base as its origin.
	out = f.ok(t, "describe_setting", map[string]any{"key": "sparse_infill_density", "preset": "process:0.28mm Standard @Creality K2 0.4 nozzle"})
	if frontOf(t, out)["origin"] != "fdm_process_common" {
		t.Errorf("origin = %v", frontOf(t, out)["origin"])
	}
	// A key the preset does not set.
	out = f.ok(t, "describe_setting", map[string]any{"key": "wall_generator", "preset": ref})
	contains(t, "body", bodyOf(out), "does not set this key")
	if _, has := frontOf(t, out)["current"]; has {
		t.Error("current is set for a key the preset lacks")
	}
}

func TestDescribeSettingPresetErrors(t *testing.T) {
	f := newFixture(t)
	contains(t, "bad form", f.errText(t, "describe_setting", map[string]any{"key": "layer_height", "preset": "no-colon"}), "code: invalid_input", "type:name")
	contains(t, "bad type", f.errText(t, "describe_setting", map[string]any{"key": "layer_height", "preset": "wheel:x"}), "code: invalid_input")
	contains(t, "unknown", f.errText(t, "describe_setting", map[string]any{"key": "layer_height", "preset": "process:No such"}), "code: not_found", "list_presets")
	g := newFixture(t, func(fi *fakeInstall, _ *Deps) { fi.install = slicer.Install{} })
	contains(t, "no install", g.errText(t, "describe_setting", map[string]any{"key": "layer_height", "preset": "process:x"}), "code: unavailable", "get_slicer_status")
}

// --- browse_settings ---

func TestBrowseSettingsWalksDownToSettings(t *testing.T) {
	f := newFixture(t)
	top := f.ok(t, "browse_settings", nil)
	contains(t, "top", bodyOf(top), "(top)", "process", "filament", "printer", "Next: browse_settings")
	if frontOf(t, top)["kind"] != "root" || frontOf(t, top)["children"].(int) < 3 {
		t.Errorf("top front = %v", frontOf(t, top))
	}
	tab := f.ok(t, "browse_settings", map[string]any{"path": "process"})
	contains(t, "tab", bodyOf(tab), "`process/Quality`", "`process/Strength`")
	group := f.ok(t, "browse_settings", map[string]any{"path": "process/Strength/Walls"})
	contains(t, "group", bodyOf(group), "label | key | unit | default", "wall_loops | ", "describe_setting")
	if frontOf(t, group)["children"] != 0 || frontOf(t, group)["kind"] != "group" {
		t.Errorf("group front = %v", frontOf(t, group))
	}
	// Case and slashes are forgiven.
	if got := f.ok(t, "browse_settings", map[string]any{"path": "/PROCESS/strength/"}); frontOf(t, got)["path"] != "process/Strength" {
		t.Errorf("path = %v", frontOf(t, got)["path"])
	}
}

func TestBrowseSettingsLevelAndErrors(t *testing.T) {
	f := newFixture(t)
	beginner := frontOf(t, f.ok(t, "browse_settings", map[string]any{"path": "process"}))["settings"].(int)
	all := frontOf(t, f.ok(t, "browse_settings", map[string]any{"path": "process", "level": "all"}))["settings"].(int)
	if beginner >= all {
		t.Errorf("beginner %d, all %d", beginner, all)
	}
	contains(t, "unknown path", f.errText(t, "browse_settings", map[string]any{"path": "process/Nowhere"}), "code: not_found", "browse_settings")
	contains(t, "bad level", f.errText(t, "browse_settings", map[string]any{"level": "wizard"}), "code: invalid_input")
}

// --- list_presets ---

func TestListPresetsDefaultsToTheK2(t *testing.T) {
	f := newFixture(t)
	out := f.ok(t, "list_presets", map[string]any{"type": "filament"})
	front := frontOf(t, out)
	if front["type"] != "filament" || front["printer"] != k2Printer || front["count"] != 2 || front["profile_version"] != "26.08.29.19" {
		t.Errorf("front = %v", front)
	}
	body := bodyOf(out)
	contains(t, "filaments", body, "name | source | type | vendor | filament_id | nozzle temp",
		k2Filament+" | system | PLA | Creality | 04001 | 220", "Generic PETG @Creality K2 0.4 nozzle | system | PETG")
	notContains(t, "filaments", body, "Creality K1", "fdm_filament_common")

	proc := f.ok(t, "list_presets", map[string]any{"type": "process"})
	contains(t, "process", bodyOf(proc), "name | source | layer height | walls | infill", k2Process+" | system | 0.2 | 2 | 15%", "0.28mm Standard")
	if frontOf(t, proc)["count"] != 2 {
		t.Errorf("process count = %v", frontOf(t, proc)["count"])
	}
}

func TestListPresetsFiltersAndPrinterChoices(t *testing.T) {
	f := newFixture(t)
	petg := f.ok(t, "list_presets", map[string]any{"type": "filament", "filament_type": "petg"})
	if frontOf(t, petg)["count"] != 1 {
		t.Errorf("PETG count = %v\n%s", frontOf(t, petg)["count"], petg)
	}
	user := f.ok(t, "list_presets", map[string]any{"type": "filament", "source": "user"})
	contains(t, "no user presets", bodyOf(user), "No filament presets match")

	printers := f.ok(t, "list_presets", map[string]any{"type": "printer"})
	contains(t, "printers", bodyOf(printers), "name | source | nozzle | bed", "Creality K2 0.4 nozzle | system | 0.4 | 260x260", "Creality K2 0.6 nozzle")
	notContains(t, "printers", bodyOf(printers), "Creality K1")
	if !strings.Contains(fmt.Sprint(frontOf(t, printers)["printer"]), "K2") {
		t.Errorf("front = %v", frontOf(t, printers))
	}
	anyP := f.ok(t, "list_presets", map[string]any{"type": "filament", "printer": "any"})
	contains(t, "any", bodyOf(anyP), "Generic PLA @Creality K1 0.4 nozzle", k2Filament)
	k1 := f.ok(t, "list_presets", map[string]any{"type": "filament", "printer": "Creality K1 0.4 nozzle"})
	contains(t, "k1", bodyOf(k1), "Generic PLA @Creality K1 0.4 nozzle")
	notContains(t, "k1", bodyOf(k1), k2Filament)
}

func TestListPresetsErrorsAndPaging(t *testing.T) {
	f := newFixture(t)
	contains(t, "unknown printer", f.errText(t, "list_presets", map[string]any{"type": "process", "printer": "Nope"}), "code: not_found", "list_presets")
	contains(t, "bad type", f.errText(t, "list_presets", map[string]any{"type": "gcode"}), "code: invalid_input")
	past := f.ok(t, "list_presets", map[string]any{"type": "filament", "page": 9})
	contains(t, "past", bodyOf(past), "past the end")
	g := newFixture(t, func(fi *fakeInstall, _ *Deps) { fi.install = slicer.Install{Reason: "Creality Print is not installed"} })
	contains(t, "no install", g.errText(t, "list_presets", map[string]any{"type": "filament"}), "code: unavailable", "get_slicer_status")
}

// --- get_preset ---

func TestGetPresetShowsGroupedFlattenedValues(t *testing.T) {
	f := newFixture(t)
	out := f.ok(t, "get_preset", map[string]any{"type": "process", "name": k2Process})
	front := frontOf(t, out)
	chain, _ := front["inherits_chain"].([]any)
	if front["name"] != k2Process || front["source"] != "system" || len(chain) != 2 || chain[1] != "fdm_process_common" {
		t.Errorf("front = %v", front)
	}
	if front["values_shown"].(int) < 3 {
		t.Errorf("values_shown = %v", front["values_shown"])
	}
	compat, _ := front["compatible_printers"].([]any)
	if len(compat) != 1 || compat[0] != k2Printer {
		t.Errorf("compatible_printers = %v", front["compatible_printers"])
	}
	body := bodyOf(out)
	// Flattened: the value the child sets wins, an inherited one shows too.
	contains(t, "body", body, "### process/Quality/Layer height", "- layer_height", "= 0.2", "### process/Strength/Walls", "- wall_loops", "= 2",
		"inherits fdm_process_common", "Showing beginner level")
	notContains(t, "body", body, "= 0.3\n") // the base's layer height is overridden
	// The GUI order: Quality before Strength.
	if strings.Index(body, "process/Quality") > strings.Index(body, "process/Strength") {
		t.Error("areas are not in the app's order")
	}
}

func TestGetPresetCompareKeysAndLevel(t *testing.T) {
	f := newFixture(t)
	parent := f.ok(t, "get_preset", map[string]any{"type": "process", "name": k2Process, "compare_to": "parent", "level": "all"})
	front := frontOf(t, parent)
	if front["compare_to"] != "fdm_process_common" || front["differences"].(int) < 3 {
		t.Errorf("front = %v", front)
	}
	contains(t, "compare", bodyOf(parent), "layer_height", "0.2 -> 0.3", "wall_loops", "2 -> 3", "enable_support", "0 -> 1")

	other := f.ok(t, "get_preset", map[string]any{"type": "process", "name": k2Process, "compare_to": "0.28mm Standard @Creality K2 0.4 nozzle", "keys": []string{"layer_height", "wall_generator"}})
	contains(t, "compare other", bodyOf(other), "layer_height", "0.2 -> 0.28", "Not set in this preset (or unchanged in compare mode): wall_generator")
	notContains(t, "compare other", bodyOf(other), "wall_loops")

	keys := f.ok(t, "get_preset", map[string]any{"type": "filament", "name": k2Filament, "keys": []string{"filament_type", "nozzle_temperature", "no_such_key"}})
	contains(t, "keys", bodyOf(keys), "filament_type", "= PLA", "nozzle_temperature", "= 220", "Not set in this preset")
	if frontOf(t, keys)["values_shown"] != 2 {
		t.Errorf("values_shown = %v", frontOf(t, keys)["values_shown"])
	}

	beginner := frontOf(t, f.ok(t, "get_preset", map[string]any{"type": "filament", "name": k2Filament}))["values_shown"].(int)
	all := frontOf(t, f.ok(t, "get_preset", map[string]any{"type": "filament", "name": k2Filament, "level": "all"}))["values_shown"].(int)
	if beginner >= all {
		t.Errorf("beginner %d shows as many values as all %d", beginner, all)
	}
}

func TestGetPresetErrors(t *testing.T) {
	f := newFixture(t)
	contains(t, "unknown", f.errText(t, "get_preset", map[string]any{"type": "process", "name": "Nope"}), "code: not_found", "list_presets")
	contains(t, "unknown compare", f.errText(t, "get_preset", map[string]any{"type": "process", "name": k2Process, "compare_to": "Nope"}), "code: not_found", "compare with")
	contains(t, "no parent", f.errText(t, "get_preset", map[string]any{"type": "process", "name": "fdm_process_common", "compare_to": "parent"}), "code: invalid_input")
	contains(t, "bad type", f.errText(t, "get_preset", map[string]any{"type": "wheel", "name": "x"}), "code: invalid_input")
}

// --- shared behaviour ---

func TestNothingLoadsUntilAToolNeedsIt(t *testing.T) {
	f := newFixture(t)
	if f.install.gets != 0 || f.install.refreshes != 0 {
		t.Errorf("constructing the server detected the install (%d gets)", f.install.gets)
	}
	f.ok(t, "get_guide", nil)
	if f.install.gets != 0 {
		t.Errorf("get_guide detected the install")
	}
	f.ok(t, "get_slicer_status", nil)
	f.ok(t, "get_slicer_status", nil)
	f.ok(t, "list_presets", map[string]any{"type": "process"})
	if f.install.refreshes != 0 {
		t.Errorf("%d refreshes", f.install.refreshes)
	}
}

// --- catalog drift ---

func TestStatusReportsCatalogDrift(t *testing.T) {
	f := newFixture(t)
	out := f.ok(t, "get_slicer_status", nil)
	front := frontOf(t, out)
	if front["catalog_drift"] != 0 {
		t.Fatalf("catalog_drift = %v, want 0 for a bundle of known settings", front["catalog_drift"])
	}
	contains(t, "body", bodyOf(out), "knows every setting the installed K2 presets set")

	g := newFixture(t, func(fi *fakeInstall, _ *Deps) {
		root := fi.install.ProfileRoot
		addKey(t, root, "process", k2Process, "zz_new_process_setting", "1")
		addKey(t, root, "filament", "Generic PETG @Creality K2 0.4 nozzle", "aa_new_filament_setting", "1")
		// A preset of another model does not count.
		addKey(t, root, "filament", "Generic PLA @Creality K1 0.4 nozzle", "k1_only_setting", "1")
	})
	out = g.ok(t, "get_slicer_status", nil)
	if frontOf(t, out)["catalog_drift"] != 2 {
		t.Fatalf("catalog_drift = %v, want 2", frontOf(t, out)["catalog_drift"])
	}
	contains(t, "body", bodyOf(out), "Catalog drift", "2 setting(s)", "aa_new_filament_setting, zz_new_process_setting", "7.2.1", "passed through untouched")
	notContains(t, "body", bodyOf(out), "k1_only_setting")
}

func TestStatusDriftListsAtMostTenKeys(t *testing.T) {
	f := newFixture(t, func(fi *fakeInstall, _ *Deps) {
		for i := 0; i < 12; i++ {
			addKey(t, fi.install.ProfileRoot, "process", k2Process, fmt.Sprintf("zz_new_%02d", i), "1")
		}
	})
	out := f.ok(t, "get_slicer_status", nil)
	contains(t, "body", bodyOf(out), "12 setting(s)", "zz_new_09", "and 2 more")
	notContains(t, "body", bodyOf(out), "zz_new_10")
}

func TestStatusHasNoDriftFieldWithoutPresets(t *testing.T) {
	f := newFixture(t, func(fi *fakeInstall, _ *Deps) { fi.install = slicer.Install{Reason: "Creality Print is not installed"} })
	if _, has := frontOf(t, f.ok(t, "get_slicer_status", nil))["catalog_drift"]; has {
		t.Error("catalog_drift reported without an install")
	}
}

// --- the command line entry points ---

func TestSlicerStatusAndPresetListAreTheToolFunctions(t *testing.T) {
	f := newFixture(t)
	srv := newServer(f.cfg)
	res := srv.SlicerStatus(context.Background(), false)
	if res.IsError || text(t, res) != f.ok(t, "get_slicer_status", nil) {
		t.Error("SlicerStatus differs from the get_slicer_status tool")
	}
	all := srv.PresetList(context.Background(), PresetListArgs{Type: "filament"})
	viaTool := f.ok(t, "list_presets", map[string]any{"type": "filament"})
	if text(t, all) != viaTool {
		t.Errorf("PresetList differs from list_presets on one page:\n%s\n---\n%s", text(t, all), viaTool)
	}
	bad := srv.PresetList(context.Background(), PresetListArgs{Type: "gcode"})
	if !bad.IsError || !strings.Contains(text(t, bad), "code: invalid_input") {
		t.Errorf("a bad type: %v", text(t, bad))
	}
	petg := srv.PresetList(context.Background(), PresetListArgs{Type: "filament", FilamentType: "PETG", Printer: "any", Source: "system"})
	contains(t, "petg", text(t, petg), "Generic PETG")
	notContains(t, "petg", text(t, petg), k2Filament)
}

func TestPresetListIgnoresPaging(t *testing.T) {
	f := newFixture(t)
	srv := newServer(f.cfg)
	for i := 0; i < 60; i++ {
		name := fmt.Sprintf("Bulk %02d @Creality K2 0.4 nozzle", i)
		writeJSON(t, filepath.Join(f.install.install.ProfileRoot, "Creality", "filament", name+".json"), preset{
			"type": "filament", "name": name, "from": "system", "instantiation": "true", "inherits": "fdm_filament_common",
			"filament_type": []string{"PLA"}, "compatible_printers": []string{k2Printer}, "filament_id": fmt.Sprintf("9%04d", i),
			"filament_vendor": []string{strings.Repeat("Vendor of a long name ", 12)},
		})
	}
	// Register them in the index.
	idxPath := filepath.Join(f.install.install.ProfileRoot, "Creality.json")
	raw, _ := os.ReadFile(idxPath)
	var idx map[string]any
	_ = json.Unmarshal(raw, &idx)
	list := idx["filament_list"].([]any)
	for i := 0; i < 60; i++ {
		name := fmt.Sprintf("Bulk %02d @Creality K2 0.4 nozzle", i)
		list = append(list, map[string]any{"name": name, "sub_path": "filament/" + name + ".json"})
	}
	idx["filament_list"] = list
	writeJSON(t, idxPath, idx)

	paged := f.ok(t, "list_presets", map[string]any{"type": "filament"})
	if frontOf(t, paged)["total_pages"].(int) < 2 {
		t.Skip("the bulk presets fit one page")
	}
	all := text(t, srv.PresetList(context.Background(), PresetListArgs{Type: "filament"}))
	front := frontOf(t, all)
	if front["total_pages"] != 1 || front["count"] != 62 {
		t.Errorf("front = %v", front)
	}
	contains(t, "all", all, "Bulk 00", "Bulk 59")
	notContains(t, "all", all, "Next: page=")
}
