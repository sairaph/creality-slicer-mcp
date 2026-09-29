package guide

import (
	"encoding/json"
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/sairaph/creality-slicer-mcp/internal/catalog"
)

// topics23 are the topics of get_guide in 23-tools-spec.md, sorted.
var topics23 = []string{
	"calibration", "glossary", "gui-handoff", "k2-combo", "modifiers-and-ranges", "multi-plate",
	"multicolor-cfs", "speed-vs-quality", "start", "strength", "supports", "surface-quality", "troubleshooting",
}

func TestEveryTopicOf23ExistsAndIsWellFormed(t *testing.T) {
	if got := Topics(); strings.Join(got, ",") != strings.Join(topics23, ",") {
		t.Fatalf("topics = %v\nwant    %v", got, topics23)
	}
	for _, name := range topics23 {
		text, ok := Topic(name)
		if !ok || strings.TrimSpace(text) == "" {
			t.Errorf("Topic(%q) = %d bytes, %v", name, len(text), ok)
			continue
		}
		first, _, _ := strings.Cut(text, "\n")
		if !strings.HasPrefix(first, "# ") || len(first) < 4 {
			t.Errorf("%s: first line %q is not a title", name, first)
		}
		if len(text) > 6600 {
			t.Errorf("%s: %d bytes, want about 6 KB at most", name, len(text))
		}
	}
	if n := len(SkillMarkdown()); n > 4096 {
		t.Errorf("SKILL.md is %d bytes, want under 4 KB", n)
	}
}

