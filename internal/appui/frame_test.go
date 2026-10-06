package appui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/sairaph/mcp-wizard/tui"
)

func TestWrapBreaksAtWords(t *testing.T) {
	got := Wrap("aaa bbb ccc", 7, 0)
	if strings.Join(got, "|") != "aaa bbb|ccc" {
		t.Errorf("Wrap = %q", got)
	}
	got = Wrap("one two three four", 10, 2)
	if strings.Join(got, "|") != "one two|  three|  four" {
		t.Errorf("hanging indent: %q", got)
	}
	got = Wrap("first\n\nsecond line", 40, 0)
	if strings.Join(got, "|") != "first||second line" {
		t.Errorf("newlines: %q", got)
	}
	if Wrap("", 10, 0) != nil {
		t.Error("empty text is not nothing")
	}
}

func TestWrapBreaksALongTokenAfterASeparator(t *testing.T) {
	path := `C:\Users\Alex\AppData\Local\creality-slicer-mcp\bin`
	got := Wrap(path, 30, 0)
	if strings.Join(got, "|") != `C:\Users\Alex\AppData\Local\|creality-slicer-mcp\bin` {
		t.Errorf("path: %q", got)
	}
	// Without a separator it breaks at the width.
	got = Wrap(strings.Repeat("x", 25), 10, 0)
	if strings.Join(got, "|") != "xxxxxxxxxx|xxxxxxxxxx|xxxxx" {
		t.Errorf("plain token: %q", got)
	}
}

func TestWrapNeverLosesATextOrOverflows(t *testing.T) {
	texts := []string{
		strings.Repeat("word ", 40),
		`C:\very\long\path\with-dashes-and-more-dashes\and\more\and\more\segments\file.name.3mf`,
		strings.Repeat("a", 200),
		"wide characters: " + strings.Repeat("\u6f22\u5b57", 30),
		"a   b\tc\nd",
	}
	for _, text := range texts {
		for _, width := range []int{1, 2, 5, 17, 40, 76} {
			for _, hang := range []int{0, 3} {
				lines := Wrap(text, width, hang)
				if got, want := squash(strings.Join(lines, "")), squash(text); got != want {
					t.Errorf("width %d hang %d lost text:\n got %q\nwant %q", width, hang, got, want)
				}
				if width < 6 || width <= hang+1 {
					continue // a wide character or the indent alone can be wider than the line
				}
				for _, l := range lines {
					if Width(l) > width {
						t.Errorf("width %d hang %d: %q is %d wide", width, hang, l, Width(l))
					}
				}
			}
		}
	}
}

func TestWrapPrefixedCountsThePrefixes(t *testing.T) {
	got := WrapPrefixed("alpha beta gamma delta", "ab: ", "    ", 14)
	if strings.Join(got, "|") != "ab: alpha beta|    gamma|    delta" {
		t.Errorf("WrapPrefixed = %q", got)
	}
}

func TestWrapRangesKeepsEveryRune(t *testing.T) {
	text := []rune(`C:\Users\Alex\Desktop\a path with spaces\and-more-and-more\file.3mf`)
	for _, width := range []int{4, 10, 25} {
		var b strings.Builder
		for _, r := range wrapRanges(text, width) {
			b.WriteString(string(text[r[0]:r[1]]))
			if Width(string(text[r[0]:r[1]])) > width {
				t.Errorf("width %d: a range is wider", width)
			}
		}
		if b.String() != string(text) {
			t.Errorf("width %d: ranges do not cover the text: %q", width, b.String())
		}
	}
	if r := wrapRanges(nil, 10); len(r) != 1 || r[0] != [2]int{0, 0} {
		t.Errorf("empty input: %v", r)
	}
}

