package clicmd

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sairaph/mcp-wizard/render"

	"github.com/sairaph/creality-slicer-mcp/internal/domain"
	"github.com/sairaph/creality-slicer-mcp/internal/mcpserver"
)

// fakeTools records what the commands asked for and returns canned results.
type fakeTools struct {
	refresh  bool
	statuses int
	args     mcpserver.PresetListArgs
	lists    int
	status   *mcp.CallToolResult
	list     *mcp.CallToolResult
	slices   []mcpserver.SliceFileArgs
	slice    *mcp.CallToolResult
	recent   *mcp.CallToolResult
}

func (f *fakeTools) SlicerStatus(_ context.Context, refresh bool) *mcp.CallToolResult {
	f.statuses++
	f.refresh = refresh
	return f.status
}

func (f *fakeTools) PresetList(_ context.Context, a mcpserver.PresetListArgs) *mcp.CallToolResult {
	f.lists++
	f.args = a
	return f.list
}

func (f *fakeTools) SliceFile(_ context.Context, a mcpserver.SliceFileArgs) *mcp.CallToolResult {
	f.slices = append(f.slices, a)
	return f.slice
}

func (f *fakeTools) RecentProjects(context.Context) *mcp.CallToolResult { return f.recent }

type front struct {
	Installed bool   `yaml:"installed"`
	Version   string `yaml:"version"`
}

func newTools() *fakeTools {
	return &fakeTools{
		slice:  render.SuccessResult(struct{ State string }{"finished"}, "Sliced 1 plate(s).\n\nPlate 1: 1m 05s."),
		recent: render.SuccessResult(struct{ Count int }{1}, "1 project(s), newest first."),
		status: render.SuccessResult(front{Installed: true, Version: "7.2.2"}, "Creality Print 7.2.2 is installed.\n\nNext: get_guide."),
		list:   render.SuccessResult(struct{ Count int }{2}, "2 filament preset(s).\n\nname | source\nA | system\nB | system"),
	}
}

func run(t *testing.T, tools *fakeTools, f func(Deps) int) (code int, out, errOut string) {
	t.Helper()
	var o, e bytes.Buffer
	code = f(Deps{Tools: tools, Stdout: &o, Stderr: &e})
	return code, o.String(), e.String()
}

func TestRunStatusPrintsFrontAndBody(t *testing.T) {
	tools := newTools()
	code, out, errOut := run(t, tools, func(d Deps) int { return RunStatus(context.Background(), d, nil) })
	if code != 0 || errOut != "" {
		t.Fatalf("code %d, stderr %q", code, errOut)
	}
	for _, want := range []string{"installed: true", "version: 7.2.2", "Creality Print 7.2.2 is installed."} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "---") {
		t.Errorf("front matter delimiters leaked into the text:\n%s", out)
	}
	if tools.refresh {
		t.Error("refresh set without --refresh")
	}
	_, _, _ = run(t, tools, func(d Deps) int { return RunStatus(context.Background(), d, []string{"--refresh"}) })
	if !tools.refresh || tools.statuses != 2 {
		t.Errorf("--refresh not passed on: refresh=%v calls=%d", tools.refresh, tools.statuses)
	}
}

func TestRunStatusUsageAndErrors(t *testing.T) {
	tools := newTools()
	if code, _, errOut := run(t, tools, func(d Deps) int { return RunStatus(context.Background(), d, []string{"extra"}) }); code != 2 || !strings.Contains(errOut, "usage") {
		t.Errorf("code %d, stderr %q", code, errOut)
	}
	if code, _, _ := run(t, tools, func(d Deps) int { return RunStatus(context.Background(), d, []string{"--bogus"}) }); code != 2 {
		t.Errorf("a bad flag: code %d", code)
	}
	tools.status = render.ErrorResult(render.Error{Code: render.CodeInternal, Message: "Failed to detect", Hint: "Try again."})
	code, out, errOut := run(t, tools, func(d Deps) int { return RunStatus(context.Background(), d, nil) })
	if code != 1 || out != "" || !strings.Contains(errOut, "Failed to detect") || !strings.Contains(errOut, "Try again.") {
		t.Errorf("code %d, stdout %q, stderr %q", code, out, errOut)
	}
}

