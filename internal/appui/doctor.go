package appui

import (
	"context"
	"fmt"
	"sort"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/sairaph/mcp-wizard/tui"
)

// clientsCheckName is the doctor check that says whether an AI client is set
// up; the screen offers to configure the clients when it is not ok.
const clientsCheckName = "AI clients"

// Doctor row columns: the outcome word, and the least width of the detail
// before a row stacks its detail under the name.
const (
	doctorNameMin   = 22
	doctorOutcomeW  = 7
	doctorMinDetail = 20
)

// sortDoctor puts failures first, then warnings, then the rest, each group in
// the order the checks ran.
func sortDoctor(rows []DoctorRow) []DoctorRow {
	out := append([]DoctorRow(nil), rows...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Level > out[j].Level })
	return out
}

func levelWord(l Level) string {
	switch l {
	case LevelFail:
		return "failed"
	case LevelWarn:
		return "warning"
	}
	return "ok"
}

func levelRole(l Level) Role {
	switch l {
	case LevelFail:
		return RoleFail
	case LevelWarn:
		return RoleWarn
	}
	return RoleOK
}

// doctorSummary is the first row: the counts, in the colour of the worst one.
func doctorSummary(rows []DoctorRow) (string, Role) {
	var failed, warned, ok int
	for _, r := range rows {
		switch r.Level {
		case LevelFail:
			failed++
		case LevelWarn:
			warned++
		default:
			ok++
		}
	}
	switch {
	case failed > 0:
		return fmt.Sprintf("%d failed, %d warning, %d ok", failed, warned, ok), RoleFail
	case warned > 0:
		return fmt.Sprintf("%d failed, %d warning, %d ok", failed, warned, ok), RoleWarn
	}
	return fmt.Sprintf("All %d checks passed", len(rows)), RoleOK
}

// doctorLines are the rows of the screen: the summary, then one row per check
// with its detail wrapped under the detail column.
func (m *Model) doctorLines() []string {
	sum, role := doctorSummary(m.doctor)
	out := []string{"  " + Paint(role, sum), ""}
	nameW := doctorNameMin
	for _, r := range m.doctor {
		nameW = max(nameW, Width(r.Name))
	}
	col := 2 + nameW + 2 + doctorOutcomeW + 2
	stacked := m.w-2-col < doctorMinDetail
	for _, r := range m.doctor {
		rr := levelRole(r.Level)
		nameRole := RoleText
		detailRole := RoleText
		if r.Level != LevelOK {
			nameRole, detailRole = rr, RoleDim
		}
		outcome := Paint(rr, PadRight(levelWord(r.Level), doctorOutcomeW))
		if stacked {
			out = append(out, "  "+Paint(nameRole, r.Name)+"  "+Paint(rr, levelWord(r.Level)))
			out = append(out, WrapRole(AppText(r.Detail), "    ", "    ", m.w-2, detailRole)...)
			continue
		}
		first := "  " + Paint(nameRole, PadRight(r.Name, nameW)) + "  " + outcome + "  "
		if r.Detail == "" {
			out = append(out, first)
			continue
		}
		out = append(out, WrapRole(AppText(r.Detail), first, Spaces(col), m.w-2, detailRole)...)
	}
	return out
}

func (m *Model) clientsNotOK() bool {
	for _, r := range m.doctor {
		if r.Name == clientsCheckName && r.Level != LevelOK {
			return true
		}
	}
	return false
}

func (m *Model) doctorHints() []tui.Hint {
	hints := []tui.Hint{{Key: "r", Label: "run again"}}
	if m.clientsNotOK() {
		hints = append(hints, tui.Hint{Key: "c", Label: "configure AI clients"})
	}
	return append(hints, tui.Hint{Key: "esc", Label: "back"})
}

func (m *Model) doctorFrame() Frame {
	lines := m.doctorLines()
	hints, rows, overflow := m.fit(len(lines), true, m.doctorHints()...)
	vis, from, to := m.doctorScroll.Window(lines, rows)
	f := Frame{Screen: "Doctor", Body: vis, Hints: hints, Context: countText(len(m.doctor), "check", "checks")}
	if overflow {
		f.Context = rangeText(from, to, len(lines))
	}
	return f
}

func (m *Model) doctorKey(k tea.KeyMsg) tea.Cmd {
	switch k.String() {
	case "esc":
		m.pop()
	case "r":
		return m.loadDoctor()
	case "c":
		if m.clientsNotOK() {
			return m.configure()
		}
	default:
		lines := m.doctorLines()
		_, rows, _ := m.fit(len(lines), true, m.doctorHints()...)
		m.doctorScroll.Keys(k, len(lines), rows)
	}
	return nil
}

func (m *Model) loadDoctor() tea.Cmd {
	be := m.opts.Backend
	return m.begin(loadDoctor, "Doctor", "Running health checks...", func(ctx context.Context) (any, error) {
		return be.Doctor(ctx), nil
	})
}
