package appui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/sairaph/mcp-wizard/app"
	"github.com/sairaph/mcp-wizard/tui"

	"github.com/sairaph/creality-slicer-mcp/internal/projects"
)

// Window size assumed until the first tea.WindowSizeMsg.
const (
	defaultWidth  = 80
	defaultHeight = 24
)

type screenID int

const (
	scMenu screenID = iota
	scStatus
	scProjects
	scProject
	scExport
	scDelete
	scDoctor
)

type loadKind int

const (
	loadSummary loadKind = iota
	loadStatus
	loadProjects
	loadProject
	loadLaunch
	loadExport
	loadDelete
	loadDoctor
)

// loadedMsg is the answer of a load. It is ignored when its generation is not
// the one of the load that is running (the load was cancelled or replaced).
type loadedMsg struct {
	gen  int
	kind loadKind
	val  any
	err  error
}

// setupDoneMsg says the installer child process ended.
type setupDoneMsg struct{ code int }

// load is the one load that has the screen: a spinner and a way to cancel.
type load struct {
	gen    int
	kind   loadKind
	screen string // name of the screen the load is for, shown in the header
	label  string
	cancel context.CancelFunc
}

// summaryState is the background load of the two rows under the menu.
type summaryState struct {
	loading bool
	gen     int
	cancel  context.CancelFunc
	val     *Summary
	err     error
}

// listState is the project list: the cursor is stored, the page is derived
// from it and the window on every View.
type listState struct {
	items    []projects.ListItem
	cursor   int
	majority string
}

// Model is the app. All state is here; the layout is computed from the window
// size in View, so a resize needs no update.
type Model struct {
	app.AppModel
	opts Options
	w, h int

	screen screenID
	back   []screenID

	loading *load
	gen     int
	ticking bool
	frame   int

	notice     string
	noticeRole Role

	menuCursor int
	menuScroll Scroller
	sum        summaryState

	status       *Status
	showPaths    bool
	statusScroll Scroller

	list listState

	detail       *ProjectView
	detailScroll Scroller

	doctor       []DoctorRow
	doctorScroll Scroller

	export        textinput.Model
	exportReplace bool
	// confirm is the option under the cursor of a two-option confirm: 0 is
	// "No, keep it".
	confirm int
}

// New builds the app.
func New(o Options) *Model {
	ti := textinput.New()
	ti.Prompt = ""
	ti.CharLimit = 0
	ti.Focus()
	return &Model{opts: o, w: defaultWidth, h: defaultHeight, export: ti}
}

// Init starts the summary of the menu.
func (m *Model) Init() tea.Cmd { return m.startSummary() }

// Size is the window the app is drawn in.
func (m *Model) Size() (w, h int) { return m.w, m.h }

// Update routes a message.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		return m, nil
	case loadedMsg:
		return m, m.onLoaded(msg)
	case setupDoneMsg:
		return m, m.onSetupDone(msg)
	}
	if tui.IsSpinMsg(msg) {
		m.frame++
		if m.loading != nil || m.sum.loading {
			return m, tui.Spinner()
		}
		m.ticking = false
		return m, nil
	}
	if m.loading != nil {
		if k, ok := msg.(tea.KeyMsg); ok && (k.String() == "ctrl+c" || k.String() == "esc") {
			m.cancelLoad() // an action that cannot be stopped ignores the key
		}
		return m, nil
	}
	if mm, ok := msg.(tea.MouseMsg); ok {
		switch mm.Button {
		case tea.MouseButtonWheelUp:
			msg = tea.KeyMsg{Type: tea.KeyUp}
		case tea.MouseButtonWheelDown:
			msg = tea.KeyMsg{Type: tea.KeyDown}
		default:
			return m, nil
		}
	}
	if handled, cmd := m.HandleGlobalKeys(msg); handled {
		return m, cmd
	}
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		// Not a key: the export input may want it (a paste, a blink).
		if m.screen == scExport && !m.exportReplace {
			var cmd tea.Cmd
			m.export, cmd = m.export.Update(msg)
			return m, cmd
		}
		return m, nil
	}
	m.notice = ""
	switch m.screen {
	case scMenu:
		return m, m.menuKey(k)
	case scStatus:
		return m, m.statusKey(k)
	case scProjects:
		return m, m.projectsKey(k)
	case scProject:
		return m, m.projectKey(k)
	case scExport:
		return m, m.exportKey(k)
	case scDelete:
		return m, m.deleteKey(k)
	case scDoctor:
		return m, m.doctorKey(k)
	}
	return m, nil
}

