package main

// The interactive app opens when the binary is run bare in a terminal. It
// starts as a menu. Menu items either run a report into a scrollable detail
// view or (later) open an interactive page. The same report functions back
// the CLI, so the app and the commands never disagree.

import (
	"bytes"
	"context"
	"io"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/sairaph/mcp-wizard/app"
	"github.com/sairaph/mcp-wizard/app/detail"
	"github.com/sairaph/mcp-wizard/app/menu"
	"github.com/sairaph/mcp-wizard/async"

	"github.com/sairaph/creality-slicer-mcp/internal/clicmd"
	"github.com/sairaph/creality-slicer-mcp/internal/domain"
)

const (
	stepMenu app.Step = iota
	stepReport
)

type appState struct {
	app.AppModel
	ctx    context.Context
	menu   *menu.Model
	detail *detail.Model
}

// menuItems are the app's menu entries. More join this list as the reports
// they open exist.
func menuItems() []menu.Item {
	return []menu.Item{
		{Label: "Run doctor", Action: "doctor"},
		{Label: "Show slicer status", Action: "status"},
		{Label: "Recent projects", Action: "projects"},
		{Label: "Quit", Action: "quit"},
	}
}

// reportFor returns the title and report function of a menu action, or false
// when the action is not a report.
func (m *appState) reportFor(action string) (title string, work func(io.Writer), ok bool) {
	switch action {
	case "doctor":
		return "Doctor", func(w io.Writer) { newDoctor().Run(m.ctx, w) }, true
	case "status":
		return "Slicer status", func(w io.Writer) { clicmd.WriteStatus(m.ctx, clicmd.NewDefaultDeps(), false, w) }, true
	case "projects":
		return "Recent projects", func(w io.Writer) { clicmd.WriteRecentProjects(m.ctx, clicmd.NewDefaultDeps(), w) }, true
	}
	return "", nil, false
}

func runApp(ctx context.Context) int {
	s := &appState{ctx: ctx}
	s.menu = menu.New(domain.BinaryName+" "+version, menuItems)
	return app.Run(ctx, s, app.Options{Title: domain.BinaryName, Version: version})
}

func (m *appState) Init() tea.Cmd { return m.menu.Init() }

func (m *appState) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if handled, cmd := m.HandleGlobalKeys(msg); handled {
		return m, cmd
	}

	switch msg := msg.(type) {
	case app.ActionMsg:
		switch msg.Source {
		case "menu":
			action, _ := msg.Data.(string)
			if msg.Value == "quit" || action == "quit" {
				m.Quit = true
				return m, tea.Quit
			}
			if title, work, ok := m.reportFor(action); ok {
				m.Step = stepReport
				m.Status = "Working..."
				m.detail = detail.New(title, m.Status)
				return m, tea.Batch(m.detail.Init(), async.Load(func() (string, error) {
					var buf bytes.Buffer
					work(&buf)
					return buf.String(), nil
				}))
			}
		case "detail":
			if msg.Value == "back" {
				m.Step = stepMenu
				return m, nil
			}
		}
		return m, nil

	case async.Result[string]:
		m.Status = ""
		if msg.Err != nil {
			m.detail.SetContent("Failed: " + msg.Err.Error())
		} else {
			m.detail.SetContent(msg.Value)
		}
		return m, nil
	}

	switch m.Step {
	case stepMenu:
		return m, m.menu.Update(msg)
	case stepReport:
		if m.detail != nil {
			return m, m.detail.Update(msg)
		}
	}
	return m, nil
}

func (m *appState) View() string {
	if m.Step == stepReport && m.detail != nil {
		return m.detail.View()
	}
	return m.menu.View()
}
