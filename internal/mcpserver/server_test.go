package mcpserver

import (
	"bytes"
	"context"
	"fmt"
	"image/png"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sairaph/mcp-wizard/render"

	"github.com/sairaph/creality-slicer-mcp/internal/userhome/testhome"
)

// TestMain isolates the home directory for the whole package run.
func TestMain(m *testing.M) {
	os.Exit(testhome.Run(m))
}

// sessionWith serves a new server, with the extra registrations, to an
// in-process client. No process, socket or Creality Print is involved.
func sessionWith(t *testing.T, extra ...func(*Server)) *mcp.ClientSession {
	t.Helper()
	srv := newServer(Config{Version: "1.2.3"}, extra...)
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

// session serves the real server, with its real tool list.
func session(t *testing.T) *mcp.ClientSession { return sessionWith(t) }

func call(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return res
}

func texts(res *mcp.CallToolResult) []string {
	var out []string
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			out = append(out, tc.Text)
		}
	}
	return out
}

func images(res *mcp.CallToolResult) []*mcp.ImageContent {
	var out []*mcp.ImageContent
	for _, c := range res.Content {
		if ic, ok := c.(*mcp.ImageContent); ok {
			out = append(out, ic)
		}
	}
	return out
}

// text is the first text content item of res.
func text(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	all := texts(res)
	if len(all) == 0 {
		t.Fatalf("no text content in %+v", res)
	}
	return all[0]
}

func sortStrings(s []string) []string {
	out := append([]string(nil), s...)
	sort.Strings(out)
	return out
}

// TestToolsAreListedWithTheirSchemas asserts the exact tool list once, so that
// adding a tool is a deliberate edit of this list. The real list is empty for
// now: the tool groups arrive with later tasks.
func TestToolsAreListedWithTheirSchemas(t *testing.T) {
	cs := session(t)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, tool := range res.Tools {
		got = append(got, tool.Name)
	}
	want := []string{"add_model", "add_modifier", "browse_settings", "create_project", "delete_project", "describe_setting", "export_project", "get_guide", "get_preset", "get_project", "get_slice_report", "get_slice_status", "get_slicer_status", "get_view", "list_presets", "list_projects", "manage_plates", "open_project", "remove_object", "remove_part", "search_settings", "set_height_ranges", "set_layer_actions", "set_presets", "slice_project", "update_object", "update_settings"}
	if strings.Join(sortStrings(got), ",") != strings.Join(want, ",") {
		t.Fatalf("tools = %v\nwant %v", got, want)
	}
}

func TestServerIdentityAndInstructions(t *testing.T) {
	cs := session(t)
	init := cs.InitializeResult()
	if init.ServerInfo.Name != "creality-slicer-mcp" || init.ServerInfo.Version != "1.2.3" {
		t.Errorf("server info = %+v", init.ServerInfo)
	}
	if init.Instructions != serverInstructions || init.Instructions == "" {
		t.Errorf("instructions = %q", init.Instructions)
	}
	if init.Capabilities.Tools == nil {
		t.Error("the tools capability is not advertised while the list is empty")
	}
}

func TestSuccessResultRendersFrontmatterAndBody(t *testing.T) {
	cs := sessionWith(t, sampleTools(t))
	res := call(t, cs, "sample_ok", map[string]any{"name": "ada", "loud": true, "count": 2})
	if res.IsError {
		t.Fatalf("error result: %v", texts(res))
	}
	got := text(t, res)
	for _, want := range []string{"---\nname: ADA\ncount: 2\n---\n", "Greeted ADA 2 time(s).", "~~~json\n{\n  \"count\": 2\n}\n~~~", "~~~text\nline one\nline two\n~~~"} {
		if !strings.Contains(got, want) {
			t.Errorf("result lacks %q:\n%s", want, got)
		}
	}
	// The schema default applies when count is omitted.
	res = call(t, cs, "sample_ok", map[string]any{"name": "bo"})
	if !strings.Contains(text(t, res), "count: 1\n") {
		t.Errorf("default count not applied:\n%s", text(t, res))
	}
}