// View draws the current screen.
func (m *Model) View() string {
	v, _ := m.render()
	return v
}

// render is View with the number of body lines that did not fit.
func (m *Model) render() (string, int) {
	f := m.currentFrame()
	if f.Notice == "" {
		f.Notice, f.NoticeRole = m.notice, m.noticeRole
	}
	return Render(m.w, m.h, f)
}

func (m *Model) currentFrame() Frame {
	if m.loading != nil {
		return m.loadingFrame()
	}
	switch m.screen {
	case scStatus:
		return m.statusFrame()
	case scProjects:
		return m.projectsFrame()
	case scProject:
		return m.projectFrame()
	case scExport:
		return m.exportFrame()
	case scDelete:
		return m.deleteFrame()
	case scDoctor:
		return m.doctorFrame()
	}
	return m.menuFrame()
}

// --- navigation and notices ---

func (m *Model) push(s screenID) {
	m.back = append(m.back, m.screen)
	m.screen = s
}

func (m *Model) pop() {
	if n := len(m.back); n > 0 {
		m.screen = m.back[n-1]
		m.back = m.back[:n-1]
	}
}

func (m *Model) say(role Role, format string, a ...any) {
	m.notice = fmt.Sprintf(format, a...)
	m.noticeRole = role
}

// errText is an error for a notice: its message and, for a projects error,
// its hint.
func errText(err error) string { return ErrText(err) }

// --- loads ---

// startTick starts the spinner ticker unless it runs.
func (m *Model) startTick() tea.Cmd {
	if m.ticking {
		return nil
	}
	m.ticking = true
	return tui.Spinner()
}

// begin starts a load: the screen shows a spinner until fn answers or ctrl+c
// cancels it.
func (m *Model) begin(kind loadKind, screen, label string, fn func(ctx context.Context) (any, error)) tea.Cmd {
	ctx, cancel := context.WithCancel(context.Background())
	m.gen++
	gen := m.gen
	m.loading = &load{gen: gen, kind: kind, screen: screen, label: label, cancel: cancel}
	run := func() tea.Msg {
		v, err := fn(ctx)
		return loadedMsg{gen: gen, kind: kind, val: v, err: err}
	}
	return tea.Batch(run, m.startTick())
}

func (m *Model) cancelLoad() {
	if m.loading == nil || !m.loading.kind.cancelable() {
		return
	}
	m.loading.cancel()
	m.loading = nil
	m.say(RoleDim, "Cancelled.")
}

