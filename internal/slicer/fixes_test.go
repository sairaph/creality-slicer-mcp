package slicer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func writePlate(dir string, n int) string {
	p := filepath.Join(dir, fmt.Sprintf("plate_%d.gcode", n))
	_ = os.WriteFile(p, []byte("; gcode\n"), 0o644)
	return p
}

// A run that fails leaves the earlier G-code where it was, and never reports it
// as its own result: the files of the folder change only when a run succeeds.
func TestFailedRunKeepsTheEarlierPlateFilesAndNeverReportsThem(t *testing.T) {
	wrote := true
	ex := &fakeExec{fn: func(_ context.Context, spec ExecSpec) (ExecResult, error) {
		if wrote {
			writeOut(spec, "plate_1.gcode")
		}
		return ExecResult{}, nil
	}}
	r, _ := newRunner(t, ex)
	req := request(t)
	other := filepath.Join(req.OutputDir, "notes.txt")
	os.WriteFile(other, []byte("keep"), 0o644)

	first, err := r.Run(context.Background(), req)
	if err != nil || !first.Outcome.OK || len(first.GCodeFiles) != 1 {
		t.Fatalf("%+v %v", first, err)
	}
	// Second run into the same folder, immediately, writes nothing at all.
	wrote = false
	second, err := r.Run(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.GCodeFiles) != 0 || second.Outcome.OK || second.Outcome.Name != OutcomeNoOutput || second.Outcome.Code != OutcomeFailed {
		t.Fatalf("a run that wrote nothing must not report the first run file: %+v", second)
	}
	if !strings.Contains(second.Outcome.Hint, "without writing G-code") {
		t.Errorf("hint %q", second.Outcome.Hint)
	}
	if b, err := os.ReadFile(filepath.Join(req.OutputDir, "plate_1.gcode")); err != nil || string(b) != "; fake\n" {
		t.Errorf("the earlier plate file must stay after a failed run: %q %v", b, err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Error("files that are not plate G-code must stay")
	}
}

// A killed (cancelled or timed out) run, also a plate 0 run that wrote some of
// its plates first, leaves no mix of new and old files.
func TestKilledRunLeavesNoMixOfNewAndOldFiles(t *testing.T) {
	ex := &fakeExec{fn: func(_ context.Context, spec ExecSpec) (ExecResult, error) {
		writeOut(spec, "plate_1.gcode") // the first plate was written, then the run was killed
		return ExecResult{Killed: true}, nil
	}}
	r, _ := newRunner(t, ex)
	req := request(t)
	for _, n := range []string{"plate_1.gcode", "plate_2.gcode"} {
		os.WriteFile(filepath.Join(req.OutputDir, n), []byte("old"), 0o644)
	}
	res, err := r.Run(context.Background(), req)
	if err != nil || res.Ending != EndTimedOut || len(res.GCodeFiles) != 0 {
		t.Fatalf("%v %+v", err, res)
	}
	for _, n := range []string{"plate_1.gcode", "plate_2.gcode"} {
		if b, _ := os.ReadFile(filepath.Join(req.OutputDir, n)); string(b) != "old" {
			t.Errorf("%s = %q after a killed run, want the old file", n, b)
		}
	}
}

// With an empty plate the slicer is started once per plate that has objects
// (the 7.3 command line fails -50 for plate 0 when any plate is empty).
func TestPlatesListSlicesOnePlateAtATime(t *testing.T) {
	var plates []string
	ex := &fakeExec{fn: func(_ context.Context, spec ExecSpec) (ExecResult, error) {
		for i, a := range spec.Args {
			if a == "--slice" {
				plates = append(plates, spec.Args[i+1])
				if spec.Args[i+1] == "0" {
					return ExecResult{ExitCode: -50}, nil // an empty plate somewhere
				}
				writeOut(spec, "plate_"+spec.Args[i+1]+".gcode")
			}
		}
		return ExecResult{}, nil
	}}
	r, _ := newRunner(t, ex)
	req := request(t)
	req.Plate, req.Plates = 0, []int{1, 3}
	res, err := r.Run(context.Background(), req)
	if err != nil || !res.Outcome.OK {
		t.Fatalf("%v %+v", err, res)
	}
	if strings.Join(plates, ",") != "1,3" || len(res.GCodeFiles) != 2 || filepath.Base(res.GCodeFiles[0]) != "plate_1.gcode" || filepath.Base(res.GCodeFiles[1]) != "plate_3.gcode" {
		t.Fatalf("plates run %v, files %v", plates, res.GCodeFiles)
	}
	// The first failing plate stops the run and nothing is moved into the folder.
	os.Remove(filepath.Join(req.OutputDir, "plate_1.gcode"))
	os.Remove(filepath.Join(req.OutputDir, "plate_3.gcode"))
	plates = nil
	ex.fn = func(_ context.Context, spec ExecSpec) (ExecResult, error) {
		for i, a := range spec.Args {
			if a == "--slice" {
				plates = append(plates, spec.Args[i+1])
				if spec.Args[i+1] == "3" {
					return ExecResult{ExitCode: -17}, nil
				}
				writeOut(spec, "plate_"+spec.Args[i+1]+".gcode")
			}
		}
		return ExecResult{}, nil
	}
	res, err = r.Run(context.Background(), req)
	if err != nil || res.Outcome.OK || len(res.GCodeFiles) != 0 {
		t.Fatalf("%v %+v", err, res)
	}
	if _, err := os.Stat(filepath.Join(req.OutputDir, "plate_1.gcode")); err == nil {
		t.Error("plate 1 was moved into the folder although plate 3 failed")
	}
}

func TestNoOutputIsJudgedPerRequestedPlate(t *testing.T) {
	var write []int
	ex := &fakeExec{fn: func(_ context.Context, spec ExecSpec) (ExecResult, error) {
		for _, n := range write {
			writeOut(spec, fmt.Sprintf("plate_%d.gcode", n))
		}
		return ExecResult{}, nil
	}}
	r, _ := newRunner(t, ex)
	for _, tc := range []struct {
		plate int
		write []int
		ok    bool
	}{
		{2, []int{2}, true},
		{2, []int{1}, false},
		{2, nil, false},
		{0, []int{1, 3}, true},
		{0, nil, false},
		{1, []int{1, 2}, true},
	} {
		req := request(t)
		req.Plate = tc.plate
		write = tc.write
		res, err := r.Run(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		if res.Outcome.OK != tc.ok || (!tc.ok && res.Outcome.Name != OutcomeNoOutput) {
			t.Errorf("plate %d wrote %v: %+v", tc.plate, tc.write, res.Outcome)
		}
	}
	// NO_OUTPUT only replaces a success: a failure keeps its own classification.
	ex.fn = func(context.Context, ExecSpec) (ExecResult, error) { return ExecResult{ExitCode: -17}, nil }
	res, _ := r.Run(context.Background(), request(t))
	if res.Outcome.Name != "PROCESS_NOT_COMPATIBLE" {
		t.Errorf("%+v", res.Outcome)
	}
}

func TestCancelledBeforeLaunchIsAResultAndTouchesNothing(t *testing.T) {
	ex := &fakeExec{fn: func(context.Context, ExecSpec) (ExecResult, error) { return ExecResult{}, nil }}
	r, _ := newRunner(t, ex)
	req := request(t)
	old := writePlate(req.OutputDir, 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, err := r.Run(ctx, req)
	if err != nil || res.Ending != EndCancelled || res.Outcome.Code != OutcomeCanceled || res.Crashed {
		t.Fatalf("%+v %v", res, err)
	}
	if ex.callCount() != 0 {
		t.Error("nothing may be launched")
	}
	if _, err := os.Stat(old); err != nil {
		t.Error("a cancelled request must not clear the output folder")
	}
	// The real launcher agrees when it is handed a cancelled context directly:
	// it starts nothing (os.Args[0] would only re-run this test binary).
	got, err := OSExec{}.Run(ctx, ExecSpec{Exe: os.Args[0], Args: []string{"--slice", "0"}})
	if err != nil || !got.Killed {
		t.Errorf("%+v %v", got, err)
	}
}

func TestJobCancelledRightAfterStartHasAResult(t *testing.T) {
	ex := &fakeExec{fn: blockUntilDone}
	jobs, _ := newJobs(t, ex)
	j, err := jobs.Start(request(t), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !j.Cancel() {
		t.Fatal("cancel")
	}
	s := j.Snapshot()
	if s.State != StateCancelled || s.Result == nil || s.Result.Ending != EndCancelled {
		t.Errorf("%+v", s)
	}
}

func TestOSExecRefusesArgvWithoutActionOrWithForbiddenFlags(t *testing.T) {
	for _, args := range [][]string{
		{"--help"},
		{},
		{"--export-3mf", "x"},
		{"--slice", "0", "--export-stl"},
		{"--slice", "0", "--datadir", "d"},
	} {
		// The executable does not exist: if the flags were not checked first
		// the error would be a launch error, and nothing would be refused.
		res, err := OSExec{}.Run(context.Background(), ExecSpec{Exe: filepath.Join(t.TempDir(), "never-started.exe"), Args: args})
		if !errors.Is(err, ErrInvalidRequest) || res.Killed {
			t.Errorf("%q: %v %+v", args, err, res)
		}
	}
}

func TestCrashDumpsAreOnlyCollectedForACrash(t *testing.T) {
	var dumpDir string
	code := int32(-100)
	ex := &fakeExec{fn: func(_ context.Context, spec ExecSpec) (ExecResult, error) {
		_ = os.WriteFile(filepath.Join(dumpDir, "during.dmp"), []byte("MDMP"), 0o644)
		// A dump that appears with an old time stamp is not from this run.
		old := filepath.Join(dumpDir, "older.dmp")
		_ = os.WriteFile(old, []byte("MDMP"), 0o644)
		past := time.Now().Add(-time.Hour)
		_ = os.Chtimes(old, past, past)
		return ExecResult{ExitCode: code}, nil
	}}
	r, dumps := newRunner(t, ex)
	dumpDir = dumps

	req := request(t)
	res, err := r.Run(context.Background(), req)
	if err != nil || res.Crashed || len(res.CrashDumps) != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	if _, err := os.Stat(filepath.Join(dumps, "during.dmp")); err != nil {
		t.Error("a run that did not crash must leave the temp folder alone")
	}
	if _, err := os.Stat(filepath.Join(req.OutputDir, "crash")); !os.IsNotExist(err) {
		t.Error("no crash folder without a crash")
	}
	os.Remove(filepath.Join(dumps, "during.dmp"))
	os.Remove(filepath.Join(dumps, "older.dmp"))

	code = -1073741819
	req2 := request(t)
	res, err = r.Run(context.Background(), req2)
	if err != nil || !res.Crashed {
		t.Fatalf("%+v %v", res, err)
	}
	if len(res.CrashDumps) != 1 || res.CrashDumps[0] != filepath.Join(req2.OutputDir, "crash", "during.dmp") {
		t.Errorf("only the dump written during the run belongs to it: %v", res.CrashDumps)
	}
	if _, err := os.Stat(filepath.Join(dumps, "older.dmp")); err != nil {
		t.Error("a dump from before the run window stays")
	}
	if res.CrashDumpsAmbiguous {
		t.Error("a lone run is not ambiguous")
	}
}

func TestOverlappingCrashesAreFlaggedAmbiguousAndDumpsClaimedOnce(t *testing.T) {
	var mu sync.Mutex
	arrived := 0
	both := make(chan struct{})
	dumpRoot := t.TempDir()
	// Both dumps are completely written before either run returns from the
	// fake slicer, so neither run can see the other's dump half written.
	var written sync.WaitGroup
	written.Add(2)
	mk := func() *Runner {
		ex := &fakeExec{fn: func(_ context.Context, spec ExecSpec) (ExecResult, error) {
			mu.Lock()
			arrived++
			n := arrived
			if arrived == 2 {
				close(both)
			}
			mu.Unlock()
			<-both // both runs are inside their window at the same time
			_ = os.WriteFile(filepath.Join(dumpRoot, fmt.Sprintf("crash-%d.dmp", n)), []byte("MDMP"), 0o644)
			written.Done()
			written.Wait()
			return ExecResult{ExitCode: -1073741819}, nil
		}}
		r, _ := newRunner(t, ex)
		r.TempDir = func() string { return dumpRoot }
		return r
	}
	var wg sync.WaitGroup
	results := make([]Result, 2)
	reqs := []SliceRequest{request(t), request(t)}
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res, err := mk().Run(context.Background(), reqs[i])
			if err != nil {
				t.Error(err)
			}
			results[i] = res
		}(i)
	}
	wg.Wait()
	total := len(results[0].CrashDumps) + len(results[1].CrashDumps)
	if total != 2 {
		t.Errorf("each dump is claimed exactly once: %v %v", results[0].CrashDumps, results[1].CrashDumps)
	}
	if !results[0].CrashDumpsAmbiguous || !results[1].CrashDumpsAmbiguous {
		t.Errorf("overlapping runs must be flagged: %v %v", results[0].CrashDumpsAmbiguous, results[1].CrashDumpsAmbiguous)
	}
	left, _ := filepath.Glob(filepath.Join(dumpRoot, "*.dmp"))
	if len(left) != 0 {
		t.Errorf("dumps left behind: %v", left)
	}
}

func TestMoveFileReportsTheCopy(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "a.dmp"), filepath.Join(dir, "b.dmp")
	os.WriteFile(src, []byte("MDMP"), 0o644)
	if !moveFile(src, dst) {
		t.Fatal("move failed")
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Error("source must be gone")
	}
	if moveFile(filepath.Join(dir, "missing"), filepath.Join(dir, "c")) {
		t.Error("a missing source cannot be moved")
	}
}

func TestTimeoutAboveTheMaximumAndLogFolderAreRejected(t *testing.T) {
	ex := &fakeExec{fn: func(_ context.Context, spec ExecSpec) (ExecResult, error) {
		writeOut(spec, "plate_1.gcode")
		return ExecResult{}, nil
	}}
	r, _ := newRunner(t, ex)
	req := request(t)
	if _, err := r.Run(context.Background(), req, WithTimeout(MaxTimeout+time.Second)); !errors.Is(err, ErrInvalidRequest) || !strings.Contains(err.Error(), "maximum") {
		t.Errorf("%v", err)
	}
	if _, err := r.Run(context.Background(), req, WithTimeout(MaxTimeout)); err != nil {
		t.Errorf("the maximum itself is allowed: %v", err)
	}
	bad := req
	bad.LogFile = filepath.Join(req.OutputDir, "no-such-folder", "slice.log")
	if _, err := r.Run(context.Background(), bad); !errors.Is(err, ErrInvalidRequest) || !strings.Contains(err.Error(), "log file") {
		t.Errorf("%v", err)
	}
	good := req
	good.LogFile = filepath.Join(req.OutputDir, "slice.log")
	if _, err := r.Run(context.Background(), good); err != nil {
		t.Errorf("%v", err)
	}
	jobs := NewJobs(r, filepath.Join(t.TempDir(), "jobs"))
	if _, err := jobs.Start(req, MaxTimeout+time.Minute); !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("%v", err)
	}
	if _, err := jobs.Start(bad, time.Minute); !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("%v", err)
	}
}

