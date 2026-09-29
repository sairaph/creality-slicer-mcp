package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sairaph/mcp-wizard/harness"

	"github.com/sairaph/creality-slicer-mcp/internal/guide"
	"github.com/sairaph/creality-slicer-mcp/internal/userhome"
)

func testHome(t *testing.T) string {
	t.Helper()
	home, err := userhome.Dir()
	if err != nil {
		t.Fatal(err)
	}
	return home
}

func skillPath(root string, folder skillFolder) string {
	return filepath.Join(root, filepath.FromSlash(string(folder)), guide.Name, "SKILL.md")
}

const foreignSkill = "---\nname: creality-slicer\n---\nmine\n"

func TestSkillFoldersMapEachClientToTheFolderItReads(t *testing.T) {
	home := testHome(t)
	rel := func(dirs []string) []string {
		var out []string
		for _, d := range dirs {
			r, _ := filepath.Rel(home, d)
			out = append(out, filepath.ToSlash(r))
		}
		return out
	}
	cases := []struct {
		name    string
		ids     []harness.ID
		want    []string
		desktop bool
	}{
		{"claude code", []harness.ID{"claude-code"}, []string{".claude/skills"}, false},
		{"codex shares .agents", []harness.ID{"codex", "gemini"}, []string{".agents/skills"}, false},
		{"cursor beside claude code is served by .claude", []harness.ID{"claude-code", "cursor"}, []string{".claude/skills"}, false},
		{"cursor alone reads .agents", []harness.ID{"cursor"}, []string{".agents/skills"}, false},
		{"kiro", []harness.ID{"amazon-q"}, []string{".kiro/skills"}, false},
		{"claude desktop has no folder", []harness.ID{"claude-desktop"}, nil, true},
	}
	for _, c := range cases {
		dirs, desktop := skillFolders(c.ids, harness.Scope{}, home)
		if got := rel(dirs); strings.Join(got, ",") != strings.Join(c.want, ",") || desktop != c.desktop {
			t.Errorf("%s: folders %v desktop %v, want %v %v", c.name, got, desktop, c.want, c.desktop)
		}
	}
	// Project scope roots the folders in the project.
	project := t.TempDir()
	dirs, _ := skillFolders([]harness.ID{"claude-code"}, harness.ProjectScopeDir(project), home)
	if len(dirs) != 1 || !strings.HasPrefix(dirs[0], project) {
		t.Errorf("project folders = %v", dirs)
	}
}

