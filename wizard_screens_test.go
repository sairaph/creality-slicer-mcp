package main

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/sairaph/mcp-wizard/flow"
	"github.com/sairaph/mcp-wizard/harness"
	"github.com/sairaph/mcp-wizard/installer"
)

const (
	codeFail = "38;5;203"
	codeOK   = "38;5;42"
)

// withColor lets the styles emit colour for the length of a test.
func withColor(t *testing.T) {
	t.Helper()
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(old) })
}

var wizardSizes = []struct{ w, h int }{{120, 36}, {80, 24}, {60, 20}, {40, 12}, {200, 50}}

func squashed(s string) string { return strings.Join(strings.Fields(ansi.Strip(s)), "") }

// checkWizardFrame asserts the invariants of a wizard view in w x h.
func checkWizardFrame(t *testing.T, name, view string, w, h int, hint string) {
	t.Helper()
	lines := strings.Split(view, "\n")
	if len(lines) != h {
		t.Fatalf("%s: %d lines, want %d:\n%s", name, len(lines), h, ansi.Strip(view))
	}
	for i, l := range lines {
		if ansi.StringWidth(l) > w {
			t.Fatalf("%s: line %d is %d wide: %q", name, i+1, ansi.StringWidth(l), ansi.Strip(l))
		}
	}
	if !strings.HasPrefix(ansi.Strip(lines[0]), "creality-slicer-mcp  Setup") || lines[1] != "" {
		t.Fatalf("%s: header rows = %q, %q", name, ansi.Strip(lines[0]), lines[1])
	}
	tail := ansi.Strip(lines[len(lines)-1])
	if w < 80 {
		tail = ansi.Strip(strings.Join(lines[len(lines)-3:], " "))
	}
	if strings.TrimSpace(ansi.Strip(lines[len(lines)-1])) == "" || !strings.Contains(squashed(tail), squashed(hint)) {
		t.Fatalf("%s: footer lacks %q: %q", name, hint, tail)
	}
}

func rowWith(t *testing.T, view, text string) string {
	t.Helper()
	for _, l := range strings.Split(view, "\n") {
		if strings.Contains(l, text) {
			return l
		}
	}
	t.Fatalf("no row with %q in:\n%s", text, ansi.Strip(view))
	return ""
}

// finishState is the state after a registration: five clients, one of which
// failed, and the guide skill written.
func finishState(dry bool, w, h int) (*AppState, applyGuard) {
	state := &AppState{}
	state.Width, state.Height = w, h
	state.Harness.Detections = []harness.Harness{
		{ID: "claude-code", Name: "Claude Code"},
		{ID: "claude-desktop", Name: "Claude Desktop"},
		{ID: "opencode", Name: "OpenCode", ReloadHint: "restart opencode"},
		{ID: "cursor", Name: "Cursor", ReloadHint: "reload the window"},
		{ID: "zed", Name: "Zed", ReloadHint: "restart Zed"},
	}
	state.Harness.Selected = map[harness.ID]bool{"claude-code": true, "claude-desktop": true, "opencode": true, "cursor": true, "zed": true}
	if dry {
		state.Results = installer.ResultsState{Done: true, Changes: []harness.Change{
			{HarnessID: "claude-code", Name: "Claude Code", State: harness.ApplyNoop, Path: `C:\Users\Alex\.claude.json`},
			{HarnessID: "claude-desktop", Name: "Claude Desktop", State: harness.ApplyNoop, Path: `C:\Users\Alex\AppData\Roaming\Claude\claude_desktop_config.json`},
			{HarnessID: "opencode", Name: "OpenCode", State: harness.Applied, Action: "add", Path: `C:\Users\Alex\.config\opencode\opencode.jsonc`},
			{HarnessID: "zed", Name: "Zed", State: harness.Applied, Action: "add", Path: `C:\Users\Alex\AppData\Roaming\Zed\settings.json`},
		}}
		state.Skills.take(skillPlan{Rows: []skillRow{
			{Dir: `C:\Users\Alex\.claude\skills\creality-slicer`, State: skillWouldWrite, Clients: 2},
			{Dir: `C:\Users\Alex\.agents\skills\creality-slicer`, State: skillWouldWrite, Clients: 1},
		}})
	} else {
		state.Results = installer.ResultsState{Done: true, Results: []harness.Result{
			{HarnessID: "claude-code", Name: "Claude Code", State: harness.ApplyNoop, Path: `C:\Users\Alex\.claude.json`},
			{HarnessID: "claude-desktop", Name: "Claude Desktop", State: harness.ApplyNoop, Path: `C:\Users\Alex\AppData\Roaming\Claude\claude_desktop_config.json`},
			{HarnessID: "opencode", Name: "OpenCode", State: harness.Applied, Path: `C:\Users\Alex\.config\opencode\opencode.jsonc`},
			{HarnessID: "cursor", Name: "Cursor", State: harness.Applied, Path: `C:\Users\Alex\.cursor\mcp.json`},
			{HarnessID: "zed", Name: "Zed", State: harness.ApplyFailed, Reason: "could not read the config file", Path: `C:\Users\Alex\AppData\Roaming\Zed\settings.json`},
		}}
		state.Skills.take(skillPlan{Desktop: true, Rows: []skillRow{
			{Dir: `C:\Users\Alex\.claude\skills\creality-slicer`, State: skillWritten, Clients: 3},
			{Dir: `C:\Users\Alex\.agents\skills\creality-slicer`, State: skillWritten, Clients: 1},
		}})
	}
	return state, applyGuard{Step: quitStep{}, dryRun: dry}
}

