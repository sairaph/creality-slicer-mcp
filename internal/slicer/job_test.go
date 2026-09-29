package slicer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newJobs(t *testing.T, ex Exec) (*Jobs, string) {
	t.Helper()
	r, _ := newRunner(t, ex)
	dir := filepath.Join(t.TempDir(), "cache", "jobs") // does not exist yet
	return NewJobs(r, dir), dir
}

func waitDone(t *testing.T, j *Job) {
	t.Helper()
	select {
	case <-j.Done():
	case <-time.After(30 * time.Second):
		t.Fatal("job did not finish")
	}
}

func TestJobRunsAndHandsOutOutputOnce(t *testing.T) {
	release := make(chan struct{})
	ex := &fakeExec{fn: func(ctx context.Context, spec ExecSpec) (ExecResult, error) {
		for i := 0; i < 300; i++ {
			fmt.Fprintf(spec.Stdout, "line %d\n", i)
		}
		fmt.Fprintln(spec.Stdout, "OpenGL probe: noise")
		<-release
		writeOut(spec, "plate_1.gcode")
		return ExecResult{}, nil
	}}
	jobs, dir := newJobs(t, ex)
	j, err := jobs.Start(request(t), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !IsJobID(j.ID) || !strings.HasPrefix(j.ID, "slice-") || len(j.ID) != len("slice-")+8 {
		t.Errorf("id %q", j.ID)
	}
	if j.LogFile() != filepath.Join(dir, "job-"+j.ID+".log") {
		t.Errorf("log %q", j.LogFile())
	}
	// While running: state, elapsed, the last TailLines lines, the log path.
	var s Snapshot
	for i := 0; i < 3000; i++ { // 30 s bound: a cold, scanned test binary can be slow
		s = j.Snapshot()
		if strings.Contains(s.Output, "line 299") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if s.State != StateRunning || s.Result != nil || s.OutputFile != j.LogFile() || s.Removed {
		t.Fatalf("%+v", s)
	}
	if lines := strings.Split(s.Output, "\n"); len(lines) != TailLines || lines[len(lines)-1] != "line 299" || strings.Contains(s.Output, "OpenGL") {
		t.Errorf("tail has %d lines, last %q", len(lines), lines[len(lines)-1])
	}
	if jobs.Running() != 1 || len(jobs.Snapshots()) != 1 || jobs.Get(j.ID) != j || jobs.Get("slice-nope") != nil {
		t.Error("registry")
	}

	close(release)
	waitDone(t, j)
	s = j.Snapshot()
	if s.State != StateFinished || s.Result == nil || !s.Result.Outcome.OK || len(s.Result.GCodeFiles) != 1 || s.Error != "" {
		t.Fatalf("%+v", s)
	}
	if !s.Removed {
		t.Error("the first read of a finished job removes the log")
	}
	if _, err := os.Stat(j.LogFile()); !os.IsNotExist(err) {
		t.Error("log file must be gone")
	}
	again := j.Snapshot()
	if again.Removed || again.Output != s.Output || !strings.Contains(again.Output, "line 299") {
		t.Errorf("second read: %+v", again)
	}
	if jobs.Running() != 0 {
		t.Error("nothing should be running")
	}
}

func TestJobCancel(t *testing.T) {
	ex := &fakeExec{fn: blockUntilDone}
	jobs, _ := newJobs(t, ex)
	j, err := jobs.Start(request(t), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !j.Cancel() {
		t.Fatal("cancel of a running job must report true")
	}
	s := j.Snapshot()
	if s.State != StateCancelled || s.Result == nil || s.Result.Ending != EndCancelled {
		t.Fatalf("%+v", s)
	}
	if j.Cancel() {
		t.Error("cancel of a finished job must report false")
	}
}

func TestJobTimeout(t *testing.T) {
	ex := &fakeExec{fn: blockUntilDone}
	jobs, _ := newJobs(t, ex)
	j, err := jobs.Start(request(t), 60*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	waitDone(t, j)
	s := j.Snapshot()
	if s.State != StateFinished || s.Result == nil || s.Result.Ending != EndTimedOut {
		t.Fatalf("%+v", s)
	}
}

func TestJobStartFailureIsReportedInTheJob(t *testing.T) {
	ex := &fakeExec{fn: func(context.Context, ExecSpec) (ExecResult, error) { return ExecResult{}, fmt.Errorf("cannot start") }}
	jobs, _ := newJobs(t, ex)
	j, err := jobs.Start(request(t), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	waitDone(t, j)
	s := j.Snapshot()
	if s.State != StateFinished || s.Result != nil || s.Error != "cannot start" {
		t.Fatalf("%+v", s)
	}
}

func TestJobStartRejectsBadRequestsWithoutCreatingFiles(t *testing.T) {
	ex := &fakeExec{fn: blockUntilDone}
	jobs, dir := newJobs(t, ex)
	req := request(t)
	bad := req
	bad.Inputs = nil
	if _, err := jobs.Start(bad, time.Minute); err == nil {
		t.Error("invalid request")
	}
	missing := req
	missing.OutputDir = filepath.Join(req.OutputDir, "nope")
	if _, err := jobs.Start(missing, time.Minute); err == nil {
		t.Error("missing output dir")
	}
	if _, err := jobs.Start(req, 0); err == nil {
		t.Error("zero timeout")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("nothing may be created for a rejected request")
	}
	if ex.callCount() != 0 {
		t.Error("exec must not run")
	}
}

func TestStopAllEndsEveryJob(t *testing.T) {
	ex := &fakeExec{fn: blockUntilDone}
	jobs, _ := newJobs(t, ex)
	var started []*Job
	for i := 0; i < 3; i++ {
		j, err := jobs.Start(request(t), time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		started = append(started, j)
	}
	if jobs.Running() != 3 {
		t.Fatalf("running %d", jobs.Running())
	}
	jobs.StopAll()
	if jobs.Running() != 0 {
		t.Errorf("running %d after StopAll", jobs.Running())
	}
	for _, j := range started {
		if s := j.Snapshot(); s.State != StateCancelled {
			t.Errorf("%s: %+v", j.ID, s)
		}
	}
	snaps := jobs.Snapshots()
	if len(snaps) != 3 {
		t.Errorf("snapshots %d", len(snaps))
	}
}

func TestSweepRemovesOldLogsAndFinishedJobs(t *testing.T) {
	ex := &fakeExec{fn: func(context.Context, ExecSpec) (ExecResult, error) { return ExecResult{}, nil }}
	jobs, dir := newJobs(t, ex)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(dir, "job-slice-deadbeef.log")
	recent := filepath.Join(dir, "job-slice-cafebabe.log")
	foreign := filepath.Join(dir, "other.log")
	for _, p := range []string{old, recent, foreign} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	past := time.Now().Add(-KeepFor - time.Minute)
	for _, p := range []string{old, foreign} {
		os.Chtimes(p, past, past)
	}
	first, err := jobs.Start(request(t), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	waitDone(t, first)
	// Age the finished job past KeepFor, then start another to sweep.
	first.mu.Lock()
	first.finished = past
	first.mu.Unlock()
	second, err := jobs.Start(request(t), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	waitDone(t, second)
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Error("old log must be removed")
	}
	if _, err := os.Stat(recent); err != nil {
		t.Error("recent log must stay")
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Error("foreign file must stay")
	}
	if jobs.Get(first.ID) != nil || jobs.Get(second.ID) == nil {
		t.Error("finished jobs older than KeepFor are forgotten")
	}
}

func TestJobWithRealExecHelper(t *testing.T) {
	t.Setenv(helperEnv, "slice")
	r, _ := newRunner(t, OSExec{})
	r.Exe = os.Args[0]
	jobs := NewJobs(r, filepath.Join(t.TempDir(), "jobs"))
	j, err := jobs.Start(request(t), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	waitDone(t, j)
	s := j.Snapshot()
	if s.Result == nil || !s.Result.Outcome.OK || len(s.Result.GCodeFiles) != 1 {
		t.Fatalf("%+v", s)
	}
	if !strings.Contains(s.Output, "[error] something real went wrong") {
		t.Errorf("output %q", s.Output)
	}
}
