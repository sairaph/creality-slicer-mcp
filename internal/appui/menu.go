package appui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/sairaph/mcp-wizard/tui"
)

// The menu entries, in order.
var menuEntries = []string{"Projects", "Slicer status", "Doctor", "Configure AI clients", "Quit"}

// MenuEntries lists the menu entries in the order the menu shows them.
func MenuEntries() []string { return append([]string(nil), menuEntries...) }

// versionText is "v0.3.5" for a release and "dev" for a development build.
func versionText(v string) string {
	if v != "" && v[0] >= '0' && v[0] <= '9' {
		return "v" + v
	}
	return v
}

// menuHints are the footer of the menu; more is added when the window is too
// small for the whole body.
func menuHints(more bool) []tui.Hint {
	hints := []tui.Hint{{Key: "↑↓", Label: "move"}, {Key: "enter", Label: "select"}, {Key: "q", Label: "quit"}}
	if more {
		hints = append([]tui.Hint{{Key: "pgup/pgdn", Label: "more"}}, hints...)
	}
	return hints
}

// menuLayout is the whole body of the menu, its footer and how many rows of it
// are shown.
func (m *Model) menuLayout() (body []string, hints []tui.Hint, rows int) {
	for i, name := range menuEntries {
		marker := "  "
		if i == m.menuCursor {
			marker = Paint(RoleCursor, "> ")
		}
		body = append(body, marker+name)
	}
	body = append(body, "")
	body = append(body, m.summaryLines()...)
	rows = BodyRows(m.w, m.h, m.notice, menuHints(true))
	if len(body) <= rows {
		return body, menuHints(false), BodyRows(m.w, m.h, m.notice, menuHints(false))
	}
	return body, menuHints(true), rows
}

func (m *Model) menuFrame() Frame {
	body, hints, rows := m.menuLayout()
	m.menuScroll.Clamp(len(body), rows)
	// The cursor stays in view in a window too small for the whole body.
	if m.menuCursor < m.menuScroll.Off {
		m.menuScroll.Off = m.menuCursor
	}
	if m.menuCursor >= m.menuScroll.Off+rows {
		m.menuScroll.Off = m.menuCursor - rows + 1
	}
	vis, _, _ := m.menuScroll.Window(body, rows)
	return Frame{Screen: "Menu", Context: versionText(m.opts.Version), Body: vis, Hints: hints}
}

// summaryLines are the two rows under the menu.
func (m *Model) summaryLines() []string {
	width := BodyWidth(m.w)
	var out []string
	add := func(role Role, text string) {
		for _, l := range Wrap(text, width, 0) {
			out = append(out, "  "+Paint(role, l))
		}
	}
	switch {
	case m.sum.loading:
		out = append(out, WrapPrefixed("Checking Creality Print...", "  "+m.spinner()+" ", "    ", m.w-2)...)
	case m.sum.err != nil:
		add(RoleWarn, "Could not read the summary: "+errText(m.sum.err))
	case m.sum.val != nil:
		s := m.sum.val
		if s.SlicerOK {
			add(RoleOK, fmt.Sprintf("%s %s.", s.SlicerLine, countText(s.Projects, "project", "projects")))
		} else {
			add(RoleWarn, s.SlicerLine)
		}
		if len(s.Clients) > 0 {
			add(RoleText, "AI clients: "+strings.Join(s.Clients, ", ")+".")
		} else {
			add(RoleWarn, "No AI client is set up yet. Choose Configure AI clients.")
		}
	}
	return out
}

// countText is "1 project" or "42 projects".
func countText(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func (m *Model) menuKey(k tea.KeyMsg) tea.Cmd {
	switch k.String() {
	case "up", "k":
		m.menuCursor = max(m.menuCursor-1, 0)
	case "down", "j":
		m.menuCursor = min(m.menuCursor+1, len(menuEntries)-1)
	case "home", "g":
		m.menuCursor = 0
	case "end", "G":
		m.menuCursor = len(menuEntries) - 1
	case "pgup", "pgdown":
		body, _, rows := m.menuLayout()
		step := max(rows-1, 1)
		if k.String() == "pgup" {
			step = -step
		}
		m.menuScroll.Off += step
		m.menuScroll.Clamp(len(body), rows)
	case "q", "esc":
		m.Quit = true
		return tea.Quit
	case "enter":
		switch menuEntries[m.menuCursor] {
		case "Projects":
			return m.loadProjects()
		case "Slicer status":
			return m.loadStatus(false)
		case "Doctor":
			return m.loadDoctor()
		case "Configure AI clients":
			return m.configure()
		default:
			m.Quit = true
			return tea.Quit
		}
	}
	return nil
}
