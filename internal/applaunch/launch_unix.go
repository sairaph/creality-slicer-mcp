//go:build !windows

package applaunch

import (
	"os/exec"
	"syscall"
)

// reap waits for the started process in the background, so when the user
// closes the window the process is collected and does not stay as a zombie of
// this server.
func reap(cmd *exec.Cmd) {
	go func() { _ = cmd.Wait() }()
}

// startDetached starts exe with args in a new session, so signals or a
// process-group kill aimed at this server do not reach the window. The working
// directory is the user's home.
func startDetached(exe string, args []string) (*exec.Cmd, error) {
	cmd := exec.Command(exe, args...)
	cmd.Dir = homeDirOrEmpty()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd, nil
}
