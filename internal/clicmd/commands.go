package clicmd

import (
	"context"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sairaph/creality-slicer-mcp/internal/domain"
	"github.com/sairaph/creality-slicer-mcp/internal/mcpserver"
)

// Tools is what the commands call: the very functions the MCP tools run, so a
// command and its tool never disagree. *mcpserver.Server is the real one.
type Tools interface {
	SlicerStatus(ctx context.Context, refresh bool) *mcp.CallToolResult
	PresetList(ctx context.Context, a mcpserver.PresetListArgs) *mcp.CallToolResult
	SliceFile(ctx context.Context, a mcpserver.SliceFileArgs) *mcp.CallToolResult
	RecentProjects(ctx context.Context) *mcp.CallToolResult
}

// tools returns Deps.Tools, or builds the real ones from the environment
// settings; a bad setting is the error, as it is for the MCP server.
func (d Deps) tools() (Tools, error) {
	if d.Tools != nil {
		return d.Tools, nil
	}
	settings, err := domain.SettingsFromEnv()
	if err != nil {
		return nil, err
	}
	return mcpserver.New(mcpserver.Config{Settings: settings}), nil
}

// replyText returns the first text of a tool result.
func replyText(res *mcp.CallToolResult) string {
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			return tc.Text
		}
	}
	return ""
}

// splitReply splits a tool reply into its front matter (without the --- lines)
// and its body.
func splitReply(text string) (front, body string) {
	rest, ok := strings.CutPrefix(text, "---\n")
	if !ok {
		return "", text
	}
	front, body, ok = strings.Cut(rest, "\n---\n")
	if !ok {
		return "", text
	}
	return front, strings.TrimSpace(body)
}

// finish prints a tool result: the body (with the front matter first when
// withFront) to stdout, or, for an error result, its message and hint to
// stderr. It returns the exit code: 0, 2 for invalid input, 1 otherwise.
func finish(d Deps, res *mcp.CallToolResult, withFront bool) int {
	text := replyText(res)
	front, body := splitReply(text)
	if res.IsError {
		fmt.Fprintln(d.Stderr, "  "+strings.ReplaceAll(strings.TrimPrefix(body, "## Error\n\n"), "\n\n", "\n  "))
		if strings.Contains(front, "code: invalid_input") {
			return 2
		}
		return 1
	}
	if withFront && front != "" {
		fmt.Fprintln(d.Stdout, front)
		fmt.Fprintln(d.Stdout)
	}
	fmt.Fprintln(d.Stdout, body)
	return 0
}

// WriteStatus prints the slicer status, the same reply as get_slicer_status,
// to w. The interactive app and the status command share it.
func WriteStatus(ctx context.Context, d Deps, refresh bool, w io.Writer) int {
	d = d.withDefaults()
	d.Stdout = w
	tools, err := d.tools()
	if err != nil {
		fmt.Fprintln(d.Stderr, "  "+err.Error())
		return 2
	}
	return finish(d, tools.SlicerStatus(ctx, refresh), true)
}

// RunStatus is `creality-slicer-mcp status [--refresh]`: whether Creality
// Print is installed and usable, printed as text.
func RunStatus(ctx context.Context, d Deps, args []string) int {
	d = d.withDefaults()
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	refresh := fs.Bool("refresh", false, "detect Creality Print again")
	if code := parseFlags(fs, args, d.Stderr); code >= 0 {
		return code
	}
	if fs.NArg() > 0 {
		return usageError(d.Stderr, "usage: creality-slicer-mcp status [--refresh]")
	}
	return WriteStatus(ctx, d, *refresh, d.Stdout)
}

const presetsUsage = "usage: creality-slicer-mcp presets <printer|process|filament> [--printer NAME|all|any] [--filament-type TYPE] [--source system|user|all]"