func TestInvalidArgumentsBecomeInvalidInput(t *testing.T) {
	cs := sessionWith(t, sampleTools(t))
	cases := []struct {
		name string
		args map[string]any
	}{
		{"missing required", map[string]any{}},
		{"wrong type", map[string]any{"name": 5}},
		{"out of range", map[string]any{"name": "x", "count": 99}},
		{"unknown argument", map[string]any{"name": "x", "bogus": true}},
	}
	for _, c := range cases {
		res := call(t, cs, "sample_ok", c.args)
		got := text(t, res)
		if !res.IsError || !strings.Contains(got, "code: invalid_input") ||
			!strings.Contains(got, "Invalid arguments:") ||
			!strings.Contains(got, "Call sample_ok again with arguments that match its input schema") {
			t.Errorf("%s: IsError=%v\n%s", c.name, res.IsError, got)
		}
	}
	res := call(t, cs, "sample_fail", map[string]any{"mode": "nonsense"})
	if !res.IsError || !strings.Contains(text(t, res), "code: invalid_input") {
		t.Errorf("enum violation: IsError=%v\n%s", res.IsError, text(t, res))
	}
}

func TestFailureClassification(t *testing.T) {
	cs := sessionWith(t, sampleTools(t))
	cases := []struct {
		mode      string
		code      string
		mustHave  []string
		mustNotBe string
	}{
		{"plain", "internal_error", []string{"Failed to run the sample: boom", internalHint}, ""},
		{"hint", "internal_error", []string{"Call sample_ok instead."}, internalHint},
		{"cancelled", "unavailable", []string{"the request was cancelled", cancelledHint}, ""},
		{"timeout", "unavailable", []string{timeoutHint}, ""},
		{"net", "unavailable", []string{unavailHint}, ""},
		{"typed", "conflict", []string{"already final", "Resolve it."}, "ignored hint"},
		{"reported", "slicer_error", []string{"Failed to slice the plate: the plate is empty", "Add a model first."}, ""},
	}
	for _, c := range cases {
		res := call(t, cs, "sample_fail", map[string]any{"mode": c.mode})
		got := text(t, res)
		if !res.IsError || !strings.Contains(got, "code: "+c.code+"\n") {
			t.Errorf("%s: IsError=%v, want code %s:\n%s", c.mode, res.IsError, c.code, got)
		}
		for _, want := range c.mustHave {
			if !strings.Contains(got, want) {
				t.Errorf("%s: lacks %q:\n%s", c.mode, want, got)
			}
		}
		if c.mustNotBe != "" && strings.Contains(got, c.mustNotBe) {
			t.Errorf("%s: contains %q:\n%s", c.mode, c.mustNotBe, got)
		}
	}
}

func TestLongErrorMessageIsCut(t *testing.T) {
	cs := sessionWith(t, sampleTools(t))
	res := call(t, cs, "sample_fail", map[string]any{"mode": "long"})
	got := text(t, res)
	if !res.IsError || !strings.Contains(got, "(message truncated)") || len(got) > 2*maxMessageBytes+1024 {
		t.Errorf("IsError=%v, %d bytes; message not cut", res.IsError, len(got))
	}
}

func TestOutputIsKeptFromTheEnd(t *testing.T) {
	cs := sessionWith(t, sampleTools(t))
	small := call(t, cs, "sample_output", map[string]any{"bytes": 1000})
	if strings.Contains(text(t, small), "output truncated") {
		t.Error("a small output was truncated")
	}
	big := call(t, cs, "sample_output", map[string]any{"bytes": 2 * maxOutputBytes})
	got := text(t, big)
	if big.IsError || !strings.Contains(got, "[output truncated: the first ") || len(got) > render.MaxBytes {
		t.Fatalf("IsError=%v, %d bytes, truncated=%v", big.IsError, len(got), strings.Contains(got, "output truncated"))
	}
}

