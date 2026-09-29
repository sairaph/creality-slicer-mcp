package main

// What each AI client has under this server's name. mcp-wizard counts a
// client as configured only when its entry is exactly what install writes,
// and treats anything else under the same name as a conflict it may
// overwrite. That misses two cases: docs/configuration.md tells users to add
// settings to the entry's env, after which it no longer matches, and another
// program (for example an older or forked build) may register the same name.
// So the entry's command is read to tell them apart.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
	"github.com/sairaph/mcp-wizard/harness"
	"github.com/tailscale/hujson"
	"gopkg.in/yaml.v3"

	"github.com/sairaph/creality-slicer-mcp/internal/domain"
)

type entryKind int

const (
	entryConfigured entryKind = iota + 1 // exactly what install writes
	entryEdited                          // runs this program, changed by hand (settings added to env, say)
	entryOutdated                        // runs creality-slicer-mcp from another path (a moved or development build)
	entryForeign                         // runs another program under the same name
)

type clientEntry struct {
	kind    entryKind
	command string // as the entry names it; empty when it could not be read
}

// keptOnInstall reports whether install leaves the entry alone unless it is
// asked to replace it.
func (e clientEntry) keptOnInstall() bool { return e.kind == entryEdited || e.kind == entryForeign }

// ours reports whether the entry runs creality-slicer-mcp, so uninstall removes it.
func (e clientEntry) ours() bool { return e.kind != entryForeign }

// ourCommand is the command install registers; tests replace it.
var ourCommand = func() string {
	exe, _ := harness.ResolveExecutable()
	return exe
}

// planReady is detect-harness's ChangeReady state, which mcp-wizard passes
// through in Change.State without a constant of its own.
const planReady harness.ApplyState = "ready"

// clientEntries returns, for each harness whose config file has an entry
// under the detector's server name, what that entry is. Planning its removal
// with ConflictReplace is ready exactly when there is one, and names the file.
func clientEntries(ctx context.Context, detector *harness.Detector, scope harness.Scope, harnesses []harness.Harness) map[harness.ID]clientEntry {
	entries := make(map[harness.ID]clientEntry)
	ids := make([]harness.ID, 0, len(harnesses))
	for _, h := range harnesses {
		ids = append(ids, h.ID)
		if h.Configured {
			entries[h.ID] = clientEntry{kind: entryConfigured}
		}
	}
	changes, err := detector.PlanResultsIn(ctx, scope, ids, harness.Absent, harness.ConflictReplace)
	if err != nil {
		return entries
	}
	for _, c := range changes {
		if c.State != planReady {
			continue
		}
		if _, configured := entries[c.HarnessID]; configured {
			continue
		}
		entries[c.HarnessID] = classifyEntry(c.Path, detector.Name())
	}
	return entries
}

// classifyEntry decides what a differing entry named name in the config file
// at path is. An entry that cannot be read is taken to be an edited one: it
// carries this server's name, and edited entries are only removed or
// replaced on request.
func classifyEntry(path, name string) clientEntry {
	command, ok := entryCommand(path, name)
	switch {
	case !ok:
		return clientEntry{kind: entryEdited}
	case !isOurBinary(command):
		return clientEntry{kind: entryForeign, command: command}
	case sameExecutable(command, ourCommand()):
		return clientEntry{kind: entryEdited, command: command}
	default:
		return clientEntry{kind: entryOutdated, command: command}
	}
}

