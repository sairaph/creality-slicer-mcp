package appui

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sairaph/creality-slicer-mcp/internal/projects"
)

var (
	longName = strings.TrimSpace(strings.Repeat("word ", 24)) // 119 characters
	// longPath has separators; longToken has none.
	longPath  = `C:\` + strings.Repeat(`abcdefghi\`, 20) + "plate_1.gcode"
	longToken = strings.Repeat("a", 100)
	longObj   = strings.Repeat("o", 60)
)

// bigBackend fills the fake with data that overflows every screen.
func bigBackend(fb *fakeBackend) {
	fb.items = manyItems(50)
	fb.items[0].Name = longName
	fb.items[0].Printer = "Creality K2 0.6 nozzle"
	fb.items[0].LastSlice = &projects.SliceStamp{Time: t0, Stale: true}
	fb.view = sampleView()
	fb.view.Info.Objects[0].Name = longObj
	fb.view.Slices[0].GCodePath = longPath
	for i := 0; i < 4; i++ {
		fb.view.Info.Warnings = append(fb.view.Info.Warnings, projects.Warning{Message: fmt.Sprintf("Warning number %d says something long enough to wrap in a narrow window, with %s.", i, longToken)})
	}
	for i := 0; i < 40; i++ {
		level := LevelOK
		switch {
		case i%7 == 0:
			level = LevelFail
		case i%5 == 0:
			level = LevelWarn
		}
		fb.doctor = append(fb.doctor, DoctorRow{Name: fmt.Sprintf("Check %02d", i), Level: level, Detail: fmt.Sprintf("%s then some words that wrap number %d", longPath, i)})
	}
	fb.doctor[1].Name = "AI clients"
	fb.doctor[1].Level = LevelFail
	fb.status.ProjectsDir = longPath
	fb.status.Problems = []string{"The installed Creality Print sets 3 settings that this server does not know yet (catalog 7.2.1, app 7.3.0): wipe_tower_x, wipe_tower_y, prime_tower_brim_width, " + longToken + "."}
	fb.exportFn = func(path string, overwrite bool) (ExportDone, error) {
		if !overwrite {
			return ExportDone{}, &ExistsError{Path: path}
		}
		return ExportDone{File: path, Bytes: 1}, nil
	}
}

type screenCase struct {
	name string
	hint string
	// setup goes to the screen.
	setup func(h *harness)
	// want is text that must be shown whole, by scrolling when the screen
	// scrolls; minH is the smallest window that has room for all of it.
	want  func(h *harness) []string
	minH  int
	scrol func(h *harness) func() int
}

func screenCases() []screenCase {
	return []screenCase{
		{name: "menu", hint: "\u2191\u2193 move", setup: func(h *harness) {}},
		{
			name: "status", hint: "r check again", setup: func(h *harness) { h.open(1) },
			scrol: func(h *harness) func() int { return func() int { return h.m.statusScroll.Off } },
			want:  func(h *harness) []string { return []string{longPath, h.fb.status.Problems[0]} },
		},
		{
			name: "projects", hint: "\u2191\u2193 move", setup: func(h *harness) { h.open(0) },
			want: func(h *harness) []string { return []string{longName} },
		},
		{
			name: "project", hint: "\u2191\u2193 scroll", setup: func(h *harness) { h.open(0); h.press("enter") },
			scrol: func(h *harness) func() int { return func() int { return h.m.detailScroll.Off } },
			want: func(h *harness) []string {
				w := []string{longPath, longObj}
				for _, wn := range h.fb.view.Info.Warnings {
					w = append(w, wn.Message)
				}
				return w
			},
		},
		{
			name: "export", hint: "enter export", setup: func(h *harness) { h.cwd = longToken; h.open(0); h.press("enter", "x") },
			want: func(h *harness) []string { return []string{filepath.Join(h.cwd, sampleID+".3mf")} },
		},
		{
			name: "export replace", hint: "\u2191\u2193 move", setup: func(h *harness) { h.cwd = longToken; h.open(0); h.press("enter", "x", "enter") },
			want: func(h *harness) []string {
				return []string{filepath.Join(h.cwd, sampleID+".3mf"), "That file exists. Replace it?"}
			},
			minH: 20,
		},
		{name: "delete", hint: "\u2191\u2193 move", setup: func(h *harness) { h.open(0); h.press("enter", "d") }},
		{
			name: "doctor", hint: "\u2191\u2193 scroll", setup: func(h *harness) { h.open(2) },
			scrol: func(h *harness) func() int { return func() int { return h.m.doctorScroll.Off } },
			want: func(h *harness) []string {
				var w []string
				for _, r := range h.fb.doctor {
					w = append(w, r.Detail)
				}
				return w
			},
		},
		{
			name: "loading", hint: "(ctrl+c to cancel)",
			setup: func(h *harness) { h.m.Update(keyMsg("enter")) },
		},
	}
}

func TestEveryScreenFitsEveryWindowAndNeverCutsText(t *testing.T) {
	for _, c := range screenCases() {
		for _, sz := range sizes {
			h := newHarness(t, bigBackend)
			h.resize(sz.w, sz.h)
			c.setup(h)
			view, dropped := h.m.render()
			name := fmt.Sprintf("%s at %dx%d", c.name, sz.w, sz.h)
			if dropped != 0 {
				t.Errorf("%s: %d body lines did not fit", name, dropped)
			}
			checkFrame(t, view, sz.w, sz.h, c.hint)
			if strings.Contains(view, "…") {
				t.Errorf("%s: a truncation ellipsis:\n%s", name, plain(view))
			}
			if c.want == nil || sz.h < c.minH {
				continue
			}
			got, words := squash(view), strings.Fields(plain(view))
			if c.scrol != nil {
				got = scrollText(t, h, c.scrol(h))
			}
			for _, want := range c.want(h) {
				if !strings.Contains(got, squash(want)) && !(c.scrol == nil && wordsInOrder(words, strings.Fields(want))) {
					t.Errorf("%s: the text was cut: %q is not shown whole in:\n%s", name, want, plain(view))
				}
			}
		}
	}
}

func TestDeleteScreenKeepsItsOptionsInEveryWindow(t *testing.T) {
	for _, sz := range sizes {
		h := newHarness(t, bigBackend)
		h.resize(sz.w, sz.h)
		h.open(0)
		h.press("enter", "d")
		view := h.view()
		for _, want := range []string{"> No, keep it", "  Yes, delete it"} {
			if !strings.Contains(view, want) {
				t.Errorf("%dx%d: the delete screen lacks %q:\n%s", sz.w, sz.h, want, view)
			}
		}
		h.press("down")
		if !strings.Contains(h.view(), "> Yes, delete it") {
			t.Errorf("%dx%d: down does not move the cursor:\n%s", sz.w, sz.h, h.view())
		}
	}
}

func TestWindowTooSmall(t *testing.T) {
	h := newHarness(t)
	h.resize(29, 24)
	if got := h.lines()[0]; got != "Window too small." {
		t.Errorf("29x24: %q", got)
	}
	h.resize(80, 5)
	if got := h.lines(); len(got) != 5 || got[0] != "Window too small." {
		t.Errorf("80x5: %q", got)
	}
	h.resize(80, 24)
	if strings.Contains(h.view(), "too small") {
		t.Error("the message stays after the window grew")
	}
}

func TestResizeDuringAnyScreenKeepsTheFrame(t *testing.T) {
	for _, c := range screenCases() {
		h := newHarness(t, bigBackend)
		c.setup(h)
		for _, sz := range sizes {
			h.resize(sz.w, sz.h)
			view, dropped := h.m.render()
			if dropped != 0 {
				t.Errorf("%s after a resize to %dx%d: %d lines did not fit", c.name, sz.w, sz.h, dropped)
			}
			checkFrame(t, view, sz.w, sz.h, c.hint)
		}
	}
}

// wordsInOrder reports whether the words of want appear in view in that order,
// other words (a table's other columns) allowed in between. A word missing at
// the end, or a word cut short, is not found. A want of one word never uses it.
func wordsInOrder(view, want []string) bool {
	if len(want) < 2 {
		return false
	}
	i := 0
	for _, w := range view {
		if w == want[i] {
			i++
			if i == len(want) {
				return true
			}
		}
	}
	return false
}