func TestRunPresetsArguments(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want mcpserver.PresetListArgs
	}{
		{"type only", []string{"filament"}, mcpserver.PresetListArgs{Type: "filament"}},
		{"flags after", []string{"filament", "--printer", "any", "--filament-type", "PETG", "--source", "system"},
			mcpserver.PresetListArgs{Type: "filament", Printer: "any", FilamentType: "PETG", Source: "system"}},
		{"flags before", []string{"--printer=all", "process"}, mcpserver.PresetListArgs{Type: "process", Printer: "all"}},
		{"name with spaces", []string{"process", "--printer", "Creality K2 0.6 nozzle"}, mcpserver.PresetListArgs{Type: "process", Printer: "Creality K2 0.6 nozzle"}},
	}
	for _, c := range cases {
		tools := newTools()
		code, out, errOut := run(t, tools, func(d Deps) int { return RunPresets(context.Background(), d, c.args) })
		if code != 0 || errOut != "" {
			t.Errorf("%s: code %d, stderr %q", c.name, code, errOut)
			continue
		}
		if tools.args != c.want {
			t.Errorf("%s: asked for %+v, want %+v", c.name, tools.args, c.want)
		}
		// The body only: no front matter, no delimiters.
		if !strings.Contains(out, "name | source") || strings.Contains(out, "---") || strings.Contains(out, "Count") {
			t.Errorf("%s: output:\n%s", c.name, out)
		}
	}
}

func TestRunPresetsUsageAndToolErrors(t *testing.T) {
	for _, args := range [][]string{nil, {"--printer", "any"}, {"filament", "process"}} {
		tools := newTools()
		code, _, errOut := run(t, tools, func(d Deps) int { return RunPresets(context.Background(), d, args) })
		if code != 2 || !strings.Contains(errOut, "usage: creality-slicer-mcp presets") || tools.lists != 0 {
			t.Errorf("%v: code %d, stderr %q, calls %d", args, code, errOut, tools.lists)
		}
	}
	tools := newTools()
	tools.list = render.ErrorResult(render.Error{Code: render.CodeInvalidInput, Message: "unknown preset type", Hint: "Use printer, process or filament."})
	if code, _, errOut := run(t, tools, func(d Deps) int { return RunPresets(context.Background(), d, []string{"gcode"}) }); code != 2 || !strings.Contains(errOut, "Use printer") {
		t.Errorf("invalid input: code %d, stderr %q", code, errOut)
	}
	tools.list = render.ErrorResult(render.Error{Code: render.CodeUnavailable, Message: "Cannot read presets", Hint: "Call get_slicer_status."})
	if code, _, _ := run(t, tools, func(d Deps) int { return RunPresets(context.Background(), d, []string{"filament"}) }); code != 1 {
		t.Errorf("unavailable: code %d, want 1", code)
	}
}

func TestWriteStatusWritesToTheGivenWriter(t *testing.T) {
	tools := newTools()
	var w bytes.Buffer
	if code := WriteStatus(context.Background(), Deps{Tools: tools}, false, &w); code != 0 || !strings.Contains(w.String(), "installed: true") {
		t.Errorf("code %d, output %q", code, w.String())
	}
}

func TestBadEnvironmentSettingStopsTheDefaultTools(t *testing.T) {
	t.Setenv(domain.EnvCmd, filepath.Join(t.TempDir(), "missing", "CrealityPrint.exe"))
	var o, e bytes.Buffer
	d := Deps{Stdout: &o, Stderr: &e} // no Tools: the real ones, built from the environment
	if code := RunStatus(context.Background(), d, nil); code != 2 || !strings.Contains(e.String(), domain.EnvCmd) {
		t.Errorf("status: code %d, stderr %q", code, e.String())
	}
	e.Reset()
	if code := RunPresets(context.Background(), d, []string{"filament"}); code != 2 || !strings.Contains(e.String(), domain.EnvCmd) {
		t.Errorf("presets: code %d, stderr %q", code, e.String())
	}
}

