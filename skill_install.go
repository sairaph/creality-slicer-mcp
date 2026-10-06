package main

// The creality-slicer skill (internal/guide) is written into the skill
// folders of the AI clients install registers, and only those: each client is
// mapped to the one folder it reads, and clients that read the same folder
// share one copy. Only a folder whose SKILL.md carries the marker
// guide.Generator is ever replaced or removed, so a skill of the same name
// that somebody else wrote or edited is left alone (the same rule as edited
// client entries). update rewrites the copies that exist; uninstall removes
// the ones it wrote.

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/sairaph/mcp-wizard/harness"

	"github.com/sairaph/creality-slicer-mcp/internal/guide"
	"github.com/sairaph/creality-slicer-mcp/internal/userhome"
)

// Client IDs from detect-harness, which mcp-wizard passes through.
const (
	clientClaudeDesktop harness.ID = "claude-desktop"
	clientClaudeCode    harness.ID = "claude-code"
	clientContinue      harness.ID = "continue"
	// clientKiro is the Kiro CLI, which detect-harness still names after its
	// predecessor.
	clientKiro harness.ID = "amazon-q"
)

// skillFolder is a client's skill folder, relative to the home directory
// (user scope) or to the project directory (project scope).
type skillFolder string

const (
	folderClaude skillFolder = ".claude/skills"
	folderAgents skillFolder = ".agents/skills"
	folderKiro   skillFolder = ".kiro/skills"
)

// readsBoth are the clients that read both the .claude and the .agents skill
// folders: they are served by .claude when Claude Code is registered too, so
// they never list the guide twice.
var readsBoth = map[harness.ID]bool{"cursor": true, "opencode": true, "vscode": true}

// skillFoldersServed is skillFolders with, for each folder, how many of ids it
// serves (Claude Desktop has no folder and is not counted).
func skillFoldersServed(ids []harness.ID, scope harness.Scope, home string) (dirs []string, served map[string]int, desktop bool) {
	root := home
	if scope.IsProject() {
		root = scope.Dir
	}
	claudeCode := false
	for _, id := range ids {
		if id == clientClaudeCode {
			claudeCode = true
		}
	}
	served = map[string]int{}
	add := func(folder skillFolder) {
		dir := filepath.Join(root, filepath.FromSlash(string(folder)))
		if served[dir] == 0 {
			dirs = append(dirs, dir)
		}
		served[dir]++
	}
	for _, id := range ids {
		switch {
		case id == clientClaudeDesktop:
			desktop = true
		case id == clientClaudeCode:
			add(folderClaude)
		case readsBoth[id] && claudeCode:
			add(folderClaude)
		case id == clientKiro:
			add(folderKiro)
		case id == clientContinue && scope.IsProject():
			add(folderClaude)
		case id == clientContinue:
			// Continue's own folder, which CONTINUE_GLOBAL_DIR moves.
			dir := filepath.Join(home, ".continue")
			if v := os.Getenv("CONTINUE_GLOBAL_DIR"); v != "" {
				dir = v
			}
			dir = filepath.Join(dir, "skills")
			if served[dir] == 0 {
				dirs = append(dirs, dir)
			}
			served[dir]++
		default:
			add(folderAgents)
		}
	}
	sort.Strings(dirs)
	return dirs, served, desktop
}

// skillFolders returns the skill folders (absolute) that serve ids under
// scope, sorted, and whether Claude Desktop is among ids: it reads skills from
// an upload only, so it has no folder.
func skillFolders(ids []harness.ID, scope harness.Scope, home string) (dirs []string, desktop bool) {
	dirs, _, desktop = skillFoldersServed(ids, scope, home)
	return dirs, desktop
}

// allSkillFolders are every folder install can write under scope, for pruning,
// update and uninstall --all.
func allSkillFolders(scope harness.Scope, home string) []string {
	dirs, _ := skillFolders([]harness.ID{clientClaudeCode, "codex", clientKiro, clientContinue}, scope, home)
	return dirs
}

// allUserSkillFolders are every user-scope folder install can write.
func allUserSkillFolders(home string) []string {
	return allSkillFolders(harness.Scope{}, home)
}

// skillHome returns the home directory, or reports why it cannot.
func skillHome(w io.Writer) (string, bool) {
	home, err := userhome.Dir()
	if err != nil {
		fmt.Fprintf(w, "  [fail] guide skill: cannot find the home directory: %v\n", err)
		return "", false
	}
	return home, true
}

// ownSkill reports whether dir holds a skill creality-slicer-mcp wrote, and whether
// dir exists at all.
func ownSkill(dir string) (ours, exists bool) {
	data, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err == nil {
		return guide.IsOurs(string(data)), true
	}
	if _, statErr := os.Stat(dir); statErr == nil {
		return false, true
	}
	return false, false
}

