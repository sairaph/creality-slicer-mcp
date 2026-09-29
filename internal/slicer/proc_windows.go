//go:build windows

package slicer

import (
	"os"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// processTree ends a child and everything it started. A Windows job object
// with KILL_ON_JOB_CLOSE does it for the descendants the job covers; taskkill
// /T /F walks the tree from the live parent and also reaches grandchildren
// that started before the child was put in the job.
type processTree struct {
	mu       sync.Mutex // guards job and assigned: attach and kill run on different goroutines
	job      windows.Handle
	assigned bool
}

func newProcessTree() *processTree {
	t := &processTree{}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return t
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(job)
		return t
	}
	t.job = job
	return t
}

// configure hides the console window of the child.
func (t *processTree) configure(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
}

// attach puts the started child into the job.
func (t *processTree) attach(cmd *exec.Cmd) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.job == 0 || cmd.Process == nil {
		return
	}
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		return
	}
	defer windows.CloseHandle(h)
	if windows.AssignProcessToJobObject(t.job, h) == nil {
		t.assigned = true
	}
}

// kill ends the child and its descendants. taskkill runs first, while the
// parent is alive and /T can walk its tree (this covers descendants started
// before the job assignment); then the job is terminated and the process
// killed.
func (t *processTree) kill(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	taskkillTree(cmd.Process.Pid)
	t.mu.Lock()
	if t.assigned && t.job != 0 {
		windows.TerminateJobObject(t.job, 1)
	}
	t.mu.Unlock()
	err := cmd.Process.Kill()
	if err != nil && err != os.ErrProcessDone {
		return err
	}
	return nil
}

// close releases the job; with KILL_ON_JOB_CLOSE anything still in it dies.
func (t *processTree) close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.job != 0 {
		windows.CloseHandle(t.job)
		t.job = 0
	}
}

func taskkillTree(pid int) {
	c := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid))
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	_ = c.Run()
}

func exitCodeOf(state *os.ProcessState) int32 {
	return int32(uint32(state.ExitCode()))
}