func TestFinishScreenSaysWhatBecameOfEachClientInWords(t *testing.T) {
	withColor(t)
	state, guard := finishState(false, 120, 36)
	view := guard.View(state)
	plainView := ansi.Strip(view)
	lines := strings.Split(plainView, "\n")
	if lines[0] != "creality-slicer-mcp  Setup  Done" || lines[2] != "  4 AI clients registered, 1 failed." {
		t.Errorf("header and summary:\n%s", plainView)
	}
	for name, word := range map[string]string{
		"Claude Code": "already set up", "Claude Desktop": "already set up", "OpenCode": "registered", "Cursor": "registered",
		"Zed": "failed: could not read the config file",
	} {
		row := rowWith(t, plainView, "  "+name)
		if !strings.HasPrefix(row, "  "+name) || !strings.HasPrefix(row[26:], word) {
			t.Errorf("%s row = %q, want %q at column 27", name, row, word)
		}
	}
	if raw := rowWith(t, view, "could not read"); !strings.Contains(raw, codeFail) || !strings.Contains(raw, "Zed") {
		t.Errorf("the failed row is not in the fail colour: %q", raw)
	}
	if raw := rowWith(t, view, "4 AI clients registered"); !strings.Contains(raw, codeFail) {
		t.Errorf("the summary of a failure is not in the fail colour: %q", raw)
	}
	if row := rowWith(t, plainView, "Guide skill"); strings.Join(strings.Fields(row), " ") != "Guide skill written for 4 AI clients" {
		t.Errorf("guide row = %q", row)
	}
	for _, want := range []string{
		"Restart these so they pick up the change:", "OpenCode restart opencode", "Cursor reload the window",
		"Claude Desktop takes the guide only as an upload", "Next: run creality-slicer-mcp to open the app.",
	} {
		if !strings.Contains(strings.Join(strings.Fields(plainView), " "), want) {
			t.Errorf("the finish screen lacks %q:\n%s", want, plainView)
		}
	}
	if strings.Contains(plainView, "Restart these") && strings.Contains(strings.Join(strings.Fields(plainView), " "), "Zed restart") {
		t.Error("a client that failed is asked to restart")
	}
	if lines[35] != "enter finish \u00b7 p show paths" {
		t.Errorf("footer = %q", lines[35])
	}
}