func (m *Model) startSummary() tea.Cmd {
	if m.sum.cancel != nil {
		m.sum.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.gen++
	gen := m.gen
	m.sum = summaryState{loading: true, gen: gen, cancel: cancel}
	be := m.opts.Backend
	run := func() tea.Msg {
		v, err := be.Summary(ctx)
		return loadedMsg{gen: gen, kind: loadSummary, val: v, err: err}
	}
	return tea.Batch(run, m.startTick())
}

// onLoaded takes the answer of a load, unless it is an old one.
func (m *Model) onLoaded(msg loadedMsg) tea.Cmd {
	if msg.kind == loadSummary {
		if msg.gen != m.sum.gen || !m.sum.loading {
			return nil
		}
		m.sum.cancel()
		m.sum.loading = false
		if msg.err != nil {
			m.sum.err = msg.err
		} else {
			s := msg.val.(Summary)
			m.sum.val = &s
		}
		return nil
	}
	if m.loading == nil || msg.gen != m.loading.gen {
		return nil
	}
	l := m.loading
	l.cancel()
	m.loading = nil
	if msg.err != nil && l.kind == loadExport {
		var ex *ExistsError
		if errors.As(msg.err, &ex) {
			m.exportReplace = true
			m.confirm = 0
			return nil
		}
	}
	if msg.err != nil {
		m.say(RoleFail, "%s", errText(msg.err))
		if l.kind == loadDelete {
			m.pop() // back to the project
		}
		return nil
	}
	switch l.kind {
	case loadStatus:
		st := msg.val.(Status)
		m.status = &st
		if m.screen != scStatus {
			m.push(scStatus)
			m.statusScroll = Scroller{}
		}
	case loadProjects:
		m.setProjects(msg.val.([]projects.ListItem))
	case loadProject:
		pv := msg.val.(ProjectView)
		m.detail = &pv
		m.detailScroll = Scroller{}
		m.push(scProject)
	case loadDoctor:
		m.doctor = sortDoctor(msg.val.([]DoctorRow))
		if m.screen != scDoctor {
			m.push(scDoctor)
			m.doctorScroll = Scroller{}
		}
	case loadLaunch:
		m.say(RoleOK, "%s", launchedText(msg.val.(Launched)))
	case loadExport:
		done := msg.val.(ExportDone)
		m.pop() // back to the project
		m.exportReplace = false
		m.say(RoleOK, "Exported %s to %s.", byteSize(done.Bytes), done.File)
	case loadDelete:
		name := m.detailName()
		m.removeFromList(m.detailID())
		m.pop() // the project
		m.pop() // the list, which is read again
		m.say(RoleOK, "Deleted %s.", name)
		return m.loadProjects()
	}
	return nil
}

// onSetupDone returns from the installer to the menu with a notice.
func (m *Model) onSetupDone(msg setupDoneMsg) tea.Cmd {
	m.screen, m.back = scMenu, nil
	switch msg.code {
	case 0:
		m.say(RoleOK, "Setup finished.")
	case 3:
		m.say(RoleDim, "Setup cancelled; nothing was changed.")
	default:
		m.say(RoleFail, "Setup did not finish (exit %d). Run Doctor.", msg.code)
	}
	return m.startSummary()
}

// configure hands the terminal to the installer.
func (m *Model) configure() tea.Cmd {
	if m.opts.ConfigureCmd == nil {
		m.say(RoleFail, "Setup cannot be started from here. Run `creality-slicer-mcp install`.")
		return nil
	}
	cmd := m.opts.ConfigureCmd()
	return tea.ExecProcess(cmd, func(err error) tea.Msg { return setupDoneMsg{code: exitCode(err)} })
}

// exitCode is the exit status in the error of a finished child.
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return 1
}

func (m *Model) cwd() string {
	if m.opts.Cwd != nil {
		return m.opts.Cwd()
	}
	if d, err := os.Getwd(); err == nil {
		return d
	}
	return "."
}

// --- loading screen ---

func (m *Model) spinner() string { return Paint(RoleCursor, SpinnerFrame(m.opts.Unicode, m.frame)) }

func (m *Model) loadingFrame() Frame {
	l := m.loading
	hints := []tui.Hint{{Key: "(ctrl+c", Label: "to cancel)"}}
	if !l.kind.cancelable() {
		hints = []tui.Hint{{Key: "please", Label: "wait"}}
	}
	return Frame{
		Screen: l.screen,
		Body:   WrapPrefixed(l.label, "  "+m.spinner()+" ", "    ", m.w-2),
		Hints:  hints,
	}
}

// cancelable says whether a load really stops when its context ends. Starting
// the app, exporting and deleting finish whatever the user does, so they are
// never shown as cancelled: the real outcome is always applied.
func (k loadKind) cancelable() bool {
	return k != loadLaunch && k != loadExport && k != loadDelete
}
