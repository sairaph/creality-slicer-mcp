package main

// The wizard runs in the alternate screen, so that its footer can sit on the
// last row of the terminal. mcp-wizard's tui.Run has no such option, so this is
// its twin with one: the flow is driven in a Bubble Tea program on the
// terminal and the flow's exit code is returned (1 when the program could not
// run).

import (
	"context"
	"io"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/sairaph/mcp-wizard/flow"
)

func runFlow[T any](ctx context.Context, f *flow.Flow[T], in io.Reader, out io.Writer) int {
	if f == nil {
		return 1
	}
	program := tea.NewProgram(f.Model(), tea.WithContext(ctx), tea.WithAltScreen(),
		tea.WithInput(in), tea.WithOutput(out))
	if _, err := program.Run(); err != nil {
		return 1
	}
	return f.ExitCode()
}