func TestFinishScreenShowsPathsOnlyOnRequest(t *testing.T) {
	state, guard := finishState(false, 80, 24)
	paths := []string{`.claude.json`, `claude_desktop_config.json`, `opencode.jsonc`, `.cursor\mcp.json`, `Zed\settings.json`, `.claude\skills\creality-slicer`, `.agents\skills\creality-slicer`}
	view := ansi.Strip(guard.View(state))
	for _, p := range paths {
		if strings.Contains(squashed(strings.Join(finishLines(state, false, 80), "\n")), squashed(p)) {
			t.Errorf("the path %s is shown before p", p)
		}
	}
	if d, _ := guard.Update(keyPress("p"), state); d != flow.Continue || !state.ShowPaths {
		t.Fatalf("p: directive %v, ShowPaths %v", d, state.ShowPaths)
	}
	all := squashed(strings.Join(finishLines(state, false, 80), "\n"))
	for _, p := range paths {
		if !strings.Contains(all, squashed(p)) {
			t.Errorf("the path %s is not shown after p", p)
		}
	}
	if last := strings.Split(ansi.Strip(guard.View(state)), "\n"); !strings.Contains(last[len(last)-1], "p hide paths") {
		t.Errorf("footer after p = %q (before: %q)", last[len(last)-1], view)
	}
	guard.Update(keyPress("p"), state)
	if state.ShowPaths {
		t.Error("p again does not hide the paths")
	}
}

func TestDryRunFinishScreen(t *testing.T) {
	state, guard := finishState(true, 120, 36)
	view := ansi.Strip(guard.View(state))
	lines := strings.Split(view, "\n")
	if lines[0] != "creality-slicer-mcp  Setup  Dry run" || lines[2] != "  Dry run - nothing was changed." {
		t.Errorf("header and summary:\n%s", view)
	}
	for name, word := range map[string]string{"Claude Code": "already set up", "OpenCode": "would add", "Zed": "would add"} {
		row := rowWith(t, view, "  "+name)
		if !strings.HasPrefix(row[26:], word) {
			t.Errorf("%s row = %q, want %q", name, row, word)
		}
	}
	text := strings.Join(strings.Fields(view), " ")
	for _, want := range []string{"Guide skill would write for 3 AI clients", "Next: run creality-slicer-mcp to open the app."} {
		if !strings.Contains(text, want) {
			t.Errorf("the dry run screen lacks %q:\n%s", want, view)
		}
	}
	for _, not := range []string{"Restart these", "Claude Desktop takes the guide"} {
		if strings.Contains(text, not) {
			t.Errorf("the dry run screen holds %q", not)
		}
	}
}

func TestFinishScreenFromTheAppSaysToPressEnter(t *testing.T) {
	state, guard := finishState(false, 120, 36)
	state.FromApp = true
	text := strings.Join(strings.Fields(ansi.Strip(guard.View(state))), " ")
	if !strings.Contains(text, "Next: press enter to return to the app.") || strings.Contains(text, "run creality-slicer-mcp to open") {
		t.Errorf("from the app:\n%s", text)
	}
}

func TestEveryWizardScreenFitsEveryWindow(t *testing.T) {
	for _, sz := range wizardSizes {
		for _, dry := range []bool{false, true} {
			state, guard := finishState(dry, sz.w, sz.h)
			checkWizardFrame(t, fmt.Sprintf("finish dry=%v %dx%d", dry, sz.w, sz.h), guard.View(state), sz.w, sz.h, "enter finish")
			state.ShowPaths = true
			checkWizardFrame(t, fmt.Sprintf("finish paths dry=%v %dx%d", dry, sz.w, sz.h), guard.View(state), sz.w, sz.h, "enter finish")

			// Registering: before the clients are done, and while the guide is written.
			state.Results.Done = false
			want := "please wait"
			if dry {
				want = "q cancel"
			}
			checkWizardFrame(t, fmt.Sprintf("registering dry=%v %dx%d", dry, sz.w, sz.h), guard.View(state), sz.w, sz.h, want)
			state.Results.Done, state.Skills = true, skillState{Started: true}
			checkWizardFrame(t, fmt.Sprintf("writing the guide dry=%v %dx%d", dry, sz.w, sz.h), guard.View(state), sz.w, sz.h, "please wait")
		}
		list, state := clientListState(sz.w, sz.h, 6)
		checkWizardFrame(t, fmt.Sprintf("client list %dx%d", sz.w, sz.h), list.View(state), sz.w, sz.h, "move")
		state.UntickedClients = []string{"Cursor"}
		checkWizardFrame(t, fmt.Sprintf("client list with a note %dx%d", sz.w, sz.h), list.View(state), sz.w, sz.h, "move")
		list, state = clientListState(sz.w, sz.h, 40)
		state.Harness.Cursor = 39
		checkWizardFrame(t, fmt.Sprintf("long client list %dx%d", sz.w, sz.h), list.View(state), sz.w, sz.h, "move")
		state.harnessDetecting = true
		checkWizardFrame(t, fmt.Sprintf("detecting %dx%d", sz.w, sz.h), list.View(state), sz.w, sz.h, "q cancel")
	}
}