func TestRunSlicePassesTheFileAndFlagsAsAbsolutePaths(t *testing.T) {
	tools := newTools()
	code, out, errOut := run(t, tools, func(d Deps) int {
		return RunSlice(context.Background(), d, []string{"cube.3mf", "--plate", "2", "--out", "gcode"})
	})
	if code != 0 || errOut != "" || !strings.Contains(out, "Sliced 1 plate(s).") || strings.Contains(out, "state:") {
		t.Fatalf("code %d, out %q, err %q", code, out, errOut)
	}
	if len(tools.slices) != 1 {
		t.Fatalf("calls = %v", tools.slices)
	}
	a := tools.slices[0]
	if !filepath.IsAbs(a.Path) || filepath.Base(a.Path) != "cube.3mf" || a.Plate != 2 || !filepath.IsAbs(a.Out) || filepath.Base(a.Out) != "gcode" {
		t.Errorf("args = %+v", a)
	}
	// The flags may come first, too.
	tools = newTools()
	if code, _, errOut := run(t, tools, func(d Deps) int { return RunSlice(context.Background(), d, []string{"--plate", "1", "cube.3mf"}) }); code != 0 {
		t.Errorf("flags first: code %d, %s", code, errOut)
	}
}

func TestRunSliceUsageErrors(t *testing.T) {
	for _, args := range [][]string{nil, {"a.3mf", "b.3mf"}, {"a.3mf", "--plate", "-1"}, {"a.3mf", "--bogus"}} {
		tools := newTools()
		code, _, errOut := run(t, tools, func(d Deps) int { return RunSlice(context.Background(), d, args) })
		if code != 2 || errOut == "" || len(tools.slices) != 0 {
			t.Errorf("%v: code %d, err %q, calls %v", args, code, errOut, tools.slices)
		}
	}
}

func TestRunSliceShowsAnErrorOnStderr(t *testing.T) {
	tools := newTools()
	tools.slice = render.ErrorResult(render.Error{Code: render.CodeInvalidInput, Message: "not a project", Hint: "use add_model"})
	code, out, errOut := run(t, tools, func(d Deps) int { return RunSlice(context.Background(), d, []string{"a.3mf"}) })
	if code != 2 || out != "" || !strings.Contains(errOut, "not a project") {
		t.Errorf("code %d, out %q, err %q", code, out, errOut)
	}
}

func TestWriteRecentProjects(t *testing.T) {
	var buf bytes.Buffer
	d := Deps{Tools: newTools(), Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}
	if code := WriteRecentProjects(context.Background(), d, &buf); code != 0 || !strings.Contains(buf.String(), "1 project(s)") {
		t.Errorf("code %d, out %q", code, buf.String())
	}
}

func TestRunSliceOverwriteFlag(t *testing.T) {
	tools := newTools()
	if code, _, _ := run(t, tools, func(d Deps) int {
		return RunSlice(context.Background(), d, []string{"a.3mf", "--out", "gcode", "--overwrite"})
	}); code != 0 || len(tools.slices) != 1 || !tools.slices[0].Overwrite {
		t.Errorf("code %d, calls %+v", code, tools.slices)
	}
	tools = newTools()
	if code, _, _ := run(t, tools, func(d Deps) int { return RunSlice(context.Background(), d, []string{"a.3mf"}) }); code != 0 || tools.slices[0].Overwrite {
		t.Errorf("overwrite defaulted on: %+v", tools.slices)
	}
	tools = newTools()
	if code, _, errOut := run(t, tools, func(d Deps) int { return RunSlice(context.Background(), d, []string{"a.3mf", "--overwrite"}) }); code != 2 || !strings.Contains(errOut, "--out") {
		t.Errorf("--overwrite without --out: code %d, %q", code, errOut)
	}
}
