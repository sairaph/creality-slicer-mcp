package appui

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sairaph/creality-slicer-mcp/internal/projects"
)

func withItems(n int) func(*fakeBackend) {
	return func(fb *fakeBackend) { fb.items = manyItems(n); fb.view = sampleView() }
}

// --- list ---

func TestProjectListPagesAndCursor(t *testing.T) {
	h := newHarness(t, withItems(50))
	h.open(0)
	lines := h.lines()
	if lines[0] != "creality-slicer-mcp  Projects  50 projects  page 1/3" {
		t.Errorf("header = %q", lines[0])
	}
	if got := words(lines[2]); got != "Printer: Creality K2 0.4 nozzle (shown under a project when it differs)" {
		t.Errorf("printer note = %q", got)
	}
	if got := words(lines[3]); got != "Name Objects Plates Last slice" {
		t.Errorf("column names = %q", got)
	}
	if !strings.HasPrefix(lines[4], "> Project 00") || !strings.HasPrefix(lines[5], "  Project 01") {
		t.Errorf("rows:\n%s", h.view())
	}
	if got := words(lines[4]); got != "> Project 00 2 1 never" {
		t.Errorf("row = %q", got)
	}
	if lines[23] != "\u2191\u2193 move \u00b7 enter details \u00b7 pgup/pgdn page \u00b7 r reload \u00b7 esc back" {
		t.Errorf("footer = %q", lines[23])
	}

	h.pressN("down", 17)
	if !strings.Contains(h.lines()[0], "page 1/3") || !strings.Contains(h.view(), "> Project 17") {
		t.Errorf("last row of page 1:\n%s", h.view())
	}
	h.press("down")
	if !strings.Contains(h.lines()[0], "page 2/3") || !strings.Contains(h.view(), "> Project 18") {
		t.Errorf("down past the page:\n%s", h.view())
	}
	h.press("home")
	h.press("pgdown")
	if !strings.Contains(h.view(), "> Project 18") {
		t.Errorf("pgdown from the first row:\n%s", h.view())
	}
	h.press("pgdown")
	if !strings.Contains(h.lines()[0], "page 3/3") || !strings.Contains(h.view(), "> Project 36") {
		t.Errorf("second pgdown:\n%s", h.view())
	}
	h.press("pgdown")
	if !strings.Contains(h.view(), "> Project 36") {
		t.Errorf("pgdown on the last page moved:\n%s", h.view())
	}
	h.press("pgup")
	if !strings.Contains(h.view(), "> Project 18") {
		t.Errorf("pgup:\n%s", h.view())
	}
	h.press("pgup")
	h.press("pgup")
	if !strings.Contains(h.view(), "> Project 00") {
		t.Errorf("pgup on the first page:\n%s", h.view())
	}
	h.press("end")
	if !strings.Contains(h.view(), "> Project 49") || !strings.Contains(h.lines()[0], "page 3/3") {
		t.Errorf("end:\n%s", h.view())
	}
	h.press("home")
	if !strings.Contains(h.view(), "> Project 00") {
		t.Errorf("home:\n%s", h.view())
	}
}

func TestProjectListEnterOpensTheProjectUnderTheCursorAndEscComesBack(t *testing.T) {
	h := newHarness(t, withItems(50))
	h.open(0)
	h.pressN("down", 3)
	h.press("enter")
	if h.fb.count("project:p03") != 1 || h.screen() != "Project" {
		t.Errorf("enter: calls %v\n%s", h.fb.calls, h.view())
	}
	h.press("esc")
	if !strings.Contains(h.view(), "> Project 03") || !strings.Contains(h.lines()[0], "Projects") {
		t.Errorf("esc did not return to the same row:\n%s", h.view())
	}
}

