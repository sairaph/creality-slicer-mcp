package main

import (
	"context"
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/sairaph/mcp-wizard/cli"
	"github.com/sairaph/mcp-wizard/flow"
	"github.com/sairaph/mcp-wizard/harness"
	"github.com/sairaph/mcp-wizard/installer"
)

// readyHarnessStep returns mcp-wizard's real harness step, wrapped as the
// wizard wraps it, after its detection finished, showing two fake clients.
func readyHarnessStep(t *testing.T) (flow.Step[AppState], *AppState) {
	t.Helper()
	det, err := harness.New(harness.ServerSpec{Name: "creality-slicer-mcp-test", Command: "creality-slicer-mcp-test", Args: []string{"mcp"}})
	if err != nil {
		t.Fatal(err)
	}
	state := &AppState{}
	step := harnessSelection{Step: installer.HarnessStep(context.Background(), det, harnessState, installer.HarnessStepOptions{AllDetected: true})}
	batch, ok := step.Init(state)().(tea.BatchMsg)
	if !ok {
		t.Fatal("Init did not return a batch")
	}
	for _, cmd := range batch {
		if cmd != nil {
			step.Update(cmd(), state) // the spinner tick and the detection result
		}
	}
	state.Harness.Detections = []harness.Harness{
		{ID: "claude-code", Name: "Claude Code", State: harness.Detected, Installed: true},
		{ID: "cursor", Name: "Cursor", State: harness.Detected, Installed: true},
	}
	state.Harness.Selected = map[harness.ID]bool{"claude-code": true, "cursor": true}
	state.Harness.Cursor = 0
	return step, state
}

func rowFor(t *testing.T, view, name string) string {
	t.Helper()
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, name) {
			return line
		}
	}
	t.Fatalf("no row for %s in:\n%s", name, view)
	return ""
}

func TestDeselectedClientIsShownAsDeselected(t *testing.T) {
	step, state := readyHarnessStep(t)
	space := tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}

	step.Update(space, state)
	view := step.View(state)
	if row := rowFor(t, view, "Claude Code"); !strings.Contains(row, "○") || strings.Contains(row, "●") {
		t.Fatalf("deselected client still shown as selected: %q", row)
	}
	if row := rowFor(t, view, "Cursor"); !strings.Contains(row, "●") {
		t.Fatalf("other client not shown as selected: %q", row)
	}
	if state.Harness.Selected["claude-code"] {
		t.Fatal("the selection itself was not changed")
	}

	step.Update(space, state)
	if row := rowFor(t, step.View(state), "Claude Code"); !strings.Contains(row, "●") {
		t.Fatalf("selecting again not shown: %q", row)
	}
}

func TestAllNoneTogglesTheWholeList(t *testing.T) {
	step, state := readyHarnessStep(t)
	a := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}}
	step.Update(a, state) // everything was selected: clear all
	view := step.View(state)
	for _, name := range []string{"Claude Code", "Cursor"} {
		if row := rowFor(t, view, name); strings.Contains(row, "●") {
			t.Fatalf("%s still shown as selected after none: %q", name, row)
		}
	}
	step.Update(a, state)
	view = step.View(state)
	for _, name := range []string{"Claude Code", "Cursor"} {
		if row := rowFor(t, view, name); !strings.Contains(row, "●") {
			t.Fatalf("%s not shown as selected after all: %q", name, row)
		}
	}
}

func TestEnterWithNoClientSelectedMovesOn(t *testing.T) {
	step, state := readyHarnessStep(t)
	enter := tea.KeyMsg{Type: tea.KeyEnter}
	step.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}}, state) // none
	if d, _ := step.Update(enter, state); d != flow.Next {
		t.Fatalf("enter with no client selected: %v, want Next (registration then reports that nothing was configured)", d)
	}
	step.Update(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}, state)
	if d, _ := step.Update(enter, state); d != flow.Next {
		t.Fatalf("enter with a client selected: %v, want Next", d)
	}
}

func TestEnterBeforeDetectionFinishesDoesNothing(t *testing.T) {
	det, err := harness.New(harness.ServerSpec{Name: "creality-slicer-mcp-test", Command: "creality-slicer-mcp-test", Args: []string{"mcp"}})
	if err != nil {
		t.Fatal(err)
	}
	state := &AppState{}
	state.Harness.Selected = map[harness.ID]bool{} // left over from an earlier visit
	step := harnessSelection{Step: installer.HarnessStep(context.Background(), det, harnessState, installer.HarnessStepOptions{AllDetected: true})}
	step.Init(state) // detection has started but not finished
	if d, _ := step.Update(tea.KeyMsg{Type: tea.KeyEnter}, state); d != flow.Continue {
		t.Fatalf("enter while detecting: %v, want Continue", d)
	}
}

func TestKeysWaitForTheListWhenTheStepIsRevisited(t *testing.T) {
	step, state := readyHarnessStep(t)
	step.Init(state) // back to the list: detection runs again
	for _, key := range []tea.KeyMsg{{Type: tea.KeyRunes, Runes: []rune{'a'}}, {Type: tea.KeySpace, Runes: []rune{' '}}, {Type: tea.KeyEnter}} {
		if d, _ := step.Update(key, state); d != flow.Continue {
			t.Fatalf("%q while detecting again: %v, want Continue", key.String(), d)
		}
	}
	if state.Harness.Selected != nil {
		t.Fatalf("a key acted on the previous list: %v", state.Harness.Selected)
	}
	if d, _ := step.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}}, state); d != flow.Quit {
		t.Fatalf("q while detecting: %v, want Quit", d)
	}
}