func TestFinishScreenScrollsInASmallWindow(t *testing.T) {
	state, guard := finishState(false, 40, 12)
	lines, hints, rows, overflow := finishLayout(state, false)
	if !overflow || len(lines) <= rows {
		t.Fatalf("the finish screen fits 40x12 (%d lines, %d rows)", len(lines), rows)
	}
	if hints[0].Label != "scroll" {
		t.Errorf("hints = %v, want the scroll hint first", hints)
	}
	var seen []string
	for i := 0; i < len(lines); i++ {
		vis, _, _ := state.Scroll.Window(lines, rows)
		seen = append(seen, vis[len(vis)-1])
		if d, _ := guard.Update(keyPress("down"), state); d != flow.Continue {
			t.Fatalf("down: %v", d)
		}
	}
	if state.Scroll.Off != len(lines)-rows {
		t.Errorf("offset after scrolling down = %d, want %d", state.Scroll.Off, len(lines)-rows)
	}
	if !strings.HasSuffix(squashed(strings.Join(seen, "")), squashed(lines[len(lines)-1])) {
		t.Error("the last line was never shown")
	}
	guard.Update(keyPress("home"), state)
	if state.Scroll.Off != 0 {
		t.Errorf("home: %d", state.Scroll.Off)
	}
}

func TestEveryFinishLineIsShownWholeAtEveryWidth(t *testing.T) {
	for _, w := range []int{40, 60, 80, 120} {
		state, _ := finishState(false, w, 24)
		state.ShowPaths = true
		got := squashed(strings.Join(finishLines(state, false, w), "\n"))
		for _, want := range []string{
			`C:\Users\Alex\AppData\Roaming\Claude\claude_desktop_config.json`, `C:\Users\Alex\.agents\skills\creality-slicer`,
			"failed: could not read the config file", "written for 4 AI clients", "Restart these so they pick up the change:",
		} {
			if !strings.Contains(got, squashed(want)) {
				t.Errorf("width %d: %q was cut", w, want)
			}
		}
		for _, l := range finishLines(state, false, w) {
			if ansi.StringWidth(l) > w {
				t.Errorf("width %d: %q is %d wide", w, ansi.Strip(l), ansi.StringWidth(l))
			}
		}
	}
}

func TestStatusWords(t *testing.T) {
	for _, c := range []struct {
		r    harness.Result
		want string
	}{
		{harness.Result{State: harness.Applied}, "registered"},
		{harness.Result{State: harness.ApplyNoop}, "already set up"},
		{harness.Result{State: harness.ApplySkipped, Reason: "needs a newer version"}, "skipped: needs a newer version"},
		{harness.Result{State: harness.ApplyFailed, Reason: "boom"}, "failed: boom"},
		{harness.Result{State: harness.ApplyConflict}, "conflict"},
	} {
		if got, _ := resultWord(c.r); got != c.want {
			t.Errorf("%v: %q, want %q", c.r.State, got, c.want)
		}
	}
	for _, c := range []struct {
		c    harness.Change
		want string
	}{
		{harness.Change{State: harness.Applied, Action: "add"}, "would add"},
		{harness.Change{State: harness.ApplyNoop}, "already set up"},
		{harness.Change{State: harness.ApplySkipped, Reason: "x"}, "skipped: x"},
	} {
		if got, _ := changeWord(c.c); got != c.want {
			t.Errorf("%v: %q, want %q", c.c.State, got, c.want)
		}
	}
}

func TestScrollbackLines(t *testing.T) {
	state, _ := finishState(false, 80, 24)
	var buf bytes.Buffer
	printScrollback(&buf, state, false)
	if got, want := buf.String(), "  4 AI clients registered, 1 failed.\n  Next: run creality-slicer-mcp to open the app.\n"; got != want {
		t.Errorf("scrollback = %q, want %q", got, want)
	}
	state, _ = finishState(true, 80, 24)
	buf.Reset()
	printScrollback(&buf, state, true)
	if !strings.HasPrefix(buf.String(), "  Dry run - nothing was changed.\n  Next:") {
		t.Errorf("dry run scrollback = %q", buf.String())
	}
}

