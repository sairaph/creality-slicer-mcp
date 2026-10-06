package main

// Install wizard plumbing. The wizard writes nothing until the AI clients are
// registered; the guide skill is written after that, inside the wizard. Leaving
// the wizard before that point (q, ctrl+c, closing it, SIGINT/SIGTERM) leaves
// the machine unchanged, and the process exits with exitCancelled so the
// install scripts can roll back the binary they just placed.

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/sairaph/mcp-wizard/cli"
	"github.com/sairaph/mcp-wizard/flow"
	"github.com/sairaph/mcp-wizard/harness"
	"github.com/sairaph/mcp-wizard/tui"

	"github.com/sairaph/creality-slicer-mcp/internal/domain"
)

// exitCancelled is the exit status of a wizard left before anything was
// written. install.ps1 and install.sh rely on it to undo their own changes.
const exitCancelled = 3

// exitInterrupted is the exit status, when the app started the wizard, of a
// wizard left while the clients were being registered. The app shows the
// message itself; run directly the wizard prints it and exits 1.
const exitInterrupted = 4

// harnessSelection adapts mcp-wizard v0.1.1's client list:
//
//   - It renders every key of HarnessState.Selected as checked, including
//     clients the user switched off (their value is false). Dropping false
//     entries after each update makes the list show what will be registered.
//   - Its enter does nothing while no client is selected. Enter moves on
//     instead, and registration reports that nothing was configured.
//   - It pre-selects clients whose entry was edited by hand or runs another
//     program (see clients.go), and registering replaces that entry. Those
//     start unticked here, with a note saying why, so what is there is only
//     replaced on request.
//   - Revisiting the step detects the clients again, but the library keeps
//     taking keys meanwhile, which would act on the previous list. Keys other
//     than cancel wait until the new list is shown.
type harnessSelection struct {
	flow.Step[AppState]
	// name is the server's name in the client configs.
	name string
	// findUnticked returns the detected clients that start unticked.
	findUnticked func([]harness.Harness) map[harness.ID]bool
}

func (h harnessSelection) Init(state *AppState) tea.Cmd {
	// The library fills Selected when detection finishes. Keys are held
	// back until then, so Selected turning non-nil marks the detection.
	state.Harness.Selected = nil
	state.harnessDetecting = true
	state.UntickedClients = nil
	return h.Step.Init(state)
}

func (h harnessSelection) Update(msg tea.Msg, state *AppState) (flow.Directive, tea.Cmd) {
	// Once cancelled, nothing else counts: keys typed in the same burst as q
	// (an enter behind it) must not move on to the registration before the
	// program has quit.
	if state.cancelled {
		return flow.Quit, nil
	}
	if tui.IsSpinMsg(msg) {
		state.Spinner.Frame++
	}
	if k, ok := msg.(tea.KeyMsg); ok {
		switch key := k.String(); {
		case state.harnessDetecting && key != "q" && key != "ctrl+c":
			return flow.Continue, nil
		case !state.harnessDetecting && key == "enter" && !anySelected(state.Harness.Selected):
			return flow.Next, nil
		}
	}
	d, cmd := h.Step.Update(msg, state)
	if _, isKey := msg.(tea.KeyMsg); isKey && d == flow.Quit {
		state.cancelled = true
	}
	if state.harnessDetecting && state.Harness.Selected != nil {
		state.harnessDetecting = false
		if h.findUnticked != nil {
			unticked := h.findUnticked(state.Harness.Detections)
			for _, c := range state.Harness.Detections {
				if unticked[c.ID] && c.Selectable() {
					delete(state.Harness.Selected, c.ID)
					state.UntickedClients = append(state.UntickedClients, c.Name)
				}
			}
		}
	}
	pruneUnselected(&state.Harness.Selected)
	return d, cmd
}

func anySelected[K comparable](selected map[K]bool) bool {
	for _, on := range selected {
		if on {
			return true
		}
	}
	return false
}

func pruneUnselected[K comparable](selected *map[K]bool) {
	for id, on := range *selected {
		if !on {
			delete(*selected, id)
		}
	}
}

// runsUnattended reports whether install or add skip the wizard: --yes, or a
// flag the wizard would ignore (--all, --clients, and the credential flags
// this server ignores but which install.sh and install.ps1 also treat as
// unattended). `install` (with or without --scope project) can receive all of
// them; `add` accepts only --all and --yes of these.
func runsUnattended(cmd cli.Command) bool {
	return cmd.Yes || cmd.All || len(cmd.Clients) > 0 || len(cmd.Credentials) > 0
}

// applyGuard is the registration step of the wizard. It keeps ctrl+c from
// ending the wizard while the registration writes are running (the library
// step accepts it, and exiting then would kill the goroutine in the middle of
// writing a client's config file), writes the guide skill once the clients are
// registered, and draws the registering and the finish screens (see
// wizard_finish.go).
type applyGuard struct {
	flow.Step[AppState]
	dryRun bool
}

// stepIndex returns the position of the step with the given ID, or -1.
func stepIndex(steps []flow.Step[AppState], id string) int {
	for i, s := range steps {
		if s.ID() == id {
			return i
		}
	}
	return -1
}

// wizardOutcome classifies how the wizard ended.
type wizardOutcome int

const (
	outcomeCompleted   wizardOutcome = iota // registration ran
	outcomeCancelled                        // left before registration: nothing written
	outcomeInterrupted                      // left during registration: some clients may be registered
	outcomeFailed                           // a step failed, or the wizard could not start, before registration
)

// classifyWizard decides the outcome from the flow's state. runCode is
// runFlow's result: non-zero without a recorded failure means the terminal
// UI could not run at all. applyStarted reports whether the flow reached the
// registration step; in a dry run that step writes nothing. signalled
// reports that the wizard's context was cancelled (SIGINT or SIGTERM), which
// ends runFlow with a non-zero code; before registration that is a cancel.
func classifyWizard(base *flow.BaseState, runCode int, applyStarted, dryRun, signalled bool) wizardOutcome {
	switch {
	case base.Settled:
		return outcomeCompleted
	case applyStarted && !dryRun:
		return outcomeInterrupted
	case signalled:
		return outcomeCancelled
	case base.Failure != nil || runCode != 0:
		return outcomeFailed
	default:
		return outcomeCancelled
	}
}

const interruptedMessage = "  Setup was interrupted while registering the AI clients, so some may be registered.\n" +
	"  Run `" + domain.BinaryName + " install` to finish, or `" + domain.BinaryName + " uninstall --all` to remove everything."
