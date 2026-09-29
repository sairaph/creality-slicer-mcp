package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/sairaph/mcp-wizard/cli"
	"github.com/sairaph/mcp-wizard/harness"
	"github.com/sairaph/mcp-wizard/installer"
)

// The tests use project scope in a temporary directory, so they never touch
// the real client configs of the machine running them.

const testCommand = "/opt/creality-slicer-mcp/bin/creality-slicer-mcp"

const editedCursorConfig = `{
  "mcpServers": {
    "creality-slicer-mcp": {
      "command": "/opt/creality-slicer-mcp/bin/creality-slicer-mcp",
      "args": ["mcp"],
      "env": {"TRANSPORT": "stdio", "CREALITY_SLICER_MCP_CMD": "/opt/creality/CrealityPrint.exe"}
    },
    "other": {"command": "other-server"}
  }
}
`

// Another program that registers the same name.
const foreignCursorConfig = `{
  "mcpServers": {
    "creality-slicer-mcp": {"command": "uvx", "args": ["creality-slicer-mcp"]}
  }
}
`

const outdatedCursorConfig = `{
  "mcpServers": {
    "creality-slicer-mcp": {"command": "/home/me/src/creality-slicer-mcp/creality-slicer-mcp", "args": ["mcp"], "env": {"TRANSPORT": "stdio"}}
  }
}
`

func testDetector(t *testing.T) *harness.Detector {
	t.Helper()
	old := ourCommand
	ourCommand = func() string { return testCommand }
	t.Cleanup(func() { ourCommand = old })
	det, err := harness.New(harness.ServerSpec{
		Name:    "creality-slicer-mcp",
		Command: testCommand,
		Args:    []string{"mcp"},
		Env:     map[string]string{"TRANSPORT": "stdio"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return det
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// servers returns the entries under topKey in a JSON client config.
func servers(t *testing.T, path, topKey string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	m, _ := doc[topKey].(map[string]any)
	return m
}

// cursorEntry returns what clientEntries reports for Cursor in a project
// whose .cursor/mcp.json holds config.
func cursorEntry(t *testing.T, config string) (clientEntry, bool) {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".cursor", "mcp.json"), config)
	det, scope := testDetector(t), harness.ProjectScopeDir(dir)
	entries := clientEntries(context.Background(), det, scope, det.DetectIn(context.Background(), scope))
	if _, found := entries["vscode"]; found {
		t.Fatal("a client without a config file was reported as having an entry")
	}
	e, found := entries["cursor"]
	return e, found
}

func TestEntriesAreClassifiedByWhatTheyRun(t *testing.T) {
	cases := []struct {
		name, config string
		want         entryKind
	}{
		{"edited by hand", editedCursorConfig, entryEdited},
		{"another program", foreignCursorConfig, entryForeign},
		{"another build of this program", outdatedCursorConfig, entryOutdated},
		{"as install writes it", `{"mcpServers": {"creality-slicer-mcp": {"command": "/opt/creality-slicer-mcp/bin/creality-slicer-mcp", "args": ["mcp"], "env": {"TRANSPORT": "stdio"}}}}`, entryConfigured},
	}
	for _, c := range cases {
		e, found := cursorEntry(t, c.config)
		if !found || e.kind != c.want {
			t.Errorf("%s: found=%v kind=%v, want %v", c.name, found, e.kind, c.want)
		}
	}
	if _, found := cursorEntry(t, `{"mcpServers": {"other": {"command": "x"}}}`); found {
		t.Error("an entry under another name was reported")
	}
}

func TestEntryCommandReadsEveryConfigFormat(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]string{
		"a.json": `{"mcpServers": {"creality-slicer-mcp": {"command": "/x/creality-slicer-mcp"}}}`,
		// JSONC with a comment, OpenCode's command array under "mcp"
		"b.jsonc": "{\n // comment\n \"mcp\": {\"creality-slicer-mcp\": {\"type\": \"local\", \"command\": [\"/x/creality-slicer-mcp\", \"mcp\"]}},\n}",
		// Zed's own form
		"c.json": `{"context_servers": {"creality-slicer-mcp": {"command": {"path": "/x/creality-slicer-mcp", "args": ["mcp"]}}}}`,
		"d.toml": "[mcp_servers.creality-slicer-mcp]\ncommand = \"/x/creality-slicer-mcp\"\nargs = [\"mcp\"]\n",
		"e.yaml": "name: Local\nmcpServers:\n  - name: other\n    command: o\n  - name: creality-slicer-mcp\n    command: /x/creality-slicer-mcp\n",
	}
	for file, content := range cases {
		path := filepath.Join(dir, file)
		writeFile(t, path, content)
		if got, ok := entryCommand(path, "creality-slicer-mcp"); !ok || got != "/x/creality-slicer-mcp" {
			t.Errorf("%s: %q %v", file, got, ok)
		}
	}
	// Claude Code nests per-project entries; only the top level counts.
	path := filepath.Join(dir, "claude.json")
	writeFile(t, path, `{"projects": {"/p": {"mcpServers": {"creality-slicer-mcp": {"command": "/x/creality-slicer-mcp"}}}}}`)
	if got, ok := entryCommand(path, "creality-slicer-mcp"); ok {
		t.Errorf("a nested project entry was read as the global one: %q", got)
	}
}

func TestIsOurBinary(t *testing.T) {
	for command, want := range map[string]bool{
		"/home/me/.creality-slicer-mcp/bin/creality-slicer-mcp":                     true,
		`C:\Users\me\AppData\Local\creality-slicer-mcp\bin\creality-slicer-mcp.exe`: true,
		"creality-slicer-mcp":           true,
		"uvx":                           false,
		"/usr/bin/creality-slicer-mcp2": false,
		"python":                        false,
	} {
		if got := isOurBinary(command); got != want {
			t.Errorf("isOurBinary(%q) = %v, want %v", command, got, want)
		}
	}
}

func TestUninstallRemovesAnEditedEntry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".cursor", "mcp.json")
	writeFile(t, path, editedCursorConfig)

	code := runUnattended(context.Background(), testDetector(t), harness.ProjectScopeDir(dir), cli.Command{}, harness.Absent)
	if code != 0 {
		t.Fatalf("exit code %d", code)
	}
	got := servers(t, path, "mcpServers")
	if _, ok := got["creality-slicer-mcp"]; ok {
		t.Fatal("the edited entry is still there")
	}
	if _, ok := got["other"]; !ok {
		t.Fatal("another server's entry was removed")
	}
}