func TestJobTagAndOnDoneRunBeforeTheJobReportsFinished(t *testing.T) {
	ex := &fakeExec{fn: func(_ context.Context, spec ExecSpec) (ExecResult, error) {
		writeOut(spec, "plate_1.gcode")
		return ExecResult{}, nil
	}}
	jobs, _ := newJobs(t, ex)
	type tag struct {
		project string
		plate   int
	}
	release := make(chan struct{})
	started := make(chan struct{})
	var seenState string
	var seen Result
	var seenErr error
	j, err := jobs.Start(request(t), time.Minute, WithTag(tag{"p1", 2}), WithOnDone(func(j *Job, res Result, err error) {
		close(started)
		seen, seenErr = res, err
		<-release // post-processing takes a while
		seenState = j.Snapshot().State
	}))
	if err != nil {
		t.Fatal(err)
	}
	if j.Tag != (tag{"p1", 2}) {
		t.Errorf("%v", j.Tag)
	}
	// Wait until the slice itself is over (the DoneFunc is blocked on release).
	select {
	case <-started:
	case <-time.After(30 * time.Second):
		t.Fatal("the DoneFunc never ran")
	}
	select {
	case <-j.Done():
		t.Fatal("the job must not be done while its DoneFunc runs")
	default:
	}
	if s := j.Snapshot(); s.State != StateRunning || s.Tag != (tag{"p1", 2}) {
		t.Errorf("still running while post-processing: %+v", s)
	}
	close(release)
	waitDone(t, j)
	s := j.Snapshot()
	if s.State != StateFinished || s.Result == nil || !s.Result.Outcome.OK || seenState != StateRunning || seenErr != nil || !seen.Outcome.OK || len(seen.GCodeFiles) != 1 {
		t.Errorf("%+v state during hook %q", s, seenState)
	}
	if snaps := jobs.Snapshots(); len(snaps) != 1 || snaps[0].Tag != (tag{"p1", 2}) {
		t.Errorf("%+v", snaps)
	}
}