// RunPresets is `creality-slicer-mcp presets <type> ...`: every preset of a
// type with its key facts, all pages, as list_presets would list them.
func RunPresets(ctx context.Context, d Deps, args []string) int {
	d = d.withDefaults()
	fs := flag.NewFlagSet("presets", flag.ContinueOnError)
	printer := fs.String("printer", "", "printer preset the presets must fit (default Creality K2 0.4 nozzle); any: every Creality preset; all: the K2 family")
	filamentType := fs.String("filament-type", "", "only filament presets of this material, such as PLA")
	source := fs.String("source", "", "system, user or all (default)")
	// The type comes first (presets filament --printer any) or after the
	// flags; flag parsing stops at the first non-flag, so take a leading one.
	var typ string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		typ, args = args[0], args[1:]
	}
	if code := parseFlags(fs, args, d.Stderr); code >= 0 {
		return code
	}
	if typ == "" && fs.NArg() == 1 {
		typ = fs.Arg(0)
	} else if fs.NArg() > 0 {
		return usageError(d.Stderr, presetsUsage)
	}
	if typ == "" {
		return usageError(d.Stderr, presetsUsage)
	}
	tools, err := d.tools()
	if err != nil {
		fmt.Fprintln(d.Stderr, "  "+err.Error())
		return 2
	}
	return finish(d, tools.PresetList(ctx, mcpserver.PresetListArgs{
		Type: typ, Printer: *printer, FilamentType: *filamentType, Source: *source,
	}), false)
}

const sliceUsage = "usage: creality-slicer-mcp slice <project.3mf> [--plate N] [--out DIR [--overwrite]]"

// RunSlice is `creality-slicer-mcp slice <project.3mf> [--plate N] [--out DIR]`:
// slice a project file with the installed Creality Print and print the summary
// that slice_project gives. The file is copied into the projects store and
// never changed; --out copies the G-code to a folder and never replaces a file
// there unless --overwrite is given.
func RunSlice(ctx context.Context, d Deps, args []string) int {
	d = d.withDefaults()
	fs := flag.NewFlagSet("slice", flag.ContinueOnError)
	plate := fs.Int("plate", 0, "plate to slice, starting at 1 (default: every plate)")
	out := fs.String("out", "", "folder to copy the G-code to")
	overwrite := fs.Bool("overwrite", false, "replace G-code files that already exist in --out")
	var path string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		path, args = args[0], args[1:]
	}
	if code := parseFlags(fs, args, d.Stderr); code >= 0 {
		return code
	}
	if path == "" && fs.NArg() == 1 {
		path = fs.Arg(0)
	} else if fs.NArg() > 0 {
		return usageError(d.Stderr, sliceUsage)
	}
	if path == "" {
		return usageError(d.Stderr, sliceUsage)
	}
	if *overwrite && *out == "" {
		return usageError(d.Stderr, "--overwrite needs --out")
	}
	if *plate < 0 {
		return usageError(d.Stderr, "--plate must be 1 or more (or left out for every plate)")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return usageError(d.Stderr, "%v", err)
	}
	outDir := *out
	if outDir != "" {
		if outDir, err = filepath.Abs(outDir); err != nil {
			return usageError(d.Stderr, "%v", err)
		}
	}
	tools, err := d.tools()
	if err != nil {
		fmt.Fprintln(d.Stderr, "  "+err.Error())
		return 2
	}
	return finish(d, tools.SliceFile(ctx, mcpserver.SliceFileArgs{Path: abs, Plate: *plate, Out: outDir, Overwrite: *overwrite}), false)
}

// WriteRecentProjects prints the recent projects as text, newest first, to w. The
// interactive app does not use it: its screens are drawn from typed data.
func WriteRecentProjects(ctx context.Context, d Deps, w io.Writer) int {
	d = d.withDefaults()
	d.Stdout = w
	tools, err := d.tools()
	if err != nil {
		fmt.Fprintln(d.Stderr, "  "+err.Error())
		return 2
	}
	return finish(d, tools.RecentProjects(ctx), false)
}