func TestScroller(t *testing.T) {
	var s Scroller
	key := func(k string) bool { return s.Keys(keyMsg(k), 50, 10) }
	if !key("down") || s.Off != 1 {
		t.Errorf("down: %d", s.Off)
	}
	key("pgdown")
	if s.Off != 10 { // a page less one line
		t.Errorf("pgdown: %d", s.Off)
	}
	key("end")
	if s.Off != 40 {
		t.Errorf("end: %d", s.Off)
	}
	key("down")
	if s.Off != 40 {
		t.Errorf("down past the end: %d", s.Off)
	}
	key("pgup")
	if s.Off != 31 {
		t.Errorf("pgup: %d", s.Off)
	}
	key("home")
	key("up")
	if s.Off != 0 {
		t.Errorf("up past the start: %d", s.Off)
	}
	if key("x") {
		t.Error("a letter was taken for a scroll key")
	}
	lines := make([]string, 25)
	for i := range lines {
		lines[i] = fmt.Sprint(i)
	}
	s.Off = 99
	vis, from, to := s.Window(lines, 10)
	if len(vis) != 10 || from != 16 || to != 25 || s.Off != 15 {
		t.Errorf("Window = %d lines %d-%d, off %d", len(vis), from, to, s.Off)
	}
	short, from, to := s.Window(lines[:3], 10)
	if len(short) != 3 || from != 1 || to != 3 {
		t.Errorf("short content: %d lines %d-%d", len(short), from, to)
	}
}

func TestWindowAroundKeepsTheLineInView(t *testing.T) {
	lines := []string{"0", "1", "2", "3", "4", "5", "6", "7"}
	join := func(l []string) string { return strings.Join(l, "") }
	if got := join(windowAround(lines, 3, 7, true)); got != "567" {
		t.Errorf("tail: %s", got)
	}
	if got := join(windowAround(lines, 3, 1, true)); got != "123" {
		t.Errorf("tail with an earlier line: %s", got)
	}
	if got := join(windowAround(lines, 3, 0, false)); got != "012" {
		t.Errorf("head: %s", got)
	}
	if got := join(windowAround(lines, 3, 5, false)); got != "345" {
		t.Errorf("head with a later line: %s", got)
	}
	if got := join(windowAround(lines, 20, 2, true)); got != "01234567" {
		t.Errorf("everything fits: %s", got)
	}
}

func TestSpinnerFrames(t *testing.T) {
	if got := strings.Join(Frames(true), ""); got != "\u280b\u2819\u2839\u2838\u283c\u2834\u2826\u2827\u2807\u280f" {
		t.Errorf("braille frames: %q", got)
	}
	if got := strings.Join(Frames(false), ""); got != `-\|/` {
		t.Errorf("ascii frames: %q", got)
	}
	if SpinnerFrame(true, 10) != SpinnerFrame(true, 0) || SpinnerFrame(false, -3) != "-" {
		t.Error("frames do not wrap")
	}
}

var testHints = []tui.Hint{{Key: "\u2191\u2193", Label: "move"}, {Key: "enter", Label: "select"}, {Key: "q", Label: "quit"}}

func bodyOf(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("  body row %d", i+1)
	}
	return out
}

func TestRenderInvariantsAtEverySize(t *testing.T) {
	for _, sz := range sizes {
		for _, n := range []int{0, 1, 40} {
			rows := BodyRows(sz.w, sz.h, "", testHints)
			body := bodyOf(n)
			if len(body) > rows {
				body = body[:rows]
			}
			view, dropped := Render(sz.w, sz.h, Frame{Screen: "Test", Context: "a context", Body: body, Hints: testHints})
			if dropped != 0 {
				t.Errorf("%dx%d: %d lines dropped from a windowed body", sz.w, sz.h, dropped)
			}
			checkFrame(t, view, sz.w, sz.h, "\u2191\u2193 move")
			if strings.HasSuffix(view, "\n") {
				t.Errorf("%dx%d: the view ends with a newline", sz.w, sz.h)
			}
		}
	}
}

