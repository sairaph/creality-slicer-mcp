package appui

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/sairaph/creality-slicer-mcp/internal/projects"
)

// registeredTools are the tools the server registers, read from its source.
func registeredTools(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob("../mcpserver/*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no mcpserver sources: %v", err)
	}
	re := regexp.MustCompile(`addTool\(s\.mcpServer, "([a-z_]+)"`)
	var names []string
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range re.FindAllStringSubmatch(string(src), -1) {
			names = append(names, m[1])
		}
	}
	if len(names) < 20 {
		t.Fatalf("found only %d tools", len(names))
	}
	return names
}

// stringLiterals are the string literals of the non-test sources of dirs.
func stringLiterals(t *testing.T, dirs ...string) []string {
	t.Helper()
	var out []string
	for _, dir := range dirs {
		files, _ := filepath.Glob(dir + "/*.go")
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			node, err := parser.ParseFile(token.NewFileSet(), f, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			ast.Inspect(node, func(n ast.Node) bool {
				if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
					if s, err := strconv.Unquote(lit.Value); err == nil {
						out = append(out, s)
					}
				}
				return true
			})
		}
	}
	return out
}

func TestToolNameListHoldsEveryRegisteredTool(t *testing.T) {
	known := map[string]bool{}
	for _, n := range toolNames {
		known[n] = true
	}
	for _, n := range registeredTools(t) {
		if !strings.HasPrefix(n, "sample_") && !known[n] {
			t.Errorf("tool %s is not in toolNames: the app would show it", n)
		}
	}
}

func TestNoAppNoticeNamesATool(t *testing.T) {
	var names []string
	for _, n := range registeredTools(t) {
		if !strings.HasPrefix(n, "sample_") {
			names = append(names, n)
		}
	}
	re := regexp.MustCompile(`\b(` + strings.Join(names, "|") + `)\b`)
	codes := []string{projects.CodeInvalidInput, projects.CodeNotFound, projects.CodeConflict, projects.CodeUnavailable, projects.CodeSlicerError, projects.CodeInternal}
	checked := 0
	for _, s := range stringLiterals(t, "../projects", "../mcpserver") {
		if !re.MatchString(s) {
			continue
		}
		for _, code := range codes {
			for _, pe := range []*projects.Error{{Code: code, Message: s, Hint: "x"}, {Code: code, Message: "x", Hint: s}, {Code: code, Message: s}} {
				if got := ErrText(pe); re.MatchString(got) {
					t.Fatalf("a notice names a tool:\n%s\nfrom %q", got, s)
				}
			}
		}
		checked++
	}
	if checked < 20 {
		t.Errorf("only %d literals name a tool: the scan found too little", checked)
	}
}

func TestToolHintsBecomeAppActions(t *testing.T) {
	for _, c := range []struct{ hint, want string }{
		{"close the program that holds the file, then call delete_project again", "press d again"},
		{"Check that Creality Print starts, then call open_in_app again.", "o again"},
		{"Call get_slicer_status to see what is missing.", "Open Slicer status"},
		{"slice_project first", "slice"},
		{"project.3mf is held by another program: close it, then repeat the call", "try again"},
	} {
		got := ErrText(&projects.Error{Message: "It failed.", Hint: c.hint})
		if !strings.HasPrefix(got, "It failed. ") || !strings.Contains(got, c.want) {
			t.Errorf("hint %q -> %q, want it to say %q", c.hint, got, c.want)
		}
	}
}

// toolRegexp matches any registered tool name.
func toolRegexp(t *testing.T) *regexp.Regexp {
	var names []string
	for _, n := range registeredTools(t) {
		if !strings.HasPrefix(n, "sample_") {
			names = append(names, n)
		}
	}
	return regexp.MustCompile(`\b(` + strings.Join(names, "|") + `)\b`)
}

// Every text of the projects, the doctor checks and the server that names a
// tool (the warnings of a project and of a slice among them) is free of the
// name once AppText has rewritten it.
func TestAppTextNamesNoTool(t *testing.T) {
	re := toolRegexp(t)
	checked := 0
	for _, s := range stringLiterals(t, "../projects", "../mcpserver", "../doctorchecks") {
		if !re.MatchString(s) {
			continue
		}
		if got := rewriteText(s); re.MatchString(got) || (strings.Contains(got, "that action") && !strings.Contains(s, "that action")) {
			t.Fatalf("the rules leave a tool name or lean on the last-resort wording:\n%s\nfrom %q", got, s)
		}
		checked++
	}
	if checked < 20 {
		t.Errorf("only %d literals name a tool: the scan found too little", checked)
	}
}

func TestAppTextWording(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"plain text", "plain text"},
		{"the slicer adds a prime tower; fewer changes reduce it (slice_project reports the tower grams and the flush per plate after slicing)",
			"the slicer adds a prime tower; fewer changes reduce it (the slice report shows the tower grams and the flush per plate)"},
		{"the pause writes only the marker: machine_pause_gcode is empty; set it with update_settings (the printer preset's own value is PAUSE on the K2)",
			"the pause writes only the marker: machine_pause_gcode is empty"},
		{"No flat face. Call get_project to see the plates.", "No flat face."},
		{"Setting descriptions are not available (no catalog); describe_setting will say so.", "Setting descriptions are not available (no catalog); the setting texts will say so."},
	} {
		if got := AppText(c.in); got != c.want {
			t.Errorf("AppText(%q)\n got %q\nwant %q", c.in, got, c.want)
		}
	}
}

// The purge warning of the projects package, as purge.go writes it, names
// update_settings with a JSON value and slice_project.
func TestAppTextPurgeWarning(t *testing.T) {
	var src string
	for _, s := range stringLiterals(t, "../projects") {
		if strings.Contains(s, "Each object here uses one filament") {
			src = s
		}
	}
	if src == "" {
		t.Fatal("the purge text is not in the projects package")
	}
	text := "Purge waste 3.0 g of 9.0 g (33%): prime tower 2.0 g + flush 1.0 g over 4 filament changes." +
		fmt.Sprintf(src, 1, 25.0)
	got := AppText(text)
	for _, want := range []string{"set the plate to print by object and slice it again with the objects arranged (by-object needs about 25 mm between objects).", "Purge waste 3.0 g"} {
		if !strings.Contains(got, want) {
			t.Errorf("purge warning: %q does not contain %q", got, want)
		}
	}
	if strings.Contains(got, "update_settings") || strings.Contains(got, "slice_project") || strings.Contains(got, "{") {
		t.Errorf("purge warning keeps the tool wording: %q", got)
	}
}
