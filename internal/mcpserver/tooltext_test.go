package mcpserver

import (
	"context"
	"encoding/json"
	"io/fs"
	"regexp"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sairaph/creality-slicer-mcp/internal/catalog"
	"github.com/sairaph/creality-slicer-mcp/internal/guide"
)

// listAllTools lists every tool of the server built with extra.
func listAllTools(t *testing.T, extra ...func(*Server)) map[string]*toolInfo {
	t.Helper()
	cs := sessionWith(t, extra...)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]*toolInfo{}
	for _, tool := range res.Tools {
		data, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		var schema jsonschema.Schema
		if err := json.Unmarshal(data, &schema); err != nil {
			t.Fatal(err)
		}
		out[tool.Name] = &toolInfo{
			description: tool.Description, schemaBytes: len(data), schema: &schema,
			titled: tool.Title != "" && tool.Annotations != nil && tool.Annotations.Title != "",
		}
	}
	return out
}

type toolInfo struct {
	description string
	schemaBytes int
	schema      *jsonschema.Schema
	titled      bool
}

// walkParams calls visit with every parameter of s, nested ones too.
func walkParams(prefix string, s *jsonschema.Schema, visit func(path, name string, p *jsonschema.Schema)) {
	for name, p := range s.Properties {
		visit(prefix+name, name, p)
		if p.Items != nil && len(p.Items.Properties) > 0 {
			walkParams(prefix+name+"[].", p.Items, visit)
		}
	}
}

// checkStandard asserts the text standard on tools.
func checkStandard(t *testing.T, tools map[string]*toolInfo) {
	t.Helper()
	if len(tools) != len(toolTexts) {
		t.Errorf("%d tools listed, %d have text", len(tools), len(toolTexts))
	}
	for name, info := range tools {
		if _, ok := toolTexts[name]; !ok {
			t.Errorf("%s has no entry in toolTexts", name)
		}
		if n := len(info.description); n == 0 || n > maxDescriptionBytes {
			t.Errorf("%s: description is %d bytes, want 1 to %d", name, n, maxDescriptionBytes)
		}
		if info.schemaBytes > maxSchemaBytes {
			t.Errorf("%s: schema is %d bytes, want at most %d", name, info.schemaBytes, maxSchemaBytes)
		}
		if !info.titled {
			t.Errorf("%s: missing title or annotations", name)
		}
		walkParams("", info.schema, func(path, _ string, p *jsonschema.Schema) {
			if n := len(p.Description); n == 0 || n > maxParamDescriptionBytes {
				t.Errorf("%s.%s: description is %d bytes, want 1 to %d", name, path, n, maxParamDescriptionBytes)
			}
		})
	}
}

func TestToolTextStaysWithinTheStandard(t *testing.T) {
	checkStandard(t, listAllTools(t))
	if n := len([]rune(serverInstructions)); n == 0 || n > maxInstructionsChars {
		t.Errorf("server instructions are %d characters, want 1 to %d", n, maxInstructionsChars)
	}
}

// The standard test above sees no tool while the real list is empty, so the
// sample tools prove that the checks themselves work.
func TestSampleToolsMeetTheStandard(t *testing.T) {
	tools := listAllTools(t, sampleTools(t))
	n := 0
	for name := range tools {
		if strings.HasPrefix(name, "sample_") {
			n++
		}
	}
	if n != 5 {
		t.Fatalf("%d sample tools listed, want 5", n)
	}
	checkStandard(t, tools)
}

func TestNoDashesInTexts(t *testing.T) {
	check := func(source, s string) {
		if strings.ContainsAny(s, string([]rune{0x2013, 0x2014})) {
			t.Errorf("%s contains an em or en dash", source)
		}
	}
	check("server instructions", serverInstructions)
	for name, tt := range toolTexts {
		check(name, tt.Description)
		for p, d := range tt.Params {
			check(name+"."+p, d)
		}
	}
	for p, d := range sharedParams {
		check("shared "+p, d)
	}
}

// snakeToken matches names such as create_project and include_preview.
var snakeToken = regexp.MustCompile(`\b[a-z]+(?:_[a-z0-9]+)+\b`)

// otherWords are the snake_case words the texts use that are neither a tool
// nor a parameter nor a catalog key: reply fields and product names.
var otherWords = map[string]bool{
	"tooltip_coverage": true,
	"filament_id":      true,
	// A client's server name, in the guide.
	"creality_k2_mcp": true,
}