func TestImageResultKeepsASmallImageAsIs(t *testing.T) {
	cs := sessionWith(t, sampleTools(t))
	res := call(t, cs, "sample_image", map[string]any{"size": 64})
	imgs := images(res)
	if res.IsError || len(imgs) != 1 || imgs[0].MIMEType != "image/png" {
		t.Fatalf("IsError=%v, %d images:\n%v", res.IsError, len(imgs), texts(res))
	}
	if !bytes.Equal(imgs[0].Data, noisyPNG(64, false)) {
		t.Error("a small image was changed")
	}
	if strings.Contains(text(t, res), "reduced") {
		t.Error("the body claims a reduction that did not happen")
	}
	// The text item comes first, the image second.
	if _, ok := res.Content[0].(*mcp.TextContent); !ok {
		t.Errorf("first content item is %T, want text", res.Content[0])
	}
}

func TestImageResultScalesALargeImageToFit(t *testing.T) {
	cs := sessionWith(t, sampleTools(t))
	res := call(t, cs, "sample_image", map[string]any{"size": 1500, "noisy": true})
	imgs := images(res)
	if res.IsError || len(imgs) != 1 {
		t.Fatalf("IsError=%v, %d images:\n%v", res.IsError, len(imgs), texts(res))
	}
	original := noisyPNG(1500, true)
	if budget := imageBudget(&mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text(t, res)}}}); len(original) <= budget {
		t.Fatalf("test image (%d bytes) fits the budget (%d): it proves nothing", len(original), budget)
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(imgs[0].Data))
	if err != nil || cfg.Width >= 1500 || cfg.Width != cfg.Height {
		t.Fatalf("decoded config = %+v, %v", cfg, err)
	}
	if !strings.Contains(text(t, res), "The image was reduced to ") {
		t.Errorf("the body does not say the image was reduced:\n%s", text(t, res))
	}
	total := len(text(t, res)) + len(imgs[0].Data)/3*4
	if total > render.MaxBytes {
		t.Errorf("reply is %d bytes once encoded, over %d", total, render.MaxBytes)
	}
}

func TestOptionalImageIsAttachedWhenItFits(t *testing.T) {
	cs := sessionWith(t, sampleTools(t))
	res := call(t, cs, "sample_image", map[string]any{"size": 32, "optional": true})
	if res.IsError || len(images(res)) != 1 {
		t.Fatalf("IsError=%v, %d images", res.IsError, len(images(res)))
	}
	// A large one is scaled down too, and never turns the call into an error.
	res = call(t, cs, "sample_image", map[string]any{"size": 1500, "noisy": true, "optional": true})
	if res.IsError || len(images(res)) != 1 {
		t.Fatalf("large optional image: IsError=%v, %d images", res.IsError, len(images(res)))
	}
}

func TestPaginationOverTheWire(t *testing.T) {
	cs := sessionWith(t, sampleTools(t))
	first := text(t, call(t, cs, "sample_list", map[string]any{}))
	if !strings.HasPrefix(first, "---\npage: 1\ntotal: 60\ntotal_pages: ") {
		t.Fatalf("frontmatter of page 1:\n%.200s", first)
	}
	if !strings.Contains(first, "record-01:") || strings.Contains(first, "record-60:") {
		t.Error("page 1 does not hold the first records only")
	}
	if !strings.Contains(first, "Next: page=2.") {
		t.Error("page 1 has no next-page hint")
	}
	// Walk every page: each record appears exactly once, in order, and only the last page has no hint.
	seen := 0
	for page := 1; ; page++ {
		body := text(t, call(t, cs, "sample_list", map[string]any{"page": page}))
		n := strings.Count(body, "record-")
		if n == 0 {
			t.Fatalf("page %d is empty", page)
		}
		if !strings.Contains(body, fmtRecord(seen+1)) {
			t.Fatalf("page %d does not start at record %d", page, seen+1)
		}
		seen += n
		if !strings.Contains(body, "Next: page=") {
			break
		}
		if page > 60 {
			t.Fatal("pagination does not end")
		}
	}
	if seen != 60 {
		t.Errorf("pages held %d records, want 60", seen)
	}
	// A page past the end is an empty window, not an error.
	past := call(t, cs, "sample_list", map[string]any{"page": 999})
	if past.IsError || strings.Contains(text(t, past), "record-") {
		t.Errorf("page 999: IsError=%v\n%.200s", past.IsError, text(t, past))
	}
}

func fmtRecord(n int) string { return fmt.Sprintf("record-%02d:", n) }
