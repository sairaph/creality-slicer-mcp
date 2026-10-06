package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sairaph/mcp-wizard/harness"
)

func planRowFor(plan skillPlan, dir string) (skillRow, bool) {
	for _, r := range plan.Rows {
		if strings.HasSuffix(filepath.ToSlash(r.Dir), filepath.ToSlash(dir)) {
			return r, true
		}
	}
	return skillRow{}, false
}

func TestPlanSkillsDryRunWritesNothing(t *testing.T) {
	project := t.TempDir()
	scope := harness.ProjectScopeDir(project)
	plan := planSkills(scope, []harness.ID{"claude-code", "codex"}, true)
	if plan.HomeErr != nil || plan.Desktop || len(plan.Rows) != 2 {
		t.Fatalf("plan = %+v", plan)
	}
	for _, folder := range []string{".claude/skills/creality-slicer", ".agents/skills/creality-slicer"} {
		row, ok := planRowFor(plan, folder)
		if !ok || row.State != skillWouldWrite || row.Clients != 1 || !strings.HasPrefix(row.Dir, project) {
			t.Errorf("%s: %+v", folder, row)
		}
	}
	entries, err := os.ReadDir(project)
	if err != nil || len(entries) != 0 {
		t.Errorf("a dry run wrote %v (%v)", entries, err)
	}
}

func TestPlanSkillsWritesInTheTemporaryProject(t *testing.T) {
	project := t.TempDir()
	scope := harness.ProjectScopeDir(project)
	plan := planSkills(scope, []harness.ID{"claude-code", "cursor", "opencode"}, false)
	if len(plan.Rows) != 1 {
		t.Fatalf("clients that read the .claude folder share one copy: %+v", plan.Rows)
	}
	row := plan.Rows[0]
	if row.State != skillWritten || row.Clients != 3 || row.Err != nil {
		t.Errorf("row = %+v", row)
	}
	if _, err := os.Stat(filepath.Join(row.Dir, "SKILL.md")); err != nil {
		t.Errorf("the guide was not written: %v", err)
	}
	// A second run replaces the copy it wrote.
	again := planSkills(scope, []harness.ID{"claude-code"}, false)
	if len(again.Rows) != 1 || again.Rows[0].State != skillWritten {
		t.Errorf("rewriting its own copy: %+v", again.Rows)
	}
}

func TestPlanSkillsLeavesAForeignFolderAlone(t *testing.T) {
	project := t.TempDir()
	scope := harness.ProjectScopeDir(project)
	target := skillPath(project, folderClaude)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(foreignSkill), 0o644); err != nil {
		t.Fatal(err)
	}
	plan := planSkills(scope, []harness.ID{"claude-code"}, false)
	if len(plan.Rows) != 1 || plan.Rows[0].State != skillSkipped {
		t.Fatalf("plan = %+v", plan.Rows)
	}
	if got, _ := os.ReadFile(target); string(got) != foreignSkill {
		t.Errorf("a skill somebody else wrote was changed: %q", got)
	}
}

func TestPlanSkillsReportsAFolderThatCannotBeWritten(t *testing.T) {
	project := t.TempDir()
	// A file where the skills folder should be: nothing can be created in it.
	if err := os.WriteFile(filepath.Join(project, ".claude"), []byte("not a folder"), 0o644); err != nil {
		t.Fatal(err)
	}
	plan := planSkills(harness.ProjectScopeDir(project), []harness.ID{"claude-code", "codex"}, false)
	claude, _ := planRowFor(plan, ".claude/skills/creality-slicer")
	agents, _ := planRowFor(plan, ".agents/skills/creality-slicer")
	if claude.State != skillFailed || claude.Err == nil {
		t.Errorf(".claude row = %+v", claude)
	}
	if agents.State != skillWritten {
		t.Errorf("one failed folder stopped the others: %+v", agents)
	}
}

func TestPlanSkillsForClaudeDesktopHasNoFolder(t *testing.T) {
	plan := planSkills(harness.ProjectScopeDir(t.TempDir()), []harness.ID{"claude-desktop"}, false)
	if !plan.Desktop || len(plan.Rows) != 0 {
		t.Errorf("plan = %+v", plan)
	}
	if plan := planSkills(harness.Scope{}, nil, false); plan.Desktop || len(plan.Rows) != 0 || plan.HomeErr != nil {
		t.Errorf("no clients: %+v", plan)
	}
}

func TestPlanSkillsInTheIsolatedHome(t *testing.T) {
	home := testHome(t)
	plan := planSkills(harness.Scope{}, []harness.ID{"claude-code"}, false)
	if len(plan.Rows) != 1 || plan.Rows[0].State != skillWritten || !strings.HasPrefix(plan.Rows[0].Dir, home) {
		t.Fatalf("plan = %+v (home %s)", plan.Rows, home)
	}
	t.Cleanup(func() { os.RemoveAll(plan.Rows[0].Dir) })
	if _, err := os.Stat(skillPath(home, folderClaude)); err != nil {
		t.Errorf("the guide is not in the isolated home: %v", err)
	}
}
