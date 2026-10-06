package main

// The screens of the install wizard. The library steps keep their logic and
// their keys; what they show is drawn here, through the same frame function as
// the app (appui.Render), so the wizard and the app look alike: the product
// name on the first row, the footer on the last, nothing cut.

import (
	"fmt"
	"strings"

	"github.com/sairaph/mcp-wizard/installer"
	"github.com/sairaph/mcp-wizard/tui"

	"github.com/sairaph/creality-slicer-mcp/internal/appui"
)

// Window assumed until the first size message reaches the flow.
const (
	wizardDefaultWidth  = 80
	wizardDefaultHeight = 24
)

// wizardUnicode says whether the spinner can use braille frames.
var wizardUnicode = appui.UnicodeTerminal

// wizardSize is the window the wizard is drawn in.
func wizardSize(state *AppState) (w, h int) {
	w, h = state.Width, state.Height
	if w <= 0 {
		w = wizardDefaultWidth
	}
	if h <= 0 {
		h = wizardDefaultHeight
	}
	return w, h
}

// wizardContext is the context of the header of a setup screen.
func wizardContext(state *AppState) string {
	if state.Harness.Scope.IsProject() {
		return "project"
	}
	return "AI clients"
}

// spinGlyph is the spinner frame of the wizard.
func spinGlyph(state *AppState) string {
	return appui.Paint(appui.RoleCursor, appui.SpinnerFrame(wizardUnicode(), state.Spinner.Frame))
}

var harnessHints = []tui.Hint{
	{Key: "↑↓", Label: "move"}, {Key: "space", Label: "toggle"}, {Key: "a", Label: "all/none"},
	{Key: "v", Label: "show all"}, {Key: "enter", Label: "continue"}, {Key: "q", Label: "cancel"},
}

// clientNameWidth is the width of the name column of the client list.
const clientNameWidth = 22

// View draws the client list: the question, one row per client, the note about
// the clients that start unticked, and the footer.
func (h harnessSelection) View(state *AppState) string {
	w, height := wizardSize(state)
	f := appui.Frame{Screen: "Setup", Context: wizardContext(state)}
	if state.harnessDetecting {
		f.Body = appui.WrapPrefixed("Looking for AI clients... (ctrl+c to cancel)", "  "+spinGlyph(state)+" ", "    ", w-2)
		f.Hints = []tui.Hint{{Key: "q", Label: "cancel"}}
		view, _ := appui.Render(w, height, f)
		return view
	}
	hs := &state.Harness
	var lines []string
	bodyWidth := appui.BodyWidth(w)
	add := func(role appui.Role, text string) {
		for _, l := range appui.Wrap(text, bodyWidth, 0) {
			lines = append(lines, "  "+appui.Paint(role, l))
		}
	}
	if hs.Scope.IsProject() {
		add(appui.RoleText, "Which AI clients should get this server in this project?")
		add(appui.RoleDim, "Project: "+hs.Scope.Dir)
	} else {
		add(appui.RoleText, "Which AI clients should be able to use this server?")
	}
	lines = append(lines, "")

	indices := installer.VisibleIndices(hs.Detections, hs.ShowAll)
	nameW := clientNameWidth
	for _, idx := range indices {
		nameW = max(nameW, appui.Width(hs.Detections[idx].Name))
	}
	cursorFrom, cursorTo := 0, 0
	if len(indices) == 0 {
		add(appui.RoleDim, "No AI clients detected.")
	}
	for _, idx := range indices {
		d := hs.Detections[idx]
		selectable := d.Selectable()
		cur := " "
		if idx == hs.Cursor {
			role := appui.RoleCursor
			if !selectable {
				role = appui.RoleDim
			}
			cur = appui.Paint(role, ">")
		}
		mark := appui.Paint(appui.RoleDim, "○")
		nameRole, statusRole := appui.RoleText, appui.RoleDim
		switch {
		case !selectable:
			mark = appui.Paint(appui.RoleDim, "·")
			nameRole, statusRole = appui.RoleDim, appui.RoleWarn
		case hs.Selected[d.ID]:
			mark = appui.Paint(appui.RoleOK, "●")
		}
		first := " " + cur + " " + mark + " " + appui.Paint(nameRole, appui.PadRight(d.Name, nameW)) + " "
		row := appui.WrapRole(d.StatusText(), first, appui.Spaces(appui.Width(first)), w-2, statusRole)
		if len(row) == 0 {
			row = []string{first}
		}
		if idx == hs.Cursor {
			cursorFrom, cursorTo = len(lines), len(lines)+len(row)-1
		}
		lines = append(lines, row...)
	}
	if hidden := len(hs.Detections) - len(indices); hidden > 0 && !hs.ShowAll {
		lines = append(lines, "")
		add(appui.RoleDim, fmt.Sprintf("press v to show %d client(s) that are not installed", hidden))
	} else if hs.ShowAll {
		lines = append(lines, "")
		add(appui.RoleDim, "press v to hide clients that are not installed")
	}
	if note := h.untickedNote(state); note != "" {
		lines = append(lines, "")
		add(appui.RoleText, note)
	}

	rows := appui.BodyRows(w, height, "", harnessHints)
	if cursorFrom < state.Scroll.Off {
		state.Scroll.Off = cursorFrom
	}
	if cursorTo >= state.Scroll.Off+rows {
		state.Scroll.Off = cursorTo - rows + 1
	}
	f.Body, _, _ = state.Scroll.Window(lines, rows)
	f.Hints = harnessHints
	view, _ := appui.Render(w, height, f)
	return view
}

// untickedNote says why some clients start unticked.
func (h harnessSelection) untickedNote(state *AppState) string {
	switch len(state.UntickedClients) {
	case 0:
		return ""
	case 1:
		return fmt.Sprintf("%s already has a %q entry that setup did not write (edited by hand, or another program's). "+
			"It starts unticked so the entry is kept; ticking it replaces it.", state.UntickedClients[0], h.name)
	}
	return fmt.Sprintf("%s already have a %q entry that setup did not write (edited by hand, or another program's). "+
		"They start unticked so the entries are kept; ticking one replaces it.", strings.Join(state.UntickedClients, ", "), h.name)
}

// registeringView is the screen while the clients are registered (or the plan
// is made) and the guide skill is written.
func registeringView(state *AppState, dryRun bool) string {
	w, height := wizardSize(state)
	n := len(selectedIDs(state.Harness.Selected))
	context, text, hints := "Registering", fmt.Sprintf("Registering with %s...", plural(n, "AI client", "AI clients")), []tui.Hint{{Key: "please", Label: "wait"}}
	if dryRun {
		context, text = "Planning", fmt.Sprintf("Planning changes for %s...", plural(n, "AI client", "AI clients"))
		if !state.Results.Done {
			hints = []tui.Hint{{Key: "q", Label: "cancel"}}
		}
	}
	view, _ := appui.Render(w, height, appui.Frame{
		Screen: "Setup", Context: context,
		Body:  appui.WrapPrefixed(text, "  "+spinGlyph(state)+" ", "    ", w-2),
		Hints: hints,
	})
	return view
}