func TestProjectListShowsTheNameOnceAndThePrinterOnlyWhenItDiffers(t *testing.T) {
	h := newHarness(t, func(fb *fakeBackend) {
		fb.items = manyItems(5)
		fb.items[2].Printer = "Creality K2 0.6 nozzle"
		fb.items[3].Printer = ""
		fb.view = sampleView()
	})
	h.open(0)
	view := h.view()
	if n := strings.Count(view, "Creality K2 0.6 nozzle"); n != 1 {
		t.Errorf("the other printer is shown %d times:\n%s", n, view)
	}
	if n := strings.Count(view, printerA); n != 1 {
		t.Errorf("the common printer is shown %d times, once in the note:\n%s", n, view)
	}
	lines := strings.Split(view, "\n")
	for i, l := range lines {
		if strings.Contains(l, "Project 02") {
			if !strings.Contains(lines[i+1], "Creality K2 0.6 nozzle") {
				t.Errorf("the printer is not under its project: %q", lines[i+1])
			}
		}
	}
	for _, it := range h.fb.items {
		if n := strings.Count(view, it.Name); n != 1 {
			t.Errorf("%s appears %d times", it.Name, n)
		}
		if strings.Contains(view, it.ID) {
			t.Errorf("the id %s is in the list", it.ID)
		}
	}
}

func TestProjectListLastSliceColumn(t *testing.T) {
	h := newHarness(t, func(fb *fakeBackend) {
		fb.items = manyItems(3)
		fb.items[0].LastSlice = &projects.SliceStamp{Time: t0}
		fb.items[1].LastSlice = &projects.SliceStamp{Time: t0, Stale: true}
		fb.view = sampleView()
	})
	h.resize(120, 36)
	h.open(0)
	raw := h.m.View()
	fresh, stale, never := rowOf(t, raw, "Project 00"), rowOf(t, raw, "Project 01"), rowOf(t, raw, "Project 02")
	if got := words(plain(fresh)); got != "> Project 00 2 1 2026-10-02 14:30" {
		t.Errorf("fresh row = %q", got)
	}
	if got := words(plain(stale)); got != "Project 01 2 1 2026-10-02 14:30 stale" || !strings.Contains(stale, codeWarn) {
		t.Errorf("stale row = %q", stale)
	}
	if got := words(plain(never)); got != "Project 02 2 1 never" {
		t.Errorf("never row = %q", got)
	}
}

