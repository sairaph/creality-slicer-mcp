package main

// The registration step of the wizard: the guide skill is written inside the
// program, once the clients are registered, and the last screen says what
// became of every client and of the guide in words, with the paths only on
// request.

import (
	"fmt"
	"io"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/sairaph/mcp-wizard/flow"
	"github.com/sairaph/mcp-wizard/harness"
	"github.com/sairaph/mcp-wizard/tui"

	"github.com/sairaph/creality-slicer-mcp/internal/appui"
)

// skillState is the writing of the guide skill after the registration.
type skillState struct {
	Started, Done bool
	Rows          []skillRow
	Desktop       bool
	HomeErr       error
	// Code is 1 when the guide could not be written for some client.
	Code int
}

// skillsDoneMsg is the answer of the command that writes the guide.
type skillsDoneMsg struct{ plan skillPlan }

func (s *skillState) take(plan skillPlan) {
	*s = skillState{Started: true, Done: true, Rows: plan.Rows, Desktop: plan.Desktop, HomeErr: plan.HomeErr}
	if plan.HomeErr != nil {
		s.Code = 1
	}
	for _, r := range plan.Rows {
		if r.State == skillFailed {
			s.Code = 1
		}
	}
}

// Init forgets what an earlier visit of the step left.
func (g applyGuard) Init(state *AppState) tea.Cmd {
	state.Skills, state.ShowPaths, state.Scroll = skillState{}, false, appui.Scroller{}
	return g.Step.Init(state)
}

// startSkills returns the command that writes the guide for the registered
// clients (or plans it, in a dry run).
func (g applyGuard) startSkills(state *AppState) tea.Cmd {
	state.Skills.Started = true
	scope, ids, dry := state.Harness.Scope, selectedIDs(state.Harness.Selected), g.dryRun
	run := state.runSkills
	if run == nil {
		run = planSkills
	}
	return func() tea.Msg { return skillsDoneMsg{plan: run(scope, ids, dry)} }
}

// Update keeps ctrl+c from ending the wizard while the registration writes
// are running, starts the guide skill when the clients are registered, ignores
// every key until it is written, and handles the keys of the last screen.
func (g applyGuard) Update(msg tea.Msg, state *AppState) (flow.Directive, tea.Cmd) {
	rs := &state.Results
	if m, ok := msg.(skillsDoneMsg); ok {
		state.Skills.take(m.plan)
		return flow.Continue, nil
	}
	var start tea.Cmd
	if rs.Done && !state.Skills.Started && state.Failure == nil {
		start = g.startSkills(state)
	}
	if k, ok := msg.(tea.KeyMsg); ok {
		switch {
		case !rs.Done:
			if k.String() == "ctrl+c" && !g.dryRun {
				return flow.Continue, nil
			}
		case !state.Skills.Done:
			return flow.Continue, start
		default:
			if d, handled := g.finishKey(k, state); handled {
				return d, nil
			}
		}
	}
	d, cmd := g.Step.Update(msg, state)
	if d == flow.Continue && rs.Done && !state.Skills.Started {
		start = g.startSkills(state)
	}
	if tui.IsSpinMsg(msg) && rs.Done && state.Skills.Started && !state.Skills.Done {
		state.Spinner.Frame++
		cmd = tea.Batch(cmd, tui.Spinner())
	}
	return d, tea.Batch(cmd, start)
}

// finishKey handles the keys of the last screen.
func (g applyGuard) finishKey(k tea.KeyMsg, state *AppState) (flow.Directive, bool) {
	switch k.String() {
	case "p":
		state.ShowPaths = !state.ShowPaths
		return flow.Continue, true
	case "enter":
		return flow.Next, true
	}
	lines, _, rows, _ := finishLayout(state, g.dryRun)
	if state.Scroll.Keys(k, len(lines), rows) {
		return flow.Continue, true
	}
	return flow.Continue, false
}

// View draws the registering screen, then the last one.
func (g applyGuard) View(state *AppState) string {
	if !state.Results.Done || !state.Skills.Done {
		return registeringView(state, g.dryRun)
	}
	w, h := wizardSize(state)
	lines, hints, rows, _ := finishLayout(state, g.dryRun)
	vis, _, _ := state.Scroll.Window(lines, rows)
	context := "Done"
	if g.dryRun {
		context = "Dry run"
	}
	view, _ := appui.Render(w, h, appui.Frame{Screen: "Setup", Context: context, Body: vis, Hints: hints})
	return view
}