// specTools are every tool of 23-tools-spec.md: names are final, so texts and
// the guide may name a tool before the phase that registers it. The exact-list
// test pins what is registered.
var specTools = []string{
	"get_slicer_status", "get_guide", "search_settings", "describe_setting", "browse_settings",
	"list_presets", "get_preset", "create_project", "open_project", "list_projects", "get_project",
	"add_model", "update_object", "remove_object", "update_settings", "set_presets", "add_modifier",
	"set_height_ranges", "set_layer_actions", "manage_plates", "export_project", "delete_project",
	"slice_project", "get_slice_status", "get_slice_report", "get_view",
}

// k2Tools are the creality-k2-mcp tools and parameters the guide and the
// instructions name (23 section 8).
var k2Tools = []string{
	"get_filaments", "upload_gcode_file", "start_print", "exclude_object", "get_current_job", "list_printers",
	"slot_map", "self_test", "object_name",
	// Fields of the slice result the guide names.
	"gcode_path", "upload_name", "exclude_names", "time_s", "time_text", "total_g", "slicer_error",
	// Values of enum parameters and an example object label.
	"layer_range", "negative_part", "support_enforcer", "support_blocker", "color_change",
	"logo_plate1", "part_id_0_copy_0", "tree_slim", "tree_strong", "tree_hybrid", "aligned_back", "stl_id_0_copy_0", "multi_material",
}

// TestTextsNameOnlyExistingTools fails when a tool description, parameter
// description, the server instructions or a file of the guide uses a
// snake_case word that is not a registered or specified tool, a parameter, a
// key of the settings catalog, a creality-k2-mcp name or one of otherWords.
func TestTextsNameOnlyExistingTools(t *testing.T) {
	tools := listAllTools(t)
	cat, err := catalog.Load(catalogVersion)
	if err != nil {
		t.Fatal(err)
	}
	known := map[string]bool{}
	for _, list := range [][]string{k2Tools} {
		for _, w := range list {
			known[w] = true
		}
	}
	for w := range otherWords {
		known[w] = true
	}
	for name, info := range tools {
		known[name] = true
		walkParams("", info.schema, func(_, param string, p *jsonschema.Schema) {
			known[param] = true
			for _, e := range p.Enum {
				if v, ok := e.(string); ok {
					known[v] = true
				}
			}
		})
	}

	check := func(source, text string) {
		for _, tok := range snakeToken.FindAllString(text, -1) {
			if known[tok] {
				continue
			}
			if _, ok := cat.Get(tok); ok {
				continue
			}
			t.Errorf("%s uses %q, which is not a tool, parameter, catalog key or listed word", source, tok)
		}
	}
	check("server instructions", serverInstructions)
	for name, info := range tools {
		check(name+" description", info.description)
		walkParams("", info.schema, func(path, _ string, p *jsonschema.Schema) {
			check(name+"."+path, p.Description)
		})
	}
	err = fs.WalkDir(guide.FS(), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(guide.FS(), path)
		if err != nil {
			return err
		}
		check("guide "+path, string(data))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// Every registered tool is one of the specified tools.
func TestRegisteredToolsAreSpecified(t *testing.T) {
	spec := map[string]bool{}
	for _, n := range specTools {
		spec[n] = true
	}
	for name := range listAllTools(t) {
		if !spec[name] {
			t.Errorf("tool %q is not in 23-tools-spec.md", name)
		}
	}
}

// mustPanic runs f and returns the panic message, failing when f does not panic.
func mustPanic(t *testing.T, f func()) (msg string) {
	t.Helper()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("did not panic")
		}
		msg, _ = r.(string)
	}()
	f()
	return ""
}

type panicInput struct {
	Name string  `json:"name"`
	Deep []inner `json:"deep,omitempty"`
}

type inner struct {
	Field string `json:"field"`
}