func TestProjectListInANarrowWindowKeepsEveryColumn(t *testing.T) {
	h := newHarness(t, func(fb *fakeBackend) {
		fb.items = manyItems(3)
		fb.items[0].LastSlice = &projects.SliceStamp{Time: t0}
		fb.items[1].LastSlice = &projects.SliceStamp{Time: t0, Stale: true}
		fb.view = sampleView()
	})
	h.resize(60, 20)
	h.open(0)
	view := h.view()
	for _, want := range []string{"2 objects, 1 plate, sliced 2026-10-02 14:30", "sliced 2026-10-02 14:30 stale", "2 objects, 1 plate, never sliced"} {
		if !strings.Contains(view, want) {
			t.Errorf("the narrow list lacks %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "Objects") || strings.Contains(view, "Last slice") {
		t.Errorf("column names in a window too narrow for the columns:\n%s", view)
	}
}

func TestEmptyProjectList(t *testing.T) {
	h := newHarness(t)
	h.open(0)
	lines := h.lines()
	if lines[0] != "creality-slicer-mcp  Projects  0 projects" || lines[2] != "  No projects yet." {
		t.Errorf("empty list:\n%s", h.view())
	}
	if got := squash(h.view()); !strings.Contains(got, squash("Ask your AI client to create or open a project. It shows up here.")) {
		t.Errorf("no explanation:\n%s", h.view())
	}
	if lines[23] != "r reload \u00b7 esc back" {
		t.Errorf("footer = %q", lines[23])
	}
	h.press("enter")
	if h.fb.count("project:") != 0 || len(h.fb.calls) != 2 { // summary, projects
		t.Errorf("enter on an empty list did something: %v", h.fb.calls)
	}
	h.press("r")
	if h.fb.count("projects") != 2 {
		t.Errorf("r did not reload: %v", h.fb.calls)
	}
}

func TestProjectListSurvivesResizes(t *testing.T) {
	h := newHarness(t, withItems(50))
	h.resize(120, 36)
	h.open(0)
	h.pressN("down", 9)
	first := h.m.View()
	var pages []string
	for _, sz := range []struct{ w, h int }{{80, 24}, {60, 20}, {40, 12}, {120, 36}} {
		h.resize(sz.w, sz.h)
		view := h.m.View()
		if !strings.Contains(plain(view), "> Project 09") {
			t.Errorf("%dx%d: the cursor row is not on the screen:\n%s", sz.w, sz.h, plain(view))
		}
		checkFrame(t, view, sz.w, sz.h, "\u2191\u2193 move")
		header := plain(strings.Split(view, "\n")[0])
		if i := strings.Index(header, "page"); i >= 0 {
			pages = append(pages, header[i:])
		} else {
			pages = append(pages, header)
		}
	}
	if pages[0] != "page 1/3" || pages[1] != "page 2/9" {
		t.Errorf("page counters = %v, want page 1/3 and page 2/9", pages)
	}
	if h.m.View() != first {
		t.Errorf("the view at 120x36 changed after resizes:\n%s\n---\n%s", plain(first), h.view())
	}
}

// --- detail ---

func TestProjectDetailShowsTheProject(t *testing.T) {
	h := newHarness(t, withItems(3))
	h.resize(120, 36)
	h.open(0)
	h.press("enter")
	view := h.view()
	if h.lines()[0] != "creality-slicer-mcp  Project" {
		t.Errorf("header = %q", h.lines()[0])
	}
	for _, want := range []string{
		"Phase gauge",
		sampleID + " revision 7 changed 2026-10-02 14:30",
		"Printer " + printerA,
		"Process 0.20mm Standard @Creality K2 0.4 nozzle",
		"Settings 3 changed from the presets",
		"1 8 objects, bed Textured PEI",
		"sliced 2026-10-02 14:30, 3h 12m 8s, 45.3 g, 142 layers",
		`G-code C:\Users\Alex\.creality-slicer-mcp\projects\gauge-3fa9c1\out\plate_1.gcode`,
		"Gauge body plate 1, 34.0 x 34.0 x 12.0 mm",
		"Thread ring plate 1, 30.0 x 30.0 x 6.0 mm, 4 instances",
		"1 PLA #FFFFFF CR-PLA @Creality K2 0.4 nozzle, spool A1",
		"! Plate 1 changed after its slice",
	} {
		if !strings.Contains(strings.Join(strings.Fields(view), " "), want) {
			t.Errorf("the project screen lacks %q:\n%s", want, view)
		}
	}
	for _, section := range []string{"Plates", "Objects", "Filaments", "Warnings"} {
		if row := rowOf(t, view, section); strings.TrimSpace(row) != section {
			t.Errorf("section row = %q", row)
		}
	}
	if got := h.lines()[35]; got != "\u2191\u2193 scroll \u00b7 o open \u00b7 x export \u00b7 d delete \u00b7 esc back" {
		t.Errorf("footer = %q", got)
	}
	if raw := rowOf(t, h.m.View(), "Plate 1 changed"); !strings.Contains(raw, codeWarn) {
		t.Errorf("a warning is not in the warn colour: %q", raw)
	}
}

func TestProjectDetailMarksAStaleSliceAndAnUnslicedPlate(t *testing.T) {
	h := newHarness(t, func(fb *fakeBackend) {
		fb.items = manyItems(1)
		fb.view = sampleView()
		fb.view.Slices[0].Stale = true
		fb.view.Info.Warnings = nil
		fb.view.Info.Plates = append(fb.view.Info.Plates, projects.PlateInfo{Index: 2, Objects: 0})
	})
	h.resize(120, 36)
	h.open(0)
	h.press("enter")
	raw := h.m.View()
	if row := rowOf(t, raw, "sliced 2026-10-02 14:30"); !strings.Contains(plain(row), "142 layers stale") || !strings.Contains(row, codeWarn) {
		t.Errorf("stale slice row = %q", row)
	}
	if !strings.Contains(plain(raw), "not sliced yet") {
		t.Errorf("no 'not sliced yet' for plate 2:\n%s", plain(raw))
	}
	if strings.Contains(plain(raw), "Warnings") {
		t.Error("an empty Warnings section")
	}
}

// --- actions ---

func openDetail(t *testing.T, mutate ...func(*fakeBackend)) *harness {
	t.Helper()
	h := newHarness(t, append([]func(*fakeBackend){withItems(3)}, mutate...)...)
	h.open(0)
	h.press("enter")
	return h
}

func TestOpenInCrealityPrint(t *testing.T) {
	h := openDetail(t, func(fb *fakeBackend) {
		fb.launch = Launched{Mode: "project", Plate: 1, PID: 18452, Version: "7.3.0", OtherWindow: true}
	})
	h.press("o")
	if h.fb.count("launch:"+sampleID) != 1 {
		t.Errorf("calls = %v", h.fb.calls)
	}
	view := h.view()
	for _, want := range []string{"Another Creality Print window was already open", "Started Creality Print 7.3.0 (process 18452) with the 3D editor on this project.", "Close that window yourself"} {
		if !strings.Contains(words(view), want) {
			t.Errorf("notice lacks %q:\n%s", want, view)
		}
	}
	if raw := noticeRaw(h); !strings.Contains(raw, codeOK) {
		t.Errorf("the notice is not in the ok colour: %q", raw)
	}
	if !strings.Contains(h.lines()[0], "Project") {
		t.Error("the project screen was left")
	}

	h = openDetail(t, func(fb *fakeBackend) {
		fb.launch = Launched{Mode: "preview", Plate: 2, PID: 7, Version: "7.3.0"}
	})
	h.press("o")
	if got := words(h.view()); !strings.Contains(got, "with the preview of plate 2.") || strings.Contains(got, "Another") {
		t.Errorf("preview notice:\n%s", h.view())
	}
}

func TestOpenInCrealityPrintFailureIsAFailNotice(t *testing.T) {
	h := openDetail(t, func(fb *fakeBackend) {
		fb.launchErr = &projects.Error{Code: "unavailable", Message: "Cannot open the app: no supported Creality Print was found", Hint: "Run the doctor."}
	})
	h.press("o")
	view := h.view()
	if !strings.Contains(words(view), "Cannot open the app: no supported Creality Print was found. Run the doctor.") {
		t.Errorf("notice:\n%s", view)
	}
	if raw := noticeRaw(h); !strings.Contains(raw, codeFail) {
		t.Errorf("the notice is not in the fail colour: %q", raw)
	}
}

func TestExportAsksForAPathAndExports(t *testing.T) {
	h := openDetail(t)
	h.resize(120, 36)
	h.press("x")
	want := filepath.Join(h.cwd, sampleID+".3mf")
	if h.lines()[0] != "creality-slicer-mcp  Export project" || !strings.Contains(squash(h.view()), squash(want)) {
		t.Errorf("export screen:\n%s", h.view())
	}
	if got := h.lines()[35]; got != "enter export \u00b7 esc cancel" {
		t.Errorf("footer = %q", got)
	}
	h.press("a", "b")
	h.press("enter")
	if h.fb.count("export:"+sampleID+":"+want+"ab") != 1 {
		t.Errorf("export calls = %v", h.fb.calls)
	}
	if h.screen() != "Project" || !strings.Contains(squash(h.view()), squash("Exported 2.0 KB to "+want+"ab.")) {
		t.Errorf("after the export:\n%s", h.view())
	}
}

func TestExportEscCancelsAndAnEmptyPathIsRefused(t *testing.T) {
	h := openDetail(t)
	h.press("x")
	h.press("esc")
	if h.screen() != "Project" || h.fb.countPrefix("export:") != 0 {
		t.Errorf("esc:\n%s", h.view())
	}
	h.press("x")
	h.m.export.SetValue("")
	h.press("enter")
	if !strings.Contains(h.view(), "Type the name of the file to save.") || h.fb.countPrefix("export:") != 0 {
		t.Errorf("empty path:\n%s calls %v", h.view(), h.fb.calls)
	}
}

func TestExportOfAnExistingFileAsksBeforeReplacing(t *testing.T) {
	var overwrites []bool
	h := openDetail(t, func(fb *fakeBackend) {
		fb.exportFn = func(path string, overwrite bool) (ExportDone, error) {
			overwrites = append(overwrites, overwrite)
			if !overwrite {
				return ExportDone{}, &ExistsError{Path: path}
			}
			return ExportDone{File: path, Bytes: 100}, nil
		}
	})
	h.press("x", "enter")
	view := h.view()
	if !strings.Contains(view, "That file exists. Replace it?") || !strings.Contains(view, "> No, keep it") || !strings.Contains(view, "  Yes, replace it") {
		t.Errorf("replace prompt:\n%s", view)
	}
	h.press("enter") // No, keep it: back to the path
	if len(overwrites) != 1 || !strings.Contains(h.view(), "File") || strings.Contains(h.view(), "Replace it?") {
		t.Errorf("after No: overwrites %v\n%s", overwrites, h.view())
	}
	h.press("enter") // exports again, exists again
	h.press("down", "enter")
	if len(overwrites) != 3 || overwrites[0] || overwrites[1] || !overwrites[2] {
		t.Errorf("overwrite flags = %v, want false false true", overwrites)
	}
	if h.screen() != "Project" || !strings.Contains(h.view(), "Exported 100 B to") {
		t.Errorf("after Yes:\n%s", h.view())
	}

	// y answers yes at once, esc goes back to the path.
	overwrites = nil
	h.press("x", "enter", "esc")
	if !strings.Contains(h.view(), "File") || len(overwrites) != 1 {
		t.Errorf("esc on the prompt:\n%s", h.view())
	}
	h.press("enter", "y")
	if len(overwrites) != 3 || !overwrites[2] {
		t.Errorf("y: overwrite flags = %v", overwrites)
	}
}

func TestExportFailureStaysOnTheScreenWithAFailNotice(t *testing.T) {
	h := openDetail(t, func(fb *fakeBackend) {
		fb.exportFn = func(string, bool) (ExportDone, error) {
			return ExportDone{}, &projects.Error{Message: "could not write the file", Hint: "check that the folder is writable"}
		}
	})
	h.press("x", "enter")
	if !strings.Contains(h.lines()[0], "Export project") || !strings.Contains(words(h.view()), "could not write the file. check that the folder is writable") {
		t.Errorf("export failure:\n%s", h.view())
	}
}

func TestDeleteAsksAndDefaultsToKeeping(t *testing.T) {
	h := openDetail(t)
	h.press("d")
	view := h.view()
	if h.lines()[0] != "creality-slicer-mcp  Delete project" || !strings.Contains(view, `Delete "Phase gauge"?`) || !strings.Contains(view, "> No, keep it") {
		t.Errorf("confirm screen:\n%s", view)
	}
	if !strings.Contains(words(view), "This cannot be undone.") {
		t.Errorf("no warning:\n%s", view)
	}
	h.press("enter")
	if h.fb.count("delete:"+sampleID) != 0 || h.screen() != "Project" {
		t.Errorf("enter on No deleted or stayed: %v\n%s", h.fb.calls, h.view())
	}
	h.press("d", "n")
	h.press("d", "esc")
	if h.fb.count("delete:"+sampleID) != 0 || h.screen() != "Project" {
		t.Errorf("n or esc deleted: %v", h.fb.calls)
	}
}

func TestDeleteRemovesTheProjectAndReloadsTheList(t *testing.T) {
	for _, keys := range [][]string{{"d", "y"}, {"d", "down", "enter"}} {
		h := newHarness(t, func(fb *fakeBackend) {
			fb.items = manyItems(3)
			fb.items[0].ID, fb.items[0].Name = sampleID, "Phase gauge"
			fb.view = sampleView()
		})
		h.open(0)
		h.press("enter")
		h.press(keys...)
		if h.fb.count("delete:"+sampleID) != 1 || h.fb.count("projects") != 2 {
			t.Errorf("%v: calls %v", keys, h.fb.calls)
		}
		view := h.view()
		if h.lines()[0] != "creality-slicer-mcp  Projects  2 projects" {
			t.Errorf("%v: header = %q", keys, h.lines()[0])
		}
		if n := strings.Count(view, "Phase gauge"); n != 1 || !strings.Contains(view, "Deleted Phase gauge.") {
			t.Errorf("%v: the project is still listed or no notice (%d):\n%s", keys, n, view)
		}
		if raw := rowOf(t, h.m.View(), "Deleted"); !strings.Contains(raw, codeOK) {
			t.Errorf("%v: notice colour %q", keys, raw)
		}
	}
}

func TestDeleteFailureReturnsToTheProjectWithAFailNotice(t *testing.T) {
	h := openDetail(t, func(fb *fakeBackend) {
		fb.deleteErr = errors.New("project is being sliced")
	})
	h.press("d", "y")
	if h.screen() != "Project" || !strings.Contains(h.view(), "project is being sliced") {
		t.Errorf("delete failure:\n%s", h.view())
	}
}

// noticeRaw is the rows above the footer, with their colour codes.
func noticeRaw(h *harness) string {
	lines := strings.Split(h.m.View(), "\n")
	return strings.Join(lines[len(lines)-5:len(lines)-1], "\n")
}

// --- actions that cannot be stopped ---

func TestCancelDuringADeleteStillAppliesTheOutcomeAndReloads(t *testing.T) {
	h := newHarness(t, func(fb *fakeBackend) {
		fb.items = manyItems(3)
		fb.items[0].ID, fb.items[0].Name = sampleID, "Phase gauge"
		fb.view = sampleView()
	})
	h.open(0)
	h.press("enter")
	h.press("d")
	_, run := h.m.Update(keyMsg("y"))
	for _, k := range []string{"ctrl+c", "esc"} {
		if _, cmd := h.m.Update(keyMsg(k)); cmd != nil || h.m.Quit {
			t.Fatalf("%s during a delete returned a command or quit", k)
		}
	}
	view := h.view()
	if h.m.loading == nil || strings.Contains(view, "Cancelled.") || strings.Contains(view, "cancel") || !strings.Contains(view, "Deleting the project...") {
		t.Fatalf("the delete was shown as cancellable:\n%s", view)
	}
	if got := h.lines()[23]; got != "please wait" {
		t.Errorf("footer = %q", got)
	}
	h.drain(run)
	if h.fb.count("delete:"+sampleID) != 1 || h.fb.count("projects") != 2 {
		t.Errorf("calls %v", h.fb.calls)
	}
	view = h.view()
	if h.lines()[0] != "creality-slicer-mcp  Projects  2 projects" || !strings.Contains(view, "Deleted Phase gauge.") || strings.Contains(view, "Cancelled.") {
		t.Errorf("after the delete:\n%s", view)
	}
}

func TestExportAndLaunchCannotBeCancelledEither(t *testing.T) {
	h := openDetail(t)
	_, run := h.m.Update(keyMsg("o"))
	h.m.Update(keyMsg("ctrl+c"))
	h.m.Update(keyMsg("esc"))
	if h.m.loading == nil {
		t.Fatal("starting the window was cancelled")
	}
	h.drain(run)
	if h.fb.count("launch:"+sampleID) != 1 || strings.Contains(h.view(), "Cancelled.") || !strings.Contains(h.view(), "Started Creality Print") {
		t.Errorf("launch outcome:\n%s", h.view())
	}
	h.press("x")
	_, run = h.m.Update(keyMsg("enter"))
	h.m.Update(keyMsg("esc"))
	if h.m.loading == nil {
		t.Fatal("the export was cancelled")
	}
	h.drain(run)
	if !strings.Contains(h.view(), "Exported") || strings.Contains(h.view(), "Cancelled.") {
		t.Errorf("export outcome:\n%s", h.view())
	}
}

// --- notices ---

func TestALongNoticeIsShownWholeAndTheBodyGivesWay(t *testing.T) {
	long := strings.TrimSpace(strings.Repeat("A long notice that has to wrap many times in a narrow window. ", 5))
	for _, sz := range []struct{ w, h int }{{40, 20}, {40, 18}, {60, 14}, {120, 36}} {
		h := newHarness(t)
		h.resize(sz.w, sz.h)
		h.m.say(RoleWarn, "%s", long)
		view, dropped := h.m.render()
		checkFrame(t, view, sz.w, sz.h, "↑↓ move")
		if dropped != 0 || !strings.Contains(squash(view), squash(long)) {
			t.Errorf("%dx%d: the notice was cut (dropped %d):\n%s", sz.w, sz.h, dropped, plain(view))
		}
	}
	// A window with no room left for the body is the too-small view.
	view, _ := Render(40, 8, Frame{Screen: "Test", Notice: long, Hints: testHints})
	lines := strings.Split(plain(view), "\n")
	if len(lines) != 8 || lines[0] != "Window too small." {
		t.Errorf("40x8 with a long notice:\n%s", plain(view))
	}
}
