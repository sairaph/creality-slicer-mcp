// Package appui is the interactive app: one frame function that draws every
// screen of the app and of the install wizard, and the screens themselves.
//
// Every view is exactly as many lines as the window is high, no line is wider
// than the window, the product name is on the first row and the footer on the
// last one. Text is wrapped or scrolled, never cut.
package appui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/sairaph/mcp-wizard/tui"

	"github.com/sairaph/creality-slicer-mcp/internal/domain"
)

// Role is a colour role of the theme (tui.DefaultTheme).
type Role int

const (
	// RoleText is the terminal default.
	RoleText Role = iota
	// RoleDim is for labels, hints, the footer, column headers.
	RoleDim
	// RoleOK, RoleWarn and RoleFail carry an outcome; the words say it too.
	RoleOK
	RoleWarn
	RoleFail
	// RoleTitle is the product name.
	RoleTitle
	// RoleCursor is the marker of the selected row.
	RoleCursor
	// RoleBold is for the name of the screen and of a project.
	RoleBold
)

// Minimum window the frame can be drawn in.
const (
	minWidth  = 30
	minHeight = 6
)

// Style returns the lipgloss style of a role.
func Style(r Role) lipgloss.Style {
	st := tui.DefaultTheme.Styles()
	switch r {
	case RoleDim:
		return st.Dim
	case RoleOK:
		return st.On
	case RoleWarn:
		return st.Hint
	case RoleFail:
		return st.Err
	case RoleTitle:
		return st.Title
	case RoleCursor:
		return st.Cursor
	case RoleBold:
		return lipgloss.NewStyle().Bold(true)
	}
	return lipgloss.NewStyle()
}

// Paint renders s in the style of a role.
func Paint(r Role, s string) string {
	if s == "" || r == RoleText {
		return s
	}
	return Style(r).Render(s)
}

// Frame is one screen.
type Frame struct {
	// Screen and Context follow the product name on the first row. The context
	// is dropped when the row would be wider than the window.
	Screen, Context string
	// Body holds styled lines, each already wrapped to BodyWidth(w) behind its
	// own left margin; the screen windowed it with Scroller.
	Body []string
	// Notice is a message above the footer, in the colour of NoticeRole.
	Notice     string
	NoticeRole Role
	// Hints make the footer.
	Hints []tui.Hint
}

// BodyWidth is the width of the text of a body line: the window less a margin
// of two columns on each side.
func BodyWidth(w int) int { return max(w-4, 1) }

// layout returns the footer rows, the notice rows and how many body rows fit.
func layout(w, h int, notice string, hints []tui.Hint) (foot, note []string, bodyRows int) {
	foot = footerRows(hints, w)
	note = noticeRows(notice, w)
	bodyRows = h - 2 - max(len(note), 1) - len(foot)
	return foot, note, bodyRows
}

// BodyRows is how many body lines a window of w x h shows with this notice and
// these hints.
func BodyRows(w, h int, notice string, hints []tui.Hint) int {
	if w < minWidth || h < minHeight {
		return 0
	}
	_, _, n := layout(w, h, notice, hints)
	return max(n, 0)
}

// Render draws the frame in a window of w x h. dropped is the number of body
// lines that did not fit: a screen that windows its body never causes any.
func Render(w, h int, f Frame) (view string, dropped int) {
	if h < 1 {
		return "", 0
	}
	if w < minWidth || h < minHeight {
		lines := make([]string, h)
		lines[0] = ansi.Truncate("Window too small.", max(w, 0), "")
		return strings.Join(lines, "\n"), 0
	}
	foot, note, rows := layout(w, h, f.Notice, f.Hints)
	if rows < 1 {
		// No room for the header, the whole notice and the footer.
		return tooSmall(w, h), 0
	}
	lines := make([]string, 0, h)
	lines = append(lines, header(w, f), "")
	body := f.Body
	if len(body) > rows {
		dropped = len(body) - rows
		body = body[:rows]
	}
	lines = append(lines, body...)
	for i := len(body); i < rows; i++ {
		lines = append(lines, "")
	}
	if len(note) == 0 {
		lines = append(lines, "")
	}
	for _, l := range note {
		lines = append(lines, "  "+Paint(f.NoticeRole, l))
	}
	lines = append(lines, foot...)
	for i, l := range lines {
		if Width(l) > w {
			lines[i] = ansi.Truncate(l, w, "")
		}
	}
	return strings.Join(lines, "\n"), dropped
}

// header is row 1: the product, the screen and, when it fits, the context.
func header(w int, f Frame) string {
	plain := domain.BinaryName
	styled := Paint(RoleTitle, domain.BinaryName)
	if f.Screen != "" {
		plain += "  " + f.Screen
		styled += "  " + Paint(RoleBold, f.Screen)
	}
	if f.Context != "" && Width(plain+"  "+f.Context) <= w {
		styled += "  " + Paint(RoleDim, f.Context)
	}
	return styled
}

// noticeRows wraps a notice to the body width.
func noticeRows(notice string, w int) []string {
	return Wrap(notice, BodyWidth(w), 0)
}

// footerRows joins the hints with a middle dot and wraps between hints when
// they do not fit one row.
func footerRows(hints []tui.Hint, w int) []string {
	const sep = " · "
	var rows []string
	cur := ""
	push := func() {
		if cur != "" {
			rows = append(rows, cur)
			cur = ""
		}
	}
	for _, hint := range hints {
		part := strings.TrimSpace(hint.Key + " " + hint.Label)
		switch {
		case Width(part) > w:
			push()
			rows = append(rows, Wrap(part, w, 0)...)
		case cur == "":
			cur = part
		case Width(cur)+Width(sep)+Width(part) <= w:
			cur += sep + part
		default:
			push()
			cur = part
		}
	}
	push()
	if len(rows) == 0 {
		return []string{""}
	}
	for i, r := range rows {
		rows[i] = Paint(RoleDim, r)
	}
	return rows
}

// tooSmall is the view of a window that cannot hold the frame: h lines with
// the message on the first.
func tooSmall(w, h int) string {
	lines := make([]string, h)
	lines[0] = ansi.Truncate("Window too small.", max(w, 0), "")
	return strings.Join(lines, "\n")
}
