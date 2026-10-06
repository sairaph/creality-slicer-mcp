package main

// Runner-level tests of every way to leave the install wizard: the real flow
// runs in a Bubble Tea program on a pipe, with fake steps in place of the
// library's, so no client config is read or written.

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/sairaph/mcp-wizard/flow"
	"github.com/sairaph/mcp-wizard/harness"
)

type fakeReadyMsg struct{}

// fakeSelect stands for the library's client list: it is ready when
// fakeReadyMsg arrives (never, when detect is false); q and ctrl+c quit, enter
// moves on.
type fakeSelect struct {
	detect bool
	ready  chan struct{}
}

func (s *fakeSelect) ID() string                                    { return "harnesses" }
func (s *fakeSelect) Title(*AppState) string                        { return "" }
func (s *fakeSelect) Hints(*AppState) []struct{ Key, Label string } { return nil }
func (s *fakeSelect) View(*AppState) string                         { return "" }
func (s *fakeSelect) Init(*AppState) tea.Cmd {
	if !s.detect {
		close(s.ready) // the test may send keys: it is "detecting"
		return nil
	}
	return func() tea.Msg { return fakeReadyMsg{} }
}
func (s *fakeSelect) Update(msg tea.Msg, state *AppState) (flow.Directive, tea.Cmd) {
	switch m := msg.(type) {
	case fakeReadyMsg:
		state.Harness.Selected = map[harness.ID]bool{"fake": true}
		close(s.ready)
	case tea.KeyMsg:
		switch m.String() {
		case "q", "ctrl+c":
			return flow.Quit, nil
		case "enter":
			return flow.Next, nil
		}
	}
	return flow.Continue, nil
}

// fakeApply stands for the registration: starting it is recorded, and q quits
// like the library's step does.
type fakeApply struct{ started chan struct{} }

func (s *fakeApply) ID() string                                    { return "apply" }
func (s *fakeApply) Title(*AppState) string                        { return "" }
func (s *fakeApply) Hints(*AppState) []struct{ Key, Label string } { return nil }
func (s *fakeApply) View(*AppState) string                         { return "" }
func (s *fakeApply) Init(*AppState) tea.Cmd                        { close(s.started); return nil }
func (s *fakeApply) Update(msg tea.Msg, _ *AppState) (flow.Directive, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok && k.String() == "q" {
		return flow.Quit, nil
	}
	return flow.Continue, nil
}

type wizardRun struct {
	code           int
	stdout, stderr string
	registered     bool
}

// runFake runs the wizard with the fake steps. keys are typed once the list is
// ready (afterApply: once the registration started); cancelCtx cancels the
// context at that moment instead, like SIGINT.
func runFake(t *testing.T, detect bool, keys string, afterApply, cancelCtx, fromApp bool) wizardRun {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sel := &fakeSelect{detect: detect, ready: make(chan struct{})}
	apply := &fakeApply{started: make(chan struct{})}
	state := &AppState{FromApp: fromApp}
	steps := []flow.Step[AppState]{harnessSelection{Step: sel}, applyGuard{Step: apply}}
	f := flow.New(steps, state)

	pr, pw := io.Pipe()
	defer pw.Close()
	go func() {
		<-sel.ready
		if afterApply {
			// Enter, then wait for the registration to start.
			pw.Write([]byte("\r"))
			select {
			case <-apply.started:
			case <-time.After(5 * time.Second):
				return
			}
		}
		if cancelCtx {
			cancel()
			return
		}
		pw.Write([]byte(keys))
	}()

	done := make(chan int, 1)
	go func() { done <- runFlow(ctx, f, pr, io.Discard) }()
	var code int
	select {
	case code = <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("the wizard did not end")
	}
	var out, errb bytes.Buffer
	exit := finishWizard(ctx, f, state, stepIndex(steps, "apply"), code, false, &out, &errb)
	started := false
	select {
	case <-apply.started:
		started = true
	default:
	}
	return wizardRun{code: exit, stdout: out.String(), stderr: errb.String(), registered: started}
}

const cancelledText = "Setup cancelled; nothing was changed."

func wantCancelled(t *testing.T, name string, r wizardRun, quiet bool) {
	t.Helper()
	if r.code != exitCancelled {
		t.Errorf("%s: exit %d, want %d", name, r.code, exitCancelled)
	}
	if r.registered {
		t.Errorf("%s: the registration started", name)
	}
	if strings.Contains(r.stderr, "interrupted") {
		t.Errorf("%s: says it was interrupted: %q", name, r.stderr)
	}
	if quiet {
		if r.stdout != "" || r.stderr != "" {
			t.Errorf("%s: printed %q %q, want nothing", name, r.stdout, r.stderr)
		}
	} else if !strings.Contains(r.stdout, cancelledText) || r.stderr != "" {
		t.Errorf("%s: printed %q %q, want only the cancel line", name, r.stdout, r.stderr)
	}
}

func TestWizardCancelPoints(t *testing.T) {
	for _, quiet := range []bool{false, true} {
		wantCancelled(t, "q on the list", runFake(t, true, "q", false, false, quiet), quiet)
		wantCancelled(t, "ctrl+c on the list", runFake(t, true, "\x03", false, false, quiet), quiet)
		wantCancelled(t, "q during detection", runFake(t, false, "q", false, false, quiet), quiet)
		wantCancelled(t, "ctrl+c during detection", runFake(t, false, "\x03", false, false, quiet), quiet)
		wantCancelled(t, "signal on the list", runFake(t, true, "", false, true, quiet), quiet)
		wantCancelled(t, "signal during detection", runFake(t, false, "", false, true, quiet), quiet)
	}
}

// Keys typed behind the q in one burst must not start the registration.
func TestWizardKeysBehindCancelDoNotRegister(t *testing.T) {
	for _, keys := range []string{"q\r", "q\r\r", "\x03\r"} {
		wantCancelled(t, "burst "+strings.ReplaceAll(keys, "\r", "<enter>"), runFake(t, true, keys, false, false, false), false)
	}
}

func TestWizardInterruptAfterApplyStarted(t *testing.T) {
	r := runFake(t, true, "q", true, false, false)
	if r.code != 1 || !r.registered || !strings.Contains(r.stderr, interruptedMessage) || r.stdout != "" {
		t.Errorf("interrupt after apply, run directly: %+v", r)
	}
	// From the app the notice is the app's own: the exit code says it, the
	// child prints nothing.
	r = runFake(t, true, "q", true, false, true)
	if r.code != exitInterrupted || !r.registered || r.stdout != "" || r.stderr != "" {
		t.Errorf("interrupt after apply, from the app: %+v", r)
	}
}
