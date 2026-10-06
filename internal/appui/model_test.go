package appui

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/sairaph/mcp-wizard/tui"

	"github.com/sairaph/creality-slicer-mcp/internal/projects"
)

var t0 = time.Date(2026, 10, 2, 14, 30, 0, 0, time.Local)

const printerA = "Creality K2 0.4 nozzle"

func item(i int) projects.ListItem {
	return projects.ListItem{ID: fmt.Sprintf("p%02d", i), Name: fmt.Sprintf("Project %02d", i), Printer: printerA, Objects: 2, Plates: 1}
}

func manyItems(n int) []projects.ListItem {
	out := make([]projects.ListItem, n)
	for i := range out {
		out[i] = item(i)
	}
	return out
}

const sampleID = "gauge-3fa9c1"

func sampleView() ProjectView {
	info := &projects.Info{
		ID: sampleID, Name: "Phase gauge", Revision: 7, Updated: t0,
		Printer: printerA, Process: "0.20mm Standard @Creality K2 0.4 nozzle", Overrides: 3,
		Plates: []projects.PlateInfo{{Index: 1, Objects: 8, BedType: "Textured PEI"}},
		Objects: []projects.ObjectInfo{
			{Name: "Gauge body", Plate: 1, Instances: 1, Size: [3]float64{34, 34, 12}},
			{Name: "Thread ring", Plate: 1, Instances: 4, Size: [3]float64{30, 30, 6}},
		},
		Filaments: []projects.FilamentInfo{{Index: 1, Type: "PLA", Colour: "#FFFFFF", Preset: "CR-PLA @Creality K2 0.4 nozzle", Spool: &projects.SpoolInfo{Slot: "A1"}}},
		LastSlice: &projects.SliceStamp{Time: t0, Revision: 7, Plates: 1},
		Warnings:  []projects.Warning{{Code: "stale", Message: "Plate 1 changed after its slice (revision 6, project at 7); slice it again before opening the preview."}},
	}
	return ProjectView{Info: info, Slices: []PlateSlice{{
		Plate: 1, Duration: "3h 12m 8s", Layers: 142, Grams: 45.3,
		GCodePath: `C:\Users\Alex\.creality-slicer-mcp\projects\gauge-3fa9c1\out\plate_1.gcode`,
	}}}
}

// rowOf is the line of a view that holds text.
func rowOf(t *testing.T, view, text string) string {
	t.Helper()
	for _, l := range strings.Split(view, "\n") {
		if strings.Contains(l, text) {
			return l
		}
	}
	t.Fatalf("no row with %q in:\n%s", text, view)
	return ""
}

// words is a row with its blanks collapsed.
func words(s string) string { return strings.Join(strings.Fields(s), " ") }