func TestRenderKeepsTheFooterOnTheLastRowWhateverTheBody(t *testing.T) {
	for _, sz := range sizes {
		rows := BodyRows(sz.w, sz.h, "", testHints)
		short, _ := Render(sz.w, sz.h, Frame{Screen: "Test", Body: bodyOf(1), Hints: testHints})
		long, _ := Render(sz.w, sz.h, Frame{Screen: "Test", Body: bodyOf(rows), Hints: testHints})
		a, b := strings.Split(short, "\n"), strings.Split(long, "\n")
		if a[len(a)-1] != b[len(b)-1] {
			t.Errorf("%dx%d: the footer moved: %q then %q", sz.w, sz.h, a[len(a)-1], b[len(b)-1])
		}
	}
}

func TestRenderReportsBodyLinesThatDoNotFit(t *testing.T) {
	rows := BodyRows(80, 24, "", testHints)
	view, dropped := Render(80, 24, Frame{Screen: "Test", Body: bodyOf(rows + 5), Hints: testHints})
	if dropped != 5 {
		t.Errorf("dropped = %d, want 5", dropped)
	}
	if got := len(strings.Split(view, "\n")); got != 24 {
		t.Errorf("%d lines", got)
	}
}

func TestRenderDropsTheContextWhenTheHeaderIsTooWide(t *testing.T) {
	wide, _ := Render(120, 24, Frame{Screen: "Slicer status", Context: "a long context", Hints: testHints})
	if !strings.Contains(plain(wide), "Slicer status  a long context") {
		t.Errorf("the context is missing from a wide header:\n%s", plain(wide))
	}
	narrow, _ := Render(40, 24, Frame{Screen: "Slicer status", Context: "a long context", Hints: testHints})
	if first := strings.Split(plain(narrow), "\n")[0]; strings.Contains(first, "context") {
		t.Errorf("the context stays in a header that is too wide: %q", first)
	}
}

func TestRenderShowsTheNoticeAboveTheFooterInItsColour(t *testing.T) {
	for _, c := range []struct {
		role Role
		code string
	}{{RoleOK, codeOK}, {RoleWarn, codeWarn}, {RoleFail, codeFail}} {
		view, _ := Render(80, 24, Frame{Screen: "Test", Notice: "Something happened.", NoticeRole: c.role, Hints: testHints})
		lines := strings.Split(view, "\n")
		row := lines[len(lines)-2]
		if !strings.Contains(plain(row), "  Something happened.") || !strings.Contains(row, c.code) {
			t.Errorf("notice row = %q, want colour %s", row, c.code)
		}
	}
}

func TestRenderWrapsAFooterThatIsTooWide(t *testing.T) {
	hints := []tui.Hint{{Key: "key", Label: "one two three"}, {Key: "key", Label: "four five six"}, {Key: "key", Label: "seven eight nine"}, {Key: "key", Label: "ten"}}
	view, _ := Render(40, 12, Frame{Screen: "Test", Hints: hints})
	lines := strings.Split(plain(view), "\n")
	foot := strings.Join(lines[len(lines)-2:], " ")
	for _, want := range []string{"one two three", "four five six", "seven eight nine", "ten"} {
		if !strings.Contains(foot, want) {
			t.Errorf("footer lacks %q: %q", want, foot)
		}
	}
	checkFrame(t, view, 40, 12, "")
}

func TestRenderSaysWhenTheWindowIsTooSmall(t *testing.T) {
	for _, sz := range []struct{ w, h int }{{29, 24}, {80, 5}, {10, 3}} {
		view, _ := Render(sz.w, sz.h, Frame{Screen: "Test", Hints: testHints})
		lines := strings.Split(view, "\n")
		if len(lines) != sz.h {
			t.Errorf("%dx%d: %d lines", sz.w, sz.h, len(lines))
		}
		if !strings.HasPrefix("Window too small.", strings.TrimSpace(plain(lines[0]))) {
			t.Errorf("%dx%d: first row %q", sz.w, sz.h, lines[0])
		}
		for _, l := range lines {
			if Width(l) > sz.w {
				t.Errorf("%dx%d: %q is too wide", sz.w, sz.h, l)
			}
		}
	}
	if view, _ := Render(80, 24, Frame{Screen: "Test"}); strings.Contains(view, "too small") {
		t.Error("a normal window is too small")
	}
}