func TestUninstallLeavesAnotherProgramsEntryUnlessNamed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".cursor", "mcp.json")
	writeFile(t, path, foreignCursorConfig)
	det, scope := testDetector(t), harness.ProjectScopeDir(dir)

	if code := runUnattended(context.Background(), det, scope, cli.Command{}, harness.Absent); code != 0 {
		t.Fatalf("exit code %d", code)
	}
	if raw, _ := os.ReadFile(path); string(raw) != foreignCursorConfig {
		t.Fatalf("uninstall changed another program's entry:\n%s", raw)
	}
	if code := runUnattended(context.Background(), det, scope, cli.Command{Clients: []string{"cursor"}}, harness.Absent); code != 0 {
		t.Fatalf("exit code %d", code)
	}
	if _, ok := servers(t, path, "mcpServers")["creality-slicer-mcp"]; ok {
		t.Fatal("--clients cursor did not remove the entry")
	}
}

func TestUninstallHonoursClients(t *testing.T) {
	dir := t.TempDir()
	cursor := filepath.Join(dir, ".cursor", "mcp.json")
	vscode := filepath.Join(dir, ".vscode", "mcp.json")
	writeFile(t, cursor, editedCursorConfig)
	writeFile(t, vscode, `{"servers": {"creality-slicer-mcp": {"type": "stdio", "command": "creality-slicer-mcp", "args": ["mcp"]}}}`)

	code := runUnattended(context.Background(), testDetector(t), harness.ProjectScopeDir(dir), cli.Command{Clients: []string{"Cursor"}}, harness.Absent)
	if code != 0 {
		t.Fatalf("exit code %d", code)
	}
	if _, ok := servers(t, cursor, "mcpServers")["creality-slicer-mcp"]; ok {
		t.Fatal("the named client's entry was not removed")
	}
	if _, ok := servers(t, vscode, "servers")["creality-slicer-mcp"]; !ok {
		t.Fatal("a client not named in --clients lost its entry")
	}
}

