package appui

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/sairaph/creality-slicer-mcp/internal/projects"
	"github.com/sairaph/creality-slicer-mcp/internal/userhome/testhome"
)

// TestMain gives the styles a colour profile, so the colour of a role can be
// asserted (38;5;203 fail, 38;5;214 warn, 38;5;42 ok), and isolates the home.
func TestMain(m *testing.M) {
	lipgloss.SetColorProfile(termenv.ANSI256)
	os.Exit(testhome.Run(m))
}

const (
	codeFail = "38;5;203"
	codeWarn = "38;5;214"
	codeOK   = "38;5;42"
)

// sizes are the windows every screen is drawn in.
var sizes = []struct{ w, h int }{{120, 36}, {80, 24}, {60, 20}, {40, 12}, {200, 50}}

func plain(s string) string { return ansi.Strip(s) }

// squash removes every blank, so text that was wrapped can be searched for.
func squash(s string) string { return strings.Join(strings.Fields(ansi.Strip(s)), "") }

// fakeBackend answers with fixed data and records what it was asked. It never
// touches the disk, a process or the network.
type fakeBackend struct {
	mu    sync.Mutex
	calls []string
	ctxs  []context.Context

	summary    Summary
	summaryErr error
	status     Status
	statusErr  error
	items      []projects.ListItem
	view       ProjectView
	launch     Launched
	launchErr  error
	doctor     []DoctorRow
	// exportFn decides the answer of Export; nil exports.
	exportFn func(path string, overwrite bool) (ExportDone, error)
	// deleteErr is the answer of Delete.
	deleteErr error
	// block makes Projects wait until its context ends.
	block bool
	// sawCancel is set when a blocked call saw its context end.
	sawCancel chan struct{}
}

func (f *fakeBackend) record(ctx context.Context, call string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
	f.ctxs = append(f.ctxs, ctx)
}

func (f *fakeBackend) count(call string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c == call {
			n++
		}
	}
	return n
}

func (f *fakeBackend) Summary(ctx context.Context) (Summary, error) {
	f.record(ctx, "summary")
	return f.summary, f.summaryErr
}

func (f *fakeBackend) Status(ctx context.Context, refresh bool) (Status, error) {
	if refresh {
		f.record(ctx, "status:refresh")
	} else {
		f.record(ctx, "status")
	}
	return f.status, f.statusErr
}

func (f *fakeBackend) Projects(ctx context.Context) ([]projects.ListItem, error) {
	f.record(ctx, "projects")
	f.mu.Lock()
	block := f.block
	f.mu.Unlock()
	if block {
		<-ctx.Done()
		close(f.sawCancel)
		return nil, ctx.Err()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]projects.ListItem(nil), f.items...), nil
}

func (f *fakeBackend) Project(ctx context.Context, id string) (ProjectView, error) {
	f.record(ctx, "project:"+id)
	return f.view, nil
}

func (f *fakeBackend) Launch(ctx context.Context, id string) (Launched, error) {
	f.record(ctx, "launch:"+id)
	return f.launch, f.launchErr
}

func (f *fakeBackend) Export(ctx context.Context, id, path string, overwrite bool) (ExportDone, error) {
	if overwrite {
		f.record(ctx, "export:"+id+":"+path+":overwrite")
	} else {
		f.record(ctx, "export:"+id+":"+path)
	}
	if f.exportFn != nil {
		return f.exportFn(path, overwrite)
	}
	return ExportDone{File: path, Bytes: 2048}, nil
}

func (f *fakeBackend) Delete(ctx context.Context, id string) error {
	f.record(ctx, "delete:"+id)
	if f.deleteErr == nil {
		f.mu.Lock()
		var kept []projects.ListItem
		for _, it := range f.items {
			if it.ID != id {
				kept = append(kept, it)
			}
		}
		f.items = kept
		f.mu.Unlock()
	}
	return f.deleteErr
}

func (f *fakeBackend) Doctor(ctx context.Context) []DoctorRow {
	f.record(ctx, "doctor")
	return append([]DoctorRow(nil), f.doctor...)
}

// harness is a model on a fake backend with the spinner ticker held back, so
// that the commands of a load are the load itself.
type harness struct {
	t         *testing.T
	m         *Model
	fb        *fakeBackend
	configure int
	cwd       string
}

