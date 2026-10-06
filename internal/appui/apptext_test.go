package appui

import (
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