// finishLayout returns the lines of the last screen, its footer and how many
// lines are shown; the scroll hint is in the footer when they do not fit.
func finishLayout(state *AppState, dryRun bool) (lines []string, hints []tui.Hint, rows int, overflow bool) {
	w, h := wizardSize(state)
	lines = finishLines(state, dryRun, w)
	paths := "show paths"
	if state.ShowPaths {
		paths = "hide paths"
	}
	base := []tui.Hint{{Key: "enter", Label: "finish"}, {Key: "p", Label: paths}}
	withScroll := append([]tui.Hint{{Key: "↑↓", Label: "scroll"}}, base...)
	rows = appui.BodyRows(w, h, "", withScroll)
	if len(lines) <= rows {
		return lines, base, appui.BodyRows(w, h, "", base), false
	}
	return lines, withScroll, rows, true
}

// clientRow is one client on the last screen.
type clientRow struct {
	Name, Word, Path string
	Role             appui.Role
	ID               harness.ID
	Applied          bool
}

func clientName(name string, id harness.ID) string {
	if name == "" {
		return string(id)
	}
	return name
}

// resultWord is the status word of a registered client.
func resultWord(r harness.Result) (string, appui.Role) {
	switch r.State {
	case harness.Applied:
		return "registered", appui.RoleOK
	case harness.ApplyNoop:
		return "already set up", appui.RoleDim
	case harness.ApplySkipped:
		return withReason("skipped", r.Reason), appui.RoleWarn
	case harness.ApplyFailed:
		return withReason("failed", r.Reason), appui.RoleFail
	case harness.ApplyConflict:
		return "conflict", appui.RoleWarn
	}
	return withReason(string(r.State), r.Reason), appui.RoleText
}

// changeWord is the status word of a client in a dry run.
func changeWord(c harness.Change) (string, appui.Role) {
	switch {
	case c.State == harness.ApplyNoop:
		return "already set up", appui.RoleDim
	case c.Action != "":
		return "would " + c.Action, appui.RoleText
	}
	return withReason(string(c.State), c.Reason), appui.RoleText
}

func withReason(word, reason string) string {
	if reason == "" {
		return word
	}
	return word + ": " + reason
}

func clientRows(state *AppState, dryRun bool) []clientRow {
	var rows []clientRow
	if dryRun {
		for _, c := range state.Results.Changes {
			word, role := changeWord(c)
			rows = append(rows, clientRow{Name: clientName(c.Name, c.HarnessID), Word: word, Role: role, Path: c.Path, ID: c.HarnessID})
		}
		return rows
	}
	for _, r := range state.Results.Results {
		word, role := resultWord(r)
		rows = append(rows, clientRow{Name: clientName(r.Name, r.HarnessID), Word: word, Role: role, Path: r.Path, ID: r.HarnessID, Applied: r.State == harness.Applied})
	}
	return rows
}

// finishSummary is the first row of the last screen, and the line that stays in
// the scrollback.
func finishSummary(state *AppState, dryRun bool) (string, appui.Role) {
	if dryRun {
		return "Dry run - nothing was changed.", appui.RoleDim
	}
	rs := state.Results.Results
	if len(rs) == 0 {
		return "No AI clients were selected, so nothing was configured.", appui.RoleText
	}
	registered, failed := 0, 0
	for _, r := range rs {
		switch r.State {
		case harness.Applied, harness.ApplyNoop:
			registered++
		case harness.ApplyFailed:
			failed++
		}
	}
	text := plural(registered, "AI client", "AI clients") + " registered"
	if failed > 0 {
		return fmt.Sprintf("%s, %d failed.", text, failed), appui.RoleFail
	}
	return text + ".", appui.RoleOK
}

// nextText is the last line of the last screen.
func nextText(fromApp bool) string {
	if fromApp {
		return "Next: press enter to return to the app."
	}
	return "Next: run creality-slicer-mcp to open the app."
}

// printScrollback writes the two plain lines that stay in the terminal after
// the program ended.
func printScrollback(w io.Writer, state *AppState, dryRun bool) {
	text, _ := finishSummary(state, dryRun)
	fmt.Fprintf(w, "  %s\n  %s\n", text, nextText(state.FromApp))
}

// finishColumn is where the status words start: a margin of two, the name
// column and two blanks.
const (
	finishNameWidth = 22
	finishColumn    = 2 + finishNameWidth + 2
)