func TestInstallSkillsWritesReplacesAndLeavesForeignFoldersAlone(t *testing.T) {
	project := t.TempDir()
	scope := harness.ProjectScopeDir(project)
	target := skillPath(project, folderClaude)
	var out bytes.Buffer

	if code := installSkills(&out, scope, []harness.ID{"claude-code"}, true, false); code != 0 {
		t.Fatalf("dry run: code %d", code)
	}
	if _, err := os.Stat(target); err == nil {
		t.Fatal("a dry run wrote the skill")
	}

	if code := installSkills(&out, scope, []harness.ID{"claude-code"}, false, false); code != 0 {
		t.Fatalf("code %d:\n%s", code, out.String())
	}
	data, err := os.ReadFile(target)
	if err != nil || !guide.IsOurs(string(data)) {
		t.Fatalf("skill not written, or not marked as ours: %v", err)
	}

	// A stale file inside our own copy goes when the folder is replaced.
	stale := filepath.Join(filepath.Dir(target), "old-topic.md")
	if err := os.WriteFile(stale, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	installSkills(&out, scope, []harness.ID{"claude-code"}, false, false)
	if _, err := os.Stat(stale); err == nil {
		t.Error("a file of the previous copy survived the rewrite")
	}

	// A skill of the same name that somebody else wrote is left alone.
	if err := os.WriteFile(target, []byte(foreignSkill), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	installSkills(&out, scope, []harness.ID{"claude-code"}, false, false)
	if got, _ := os.ReadFile(target); string(got) != foreignSkill {
		t.Error("a foreign skill was overwritten")
	}
	if !strings.Contains(out.String(), "[skip]") {
		t.Errorf("no [skip] line:\n%s", out.String())
	}
}

func TestInstallSkillsNamesClaudeDesktopsLimit(t *testing.T) {
	var out bytes.Buffer
	if code := installSkills(&out, harness.ProjectScopeDir(t.TempDir()), []harness.ID{"claude-desktop"}, false, false); code != 0 {
		t.Fatal(code)
	}
	if !strings.Contains(out.String(), "Claude Desktop takes skills only as an upload") {
		t.Errorf("output:\n%s", out.String())
	}
}

func TestRemoveSkillsRemovesOnlyOurCopiesAndKeepsFoldersOthersStillRead(t *testing.T) {
	project := t.TempDir()
	scope := harness.ProjectScopeDir(project)
	var out bytes.Buffer
	installSkills(&out, scope, []harness.ID{"claude-code", "codex"}, false, false)
	claude, agents := skillPath(project, folderClaude), skillPath(project, folderAgents)
	for _, p := range []string{claude, agents} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("setup: %v", err)
		}
	}

	// Removing codex removes .agents; claude-code stays registered and keeps .claude.
	removeSkills(&out, scope, []harness.ID{"codex"}, []harness.ID{"claude-code"}, false)
	if _, err := os.Stat(agents); err == nil {
		t.Error("the removed client's copy is still there")
	}
	if _, err := os.Stat(claude); err != nil {
		t.Error("the remaining client's copy was removed")
	}

	// A foreign copy is never removed.
	if err := os.WriteFile(claude, []byte(foreignSkill), 0o600); err != nil {
		t.Fatal(err)
	}
	removeSkills(&out, scope, []harness.ID{"claude-code"}, nil, false)
	if _, err := os.Stat(claude); err != nil {
		t.Error("a foreign copy was removed")
	}
}

func TestPruneRemovesACopyNoRegisteredClientReadsAnyMore(t *testing.T) {
	project := t.TempDir()
	scope := harness.ProjectScopeDir(project)
	var out bytes.Buffer
	installSkills(&out, scope, []harness.ID{"codex"}, false, false)
	agents := skillPath(project, folderAgents)
	installSkills(&out, scope, []harness.ID{"claude-code"}, false, true)
	if _, err := os.Stat(agents); err == nil {
		t.Error("with prune, the copy in an unread folder was kept")
	}
	if _, err := os.Stat(skillPath(project, folderClaude)); err != nil {
		t.Error("the copy for the registered client is missing")
	}
}

func TestRefreshAndRemoveAllTouchOnlyOurUserCopies(t *testing.T) {
	home := testHome(t)
	ours := skillPath(home, folderClaude)
	foreign := skillPath(home, folderAgents)
	oursOld := "---\nname: creality-slicer\nmetadata:\n  " + guide.Generator + "\n---\nold\n"
	for _, p := range []string{ours, foreign} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(ours, []byte(oursOld), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(foreign, []byte(foreignSkill), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer

	if code := refreshSkills(&out, true); code != 0 {
		t.Fatal(code)
	}
	if got, _ := os.ReadFile(ours); string(got) != oursOld {
		t.Error("a dry run rewrote the copy")
	}
	if code := refreshSkills(&out, false); code != 0 {
		t.Fatal(code)
	}
	if got, _ := os.ReadFile(ours); string(got) != guide.SkillMarkdown() {
		t.Error("the copy was not refreshed to the embedded guide")
	}
	if got, _ := os.ReadFile(foreign); string(got) != foreignSkill {
		t.Error("a foreign copy was refreshed")
	}

	out.Reset()
	if code := removeAllUserSkills(&out, false); code != 0 {
		t.Fatal(code)
	}
	if _, err := os.Stat(ours); err == nil {
		t.Error("our copy survived uninstall --all")
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Error("a foreign copy was removed by uninstall --all")
	}
	out.Reset()
	removeAllUserSkills(&out, false)
	if !strings.Contains(out.String(), "nothing to remove") {
		t.Errorf("second removal output:\n%s", out.String())
	}
}
