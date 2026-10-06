package appui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/sairaph/mcp-wizard/tui"

	"github.com/sairaph/creality-slicer-mcp/internal/projects"
)

// Project list columns right of the name.
const (
	colGap     = 2
	colObjects = 7
	colPlates  = 6
	colLast    = 22
	// listFixed is the width the columns and margins take: the name column is
	// the window width less this.
	listFixed = 47
	// narrowBelow is the window width under which the counts and the date move
	// under the name.
	narrowBelow = 64
)

const sliceTimeLayout = "2006-01-02 15:04"

// localTime is "YYYY-MM-DD HH:MM" in local time.
func localTime(t time.Time) string { return t.Local().Format(sliceTimeLayout) }

// listWidths returns the width of the name column and whether the window is
// too narrow for the other columns.
func (m *Model) listWidths() (nameW int, narrow bool) {
	if m.w < narrowBelow {
		return BodyWidth(m.w), true
	}
	return m.w - listFixed, false
}

func itemName(it projects.ListItem) string {
	if strings.TrimSpace(it.Name) != "" {
		return it.Name
	}
	return it.ID
}

// majorityPrinter is the printer most projects use ("" when none states one);
// a tie goes to the one met first.
func majorityPrinter(items []projects.ListItem) string {
	count := map[string]int{}
	var order []string
	for _, it := range items {
		if it.Printer == "" {
			continue
		}
		if count[it.Printer] == 0 {
			order = append(order, it.Printer)
		}
		count[it.Printer]++
	}
	best := ""
	for _, p := range order {
		if best == "" || count[p] > count[best] {
			best = p
		}
	}
	return best
}

// setProjects takes a fresh list. A list that is shown stays where it is, the
// cursor kept in range; from the menu it opens at the first row.
func (m *Model) setProjects(items []projects.ListItem) {
	m.list.items = items
	m.list.majority = majorityPrinter(items)
	if m.screen == scProjects {
		m.list.cursor = min(max(m.list.cursor, 0), max(len(items)-1, 0))
		return
	}
	m.list.cursor = 0
	m.push(scProjects)
}

// rowLines are the terminal rows of project i.
func (m *Model) rowLines(i int, cursor bool) []string {
	it := m.list.items[i]
	nameW, narrow := m.listWidths()
	marker := "  "
	if cursor {
		marker = Paint(RoleCursor, "> ")
	}
	names := Wrap(itemName(it), nameW, 0)
	if len(names) == 0 {
		names = []string{""}
	}
	var out []string
	if narrow {
		out = append(out, marker+names[0])
	} else {
		objects := PadLeft(fmt.Sprint(it.Objects), colObjects)
		plates := PadLeft(fmt.Sprint(it.Plates), colPlates)
		out = append(out, marker+PadRight(names[0], nameW)+Spaces(colGap)+objects+Spaces(colGap)+plates+Spaces(colGap)+lastSliceCell(it))
	}
	for _, n := range names[1:] {
		out = append(out, "  "+n)
	}
	if it.Printer != "" && it.Printer != m.list.majority {
		for _, l := range Wrap(it.Printer, nameW, 0) {
			out = append(out, "  "+Paint(RoleDim, l))
		}
	}
	if narrow {
		out = append(out, narrowSummary(it, nameW)...)
	}
	return out
}

// lastSliceCell is the "Last slice" column of a project.
func lastSliceCell(it projects.ListItem) string {
	if it.LastSlice == nil {
		return Paint(RoleDim, "never")
	}
	cell := localTime(it.LastSlice.Time)
	if it.LastSlice.Stale {
		cell += " " + Paint(RoleWarn, "stale")
	}
	return cell
}

// narrowSummary is the dim row that holds the counts and the date in a window
// too narrow for their columns.
func narrowSummary(it projects.ListItem, width int) []string {
	text := countText(it.Objects, "object", "objects") + ", " + countText(it.Plates, "plate", "plates") + ", "
	stale := it.LastSlice != nil && it.LastSlice.Stale
	if it.LastSlice == nil {
		text += "never sliced"
	} else {
		text += "sliced " + localTime(it.LastSlice.Time)
	}
	if stale {
		text += " stale"
	}
	lines := Wrap(text, width, 0)
	for i, l := range lines {
		if stale && i == len(lines)-1 && strings.HasSuffix(l, " stale") {
			lines[i] = "  " + Paint(RoleDim, strings.TrimSuffix(l, " stale")) + " " + Paint(RoleWarn, "stale")
			continue
		}
		lines[i] = "  " + Paint(RoleDim, l)
	}
	return lines
}

// listHead are the rows above the project rows: the common printer and the
// column names.
func (m *Model) listHead(level int) []string {
	if level >= 2 {
		return nil
	}
	var out []string
	if m.list.majority != "" && level == 0 {
		for _, l := range Wrap("Printer: "+m.list.majority+" (shown under a project when it differs)", BodyWidth(m.w), 0) {
			out = append(out, "  "+Paint(RoleDim, l))
		}
	}
	nameW, narrow := m.listWidths()
	if narrow {
		return append(out, "  "+Paint(RoleDim, "Name"))
	}
	hdr := PadRight("Name", nameW) + Spaces(colGap) + PadLeft("Objects", colObjects) + Spaces(colGap) + PadLeft("Plates", colPlates) + Spaces(colGap) + "Last slice"
	return append(out, "  "+Paint(RoleDim, hdr))
}

