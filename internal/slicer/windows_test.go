//go:build windows

package slicer

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func processAlive(pid int) bool {
	h, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	ev, err := windows.WaitForSingleObject(h, 0)
	return err == nil && ev == uint32(windows.WAIT_TIMEOUT)
}

// The whole process tree must end on timeout: the fake slicer starts a child,
// and the child must not survive.
func TestOSExecKillsTheWholeProcessTree(t *testing.T) {
	r, _ := helperRunner(t, "tree")
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	t.Setenv("HELPER_PIDFILE", pidFile)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type outcome struct {
		res Result
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := r.Run(ctx, request(t), WithTimeout(time.Minute))
		done <- outcome{res, err}
	}()
	// Wait (bounded, generously: a cold scanned test binary is slow) until the
	// helper has started its child and written the pid, then end the run.
	var pid int
	deadline := time.Now().Add(30 * time.Second)
	for pid == 0 {
		if data, err := os.ReadFile(pidFile); err == nil {
			pid, _ = strconv.Atoi(strings.TrimSpace(string(data)))
		}
		if pid == 0 {
			if time.Now().After(deadline) {
				cancel()
				<-done
				t.Fatal("the helper did not record its child within 30 s")
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	cancel()
	out := <-done
	if out.err != nil || out.res.Ending != EndCancelled {
		t.Fatalf("%+v %v", out.res, out.err)
	}
	deadline = time.Now().Add(10 * time.Second)
	for processAlive(pid) {
		if time.Now().After(deadline) {
			windows.TerminateProcess(mustOpen(t, pid), 1)
			t.Fatalf("child process %d survived the kill of the tree", pid)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func mustOpen(t *testing.T, pid int) windows.Handle {
	t.Helper()
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestWindowsFileVersionReadsARealExe(t *testing.T) {
	v, err := winFileVersion{}.FileVersion(filepath.Join(os.Getenv("SystemRoot"), "System32", "kernel32.dll"))
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^\d+\.\d+\.\d+\.\d+`).MatchString(v) {
		t.Errorf("version %q", v)
	}
	if _, err := (winFileVersion{}).FileVersion(filepath.Join(t.TempDir(), "missing.exe")); err == nil {
		t.Error("a missing file must fail")
	}
}

func TestWindowsProcessListFindsThisProcess(t *testing.T) {
	pids, err := winProcesses{}.PIDsByName(filepath.Base(os.Args[0]))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range pids {
		if p == os.Getpid() {
			found = true
		}
	}
	if !found {
		t.Errorf("pid %d not in %v", os.Getpid(), pids)
	}
	if none, _ := (winProcesses{}).PIDsByName("no-such-process-name.exe"); len(none) != 0 {
		t.Errorf("%v", none)
	}
}

func TestWindowsPlatformIsSupported(t *testing.T) {
	if p := OSPlatform(); !p.Supported || p.Registry == nil || p.FileVersion == nil || p.Processes == nil || p.FS == nil {
		t.Errorf("%+v", p)
	}
}