func quit(t *testing.T, cmd tea.Cmd) bool {
	t.Helper()
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

// --- menu ---

func TestMenuShowsTheEntriesAndTheSummary(t *testing.T) {
	h := newHarness(t)
	lines := h.lines()
	want := map[int]string{
		0: "creality-slicer-mcp  Menu  v0.3.5",
		1: "",
		2: "> Projects",
		3: "  Slicer status",
		4: "  Doctor",
		5: "  Configure AI clients",
		6: "  Quit",
		7: "",
		8: "  Creality Print 7.3.0 is ready. 42 projects.",
		9: "  AI clients: Claude Code, Codex CLI, OpenCode.",
	}
	for i, w := range want {
		if lines[i] != w {
			t.Errorf("row %d = %q, want %q", i+1, lines[i], w)
		}
	}
	if len(lines) != 24 || lines[23] != "\u2191\u2193 move \u00b7 enter select \u00b7 q quit" {
		t.Errorf("footer = %q on row %d", lines[len(lines)-1], len(lines))
	}
	if raw := rowOf(t, h.m.View(), "is ready"); !strings.Contains(raw, codeOK) {
		t.Errorf("the ready row is not in the ok colour: %q", raw)
	}
}

func TestMenuSummaryWhileLoadingAndWhenNotReady(t *testing.T) {
	fb := &fakeBackend{}
	m := New(Options{Backend: fb, Version: "0.3.5", Unicode: true})
	if cmd := m.Init(); cmd == nil {
		t.Fatal("Init starts no summary")
	}
	if row := rowOf(t, plain(m.View()), "Checking"); row != "  \u280b Checking Creality Print..." {
		t.Errorf("loading row = %q", row)
	}

	h := newHarness(t, func(fb *fakeBackend) {
		fb.summary = Summary{SlicerLine: "Creality Print was not found. Choose Slicer status."}
	})
	view := h.m.View()
	if row := rowOf(t, view, "was not found"); !strings.Contains(row, codeWarn) || plain(row) != "  Creality Print was not found. Choose Slicer status." {
		t.Errorf("not found row = %q", row)
	}
	if row := rowOf(t, view, "No AI client"); !strings.Contains(row, codeWarn) || !strings.Contains(plain(row), "Choose Configure AI clients.") {
		t.Errorf("no client row = %q", row)
	}

	h = newHarness(t, func(fb *fakeBackend) { fb.summaryErr = errors.New("boom") })
	if !strings.Contains(h.view(), "Could not read the summary: boom") {
		t.Errorf("a failed summary is not shown:\n%s", h.view())
	}
}

func TestMenuKeys(t *testing.T) {
	h := newHarness(t)
	h.press("down", "down")
	if !strings.Contains(h.view(), "> Doctor") {
		t.Errorf("down down:\n%s", h.view())
	}
	h.press("up")
	if !strings.Contains(h.view(), "> Slicer status") {
		t.Errorf("up:\n%s", h.view())
	}
	h.pressN("up", 5)
	if !strings.Contains(h.view(), "> Projects") {
		t.Errorf("up past the first entry:\n%s", h.view())
	}
	h.m.Update(tea.MouseMsg{Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	if !strings.Contains(h.view(), "> Slicer status") {
		t.Errorf("the mouse wheel does not move the cursor:\n%s", h.view())
	}
	for _, k := range []string{"q", "esc"} {
		if _, cmd := h.m.Update(keyMsg(k)); !quit(t, cmd) {
			t.Errorf("%s does not quit the menu", k)
		}
	}
	h = newHarness(t)
	h.pressN("down", 4)
	if _, cmd := h.m.Update(keyMsg("enter")); !quit(t, cmd) {
		t.Error("the Quit entry does not quit")
	}
}

func TestCtrlCOutsideALoadQuits(t *testing.T) {
	h := newHarness(t)
	_, cmd := h.m.Update(keyMsg("ctrl+c"))
	if !quit(t, cmd) || !h.m.Quit {
		t.Error("ctrl+c does not quit the app")
	}
}

// --- loading and cancelling ---

func TestLoadingShowsAMovingSpinnerAndHowToCancel(t *testing.T) {
	h := newHarness(t, func(fb *fakeBackend) { fb.items = manyItems(3) })
	h.m.ticking = false // let the load start the ticker
	_, cmd := h.m.Update(keyMsg("enter"))
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatalf("a load starts the load and the ticker: %T", cmd())
	}
	lines := h.lines()
	if lines[0] != "creality-slicer-mcp  Projects" || lines[2] != "  \u280b Reading the projects..." {
		t.Errorf("loading screen:\n%s", h.view())
	}
	if lines[23] != "(ctrl+c to cancel)" {
		t.Errorf("footer = %q", lines[23])
	}
	for _, l := range lines {
		if strings.Contains(l, "%") {
			t.Errorf("a scroll percentage on a loading screen: %q", l)
		}
	}
	// A tick moves the frame and asks for the next tick while the load runs.
	spin := tui.Spinner()()
	_, next := h.m.Update(spin)
	if next == nil {
		t.Error("the ticker stops while the load runs")
	}
	if h.lines()[2] != "  \u2819 Reading the projects..." {
		t.Errorf("the spinner did not move: %q", h.lines()[2])
	}
	// The load ends: the ticker stops with the next tick.
	h.drain(batch[0])
	if _, again := h.m.Update(spin); again != nil {
		t.Error("the ticker goes on after the load ended")
	}
}

func TestCtrlCCancelsALoadAndALateAnswerIsIgnored(t *testing.T) {
	h := newHarness(t, func(fb *fakeBackend) {
		fb.block = true
		fb.sawCancel = make(chan struct{})
		fb.items = manyItems(3)
	})
	_, first := h.m.Update(keyMsg("enter"))
	done := make(chan tea.Msg, 1)
	go func() { done <- first() }()
	for i := 0; h.fb.count("projects") == 0; i++ {
		if i > 400 {
			t.Fatal("the load never started")
		}
		time.Sleep(5 * time.Millisecond)
	}

	if _, cmd := h.m.Update(keyMsg("ctrl+c")); cmd != nil {
		t.Error("ctrl+c in a load returned a command: it must not quit the app")
	}
	select {
	case <-h.fb.sawCancel:
	case <-time.After(2 * time.Second):
		t.Fatal("the context of the load was not cancelled")
	}
	late := <-done
	lines := h.lines()
	if !strings.Contains(lines[len(lines)-2], "Cancelled.") || !strings.Contains(h.view(), "> Projects") {
		t.Errorf("after the cancel:\n%s", h.view())
	}

	// A new load is not answered by the old one.
	h.fb.mu.Lock()
	h.fb.block = false
	h.fb.mu.Unlock()
	_, second := h.m.Update(keyMsg("enter"))
	if _, cmd := h.m.Update(late); cmd != nil {
		t.Error("the answer of the cancelled load was taken")
	}
	if h.m.loading == nil {
		t.Fatal("the old answer ended the new load")
	}
	h.drain(second)
	if !strings.Contains(h.lines()[0], "Projects  3 projects") {
		t.Errorf("the new load was not shown:\n%s", h.view())
	}
}

func TestEscCancelsALoadToo(t *testing.T) {
	h := newHarness(t)
	h.m.Update(keyMsg("enter"))
	h.m.Update(keyMsg("esc"))
	if h.m.loading != nil || !strings.Contains(h.view(), "Cancelled.") {
		t.Errorf("esc did not cancel:\n%s", h.view())
	}
	if h.m.Quit {
		t.Error("esc in a load quit the app")
	}
}

// --- configure hand-off ---

func TestConfigureHandsOffToTheInstallerAndComesBackWithANotice(t *testing.T) {
	h := newHarness(t)
	h.pressN("down", 3)
	_, cmd := h.m.Update(keyMsg("enter"))
	if cmd == nil || h.configure != 1 {
		t.Fatalf("hand-off: command %v, ConfigureCmd called %d times", cmd != nil, h.configure)
	}
	for _, c := range []struct {
		code int
		role string
		text string
	}{
		{0, codeOK, "Setup finished."},
		{3, "", "Setup cancelled; nothing was changed."},
		{1, codeFail, "Setup did not finish (exit 1). Run Doctor."},
	} {
		before := h.fb.count("summary")
		_, next := h.m.Update(setupDoneMsg{code: c.code})
		h.drain(next)
		lines := h.lines()
		row := lines[len(lines)-2]
		if row != "  "+c.text {
			t.Errorf("exit %d: notice row = %q, want %q", c.code, row, c.text)
		}
		if raw := strings.Split(h.m.View(), "\n"); c.role != "" && !strings.Contains(raw[len(raw)-2], c.role) {
			t.Errorf("exit %d: notice colour: %q", c.code, raw[len(raw)-2])
		}
		if h.fb.count("summary") != before+1 {
			t.Errorf("exit %d: the summary was not read again", c.code)
		}
		if !strings.Contains(h.lines()[0], "Menu") {
			t.Errorf("exit %d: not back on the menu", c.code)
		}
	}
}

func TestConfigureWithoutAHandOffSaysSo(t *testing.T) {
	fb := &fakeBackend{}
	m := New(Options{Backend: fb, Unicode: true})
	m.ticking = true
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.menuCursor = 3
	if _, cmd := m.Update(keyMsg("enter")); cmd != nil {
		t.Error("a hand-off without a command")
	}
	if !strings.Contains(plain(m.View()), "Setup cannot be started from here") {
		t.Errorf("no notice:\n%s", plain(m.View()))
	}
}

// --- status ---

func TestStatusShowsHumanRows(t *testing.T) {
	h := newHarness(t, func(fb *fakeBackend) {
		fb.status.Exe = `C:\Program Files\Creality\CrealityPrint.exe`
		fb.status.DataDir = `C:\Users\Alex\AppData\Roaming\Creality\Creality Print\7.3`
		fb.status.Program = `C:\Users\Alex\AppData\Local\creality-slicer-mcp\bin\creality-slicer-mcp.exe`
	})
	h.resize(120, 36)
	h.open(1)
	view := h.view()
	if got := h.lines()[0]; got != "creality-slicer-mcp  Slicer status" {
		t.Errorf("header = %q", got)
	}
	for label, want := range map[string]string{
		"Creality Print":  "Creality Print 7.3.0 (build 6149), supported",
		"Window":          "Window not running",
		"Profiles":        "Profiles 26.09.29.08, from the install folder",
		"Projects folder": `Projects folder C:\Users\Alex\.creality-slicer-mcp\projects`,
		"Problems":        "Problems none",
	} {
		if got := words(rowOf(t, view, label)); got != want {
			t.Errorf("%s row = %q, want %q", label, got, want)
		}
	}
	if raw := rowOf(t, h.m.View(), "supported"); !strings.Contains(raw, codeOK) {
		t.Errorf("supported is not in the ok colour: %q", raw)
	}
	for _, bad := range []string{"catalog_drift", "dialect:", "installed:", "---"} {
		if strings.Contains(view, bad) {
			t.Errorf("the view holds %q, which is tool reply text", bad)
		}
	}
	if strings.Contains(view, "Creality exe") {
		t.Error("the path rows show before p")
	}
	if last := h.lines()[35]; last != "r check again \u00b7 p show paths \u00b7 esc back" {
		t.Errorf("footer = %q", last)
	}

	h.press("p")
	view = h.view()
	for label, want := range map[string]string{
		"Program":       `Program C:\Users\Alex\AppData\Local\creality-slicer-mcp\bin\creality-slicer-mcp.exe`,
		"Creality exe":  `Creality exe C:\Program Files\Creality\CrealityPrint.exe`,
		"Creality data": `Creality data C:\Users\Alex\AppData\Roaming\Creality\Creality Print\7.3`,
	} {
		if got := words(rowOf(t, view, label)); got != want {
			t.Errorf("%s row = %q, want %q", label, got, want)
		}
	}
	if last := h.lines()[35]; !strings.Contains(last, "p hide paths") {
		t.Errorf("footer after p = %q", last)
	}
	h.press("p")
	if strings.Contains(h.view(), "Creality exe") {
		t.Error("p again does not hide the paths")
	}
}

func TestStatusProblemsAreWarningsAndRefreshReloads(t *testing.T) {
	problem := "The installed Creality Print sets 3 settings that this server does not know yet (catalog 7.2.1, app 7.3.0): wipe_tower_x, wipe_tower_y, prime_tower_brim_width."
	h := newHarness(t, func(fb *fakeBackend) {
		fb.status.Problems = []string{problem, "The projects folder cannot be resolved."}
	})
	h.open(1)
	raw := h.m.View()
	if got := squash(raw); !strings.Contains(got, squash(problem)) || !strings.Contains(got, "Theprojectsfoldercannotberesolved.") {
		t.Errorf("problems not shown whole:\n%s", plain(raw))
	}
	for _, l := range strings.Split(raw, "\n") {
		if strings.Contains(plain(l), "wipe_tower_x") && !strings.Contains(l, codeWarn) {
			t.Errorf("a problem row is not in the warn colour: %q", l)
		}
	}
	h.press("r")
	if h.fb.count("status:refresh") != 1 || !strings.Contains(h.lines()[0], "Slicer status") {
		t.Errorf("r: calls %v", h.fb.calls)
	}
	h.press("esc")
	if !strings.Contains(h.lines()[0], "Menu") {
		t.Error("esc does not go back to the menu")
	}
}

func TestStatusWithoutCrealityPrint(t *testing.T) {
	h := newHarness(t, func(fb *fakeBackend) {
		fb.status = Status{Reason: "Creality Print was not found", Problems: []string{"Creality Print was not found. Presets need it."}}
	})
	h.open(1)
	row := rowOf(t, h.m.View(), "not found:")
	if !strings.Contains(row, codeFail) || words(plain(row)) != "Creality Print not found: Creality Print was not found" {
		t.Errorf("row = %q", plain(row))
	}
	if strings.Contains(h.view(), "Window") {
		t.Error("a window row without an install")
	}
}

// --- doctor ---

func doctorRows() []DoctorRow {
	return []DoctorRow{
		{"Executable", LevelOK, `C:\Users\Alex\AppData\Local\creality-slicer-mcp\bin\creality-slicer-mcp.exe`},
		{"AI clients", LevelFail, "no client is configured; run `creality-slicer-mcp install`"},
		{"PATH", LevelWarn, `C:\Users\Alex\AppData\Local\creality-slicer-mcp\bin is not on PATH`},
		{"Update", LevelOK, "up to date (0.3.5)"},
	}
}

func TestDoctorListsFailuresFirstInAlignedColumns(t *testing.T) {
	h := newHarness(t, func(fb *fakeBackend) { fb.doctor = doctorRows() })
	h.resize(120, 36)
	h.open(2)
	lines := h.lines()
	if lines[0] != "creality-slicer-mcp  Doctor  4 checks" || lines[2] != "  1 failed, 1 warning, 2 ok" {
		t.Errorf("header and summary:\n%s", h.view())
	}
	order := []struct{ name, word string }{{"AI clients", "failed"}, {"PATH", "warning"}, {"Executable", "ok"}, {"Update", "ok"}}
	for i, want := range order {
		row := lines[4+i]
		if !strings.HasPrefix(row, "  "+want.name) || !strings.HasPrefix(row[26:], want.word) {
			t.Errorf("row %d = %q, want %s at column 27 then %s", i+1, row, want.name, want.word)
		}
	}
	raw := strings.Split(h.m.View(), "\n")
	if !strings.Contains(raw[4], codeFail) || !strings.Contains(raw[5], codeWarn) || !strings.Contains(raw[6], codeOK) || !strings.Contains(raw[2], codeFail) {
		t.Errorf("colours:\n%q\n%q\n%q\n%q", raw[2], raw[4], raw[5], raw[6])
	}
	if last := lines[35]; !strings.Contains(last, "c configure AI clients") {
		t.Errorf("footer = %q", last)
	}
}

func TestDoctorSummaryAndTheConfigureKey(t *testing.T) {
	h := newHarness(t, func(fb *fakeBackend) {
		fb.doctor = []DoctorRow{{"AI clients", LevelOK, "Claude Code"}, {"PATH", LevelOK, "on PATH"}}
	})
	h.open(2)
	lines := h.lines()
	if lines[2] != "  All 2 checks passed" || !strings.Contains(h.m.View(), codeOK) {
		t.Errorf("summary:\n%s", h.view())
	}
	if strings.Contains(lines[23], "configure") {
		t.Errorf("c is offered although the clients are set up: %q", lines[23])
	}
	if _, cmd := h.m.Update(keyMsg("c")); cmd != nil || h.configure != 0 {
		t.Error("c handed off although the clients are set up")
	}

	h = newHarness(t, func(fb *fakeBackend) { fb.doctor = doctorRows() })
	h.open(2)
	if _, cmd := h.m.Update(keyMsg("c")); cmd == nil || h.configure != 1 {
		t.Errorf("c: command %v, ConfigureCmd called %d times", cmd != nil, h.configure)
	}
	h.press("r")
	if h.fb.count("doctor") != 2 {
		t.Errorf("r did not run the checks again: %v", h.fb.calls)
	}
	h.press("esc")
	if !strings.Contains(h.lines()[0], "Menu") {
		t.Error("esc does not go back to the menu")
	}
}

func TestDoctorFromTheHandOffReturnsToTheMenu(t *testing.T) {
	h := newHarness(t, func(fb *fakeBackend) { fb.doctor = doctorRows() })
	h.open(2)
	h.m.Update(keyMsg("c"))
	h.drain(func() tea.Msg { return setupDoneMsg{code: 0} })
	if !strings.Contains(h.lines()[0], "Menu") || !strings.Contains(h.view(), "Setup finished.") {
		t.Errorf("after the hand-off from Doctor:\n%s", h.view())
	}
}