func (m *Model) listHints() []tui.Hint {
	if len(m.list.items) == 0 {
		return []tui.Hint{{Key: "r", Label: "reload"}, {Key: "esc", Label: "back"}}
	}
	return []tui.Hint{
		{Key: "↑↓", Label: "move"}, {Key: "enter", Label: "details"}, {Key: "pgup/pgdn", Label: "page"},
		{Key: "r", Label: "reload"}, {Key: "esc", Label: "back"},
	}
}

// pageStarts is the index of the first project of each page: pages hold whole
// rows, filled in order.
func (m *Model) pageStarts(head []string) (starts []int, rows, tallest int) {
	rows = max(BodyRows(m.w, m.h, m.notice, m.listHints())-len(head), 1)
	starts = []int{0}
	used := 0
	for i := range m.list.items {
		full := len(m.rowLines(i, false))
		tallest = max(tallest, full)
		h := min(full, rows)
		if used+h > rows && used > 0 {
			starts = append(starts, i)
			used = 0
		}
		used += h
	}
	return starts, rows, tallest
}

// listPlan is how the list is laid out in this window: the rows above the
// projects, where each page starts and how many rows a page has. In a window
// too small for the tallest project, the note about the common printer and
// then the column names make room.
func (m *Model) listPlan() (head []string, starts []int, rows int) {
	for level := 0; ; level++ {
		head = m.listHead(level)
		var tallest int
		starts, rows, tallest = m.pageStarts(head)
		if tallest <= rows || level == 2 {
			return head, starts, rows
		}
	}
}

// pageOf is the page that holds project i.
func pageOf(starts []int, i int) int {
	p := 0
	for k, s := range starts {
		if s <= i {
			p = k
		}
	}
	return p
}

func (m *Model) projectsFrame() Frame {
	f := Frame{Screen: "Projects", Hints: m.listHints()}
	total := len(m.list.items)
	f.Context = countText(total, "project", "projects")
	if total == 0 {
		f.Body = append(f.Body, "  No projects yet.", "")
		f.Body = append(f.Body, prefixAll("  ", Wrap("Ask your AI client to create one (create_project) or to open a 3MF (open_project). It shows up here.", BodyWidth(m.w), 0))...)
		return f
	}
	head, starts, rows := m.listPlan()
	m.list.cursor = min(max(m.list.cursor, 0), total-1)
	page := pageOf(starts, m.list.cursor)
	if len(starts) > 1 {
		f.Context += fmt.Sprintf("  page %d/%d", page+1, len(starts))
	}
	end := total
	if page+1 < len(starts) {
		end = starts[page+1]
	}
	f.Body = append(f.Body, head...)
	var body []string
	for i := starts[page]; i < end; i++ {
		body = append(body, m.rowLines(i, i == m.list.cursor)...)
	}
	f.Body = append(f.Body, body[:min(len(body), rows)]...)
	return f
}

// prefixAll puts prefix in front of every line.
func prefixAll(prefix string, lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = prefix + l
	}
	return out
}

func (m *Model) projectsKey(k tea.KeyMsg) tea.Cmd {
	total := len(m.list.items)
	switch k.String() {
	case "esc":
		m.pop()
	case "r":
		return m.loadProjects()
	case "enter":
		if total > 0 {
			return m.loadProject(m.list.items[m.list.cursor].ID)
		}
	case "up", "k":
		m.list.cursor = max(m.list.cursor-1, 0)
	case "down", "j":
		m.list.cursor = min(m.list.cursor+1, max(total-1, 0))
	case "home", "g":
		m.list.cursor = 0
	case "end", "G":
		m.list.cursor = max(total-1, 0)
	case "pgdown", "pgup":
		if total == 0 {
			return nil
		}
		_, starts, _ := m.listPlan()
		page := pageOf(starts, m.list.cursor)
		switch {
		case k.String() == "pgdown" && page+1 < len(starts):
			m.list.cursor = starts[page+1]
		case k.String() == "pgup" && page > 0:
			m.list.cursor = starts[page-1]
		case k.String() == "pgup":
			m.list.cursor = 0
		}
	}
	return nil
}

func (m *Model) loadProjects() tea.Cmd {
	be := m.opts.Backend
	return m.begin(loadProjects, "Projects", "Reading the projects...", func(ctx context.Context) (any, error) {
		return be.Projects(ctx)
	})
}

func (m *Model) loadProject(id string) tea.Cmd {
	be := m.opts.Backend
	return m.begin(loadProject, "Project", "Reading the project...", func(ctx context.Context) (any, error) {
		return be.Project(ctx, id)
	})
}

// removeFromList drops a project that was just deleted.
func (m *Model) removeFromList(id string) {
	items := m.list.items[:0:0]
	for _, it := range m.list.items {
		if it.ID != id {
			items = append(items, it)
		}
	}
	m.list.items = items
	m.list.majority = majorityPrinter(items)
	m.list.cursor = min(m.list.cursor, max(len(items)-1, 0))
}