func TestAddToolPanicsOnIncompleteText(t *testing.T) {
	handler := func(context.Context, *mcp.CallToolRequest, panicInput) (*mcp.CallToolResult, any, error) {
		return nil, nil, nil
	}
	srv := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "0"}, nil)
	setup := func(t *testing.T, name string, text toolText, ann bool) {
		t.Helper()
		toolTexts[name] = text
		if ann {
			toolAnnotations[name] = annReadOnly
		}
		t.Cleanup(func() { delete(toolTexts, name); delete(toolAnnotations, name) })
	}
	full := toolText{Description: "d", Params: map[string]string{"name": "n", "deep": "d", "deep[].field": "f"}}

	if msg := mustPanic(t, func() { addTool(srv, "nope_tool", inputSchema[panicInput](nil), handler) }); !strings.Contains(msg, "has no text") {
		t.Errorf("no text: %q", msg)
	}
	setup(t, "no_ann_tool", full, false)
	if msg := mustPanic(t, func() { addTool(srv, "no_ann_tool", inputSchema[panicInput](nil), handler) }); !strings.Contains(msg, "has no annotations") {
		t.Errorf("no annotations: %q", msg)
	}
	setup(t, "missing_param_tool", toolText{Description: "d", Params: map[string]string{"name": "n"}}, true)
	if msg := mustPanic(t, func() { addTool(srv, "missing_param_tool", inputSchema[panicInput](nil), handler) }); !strings.Contains(msg, `parameter "deep" has no description`) {
		t.Errorf("missing parameter text: %q", msg)
	}
	setup(t, "missing_nested_tool", toolText{Description: "d", Params: map[string]string{"name": "n", "deep": "d"}}, true)
	if msg := mustPanic(t, func() { addTool(srv, "missing_nested_tool", inputSchema[panicInput](nil), handler) }); !strings.Contains(msg, `parameter "deep[].field" has no description`) {
		t.Errorf("missing nested parameter text: %q", msg)
	}
	extra := toolText{Description: "d", Params: map[string]string{"name": "n", "deep": "d", "deep[].field": "f", "ghost": "g"}}
	setup(t, "ghost_param_tool", extra, true)
	if msg := mustPanic(t, func() { addTool(srv, "ghost_param_tool", inputSchema[panicInput](nil), handler) }); !strings.Contains(msg, `unknown parameter "ghost"`) {
		t.Errorf("unknown parameter text: %q", msg)
	}
	setup(t, "ok_tool", full, true)
	addTool(srv, "ok_tool", inputSchema[panicInput](nil), handler) // must not panic
}

func TestSharedParamsDescribeParametersOfAnyTool(t *testing.T) {
	sharedParams["name"] = "shared name text"
	t.Cleanup(func() { delete(sharedParams, "name") })
	schema := inputSchema[panicInput](nil)
	describeSchema("x", schema, map[string]string{"deep": "d", "deep[].field": "f"})
	if got := schema.Properties["name"].Description; got != "shared name text" {
		t.Errorf("name description = %q", got)
	}
	// A nested parameter never takes a shared text: it must be described.
	sharedParams["field"] = "shared field text"
	t.Cleanup(func() { delete(sharedParams, "field") })
	mustPanic(t, func() { describeSchema("x", inputSchema[panicInput](nil), map[string]string{"deep": "d"}) })
}

func TestAnnotationsFollowTheTable(t *testing.T) {
	toolAnnotations["a_ro"], toolAnnotations["a_add"] = annReadOnly, annAdditive
	toolAnnotations["a_chg"], toolAnnotations["a_idem"] = annChanging, annIdempotent
	t.Cleanup(func() {
		for _, n := range []string{"a_ro", "a_add", "a_chg", "a_idem"} {
			delete(toolAnnotations, n)
		}
	})
	ro := annotationsFor("a_ro")
	if !ro.ReadOnlyHint || !ro.IdempotentHint || ro.DestructiveHint != nil || ro.Title != "A ro" {
		t.Errorf("read-only annotations = %+v", ro)
	}
	add := annotationsFor("a_add")
	if add.ReadOnlyHint || add.IdempotentHint || add.DestructiveHint == nil || *add.DestructiveHint {
		t.Errorf("additive annotations = %+v", add)
	}
	if chg := annotationsFor("a_chg"); chg.DestructiveHint == nil || !*chg.DestructiveHint {
		t.Errorf("changing annotations = %+v", chg)
	}
	if idem := annotationsFor("a_idem"); !idem.IdempotentHint || idem.ReadOnlyHint {
		t.Errorf("idempotent annotations = %+v", idem)
	}
	for _, a := range []*mcp.ToolAnnotations{ro, add} {
		if a.OpenWorldHint == nil || *a.OpenWorldHint {
			t.Errorf("open world hint = %v, want false", a.OpenWorldHint)
		}
	}
}

func TestToolTitleAndNames(t *testing.T) {
	if got := toolTitle("create_project"); got != "Create project" {
		t.Errorf("toolTitle = %q", got)
	}
	toolTexts["zz_tool"], toolTexts["aa_tool"] = toolText{}, toolText{}
	t.Cleanup(func() { delete(toolTexts, "zz_tool"); delete(toolTexts, "aa_tool") })
	names := toolTextNames()
	if len(names) < 2 || names[0] != "aa_tool" || names[len(names)-1] != "zz_tool" {
		t.Errorf("toolTextNames = %v", names)
	}
}