func TestOnDoneSeesStartFailuresAndPanicsAreContained(t *testing.T) {
	ex := &fakeExec{fn: func(context.Context, ExecSpec) (ExecResult, error) { return ExecResult{}, errors.New("cannot start") }}
	jobs, _ := newJobs(t, ex)
	var got error
	j, err := jobs.Start(request(t), time.Minute, WithOnDone(func(j *Job, res Result, err error) { got = err }))
	if err != nil {
		t.Fatal(err)
	}
	waitDone(t, j)
	if got == nil || got.Error() != "cannot start" {
		t.Errorf("%v", got)
	}
	if s := j.Snapshot(); s.Error != "cannot start" || s.Result != nil {
		t.Errorf("%+v", s)
	}

	ok := &fakeExec{fn: func(context.Context, ExecSpec) (ExecResult, error) { return ExecResult{}, nil }}
	jobs2, _ := newJobs(t, ok)
	j2, err := jobs2.Start(request(t), time.Minute, WithOnDone(func(*Job, Result, error) { panic("boom") }))
	if err != nil {
		t.Fatal(err)
	}
	waitDone(t, j2)
	s := j2.Snapshot()
	if s.State != StateFinished || !strings.Contains(s.Error, "boom") || s.Result == nil {
		t.Errorf("a panic in the hook must be reported, not crash the server: %+v", s)
	}
}