// finishLines are the rows of the last screen.
func finishLines(state *AppState, dryRun bool, w int) []string {
	var out []string
	pad := appui.Spaces(finishColumn)
	row := func(label, word string, role, labelRole appui.Role) []string {
		first := "  " + appui.Paint(labelRole, appui.PadRight(label, finishNameWidth)) + "  "
		lines := appui.WrapRole(word, first, pad, w-2, role)
		if len(lines) == 0 {
			lines = []string{first}
		}
		return lines
	}
	paths := func(list ...string) {
		if !state.ShowPaths {
			return
		}
		for _, p := range list {
			if p != "" {
				out = append(out, appui.WrapRole(p, pad, pad, w-2, appui.RoleDim)...)
			}
		}
	}

	text, role := finishSummary(state, dryRun)
	out = append(out, appui.WrapRole(text, "  ", "  ", w-2, role)...)
	rows := clientRows(state, dryRun)
	if len(rows) > 0 {
		out = append(out, "")
	}
	for _, r := range rows {
		names := appui.Wrap(r.Name, finishNameWidth, 0)
		if len(names) == 0 {
			names = []string{""}
		}
		labelRole := appui.RoleText
		if r.Role == appui.RoleFail {
			labelRole = appui.RoleFail
		}
		out = append(out, row(names[0], r.Word, r.Role, labelRole)...)
		for _, extra := range names[1:] {
			out = append(out, "  "+appui.Paint(labelRole, extra))
		}
		paths(r.Path)
	}

	out = append(out, guideLines(state, w, row, paths)...)

	if !dryRun {
		byID := map[harness.ID]harness.Harness{}
		for _, h := range state.Harness.Detections {
			byID[h.ID] = h
		}
		var restart []clientRow
		for _, r := range rows {
			if r.Applied && byID[r.ID].ReloadHint != "" {
				restart = append(restart, r)
			}
		}
		if len(restart) > 0 {
			out = append(out, "")
			out = append(out, appui.WrapRole("Restart these so they pick up the change:", "  ", "  ", w-2, appui.RoleText)...)
			for _, r := range restart {
				out = append(out, row(r.Name, byID[r.ID].ReloadHint, appui.RoleText, appui.RoleText)...)
			}
		}
		if state.Skills.Desktop {
			out = append(out, "")
			out = append(out, appui.WrapRole("Claude Desktop takes the guide only as an upload (Settings > Capabilities > Skills). The tools work without it.", "  ", "  ", w-2, appui.RoleText)...)
		}
	}
	out = append(out, "")
	out = append(out, appui.WrapRole(nextText(state.FromApp), "  ", "  ", w-2, appui.RoleText)...)
	return out
}

// guideLines are the rows about the guide skill: one per outcome.
func guideLines(state *AppState, w int, row func(label, word string, role, labelRole appui.Role) []string, paths func(...string)) []string {
	sk := &state.Skills
	var out []string
	label := "Guide skill"
	add := func(word string, role appui.Role, dirs []string) {
		if len(out) == 0 {
			out = append(out, "")
		}
		out = append(out, row(label, word, role, appui.RoleText)...)
		paths(dirs...)
		label = ""
	}
	if sk.HomeErr != nil {
		add("failed: cannot find the home directory: "+sk.HomeErr.Error(), appui.RoleFail, nil)
		return out
	}
	by := map[string][]skillRow{}
	for _, r := range sk.Rows {
		by[r.State] = append(by[r.State], r)
	}
	dirsOf := func(rows []skillRow) []string {
		var d []string
		for _, r := range rows {
			d = append(d, r.Dir)
		}
		return d
	}
	clients := func(rows []skillRow) int {
		n := 0
		for _, r := range rows {
			n += r.Clients
		}
		return n
	}
	if rows := by[skillWritten]; len(rows) > 0 {
		add("written for "+plural(clients(rows), "AI client", "AI clients"), appui.RoleText, dirsOf(rows))
	}
	if rows := by[skillWouldWrite]; len(rows) > 0 {
		add("would write for "+plural(clients(rows), "AI client", "AI clients"), appui.RoleText, dirsOf(rows))
	}
	if rows := by[skillSkipped]; len(rows) > 0 {
		word := "skipped: the folder holds a skill this program did not write"
		if len(rows) > 1 {
			word = fmt.Sprintf("skipped: %d folders hold a skill this program did not write", len(rows))
		}
		add(word, appui.RoleWarn, dirsOf(rows))
	}
	if rows := by[skillFailed]; len(rows) > 0 {
		word := fmt.Sprintf("failed: %v", rows[0].Err)
		if len(rows) > 1 {
			word += fmt.Sprintf(" (%d folders failed)", len(rows))
		}
		add(word, appui.RoleFail, dirsOf(rows))
	}
	return out
}
