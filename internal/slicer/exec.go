package slicer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// ExecSpec is one process to run.
type ExecSpec struct {
	Exe    string
	Args   []string
	Stdout io.Writer
	Stderr io.Writer
}

// ExecResult is how a process ended.
type ExecResult struct {
	// ExitCode is the signed exit code (a Windows process reports 0xFFFFFFFE
	// for -2 and 0xC0000005 for a crash; both come back as negative int32).
	ExitCode int32
	// Killed is true when Exec ended the process (and its children) because
	// the context finished first.
	Killed bool
}

// Exec runs a process to completion. It is an interface so tool-level code
// and tests use a fake and never start the real slicer.
//
// Run blocks until the process has ended. When ctx finishes first it ends the
// whole process tree, returns Killed true and a nil error. The error is only
// for a process that could not be started.
type Exec interface {
	Run(ctx context.Context, spec ExecSpec) (ExecResult, error)
}

// OSExec is the real Exec: a hidden-window child process whose whole tree is
// ended on timeout or cancellation.
type OSExec struct{}

// waitDelay keeps a child that inherited the pipes from blocking Wait.
const waitDelay = 2 * time.Second

// Run implements Exec.
func (OSExec) Run(ctx context.Context, spec ExecSpec) (ExecResult, error) {
	// Whatever the caller, a process is only started with an action flag and
	// without a forbidden flag (an argv without --slice would open the GUI).
	if err := CheckArgs(spec.Args); err != nil {
		return ExecResult{}, err
	}
	if err := refuseRealProgramUnderTest(spec.Exe); err != nil {
		return ExecResult{}, err
	}
	cmd := exec.CommandContext(ctx, spec.Exe, spec.Args...)
	cmd.Stdout, cmd.Stderr = spec.Stdout, spec.Stderr
	cmd.WaitDelay = waitDelay
	tree := newProcessTree()
	tree.configure(cmd)
	var killed atomic.Bool
	cmd.Cancel = func() error {
		killed.Store(true)
		return tree.kill(cmd)
	}
	if err := cmd.Start(); err != nil {
		tree.close()
		if ctx.Err() != nil {
			// Cancelled before launch: nothing ran, and that is not an error.
			return ExecResult{Killed: true}, nil
		}
		return ExecResult{}, fmt.Errorf("could not start %s: %w", spec.Exe, err)
	}
	tree.attach(cmd)
	err := cmd.Wait()
	tree.close()
	if cmd.ProcessState == nil {
		return ExecResult{}, fmt.Errorf("could not run %s: %w", spec.Exe, err)
	}
	// Other errors (the exit status itself, a pipe held open by a child past
	// WaitDelay) do not change what the process returned.
	if err != nil && !errors.Is(err, exec.ErrWaitDelay) {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) && !killed.Load() && ctx.Err() == nil {
			return ExecResult{}, fmt.Errorf("could not run %s: %w", spec.Exe, err)
		}
	}
	return ExecResult{ExitCode: exitCodeOf(cmd.ProcessState), Killed: killed.Load()}, nil
}

// refuseRealProgramUnderTest makes a test binary unable to start any program
// but itself (the helper-process pattern): a test can never launch the real
// Creality Print, on any machine. It only acts while testing.Testing() is true.
func refuseRealProgramUnderTest(exe string) error {
	if !testing.Testing() {
		return nil
	}
	clean := func(p string) string {
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
		return strings.ToLower(filepath.Clean(p))
	}
	self := []string{os.Args[0]}
	if e, err := os.Executable(); err == nil {
		self = append(self, e)
	}
	for _, s := range self {
		if clean(s) == clean(exe) {
			return nil
		}
	}
	return fmt.Errorf("refusing to start %q under test: a test binary may only start itself (helper process), never the real Creality Print", exe)
}
