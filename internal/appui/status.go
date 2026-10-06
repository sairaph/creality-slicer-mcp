package appui

import (
	"context"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/sairaph/mcp-wizard/tui"
)

// LabelRows is a labelled row: the label dim in a column of labelW columns
// behind a margin of two, the value wrapped under itself in the colour of a
// role. w is the window width.
func LabelRows(label string, labelW int, value string, role Role, w int) []string {
	first := "  " + Paint(RoleDim, PadRight(label, labelW)) + "  "
	rest := Spaces(2 + labelW + 2)
	if value == "" {
		return []string{first}
	}
	return WrapRole(value, first, rest, w-2, role)
}

var scrollHint = tui.Hint{Key: "↑↓", Label: "scroll"}

// fit decides the footer of a scrolling screen and how many body lines it
// shows. The scroll hint is part of the footer when the content does not fit,
// or always when always is set.
func (m *Model) fit(total int, always bool, hints ...tui.Hint) (all []tui.Hint, rows int, overflow bool) {
	withScroll := append([]tui.Hint{scrollHint}, hints...)
	rows = BodyRows(m.w, m.h, m.notice, withScroll)
	if total <= rows && !always {
		return hints, BodyRows(m.w, m.h, m.notice, hints), false
	}
	return withScroll, rows, total > rows
}

// rangeText is "1-20 of 24".
func rangeText(from, to, total int) string { return fmt.Sprintf("%d-%d of %d", from, to, total) }

func (m *Model) statusHints() []tui.Hint {
	paths := "show paths"
	if m.showPaths {
		paths = "hide paths"
	}
	return []tui.Hint{{Key: "r", Label: "check again"}, {Key: "p", Label: paths}, {Key: "esc", Label: "back"}}
}

// statusLines are the rows of the slicer status.
func (m *Model) statusLines() []string {
	st := m.status
	var out []string
	row := func(label, value string, role Role) {
		out = append(out, LabelRows(label, 16, value, role, m.w)...)
	}
	build := ""
	if st.Build != "" {
		build = " (build " + st.Build + ")"
	}
	switch {
	case !st.Found:
		reason := st.Reason
		if reason == "" {
			reason = "Creality Print was not found"
		}
		row("Creality Print", "not found: "+AppText(reason), RoleFail)
	case !st.Supported:
		reason := st.Reason
		if reason == "" {
			reason = "only versions 7.2 and 7.3 are supported"
		}
		row("Creality Print", fmt.Sprintf("%s%s, not supported: %s", st.Version, build, AppText(reason)), RoleFail)
	default:
		row("Creality Print", fmt.Sprintf("%s%s, supported", st.Version, build), RoleOK)
	}
	if st.Found {
		if st.GUIRunning {
			row("Window", "open (it does not affect slicing)", RoleText)
		} else {
			row("Window", "not running", RoleText)
		}
	}
	if st.ProfileVersion != "" {
		from := "install folder"
		if st.ProfileSource == "data_dir" {
			from = "data folder"
		}
		row("Profiles", fmt.Sprintf("%s, from the %s", st.ProfileVersion, from), RoleText)
	}
	if st.ProjectsDir != "" {
		row("Projects folder", st.ProjectsDir, RoleText)
	}
	if m.showPaths {
		if st.Program != "" {
			row("Program", st.Program, RoleText)
		}
		if st.Exe != "" {
			row("Creality exe", st.Exe, RoleText)
		}
		if st.DataDir != "" {
			row("Creality data", st.DataDir, RoleText)
		}
	}
	out = append(out, "")
	if len(st.Problems) == 0 {
		row("Problems", "none", RoleOK)
	}
	for i, p := range st.Problems {
		label := ""
		if i == 0 {
			label = "Problems"
		}
		row(label, AppText(p), RoleWarn)
	}
	return out
}

func (m *Model) statusFrame() Frame {
	f := Frame{Screen: "Slicer status"}
	if m.status == nil {
		return f
	}
	lines := m.statusLines()
	hints, rows, overflow := m.fit(len(lines), false, m.statusHints()...)
	vis, from, to := m.statusScroll.Window(lines, rows)
	if overflow {
		f.Context = rangeText(from, to, len(lines))
	}
	f.Body, f.Hints = vis, hints
	return f
}

func (m *Model) statusKey(k tea.KeyMsg) tea.Cmd {
	switch k.String() {
	case "esc":
		m.pop()
	case "r":
		return m.loadStatus(true)
	case "p":
		m.showPaths = !m.showPaths
	default:
		if m.status != nil {
			_, rows, _ := m.fit(len(m.statusLines()), false, m.statusHints()...)
			m.statusScroll.Keys(k, len(m.statusLines()), rows)
		}
	}
	return nil
}

func (m *Model) loadStatus(refresh bool) tea.Cmd {
	label := "Checking Creality Print..."
	be := m.opts.Backend
	return m.begin(loadStatus, "Slicer status", label, func(ctx context.Context) (any, error) {
		return be.Status(ctx, refresh)
	})
}