func TestJobStateComesFromHowTheRunEnded(t *testing.T) {
	// A run that finished normally stays finished even if Cancel was asked in
	// the same instant: Cancel on a finished job is a no-op.
	ex := &fakeExec{fn: func(_ context.Context, spec ExecSpec) (ExecResult, error) {
		writeOut(spec, "plate_1.gcode")
		return ExecResult{}, nil
	}}
	jobs, _ := newJobs(t, ex)
	j, err := jobs.Start(request(t), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	waitDone(t, j)
	if j.Cancel() {
		t.Error("cancel of a finished job")
	}
	if s := j.Snapshot(); s.State != StateFinished {
		t.Errorf("%+v", s)
	}
	// Cancel racing a run that ends by itself: whatever the interleaving, the
	// state matches the Result.
	for i := 0; i < 20; i++ {
		j, err := jobs.Start(request(t), time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		j.Cancel()
		waitDone(t, j)
		s := j.Snapshot()
		want := StateFinished
		if s.Result != nil && s.Result.Ending == EndCancelled {
			want = StateCancelled
		}
		if s.State != want {
			t.Errorf("state %s with ending %v", s.State, s.Result)
		}
	}
}

// A dump that cannot be removed after the copy (the crash reporter still has it
// open on Windows) is not claimed: the copy goes, the original stays for its
// owner, so two overlapping runs never claim one dump.
func TestMoveFileDoesNotClaimAFileItCannotRemove(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "a.dmp"), filepath.Join(dir, "b.dmp")
	os.WriteFile(src, []byte("MDMP"), 0o644)
	oldRename, oldRemove := renameFile, removeFile
	defer func() { renameFile, removeFile = oldRename, oldRemove }()
	renameFile = func(a, b string) error { return errors.New("sharing violation") }
	removeFile = func(p string) error {
		if p == src {
			return errors.New("sharing violation")
		}
		return os.Remove(p)
	}
	if moveFile(src, dst) {
		t.Fatal("a file that could not be removed was claimed")
	}
	if _, err := os.Stat(src); err != nil {
		t.Error("the original must stay")
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Error("the copy must not stay")
	}
	// When the removal works, the copy is claimed and the source is gone.
	removeFile = os.Remove
	if !moveFile(src, dst) {
		t.Fatal("copy and remove failed")
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Error("source must be gone")
	}
}