// --- the keys of the registration step ---

func keyPress(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "home":
		return tea.KeyMsg{Type: tea.KeyHome}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func TestEnterWaitsForTheGuideSkillThenFinishes(t *testing.T) {
	state := &AppState{}
	state.Harness.Scope = harness.ProjectScopeDir("project-dir")
	state.Harness.Selected = map[harness.ID]bool{"claude-code": true, "codex": true}
	state.Results = installer.ResultsState{Done: true, Results: []harness.Result{{HarnessID: "claude-code", Name: "Claude Code", State: harness.Applied}}}
	var gotScope harness.Scope
	var gotIDs []harness.ID
	var gotDry bool
	calls := 0
	state.runSkills = func(scope harness.Scope, ids []harness.ID, dryRun bool) skillPlan {
		calls++
		gotScope, gotIDs, gotDry = scope, ids, dryRun
		return skillPlan{Rows: []skillRow{{Dir: "x", State: skillWritten, Clients: 2}}}
	}
	guard := applyGuard{Step: quitStep{}, dryRun: false}

	d, start := guard.Update(keyPress("enter"), state)
	if d != flow.Continue || start == nil || calls != 0 {
		t.Fatalf("enter before the guide is written: directive %v, command %v, runSkills calls %d", d, start != nil, calls)
	}
	if state.Skills.Done {
		t.Fatal("the guide skill is done before it ran")
	}
	if d, _ := guard.Update(keyPress("enter"), state); d != flow.Continue {
		t.Fatalf("a second enter while the guide is written: %v", d)
	}
	msg := start()
	if calls != 1 || gotScope.Dir != "project-dir" || gotDry || len(gotIDs) != 2 || gotIDs[0] != "claude-code" {
		t.Fatalf("runSkills got scope %v, ids %v, dry %v (%d calls)", gotScope, gotIDs, gotDry, calls)
	}
	if d, _ := guard.Update(keyPress("enter"), state); d != flow.Continue {
		t.Fatalf("enter before the answer arrived: %v", d)
	}
	if d, _ := guard.Update(msg, state); d != flow.Continue || !state.Skills.Done || len(state.Skills.Rows) != 1 || state.Skills.Code != 0 {
		t.Fatalf("answer: directive %v, skills %+v", d, state.Skills)
	}
	if d, _ := guard.Update(keyPress("enter"), state); d != flow.Next {
		t.Fatalf("enter after the guide is written: %v, want Next", d)
	}
	if calls != 1 {
		t.Errorf("the guide skill ran %d times", calls)
	}
}

func TestAFailedGuideSkillSetsTheExitCode(t *testing.T) {
	var s skillState
	s.take(skillPlan{Rows: []skillRow{{State: skillWritten}, {State: skillFailed, Err: errors.New("disk full")}}})
	if s.Code != 1 || !s.Done {
		t.Errorf("state = %+v", s)
	}
	s.take(skillPlan{HomeErr: errors.New("no home")})
	if s.Code != 1 {
		t.Errorf("a missing home gives code %d", s.Code)
	}
	s.take(skillPlan{Rows: []skillRow{{State: skillSkipped}}})
	if s.Code != 0 {
		t.Errorf("a skipped folder gives code %d", s.Code)
	}
}

func TestGuideRowsForSkippedAndFailedFolders(t *testing.T) {
	state, _ := finishState(false, 120, 36)
	state.Skills.take(skillPlan{Rows: []skillRow{
		{Dir: "a", State: skillSkipped, Clients: 1},
		{Dir: "b", State: skillFailed, Err: errors.New("disk full"), Clients: 1},
	}})
	text := strings.Join(strings.Fields(ansi.Strip(strings.Join(finishLines(state, false, 120), "\n"))), " ")
	for _, want := range []string{"Guide skill skipped: the folder holds a skill this program did not write", "failed: disk full"} {
		if !strings.Contains(text, want) {
			t.Errorf("the finish lines lack %q:\n%s", want, text)
		}
	}
}

// --- the client list ---

func clientListState(w, h, n int) (harnessSelection, *AppState) {
	state := &AppState{}
	state.Width, state.Height = w, h
	for i := 0; i < n; i++ {
		state.Harness.Detections = append(state.Harness.Detections, harness.Harness{
			ID: harness.ID(fmt.Sprintf("client-%02d", i)), Name: fmt.Sprintf("Client %02d", i), State: harness.Detected, Installed: true,
		})
	}
	state.Harness.Selected = map[harness.ID]bool{"client-00": true, "client-01": true}
	return harnessSelection{name: "creality-slicer-mcp"}, state
}

func TestClientListRows(t *testing.T) {
	list, state := clientListState(120, 36, 3)
	state.Harness.Detections[2].State, state.Harness.Detections[2].Installed = harness.NotDetected, false
	view := ansi.Strip(list.View(state))
	lines := strings.Split(view, "\n")
	if lines[0] != "creality-slicer-mcp  Setup  AI clients" || lines[2] != "  Which AI clients should be able to use this server?" {
		t.Errorf("header and question:\n%s", view)
	}
	for i, want := range []string{" > \u25cf Client 00 ", "   \u25cf Client 01 "} {
		if !strings.HasPrefix(lines[4+i], want) {
			t.Errorf("row %d = %q, want it to start with %q", i+1, lines[4+i], want)
		}
	}
	if strings.Contains(view, "Client 02") {
		t.Error("a client that is not installed is listed before v")
	}
	if !strings.Contains(view, "press v to show 1 client(s) that are not installed") {
		t.Errorf("no hint about the hidden client:\n%s", view)
	}
	if last := lines[35]; last != "\u2191\u2193 move \u00b7 space toggle \u00b7 a all/none \u00b7 v show all \u00b7 enter continue \u00b7 q cancel" {
		t.Errorf("footer = %q", last)
	}
	state.Harness.ShowAll = true
	if view = ansi.Strip(list.View(state)); !strings.Contains(view, "Client 02") || !strings.Contains(view, "press v to hide clients that are not installed") {
		t.Errorf("after v:\n%s", view)
	}
}

func TestClientListNoteAndDetecting(t *testing.T) {
	list, state := clientListState(80, 24, 2)
	state.UntickedClients = []string{"Client 01"}
	text := strings.Join(strings.Fields(ansi.Strip(list.View(state))), " ")
	if !strings.Contains(text, `Client 01 already has a "creality-slicer-mcp" entry that setup did not write`) || !strings.Contains(text, "ticking it replaces it.") {
		t.Errorf("note:\n%s", text)
	}
	state.UntickedClients = []string{"Client 00", "Client 01"}
	if text = strings.Join(strings.Fields(ansi.Strip(list.View(state))), " "); !strings.Contains(text, "Client 00, Client 01 already have") || !strings.Contains(text, "ticking one replaces it.") {
		t.Errorf("note for two clients:\n%s", text)
	}
	state.harnessDetecting = true
	view := ansi.Strip(list.View(state))
	if !strings.Contains(view, "Looking for AI clients... (ctrl+c to cancel)") || strings.Contains(view, "Client 00") {
		t.Errorf("detecting:\n%s", view)
	}
}

func TestClientListKeepsTheCursorInView(t *testing.T) {
	list, state := clientListState(40, 12, 40)
	for _, cursor := range []int{0, 12, 39, 5} {
		state.Harness.Cursor = cursor
		view := ansi.Strip(list.View(state))
		want := fmt.Sprintf("> \u25cb Client %02d", cursor)
		if cursor < 2 {
			want = fmt.Sprintf("> \u25cf Client %02d", cursor)
		}
		if !strings.Contains(view, want) {
			t.Errorf("cursor %d is not on the screen:\n%s", cursor, view)
		}
	}
}

func TestProjectScopeHeaderAndQuestion(t *testing.T) {
	list, state := clientListState(120, 36, 2)
	state.Harness.Scope = harness.ProjectScopeDir(`C:\work\project`)
	view := ansi.Strip(list.View(state))
	lines := strings.Split(view, "\n")
	if lines[0] != "creality-slicer-mcp  Setup  project" || lines[2] != "  Which AI clients should get this server in this project?" || lines[3] != `  Project: C:\work\project` {
		t.Errorf("project scope:\n%s", view)
	}
}
