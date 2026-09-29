// Package clicmd implements creality-slicer-mcp's one-shot CLI commands. Every
// command is a Run<Name>(ctx, Deps, args) int function that calls the same
// business-logic functions the MCP tools (internal/mcpserver) call, so the two
// surfaces never disagree, and that takes everything it touches from Deps, so
// tests replace the seams instead of running processes.
//
// The commands are status and presets; main.go registers each one in
// oneShotCommands.
package clicmd

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/sairaph/mcp-wizard/tui"
)

// Deps bundles everything a one-shot command needs beyond its own arguments.
// A field a command needs but a test does not care about is left zero:
// withDefaults fills it with its production default.
type Deps struct {
	// IsInteractive reports whether this process can prompt for input.
	// Defaults to github.com/sairaph/mcp-wizard/tui.IsInteractive.
	IsInteractive func() bool

	// Tools are the functions the MCP tools run; nil means the real ones,
	// built from the environment settings on first use.
	Tools Tools

	Stdout io.Writer
	Stderr io.Writer
}

// withDefaults fills in every unset field with its production default. Every
// exported Run function calls this first, so a caller only sets the fields a
// test wants to fake.
func (d Deps) withDefaults() Deps {
	if d.IsInteractive == nil {
		d.IsInteractive = tui.IsInteractive
	}
	if d.Stdout == nil {
		d.Stdout = os.Stdout
	}
	if d.Stderr == nil {
		d.Stderr = os.Stderr
	}
	return d
}

// NewDefaultDeps builds the production Deps.
func NewDefaultDeps() Deps {
	return Deps{}.withDefaults()
}

// parseFlags parses args into fs, which a command builds with
// flag.NewFlagSet(name, flag.ContinueOnError). It returns the exit code to
// stop with, or -1 to carry on: 0 after -h or -help, 2 for a bad flag.
// Parse errors and usage go to stderr.
func parseFlags(fs *flag.FlagSet, args []string, stderr io.Writer) int {
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	return -1
}

// usageError reports a misuse of a command on stderr and returns exit code 2.
func usageError(stderr io.Writer, format string, args ...any) int {
	fmt.Fprintf(stderr, "  "+format+"\n", args...)
	return 2
}