func newHarness(t *testing.T, mutate ...func(*fakeBackend)) *harness {
	t.Helper()
	fb := &fakeBackend{
		summary: Summary{SlicerLine: "Creality Print 7.3.0 is ready.", SlicerOK: true, Projects: 42, Clients: []string{"Claude Code", "Codex CLI", "OpenCode"}},
		status:  Status{Found: true, Supported: true, Version: "7.3.0", Build: "6149", ProfileVersion: "26.09.29.08", ProfileSource: "install", ProjectsDir: `C:\Users\Alex\.creality-slicer-mcp\projects`},
	}
	for _, f := range mutate {
		f(fb)
	}
	h := &harness{t: t, fb: fb, cwd: t.TempDir()}
	h.m = New(Options{
		Backend: fb, Version: "0.3.5", Unicode: true,
		Cwd: func() string { return h.cwd },
		ConfigureCmd: func() *exec.Cmd {
			h.configure++
			return exec.Command("never-started")
		},
	})
	h.m.ticking = true
	h.resize(80, 24)
	h.drain(h.m.Init())
	return h
}

func (h *harness) resize(w, hh int) {
	h.m.Update(tea.WindowSizeMsg{Width: w, Height: hh})
}

// drain runs a command, feeds its message back and goes on with what that
// returns, until a command is nil or quits.
func (h *harness) drain(cmd tea.Cmd) {
	for i := 0; cmd != nil && i < 20; i++ {
		msg := cmd()
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, c := range batch {
				h.drain(c)
			}
			return
		}
		if _, ok := msg.(tea.QuitMsg); ok {
			return
		}
		_, cmd = h.m.Update(msg)
	}
}

func keyMsg(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "pgup":
		return tea.KeyMsg{Type: tea.KeyPgUp}
	case "pgdown":
		return tea.KeyMsg{Type: tea.KeyPgDown}
	case "home":
		return tea.KeyMsg{Type: tea.KeyHome}
	case "end":
		return tea.KeyMsg{Type: tea.KeyEnd}
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// press sends keys one after the other, running every command they return.
func (h *harness) press(keys ...string) {
	h.t.Helper()
	for _, k := range keys {
		_, cmd := h.m.Update(keyMsg(k))
		h.drain(cmd)
	}
}

// pressN sends one key n times.
func (h *harness) pressN(key string, n int) {
	h.t.Helper()
	for i := 0; i < n; i++ {
		h.press(key)
	}
}

// view is the plain view.
func (h *harness) view() string { return plain(h.m.View()) }

func (h *harness) lines() []string { return strings.Split(h.view(), "\n") }

// open goes from the menu to a screen: the entry at index i.
func (h *harness) open(i int) {
	h.t.Helper()
	h.pressN("down", i)
	h.press("enter")
}

// checkFrame asserts the invariants of a view in a window of w x hh.
func checkFrame(t *testing.T, view string, w, hh int, firstHint string) {
	t.Helper()
	lines := strings.Split(view, "\n")
	if len(lines) != hh {
		t.Fatalf("%dx%d: %d lines, want %d:\n%s", w, hh, len(lines), hh, plain(view))
	}
	for i, l := range lines {
		if Width(l) > w {
			t.Fatalf("%dx%d: line %d is %d wide: %q", w, hh, i+1, Width(l), plain(l))
		}
	}
	if !strings.HasPrefix(plain(lines[0]), "creality-slicer-mcp  ") {
		t.Fatalf("%dx%d: row 1 = %q", w, hh, plain(lines[0]))
	}
	if lines[1] != "" {
		t.Fatalf("%dx%d: row 2 = %q, want blank", w, hh, plain(lines[1]))
	}
	last := plain(lines[len(lines)-1])
	if strings.TrimSpace(last) == "" {
		t.Fatalf("%dx%d: the last row is empty, the footer belongs there", w, hh)
	}
	if firstHint != "" {
		tail := last
		if w < 80 && len(lines) >= 3 {
			tail = plain(strings.Join(lines[len(lines)-3:], " "))
		}
		if !strings.Contains(squash(tail), squash(firstHint)) {
			t.Fatalf("%dx%d: footer lacks %q: %q", w, hh, firstHint, tail)
		}
	}
}

// scrollText collects every line of a scrolling screen: it goes to the top and
// down one line at a time, taking the line that comes into view.
func scrollText(t *testing.T, h *harness, off func() int) string {
	t.Helper()
	h.press("home")
	frame := h.m.currentFrame()
	all := append([]string(nil), frame.Body...)
	for i := 0; i < 2000; i++ {
		before := off()
		h.press("down")
		if off() == before {
			break
		}
		body := h.m.currentFrame().Body
		all = append(all, body[len(body)-1])
	}
	return squash(strings.Join(all, ""))
}

// screen is the name of the screen in the header.
func (h *harness) screen() string {
	rest := strings.TrimPrefix(h.lines()[0], "creality-slicer-mcp  ")
	return strings.SplitN(rest, "  ", 2)[0]
}

// countPrefix is how many calls start with prefix.
func (f *fakeBackend) countPrefix(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}
