package appui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/sairaph/mcp-wizard/tui"

	"github.com/sairaph/creality-slicer-mcp/internal/projects"
)

// detailName is the name of the project on the detail screen.
func (m *Model) detailName() string {
	if m.detail == nil || m.detail.Info == nil {
		return ""
	}
	if n := strings.TrimSpace(m.detail.Info.Name); n != "" {
		return n
	}
	return m.detail.Info.ID
}

func (m *Model) detailID() string {
	if m.detail == nil || m.detail.Info == nil {
		return ""
	}
	return m.detail.Info.ID
}

// launchedText is the notice of a started Creality Print window.
func launchedText(l Launched) string {
	what := "the 3D editor on this project"
	if l.Mode != projects.ViewProject {
		what = fmt.Sprintf("the preview of plate %d", l.Plate)
	}
	app := "Creality Print"
	if l.Version != "" {
		app += " " + l.Version
	}
	text := fmt.Sprintf("Started %s (process %d) with %s. Close that window yourself when you are done.", app, l.PID, what)
	if l.OtherWindow {
		text = "Another Creality Print window was already open and was not touched. " + text
	}
	return text
}

// byteSize is a size in bytes for a person.
func byteSize(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

// section is a section title of the detail screen.
func section(title string) string { return "  " + Paint(RoleBold, title) }

// sliceFor is the slice of a plate, if it has one.
func (pv *ProjectView) sliceFor(plate int) (PlateSlice, bool) {
	for _, s := range pv.Slices {
		if s.Plate == plate {
			return s, true
		}
	}
	return PlateSlice{}, false
}

// withStale adds the warning word "stale" to the last of lines, on a line of
// its own when it does not fit.
func withStale(lines []string, indent string, w int) []string {
	if len(lines) > 0 && Width(lines[len(lines)-1])+6 <= w-2 {
		lines[len(lines)-1] += " " + Paint(RoleWarn, "stale")
		return lines
	}
	return append(lines, indent+Paint(RoleWarn, "stale"))
}

// detailLines are the rows of the project screen.
func (m *Model) detailLines() []string {
	pv := m.detail
	info := pv.Info
	w := m.w
	const sliceIndent = 9
	var out []string
	out = append(out, WrapRole(m.detailName(), "  ", "  ", w-2, RoleBold)...)
	changed := localTime(info.Updated)
	meta := fmt.Sprintf("%s  revision %d  changed %s", info.ID, info.Revision, changed)
	out = append(out, WrapRole(meta, "  ", "  ", w-2, RoleDim)...)
	out = append(out, "")
	row := func(label, value string) {
		out = append(out, LabelRows(label, 10, value, RoleText, w)...)
	}
	row("Printer", info.Printer)
	row("Process", info.Process)
	row("Settings", fmt.Sprintf("%d changed from the presets", info.Overrides))

	out = append(out, "", section("Plates"))
	for _, p := range info.Plates {
		text := countText(p.Objects, "object", "objects")
		if p.BedType != "" {
			text += ", bed " + p.BedType
		}
		out = append(out, WrapPrefixed(text, "    "+PadRight(fmt.Sprint(p.Index), 5), Spaces(sliceIndent), w-2)...)
		indent := Spaces(sliceIndent)
		s, ok := pv.sliceFor(p.Index)
		if !ok {
			out = append(out, indent+Paint(RoleDim, "not sliced yet"))
			continue
		}
		var parts []string
		stamp := "sliced"
		if info.LastSlice != nil {
			stamp += " " + localTime(info.LastSlice.Time)
		}
		parts = append(parts, stamp)
		if s.Duration != "" {
			parts = append(parts, s.Duration)
		}
		if s.Grams > 0 {
			parts = append(parts, fmt.Sprintf("%.1f g", s.Grams))
		}
		if s.Layers > 0 {
			parts = append(parts, fmt.Sprintf("%d layers", s.Layers))
		}
		lines := WrapPrefixed(strings.Join(parts, ", "), indent, indent, w-2)
		if s.Stale {
			lines = withStale(lines, indent, w)
		}
		out = append(out, lines...)
		if s.GCodePath != "" {
			out = append(out, WrapPrefixed(s.GCodePath, indent+Paint(RoleDim, "G-code")+"  ", indent, w-2)...)
		}
	}

	out = append(out, "", section("Objects"))
	nameCol := 0
	for _, o := range info.Objects {
		nameCol = max(nameCol, min(Width(o.Name), 24))
	}
	for _, o := range info.Objects {
		detail := fmt.Sprintf("plate %d, %.1f x %.1f x %.1f mm", o.Plate, o.Size[0], o.Size[1], o.Size[2])
		if o.Instances > 1 {
			detail += fmt.Sprintf(", %d instances", o.Instances)
		}
		hang := Spaces(4 + nameCol + 2)
		if Width(o.Name) > nameCol {
			out = append(out, WrapRole(o.Name, "    ", "    ", w-2, RoleText)...)
			out = append(out, WrapPrefixed(detail, hang, hang, w-2)...)
			continue
		}
		out = append(out, WrapPrefixed(detail, "    "+PadRight(o.Name, nameCol)+"  ", hang, w-2)...)
	}

	out = append(out, "", section("Filaments"))
	for _, f := range info.Filaments {
		first := "    " + PadRight(fmt.Sprint(f.Index), 5)
		for _, part := range []string{f.Type, f.Colour} {
			if part != "" {
				first += part + "  "
			}
		}
		text := f.Preset
		if f.Spool != nil && f.Spool.Slot != "" {
			text += ", spool " + f.Spool.Slot
		}
		out = append(out, WrapPrefixed(text, first, Spaces(sliceIndent), w-2)...)
	}

	var warnings []string
	for _, wn := range info.Warnings {
		warnings = append(warnings, wn.Message)
	}
	if n := len(info.Drift); n > 0 {
		warnings = append(warnings, fmt.Sprintf("%s the presets set are not known to this server yet.", countText(n, "setting", "settings")))
	}
	if len(warnings) > 0 {
		out = append(out, "", section("Warnings"))
		for _, msg := range warnings {
			out = append(out, WrapRole(msg, "    "+Paint(RoleWarn, "!")+"    ", Spaces(sliceIndent), w-2, RoleWarn)...)
		}
	}
	return out
}

func (m *Model) projectHints() []tui.Hint {
	return []tui.Hint{{Key: "o", Label: "open"}, {Key: "x", Label: "export"}, {Key: "d", Label: "delete"}, {Key: "esc", Label: "back"}}
}

func (m *Model) projectFrame() Frame {
	lines := m.detailLines()
	hints, rows, overflow := m.fit(len(lines), true, m.projectHints()...)
	vis, from, to := m.detailScroll.Window(lines, rows)
	f := Frame{Screen: "Project", Body: vis, Hints: hints}
	if overflow {
		f.Context = rangeText(from, to, len(lines))
	}
	return f
}

func (m *Model) projectKey(k tea.KeyMsg) tea.Cmd {
	be := m.opts.Backend
	id := m.detailID()
	switch k.String() {
	case "esc":
		m.pop()
	case "o":
		return m.begin(loadLaunch, "Project", "Starting Creality Print...", func(ctx context.Context) (any, error) {
			return be.Launch(ctx, id)
		})
	case "x":
		m.export.SetValue(filepath.Join(m.cwd(), id+".3mf"))
		m.export.CursorEnd()
		m.exportReplace = false
		m.push(scExport)
	case "d":
		m.confirm = 0
		m.push(scDelete)
	default:
		lines := m.detailLines()
		_, rows, _ := m.fit(len(lines), true, m.projectHints()...)
		m.detailScroll.Keys(k, len(lines), rows)
	}
	return nil
}

// --- export ---

// inputLines draw the text field: the value wrapped, never cut, with the
// cursor shown in it.
func (m *Model) inputLines(first, rest string) (lines []string, cursorLine int) {
	runes := []rune(m.export.Value())
	pos := m.export.Position()
	avail := max(m.w-2-Width(first)-1, 4)
	ranges := wrapRanges(runes, avail)
	cursor := lipgloss.NewStyle().Reverse(true)
	var out []string
	for i, r := range ranges {
		var b strings.Builder
		if pos >= r[0] && pos < r[1] {
			cursorLine = i
		}
		for j := r[0]; j < r[1]; j++ {
			if j == pos {
				b.WriteString(cursor.Render(string(runes[j])))
			} else {
				b.WriteRune(runes[j])
			}
		}
		if i == len(ranges)-1 && pos >= len(runes) {
			b.WriteString(Paint(RoleCursor, "_"))
			cursorLine = i
		}
		prefix := rest
		if i == 0 {
			prefix = first
		}
		out = append(out, prefix+b.String())
	}
	return out, cursorLine
}

func (m *Model) exportFrame() Frame {
	f := Frame{Screen: "Export project"}
	w := m.w
	body := WrapRole(m.detailName(), "  ", "  ", w-2, RoleBold)
	body = append(body, "")
	keep := 0
	if m.exportReplace {
		body = append(body, WrapRole("That file exists. Replace it?", "  ", "  ", w-2, RoleText)...)
		body = append(body, WrapRole(strings.TrimSpace(m.export.Value()), "  ", "  ", w-2, RoleDim)...)
		body = append(body, "")
		keep = len(body) + m.confirm
		body = append(body, choiceLines(m.confirm, "No, keep it", "Yes, replace it")...)
		f.Hints = []tui.Hint{{Key: "↑↓", Label: "move"}, {Key: "enter", Label: "select"}, {Key: "y", Label: "replace"}, {Key: "n/esc", Label: "back"}}
	} else {
		body = append(body, prefixAll("  ", Wrap("Save a copy of the project as a 3MF file that Creality Print opens. The stored project is not changed.", BodyWidth(w), 0))...)
		body = append(body, "")
		input, cursorLine := m.inputLines("  "+Paint(RoleDim, "File")+"  ", Spaces(8))
		keep = len(body) + cursorLine
		body = append(body, input...)
		f.Hints = []tui.Hint{{Key: "enter", Label: "export"}, {Key: "esc", Label: "cancel"}}
	}
	// A window too small for the whole screen shows its end, where the input
	// or the options are.
	f.Body = windowAround(body, BodyRows(w, m.h, m.notice, f.Hints), keep, true)
	return f
}

// choiceLines are the options of a confirm, the one under the cursor marked.
func choiceLines(cursor int, options ...string) []string {
	out := make([]string, len(options))
	for i, o := range options {
		marker := "  "
		if i == cursor {
			marker = Paint(RoleCursor, "> ")
		}
		out[i] = marker + o
	}
	return out
}

func (m *Model) startExport(overwrite bool) tea.Cmd {
	be := m.opts.Backend
	id, path := m.detailID(), strings.TrimSpace(m.export.Value())
	return m.begin(loadExport, "Export project", "Exporting the project...", func(ctx context.Context) (any, error) {
		return be.Export(ctx, id, path, overwrite)
	})
}

func (m *Model) exportKey(k tea.KeyMsg) tea.Cmd {
	if m.exportReplace {
		switch k.String() {
		case "up", "k", "down", "j":
			m.confirm = 1 - m.confirm
		case "y":
			return m.startExport(true)
		case "n", "esc":
			m.exportReplace = false
		case "enter":
			if m.confirm == 1 {
				return m.startExport(true)
			}
			m.exportReplace = false
		}
		return nil
	}
	switch k.String() {
	case "esc":
		m.pop()
	case "enter":
		if strings.TrimSpace(m.export.Value()) == "" {
			m.say(RoleFail, "Type the name of the file to save.")
			return nil
		}
		return m.startExport(false)
	default:
		var cmd tea.Cmd
		m.export, cmd = m.export.Update(k)
		return cmd
	}
	return nil
}

// --- delete ---

func (m *Model) deleteFrame() Frame {
	f := Frame{Screen: "Delete project"}
	body := WrapRole(fmt.Sprintf("Delete %q?", m.detailName()), "  ", "  ", m.w-2, RoleBold)
	body = append(body, "")
	body = append(body, prefixAll("  ", Wrap("This removes the project and the G-code of its slices from this server's store. Files you exported are not touched. This cannot be undone.", BodyWidth(m.w), 0))...)
	body = append(body, "")
	keep := len(body) + m.confirm
	body = append(body, choiceLines(m.confirm, "No, keep it", "Yes, delete it")...)
	f.Hints = []tui.Hint{{Key: "↑↓", Label: "move"}, {Key: "enter", Label: "select"}, {Key: "y", Label: "delete"}, {Key: "n/esc", Label: "keep"}}
	f.Body = windowAround(body, BodyRows(m.w, m.h, m.notice, f.Hints), keep, true)
	return f
}

func (m *Model) startDelete() tea.Cmd {
	be := m.opts.Backend
	id := m.detailID()
	return m.begin(loadDelete, "Delete project", "Deleting the project...", func(ctx context.Context) (any, error) {
		return nil, be.Delete(ctx, id)
	})
}

func (m *Model) deleteKey(k tea.KeyMsg) tea.Cmd {
	switch k.String() {
	case "up", "k", "down", "j":
		m.confirm = 1 - m.confirm
	case "y":
		return m.startDelete()
	case "n", "esc":
		m.pop()
	case "enter":
		if m.confirm == 1 {
			return m.startDelete()
		}
		m.pop()
	}
	return nil
}