// writeSkillFolder replaces dir with the embedded guide.
func writeSkillFolder(dir string) error { return writeSkillFrom(guide.FS(), dir) }

// writeSkillFrom replaces dir with the files of src. They are written to a
// sibling folder first, so a failure midway (disk full, a file locked by an
// editor) removes only that folder and leaves the previous copy as it was.
func writeSkillFrom(src fs.FS, dir string) error {
	tmp := dir + ".new"
	if err := os.RemoveAll(tmp); err != nil {
		return err
	}
	err := fs.WalkDir(src, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(tmp, filepath.FromSlash(path))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := fs.ReadFile(src, path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err == nil {
		if err = os.RemoveAll(dir); err == nil {
			err = os.Rename(tmp, dir)
		}
	}
	if err != nil {
		os.RemoveAll(tmp)
	}
	return err
}

// skill states of a skillRow.
const (
	skillWritten    = "written"
	skillWouldWrite = "would write"
	skillSkipped    = "skipped"
	skillFailed     = "failed"
)

// skillRow is what became of one skill folder.
type skillRow struct {
	// Dir is the folder of the skill itself (<skills folder>/creality-slicer).
	Dir   string
	State string
	Err   error
	// Clients is how many of the registered clients read this folder.
	Clients int
}

// skillPlan is what installing the guide for some clients does or would do.
type skillPlan struct {
	Rows []skillRow
	// Desktop is true when Claude Desktop is among the clients: it takes
	// skills only as an upload, so it has no folder.
	Desktop bool
	// HomeErr is why the home directory cannot be found; Rows is then empty.
	HomeErr error
	home    string
}

// planSkills decides, and with dryRun false does, the writing of the guide for
// ids' clients: one row per folder. A folder that holds a skill this program
// did not write is skipped, as it is everywhere else.
func planSkills(scope harness.Scope, ids []harness.ID, dryRun bool) skillPlan {
	if len(ids) == 0 {
		return skillPlan{}
	}
	home, err := userhome.Dir()
	if err != nil {
		return skillPlan{HomeErr: err}
	}
	dirs, served, desktop := skillFoldersServed(ids, scope, home)
	plan := skillPlan{Desktop: desktop, home: home}
	for _, dir := range dirs {
		target := filepath.Join(dir, guide.Name)
		row := skillRow{Dir: target, Clients: served[dir]}
		ours, exists := ownSkill(target)
		switch {
		case exists && !ours:
			row.State = skillSkipped
		case dryRun:
			row.State = skillWouldWrite
		default:
			if err := writeSkillFolder(target); err != nil {
				row.State, row.Err = skillFailed, err
			} else {
				row.State = skillWritten
			}
		}
		plan.Rows = append(plan.Rows, row)
	}
	return plan
}

// installSkills writes the guide for ids' clients, printing one line per
// folder. It returns an exit code: a folder that could not be written fails.
// With prune, ids is every registered client, so a copy that creality-slicer-mcp wrote
// into another folder no client of ids reads any more is removed (a client
// registered later can move from one folder to another); the wizard's partial
// selection never prunes.
func installSkills(w io.Writer, scope harness.Scope, ids []harness.ID, dryRun, prune bool) int {
	if len(ids) == 0 {
		return 0
	}
	plan := planSkills(scope, ids, dryRun)
	if plan.HomeErr != nil {
		fmt.Fprintf(w, "  [fail] guide skill: cannot find the home directory: %v\n", plan.HomeErr)
		return 1
	}
	if len(plan.Rows) == 0 && !plan.Desktop {
		return 0
	}
	fmt.Fprintln(w, "\n  Guide skill")
	code := 0
	for _, row := range plan.Rows {
		switch row.State {
		case skillSkipped:
			fmt.Fprintf(w, "  [skip] %s holds a skill that creality-slicer-mcp did not write; it is left as it is.\n", row.Dir)
		case skillWouldWrite:
			fmt.Fprintf(w, "  [ok]   would write the guide skill to %s\n", row.Dir)
		case skillFailed:
			fmt.Fprintf(w, "  [fail] %s: %v\n", row.Dir, row.Err)
			code = 1
		default:
			fmt.Fprintf(w, "  [ok]   wrote the guide skill to %s\n", row.Dir)
		}
	}
	if prune && len(plan.Rows) > 0 {
		wanted := map[string]bool{}
		for _, row := range plan.Rows {
			wanted[filepath.Dir(row.Dir)] = true
		}
		var stale []string
		for _, dir := range allSkillFolders(scope, plan.home) {
			if !wanted[dir] {
				stale = append(stale, filepath.Join(dir, guide.Name))
			}
		}
		code = max(code, removeOwnSkills(w, stale, dryRun))
	}
	if plan.Desktop {
		fmt.Fprintln(w, "  Claude Desktop takes skills only as an upload (Settings > Capabilities > Skills), so the guide")
		fmt.Fprintln(w, "  is not installed there. The tools and the server instructions work without it.")
	}
	return code
}

// removeSkills removes the guide from the folders that serve removed and no
// client in remaining, only where creality-slicer-mcp wrote it.
func removeSkills(w io.Writer, scope harness.Scope, removed, remaining []harness.ID, dryRun bool) int {
	if len(removed) == 0 {
		return 0
	}
	home, ok := skillHome(w)
	if !ok {
		return 1
	}
	dirs, _ := skillFolders(removed, scope, home)
	stillNeeded, _ := skillFolders(remaining, scope, home)
	keep := map[string]bool{}
	for _, dir := range stillNeeded {
		keep[dir] = true
	}
	var targets []string
	for _, dir := range dirs {
		target := filepath.Join(dir, guide.Name)
		if ours, _ := ownSkill(target); ours && !keep[dir] {
			targets = append(targets, target)
		}
	}
	code := removeOwnSkills(w, targets, dryRun)
	if len(targets) == 0 {
		return code
	}
	// A client that stays registered may have read the folder just emptied
	// (Cursor read Claude Code's): give it a copy where it looks.
	for _, dir := range stillNeeded {
		target := filepath.Join(dir, guide.Name)
		if _, exists := ownSkill(target); exists {
			continue
		}
		switch {
		case dryRun:
			fmt.Fprintf(w, "  [ok]   would write the guide skill to %s\n", target)
		default:
			if err := writeSkillFolder(target); err != nil {
				fmt.Fprintf(w, "  [fail] %s: %v\n", target, err)
				code = 1
				continue
			}
			fmt.Fprintf(w, "  [ok]   wrote the guide skill to %s\n", target)
		}
	}
	return code
}

// removeOwnSkills removes each folder that creality-slicer-mcp wrote, saying nothing
// about the others.
func removeOwnSkills(w io.Writer, targets []string, dryRun bool) int {
	code := 0
	for _, target := range targets {
		if ours, _ := ownSkill(target); !ours {
			continue
		}
		if dryRun {
			fmt.Fprintf(w, "  [ok]   would remove the guide skill %s\n", target)
			continue
		}
		if err := os.RemoveAll(target); err != nil {
			fmt.Fprintf(w, "  [fail] %s: %v\n", target, err)
			code = 1
			continue
		}
		fmt.Fprintf(w, "  [ok]   removed the guide skill %s\n", target)
	}
	return code
}

// removeAllUserSkills removes every user-scope copy creality-slicer-mcp wrote, for
// uninstall --all.
func removeAllUserSkills(w io.Writer, dryRun bool) int {
	home, ok := skillHome(w)
	if !ok {
		return 1
	}
	var targets []string
	for _, dir := range allUserSkillFolders(home) {
		targets = append(targets, filepath.Join(dir, guide.Name))
	}
	before := 0
	for _, t := range targets {
		if ours, _ := ownSkill(t); ours {
			before++
		}
	}
	if before == 0 {
		fmt.Fprintln(w, "  nothing to remove")
		return 0
	}
	return removeOwnSkills(w, targets, dryRun)
}

// refreshSkills rewrites the copies of the guide that creality-slicer-mcp wrote before
// with the embedded version, so an update never leaves an old guide behind.
func refreshSkills(w io.Writer, dryRun bool) int {
	home, ok := skillHome(w)
	if !ok {
		return 1
	}
	code := 0
	for _, dir := range allUserSkillFolders(home) {
		target := filepath.Join(dir, guide.Name)
		if ours, _ := ownSkill(target); !ours {
			continue
		}
		if dryRun {
			fmt.Fprintf(w, "  would update the guide skill in %s\n", target)
			continue
		}
		if err := writeSkillFolder(target); err != nil {
			fmt.Fprintf(w, "  [fail] %s: %v\n", target, err)
			code = 1
			continue
		}
		fmt.Fprintf(w, "  [ok]   updated the guide skill in %s\n", target)
	}
	return code
}

// selectedIDs lists the clients the wizard registered.
func selectedIDs(selected map[harness.ID]bool) []harness.ID {
	var ids []harness.ID
	for id, on := range selected {
		if on {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// skillClients are every client that runs creality-slicer-mcp after an unattended
// install: the ones registered now plus every other client whose entry runs it
// already (--clients narrows what is registered, not what reads the guide), so
// clients that share a folder get one copy in the right place.
func skillClients(harnesses []harness.Harness, entries map[harness.ID]clientEntry, registered []harness.ID) []harness.ID {
	out := append([]harness.ID(nil), registered...)
	in := map[harness.ID]bool{}
	for _, id := range registered {
		in[id] = true
	}
	for _, h := range harnesses {
		if e, ok := entries[h.ID]; ok && e.ours() && !in[h.ID] {
			out = append(out, h.ID)
		}
	}
	return out
}