// isOurBinary reports whether command runs a creality-slicer-mcp binary.
func isOurBinary(command string) bool {
	base := filepath.Base(strings.ReplaceAll(command, `\`, "/"))
	base = strings.TrimSuffix(strings.ToLower(base), ".exe")
	return strings.EqualFold(base, domain.BinaryName)
}

func sameExecutable(a, b string) bool {
	return a != "" && b != "" && samePath(a, b)
}

// entryCommand reads the command of the entry named name in a client config
// file: JSON or JSONC under the top-level key the clients use, TOML under
// mcp_servers (Codex), or a YAML list under mcpServers (Continue).
func entryCommand(path, name string) (string, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	raw = bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF})
	var entry any
	switch strings.ToLower(filepath.Ext(path)) {
	case ".toml":
		var doc map[string]any
		if toml.Unmarshal(raw, &doc) != nil {
			return "", false
		}
		entry = member(doc, "mcp_servers", name)
	case ".yaml", ".yml":
		var doc map[string]any
		if yaml.Unmarshal(raw, &doc) != nil {
			return "", false
		}
		list, _ := doc["mcpServers"].([]any)
		for _, item := range list {
			if m, ok := item.(map[string]any); ok && m["name"] == name {
				entry = m
			}
		}
	default:
		standard, err := hujson.Standardize(raw)
		if err != nil {
			return "", false
		}
		var doc map[string]any
		if json.Unmarshal(standard, &doc) != nil {
			return "", false
		}
		// Only top-level keys: Claude Code's file also nests per-project
		// entries under "projects".
		for _, key := range []string{"mcpServers", "context_servers", "mcp", "servers"} {
			if entry = member(doc, key, name); entry != nil {
				break
			}
		}
	}
	return commandOf(entry)
}

func member(doc map[string]any, key, name string) any {
	servers, _ := doc[key].(map[string]any)
	return servers[name]
}

// commandOf returns the program an entry runs.
func commandOf(entry any) (string, bool) {
	m, ok := entry.(map[string]any)
	if !ok {
		return "", false
	}
	switch c := m["command"].(type) {
	case string:
		return c, c != ""
	case []any: // OpenCode: the program followed by its arguments
		if len(c) > 0 {
			s, ok := c[0].(string)
			return s, ok && s != ""
		}
	case map[string]any: // Zed's own form: {"path": ..., "args": [...]}
		s, ok := c["path"].(string)
		return s, ok && s != ""
	}
	return "", false
}

// untickedClients returns the harnesses the wizard starts unticked: those
// whose entry install keeps unless asked.
func untickedClients(entries map[harness.ID]clientEntry) map[harness.ID]bool {
	out := make(map[harness.ID]bool)
	for id, e := range entries {
		if e.keptOnInstall() {
			out[id] = true
		}
	}
	return out
}

// matchClients resolves --clients names to harness IDs, case-insensitively,
// and returns the names that match no known client.
func matchClients(harnesses []harness.Harness, names []string) (map[harness.ID]bool, []string) {
	matched := make(map[harness.ID]bool)
	var unknown []string
	for _, name := range names {
		found := false
		for _, h := range harnesses {
			if strings.EqualFold(string(h.ID), name) {
				matched[h.ID] = true
				found = true
			}
		}
		if !found {
			unknown = append(unknown, name)
		}
	}
	return matched, unknown
}

// describeEntry says what a kept entry is, for the messages below.
func describeEntry(name string, e clientEntry) string {
	if e.kind == entryForeign {
		return fmt.Sprintf("its %q entry runs another program (%s)", name, e.command)
	}
	return fmt.Sprintf("its %q entry differs from what install writes (edited by hand?)", name)
}

// clientHint is `creality-slicer-mcp <command> --clients <id>` with the flags that
// select the scope and server name in use.
func clientHint(command string, id harness.ID, scope harness.Scope, name string) string {
	hint := fmt.Sprintf("%s %s --clients %s", domain.BinaryName, command, id)
	if scope.IsProject() {
		hint += fmt.Sprintf(" --scope project --dir %q", scope.Dir)
	}
	if name != domain.ServerName {
		hint += fmt.Sprintf(" --name %q", name)
	}
	return hint
}

// printKept explains, for each client install left alone, why and how to
// replace its entry.
func printKept(w io.Writer, kept []harness.Harness, entries map[harness.ID]clientEntry, scope harness.Scope, name string, dryRun bool) {
	verb := "is left as it is"
	if dryRun {
		verb = "would be left as it is"
	}
	for _, h := range kept {
		fmt.Fprintf(w, "  %s: %s, so it %s.\n", h.Name, describeEntry(name, entries[h.ID]), verb)
		fmt.Fprintf(w, "    To replace it: %s\n", clientHint("install --yes", h.ID, scope, name))
	}
}

// printSkippedForeign names the clients uninstall left alone because their
// entry runs another program.
func printSkippedForeign(w io.Writer, skipped []harness.Harness, entries map[harness.ID]clientEntry, scope harness.Scope, name string) {
	for _, h := range skipped {
		fmt.Fprintf(w, "  %s: %s, not %s, so it is left in place.\n", h.Name, describeEntry(name, entries[h.ID]), domain.BinaryName)
		fmt.Fprintf(w, "    To remove it anyway: %s\n", clientHint("uninstall", h.ID, scope, name))
	}
}