func TestUninstallRejectsAnUnknownClient(t *testing.T) {
	code := runUnattended(context.Background(), testDetector(t), harness.ProjectScopeDir(t.TempDir()), cli.Command{Clients: []string{"no-such-client"}}, harness.Absent)
	if code != 2 {
		t.Fatalf("exit code %d, want 2", code)
	}
}

func TestUninstallAllRefusesClients(t *testing.T) {
	if code := runUninstall(context.Background(), cli.Command{All: true, Clients: []string{"cursor"}, DryRun: true}); code != 2 {
		t.Fatalf("exit code %d, want 2: --all would delete the program other clients still run", code)
	}
}

func TestInstallKeepsAnEditedEntryUnlessNamed(t *testing.T) {
	for _, config := range []string{editedCursorConfig, foreignCursorConfig} {
		dir := t.TempDir()
		path := filepath.Join(dir, ".cursor", "mcp.json")
		writeFile(t, path, config)
		det, scope := testDetector(t), harness.ProjectScopeDir(dir)

		runUnattended(context.Background(), det, scope, cli.Command{}, harness.Present)
		if raw, _ := os.ReadFile(path); string(raw) != config {
			t.Fatalf("a default install changed the entry:\n%s", raw)
		}

		if code := runUnattended(context.Background(), det, scope, cli.Command{Clients: []string{"cursor"}}, harness.Present); code != 0 {
			t.Fatalf("exit code %d", code)
		}
		entry, _ := servers(t, path, "mcpServers")["creality-slicer-mcp"].(map[string]any)
		if entry["command"] != testCommand {
			t.Fatalf("--clients cursor did not replace the entry: %v", entry)
		}
	}
}

func TestInstallAllReplacesAnEditedEntryButNotAnotherProgramsEntry(t *testing.T) {
	for config, replaced := range map[string]bool{editedCursorConfig: true, foreignCursorConfig: false} {
		dir := t.TempDir()
		path := filepath.Join(dir, ".cursor", "mcp.json")
		writeFile(t, path, config)
		runUnattended(context.Background(), testDetector(t), harness.ProjectScopeDir(dir), cli.Command{All: true}, harness.Present)
		raw, _ := os.ReadFile(path)
		if got := string(raw) != config; got != replaced {
			t.Fatalf("--all replaced=%v, want %v:\n%s", got, replaced, raw)
		}
	}
}

func TestInstallUpdatesAnOutdatedEntry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".cursor", "mcp.json")
	writeFile(t, path, outdatedCursorConfig)
	runUnattended(context.Background(), testDetector(t), harness.ProjectScopeDir(dir), cli.Command{}, harness.Present)
	entry, _ := servers(t, path, "mcpServers")["creality-slicer-mcp"].(map[string]any)
	if entry["command"] != testCommand {
		t.Fatalf("an entry running another build of this program was not pointed at this one: %v", entry)
	}
}

func TestWizardStartsWithAKeptClientUnticked(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".cursor", "mcp.json"), editedCursorConfig)
	ctx := context.Background()
	det, scope := testDetector(t), harness.ProjectScopeDir(dir)
	step := harnessSelection{
		Step: installer.HarnessStep(ctx, det, harnessState, installer.HarnessStepOptions{AllDetected: true, Scope: scope}),
		name: "creality-slicer-mcp",
		findUnticked: func(hs []harness.Harness) map[harness.ID]bool {
			return untickedClients(clientEntries(ctx, det, scope, hs))
		},
	}
	state := &AppState{}
	batch, ok := step.Init(state)().(tea.BatchMsg)
	if !ok {
		t.Fatal("Init did not return a batch")
	}
	for _, cmd := range batch {
		if cmd != nil {
			step.Update(cmd(), state)
		}
	}
	if state.Harness.Selected == nil || state.harnessDetecting {
		t.Fatal("detection did not finish")
	}
	if state.Harness.Selected["cursor"] {
		t.Fatal("the client with an edited entry starts ticked, so registering would drop the user's changes")
	}
	if len(state.UntickedClients) != 1 || state.UntickedClients[0] != "Cursor" {
		t.Fatalf("UntickedClients = %v", state.UntickedClients)
	}
	if view := step.View(state); !strings.Contains(view, "starts unticked so the entry is kept") || !strings.Contains(view, "Cursor") {
		t.Fatalf("no note explains the unticked client:\n%s", view)
	}
}