func TestApplyStepIsFoundByID(t *testing.T) {
	det, err := harness.New(harness.ServerSpec{Name: "t", Command: "t"})
	if err != nil {
		t.Fatal(err)
	}
	steps := []flow.Step[AppState]{
		harnessSelection{Step: installer.HarnessStep(context.Background(), det, harnessState, installer.HarnessStepOptions{})},
		applyGuard{installer.ApplyStep(context.Background(), det, harnessState, resultsState, installer.ApplyStepOptions{}), false},
	}
	if got := stepIndex(steps, "apply"); got != 1 {
		t.Fatalf("stepIndex = %d, want 1", got)
	}
}

func TestCtrlCIsIgnoredWhileRegistrationRuns(t *testing.T) {
	inner := quitStep{}
	state := &AppState{}
	ctrlC := tea.KeyMsg{Type: tea.KeyCtrlC}
	if d, _ := (applyGuard{inner, false}).Update(ctrlC, state); d != flow.Continue {
		t.Fatalf("ctrl+c ended the wizard during registration: %v", d)
	}
	// Registered, and the guide skill is being written: still ignored.
	state.Results.Done = true
	state.runSkills = func(harness.Scope, []harness.ID, bool) skillPlan { return skillPlan{} }
	if d, _ := (applyGuard{inner, false}).Update(ctrlC, state); d != flow.Continue {
		t.Fatalf("ctrl+c ended the wizard while the guide skill is written: %v", d)
	}
	state.Skills.take(skillPlan{})
	if d, _ := (applyGuard{inner, false}).Update(ctrlC, state); d != flow.Quit {
		t.Fatalf("ctrl+c after the guide skill was written was swallowed: %v", d)
	}
	state.Skills = skillState{}
	state.Results.Done = false
	if d, _ := (applyGuard{inner, true}).Update(ctrlC, state); d != flow.Quit {
		t.Fatalf("ctrl+c during a dry run was swallowed: %v", d)
	}
}

// quitStep quits on any key, like the library's apply step on ctrl+c.
type quitStep struct{}

func (quitStep) ID() string                                    { return "apply" }
func (quitStep) Title(*AppState) string                        { return "" }
func (quitStep) Hints(*AppState) []struct{ Key, Label string } { return nil }
func (quitStep) Init(*AppState) tea.Cmd                        { return nil }
func (quitStep) View(*AppState) string                         { return "" }
func (quitStep) Update(tea.Msg, *AppState) (flow.Directive, tea.Cmd) {
	return flow.Quit, nil
}

func TestClassifyWizard(t *testing.T) {
	cases := []struct {
		name         string
		base         flow.BaseState
		runCode      int
		applyStarted bool
		dryRun       bool
		want         wizardOutcome
	}{
		{"quit before registration", flow.BaseState{}, 0, false, false, outcomeCancelled},
		{"quit during registration", flow.BaseState{}, 0, true, false, outcomeInterrupted},
		{"registration ran", flow.BaseState{Settled: true}, 0, true, false, outcomeCompleted},
		{"a step failed", flow.BaseState{Failure: os.ErrPermission}, 1, false, false, outcomeFailed},
		{"the terminal UI could not start", flow.BaseState{}, 1, false, false, outcomeFailed},
		{"registration failed in flight", flow.BaseState{Failure: os.ErrPermission}, 1, true, false, outcomeInterrupted},
		{"quit while a dry run plans", flow.BaseState{}, 0, true, true, outcomeCancelled},
		{"a dry run's plan failed", flow.BaseState{Failure: os.ErrPermission}, 1, true, true, outcomeFailed},
		{"a dry run completed", flow.BaseState{Settled: true}, 0, true, true, outcomeCompleted},
	}
	for _, c := range cases {
		if got := classifyWizard(&c.base, c.runCode, c.applyStarted, c.dryRun, false); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestRunsUnattended(t *testing.T) {
	cases := []struct {
		name string
		cmd  cli.Command
		want bool
	}{
		{"plain", cli.Command{}, false},
		{"yes", cli.Command{Yes: true}, true},
		{"all", cli.Command{All: true}, true},
		{"clients", cli.Command{Clients: []string{"cursor"}}, true},
		// install.sh and install.ps1 treat these as unattended too.
		{"token", cli.Command{Credentials: map[string]string{"token": "x"}}, true},
		{"email", cli.Command{Credentials: map[string]string{"email": "x"}}, true},
		{"dry run only", cli.Command{DryRun: true}, false},
	}
	for _, c := range cases {
		if got := runsUnattended(c.cmd); got != c.want {
			t.Errorf("%s: runsUnattended = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestSignalBeforeRegistrationIsACancel(t *testing.T) {
	if got := classifyWizard(&flow.BaseState{}, 1, false, false, true); got != outcomeCancelled {
		t.Errorf("a signal before registration: %v, want outcomeCancelled", got)
	}
}

func TestExitCancelledMatchesTheInstallScripts(t *testing.T) {
	if exitCancelled != 3 {
		t.Fatalf("exitCancelled = %d: install.sh and install.ps1 undo their changes on exit code 3", exitCancelled)
	}
	for script, want := range map[string]string{"install.sh": "EXIT_CANCELLED=3", "install.ps1": "$exitCancelled = 3"} {
		data, err := os.ReadFile(script)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), want) {
			t.Errorf("%s does not set %s", script, want)
		}
	}
}
