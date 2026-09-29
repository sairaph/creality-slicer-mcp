//go:build !windows

package slicer

import (
	"os"
	"os/exec"
	"syscall"
)

// processTree ends a child and everything it started by running the child in
// its own process group and killing the group.
type processTree struct{}

func newProcessTree() *processTree { return &processTree{} }

func (t *processTree) configure(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func (t *processTree) attach(*exec.Cmd) {}

func (t *processTree) kill(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		return cmd.Process.Kill()
	}
	return nil
}

func (t *processTree) close() {}

// exitCodeOf reports a signal death as the negative signal number.
func exitCodeOf(state *os.ProcessState) int32 {
	if ws, ok := state.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return -int32(ws.Signal())
	}
	return int32(state.ExitCode())
}
