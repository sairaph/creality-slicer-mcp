//go:build !windows

package applaunch

import (
	"syscall"
	"testing"
	"time"
)

// A process that ends is collected: it does not stay a zombie of the server.
// The child is a shell that exits at once, not the application.
func TestReapCollectsAnExitedChild(t *testing.T) {
	cmd, err := startDetached("/bin/sh", []string{"-c", "exit 0"})
	if err != nil {
		t.Skipf("no /bin/sh: %v", err)
	}
	pid := cmd.Process.Pid
	reap(cmd)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		// kill(pid, 0) succeeds for a zombie and fails with ESRCH once it is gone.
		if err := syscall.Kill(pid, 0); err == syscall.ESRCH {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("process %d is still there: a zombie", pid)
}