func TestNoEmOrEnDashesAnywhere(t *testing.T) {
	bad := string([]rune{0x2013, 0x2014})
	err := fs.WalkDir(FS(), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(FS(), path)
		if err != nil {
			return err
		}
		if strings.ContainsAny(string(data), bad) {
			t.Errorf("%s contains an em or en dash", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSkillIndexListsEveryTopic(t *testing.T) {
	skill := SkillMarkdown()
	for _, name := range topics23 {
		if !strings.Contains(skill, "`"+name+"`") {
			t.Errorf("SKILL.md does not list topic %q", name)
		}
	}
	front, _ := splitFrontmatter(skill)
	for _, want := range []string{"name: creality-slicer", "generator: creality-slicer-mcp", "version: \"0.1.0\"", "Use when"} {
		if !strings.Contains(front, want) {
			t.Errorf("frontmatter lacks %q", want)
		}
	}
}

// Tool names and parameters are final (23-tools-spec.md); the guide may use
// exactly these snake_case words, the keys of the settings catalog, and the
// few extra words below.
var guideTools = []string{
	"get_slicer_status", "get_guide", "search_settings", "describe_setting", "browse_settings",
	"list_presets", "get_preset", "create_project", "open_project", "list_projects", "get_project",
	"add_model", "update_object", "remove_object", "update_settings", "set_presets", "add_modifier",
	"set_height_ranges", "set_layer_actions", "manage_plates", "export_project", "delete_project",
	"slice_project", "get_slice_status", "get_slice_report", "get_view", "remove_part", "open_in_app",
	// The creality-k2-mcp server.
	"upload_gcode_file", "get_filaments", "start_print", "exclude_object", "list_printers",
}

var guideParams = []string{
	"refresh", "topic", "term", "query", "area", "level", "scope", "page", "key", "project", "preset",
	"type", "printer", "compare_to", "keys", "filament_type", "source", "name", "process", "filaments",
	"bed_type", "preview", "path", "position", "rotation", "plate", "copies", "objects", "object",
	"target", "values", "allow_locked", "keep_changes", "flush_matrix", "flush_multiplier", "kind",
	"shape", "size", "ranges", "from_z", "to_z", "actions", "action", "overwrite", "confirm", "arrange",
	"orient", "overrides", "background", "timeout", "thumbnails", "job_id", "cancel", "section", "layer",
	"spools", "into", "mode", "slot", "catalog_id", "material", "status", "color_by", "lay_flat", "colour", "filament", "scale", "focus", "view_name", "include_screenshot", "show_parts", "show_labels", "show_ranges", "hide", "isolate", "width", "height",
	// creality-k2-mcp parameters (23 section 8).
	"slot_map", "self_test", "object_name",
}

var guideWords = []string{
	// Fields of the slice result and the error code.
	"gcode_path", "upload_name", "exclude_names", "time_s", "time_text", "total_g", "slicer_error",
	// An example object label.
	"stl_id_0_copy_0", "logo_plate1", "part_id_0_copy_0",
	// Exit code names, in capitals in the text; and spelled-out words.
	"multi_material",
	// Values of enum parameters (kind, action type, support style, seam).
	"layer_range", "negative_part", "support_enforcer", "support_blocker", "color_change",
	"tree_slim", "tree_strong", "tree_hybrid", "aligned_back",
}

var snake = regexp.MustCompile(`\b[a-z][a-z0-9]*(?:_[a-z0-9]+)+\b`)

func TestGuideNamesOnlyRealToolsParametersAndSettings(t *testing.T) {
	cat, err := catalog.Load("7.2.1")
	if err != nil {
		t.Fatal(err)
	}
	known := map[string]bool{}
	for _, list := range [][]string{guideTools, guideParams, guideWords} {
		for _, w := range list {
			known[w] = true
		}
	}
	err = fs.WalkDir(FS(), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(FS(), path)
		if err != nil {
			return err
		}
		for _, tok := range snake.FindAllString(string(data), -1) {
			if known[tok] {
				continue
			}
			if _, ok := cat.Get(tok); ok {
				continue
			}
			t.Errorf("%s uses %q: not a tool, a parameter, or a setting key of the catalog", path, tok)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

var jsonBlock = regexp.MustCompile("(?s)```json\n(.*?)\n```")

func TestJSONExamplesAreValid(t *testing.T) {
	n := 0
	for _, name := range append(Topics(), "SKILL") {
		var text string
		if name == "SKILL" {
			text = SkillMarkdown()
		} else {
			text, _ = Topic(name)
		}
		for _, m := range jsonBlock.FindAllStringSubmatch(text, -1) {
			n++
			var v map[string]any
			if err := json.Unmarshal([]byte(m[1]), &v); err != nil {
				t.Errorf("%s: invalid JSON example: %v\n%s", name, err, m[1])
			}
		}
	}
	if n < 12 {
		t.Errorf("only %d JSON examples, want the workflows to carry calls", n)
	}
}

func TestExampleSettingsExistInTheCatalogWithValidValues(t *testing.T) {
	cat, err := catalog.Load("7.2.1")
	if err != nil {
		t.Fatal(err)
	}
	// Every settings map ("values") in an example: keys must be catalog keys.
	for _, name := range Topics() {
		text, _ := Topic(name)
		for _, m := range jsonBlock.FindAllStringSubmatch(text, -1) {
			var v struct {
				Values map[string]any `json:"values"`
			}
			if json.Unmarshal([]byte(m[1]), &v) != nil {
				continue
			}
			for key := range v.Values {
				if _, ok := cat.Get(key); !ok {
					t.Errorf("%s: example sets %q, which is not a catalog key", name, key)
				}
			}
		}
	}
}

func TestGlossaryLinesStartWithTheirTerm(t *testing.T) {
	text, _ := Topic("glossary")
	terms := 0
	for _, line := range strings.Split(text, "\n") {
		if line == "" || strings.HasPrefix(line, "#") || !strings.HasPrefix(line, "- ") && terms == 0 {
			continue
		}
		if !strings.HasPrefix(line, "- **") || !strings.Contains(line, "** - ") {
			if strings.HasPrefix(line, "One line per term") {
				continue
			}
			t.Errorf("glossary line does not start with a bold term: %q", line)
			continue
		}
		terms++
	}
	if terms < 70 {
		t.Errorf("%d terms, want a fuller glossary", terms)
	}
	// Every term is unique.
	seen := map[string]bool{}
	for _, line := range strings.Split(text, "\n") {
		if rest, ok := strings.CutPrefix(line, "- **"); ok {
			term, _, _ := strings.Cut(rest, "**")
			if seen[strings.ToLower(term)] {
				t.Errorf("term %q is defined twice", term)
			}
			seen[strings.ToLower(term)] = true
		}
	}
}

func TestGlossaryLookup(t *testing.T) {
	got, ok := Glossary("flush")
	if !ok || !strings.Contains(got, "**Flush**") || !strings.Contains(got, "**Flush matrix**") || strings.Contains(got, "**Seam**") {
		t.Errorf("Glossary(flush) = %q, %v", got, ok)
	}
	if lines := strings.Split(got, "\n"); len(lines) < 3 {
		t.Errorf("flush matched %d lines: %v", len(lines), lines)
	}
	if got, ok := Glossary("  SEAM "); !ok || !strings.Contains(got, "**Seam**") {
		t.Errorf("Glossary is not case and space insensitive: %q %v", got, ok)
	}
	if _, ok := Glossary("no such term"); ok {
		t.Error("an unknown term matched")
	}
	all, ok := Glossary("")
	if !ok || !strings.Contains(all, "# Glossary") {
		t.Error("an empty term should give the whole glossary")
	}
	// Terms inside a definition do not count.
	if got, _ := Glossary("nozzle"); strings.Contains(got, "**Layer height**") {
		t.Error("a term matched inside another line's definition")
	}
}

func TestTopicsAreSortedAndUnique(t *testing.T) {
	got := Topics()
	if !sort.StringsAreSorted(got) {
		t.Errorf("topics not sorted: %v", got)
	}
}
