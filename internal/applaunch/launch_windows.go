//go:build windows

package applaunch

import (
	"errors"
	"os/exec"
	"syscall"
)

const (
	createNewProcessGroup  = 0x00000200
	detachedProcess        = 0x00000008
	createBreakawayFromJob = 0x01000000
	normalPriorityClass    = 0x00000020

	// errorAccessDenied is what CreateProcess returns for
	// CREATE_BREAKAWAY_FROM_JOB when the job object this process is in does not
	// allow breakaway.
	errorAccessDenied syscall.Errno = 5
)

// reap drops the handle of the started process: Windows keeps no zombie, but
// the handle would stay open as long as this server runs.
func reap(cmd *exec.Cmd) {
	_ = cmd.Process.Release()
}

// startDetached starts exe with args in a new process group, without a
// console, and broken away from the job object an AI client's launcher may have
// put this server in (a kill-on-close job would take the window down with the
// server). A job that forbids breakaway answers access denied; the start is
// then retried without CREATE_BREAKAWAY_FROM_JOB. The working directory is the
// user's home. The priority class is set to normal so the window the user
// works in never inherits a lower one.
func startDetached(exe string, args []string) (*exec.Cmd, error) {
	dir := homeDirOrEmpty()
	start := func(flags uint32) (*exec.Cmd, error) {
		cmd := exec.Command(exe, args...)
		cmd.Dir = dir
		cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: flags}
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		return cmd, nil
	}
	cmd, err := start(createNewProcessGroup | detachedProcess | createBreakawayFromJob | normalPriorityClass)
	if err == nil {
		return cmd, nil
	}
	var errno syscall.Errno
	if !errors.As(err, &errno) || errno != errorAccessDenied {
		return nil, err
	}
	return start(createNewProcessGroup | detachedProcess | normalPriorityClass)
}
