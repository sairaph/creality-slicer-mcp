// Package applaunch starts the Creality Print window on one file, detached
// from this process. It never passes an option, never talks to a running
// window and never closes one: the user owns every app window it opens.
package applaunch

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// Launcher starts the application on a file and returns the process id. It is
// an interface so tests use a fake and never start the real application.
type Launcher interface {
	Launch(exe, file string) (pid int, err error)
}

// ErrUnderTest is returned by the real launcher when it is called from a test
// binary: no test may start Creality Print.
var ErrUnderTest = errors.New("applaunch: the real launcher does not run under go test; inject a fake")

// Real starts the real application, detached.
type Real struct{}

// Launch runs exe with file as its only argument. The window is a new process
// in its own process group with no console, so it outlives this server and
// does not receive its console events. exe and file must be absolute paths of
// existing files.
func (Real) Launch(exe, file string) (int, error) {
	if testing.Testing() {
		return 0, ErrUnderTest
	}
	if err := check(exe, file); err != nil {
		return 0, err
	}
	cmd, err := startDetached(exe, Args(file))
	if err != nil {
		return 0, fmt.Errorf("could not start %s: %w", exe, err)
	}
	pid := cmd.Process.Pid
	// The window is not waited for by the caller, but its process is reaped when
	// it ends (unix) or its handle dropped (Windows), so nothing is left behind.
	reap(cmd)
	return pid, nil
}

// Args is the argument list the application is started with: the one file and
// nothing else. In particular never --single-instance (which would hand the
// file to a running window and could replace its project) or --datadir.
func Args(file string) []string { return []string{file} }

func check(exe, file string) error {
	for what, p := range map[string]string{"the application": exe, "the file": file} {
		if !filepath.IsAbs(p) {
			return fmt.Errorf("applaunch: %s %q is not an absolute path", what, p)
		}
		if st, err := os.Stat(p); err != nil || st.IsDir() {
			return fmt.Errorf("applaunch: %s %q is not a file", what, p)
		}
	}
	return nil
}

func homeDirOrEmpty() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}
