package slicer

import (
	"bytes"
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

// fakeExec is an Exec that runs a function instead of a process.
type fakeExec struct {
	mu    sync.Mutex
	calls []ExecSpec
	fn    func(ctx context.Context, spec ExecSpec) (ExecResult, error)
}

func (f *fakeExec) Run(ctx context.Context, spec ExecSpec) (ExecResult, error) {
	f.mu.Lock()
	f.calls = append(f.calls, spec)
	f.mu.Unlock()
	return f.fn(ctx, spec)
}

func (f *fakeExec) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// blockUntilDone waits for the context like a slicer that never finishes; the
// real Exec ends the process tree and reports Killed.
func blockUntilDone(ctx context.Context, _ ExecSpec) (ExecResult, error) {
	<-ctx.Done()
	return ExecResult{ExitCode: 1, Killed: true}, nil
}

func newRunner(t *testing.T, ex Exec) (*Runner, string) {
	t.Helper()
	d, err := NewDialect("v72", testLookup)
	if err != nil {
		t.Fatal(err)
	}
	dumps := t.TempDir()
	return &Runner{Exe: "CrealityPrint.exe", Dialect: d, Exec: ex, TempDir: func() string { return dumps }}, dumps
}

func request(t *testing.T) SliceRequest {
	t.Helper()
	return SliceRequest{Inputs: []string{filepath.Join(t.TempDir(), "in.stl")}, OutputDir: t.TempDir(), Plate: 0}
}

func writeOut(spec ExecSpec, name string) {
	for i, a := range spec.Args {
		if a == "--outputdir" {
			_ = os.WriteFile(filepath.Join(spec.Args[i+1], name), []byte("; fake\n"), 0o644)
		}
	}
}

func TestRunSuccessCleansOutputAndFindsPlates(t *testing.T) {
	ex := &fakeExec{fn: func(_ context.Context, spec ExecSpec) (ExecResult, error) {
		fmt.Fprint(spec.Stdout, "OpenGL probe: gl 4.6\r\n[2026-09-29 07:03:52.123] [0x00001a2b] [info] loading\r\n[2026-09-29 07:03:52.124] [0x00001a2b] [warning] careful\r\n\r\nplain line\r\n")
		fmt.Fprint(spec.Stderr, "\xff bad utf8\n")
		writeOut(spec, "plate_2.gcode")
		writeOut(spec, "plate_10.gcode")
		writeOut(spec, "plate_1.gcode")
		writeOut(spec, "plate_1.gcode.tmp")
		writeOut(spec, "other.gcode")
		return ExecResult{}, nil
	}}
	r, _ := newRunner(t, ex)
	req := request(t)
	res, err := r.Run(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if res.Ending != EndExited || !res.Outcome.OK || res.Outcome.Code != OutcomeOK || res.Crashed {
		t.Fatalf("%+v", res)
	}
	if res.Stdout != "[2026-09-29 07:03:52.124] [0x00001a2b] [warning] careful\nplain line" {
		t.Errorf("stdout %q", res.Stdout)
	}
	if res.Stderr != "� bad utf8" {
		t.Errorf("stderr %q", res.Stderr)
	}
	want := []string{"plate_1.gcode", "plate_2.gcode", "plate_10.gcode"}
	if len(res.GCodeFiles) != 3 {
		t.Fatalf("plates %v", res.GCodeFiles)
	}
	for i, w := range want {
		if res.GCodeFiles[i] != filepath.Join(req.OutputDir, w) {
			t.Errorf("plate %d = %q, want %q", i, res.GCodeFiles[i], w)
		}
	}
	if res.Args[0] != "--slice" || ex.calls[0].Exe != "CrealityPrint.exe" {
		t.Errorf("args %q exe %q", res.Args, ex.calls[0].Exe)
	}
}

func TestRunKeepsErrorLinesAndClassifies(t *testing.T) {
	ex := &fakeExec{fn: func(_ context.Context, spec ExecSpec) (ExecResult, error) {
		fmt.Fprint(spec.Stdout, "[2026-09-29 07:03:52.123] [0x00001a2b] [error] process not compatible\n")
		fmt.Fprint(spec.Stderr, "the process is not compatible\n")
		return ExecResult{ExitCode: -17}, nil
	}}
	r, _ := newRunner(t, ex)
	res, err := r.Run(context.Background(), request(t))
	if err != nil {
		t.Fatal(err)
	}
	if res.Ending != EndExited || res.Outcome.OK || res.Outcome.Name != "PROCESS_NOT_COMPATIBLE" || res.ExitCode != -17 {
		t.Fatalf("%+v", res)
	}
	if !strings.Contains(res.Stdout, "[error]") || !strings.Contains(res.Outcome.Message, "the process is not compatible") {
		t.Errorf("stdout %q message %q", res.Stdout, res.Outcome.Message)
	}
	if len(res.GCodeFiles) != 0 {
		t.Errorf("plates %v", res.GCodeFiles)
	}
}

func TestRunIgnoresStalePlateFiles(t *testing.T) {
	ex := &fakeExec{fn: func(context.Context, ExecSpec) (ExecResult, error) { return ExecResult{ExitCode: -100}, nil }}
	r, _ := newRunner(t, ex)
	req := request(t)
	stale := filepath.Join(req.OutputDir, "plate_1.gcode")
	if err := os.WriteFile(stale, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Minute)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	res, err := r.Run(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.GCodeFiles) != 0 {
		t.Errorf("stale file reported: %v", res.GCodeFiles)
	}
}

func TestRunCrashCodeAndDumps(t *testing.T) {
	var dumpDir string
	ex := &fakeExec{fn: func(_ context.Context, spec ExecSpec) (ExecResult, error) {
		_ = os.WriteFile(filepath.Join(dumpDir, "new-crash.dmp"), []byte("MDMP"), 0o644)
		_ = os.WriteFile(filepath.Join(dumpDir, "notes.txt"), []byte("x"), 0o644)
		return ExecResult{ExitCode: -1073741819}, nil
	}}
	r, dumps := newRunner(t, ex)
	dumpDir = dumps
	// A dump that existed before the run stays where it is.
	if err := os.WriteFile(filepath.Join(dumps, "old.dmp"), []byte("MDMP"), 0o644); err != nil {
		t.Fatal(err)
	}
	req := request(t)
	res, err := r.Run(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Crashed || res.CrashName != "EXCEPTION_ACCESS_VIOLATION" || res.Outcome.Code != OutcomeCrashed || res.Ending != EndExited {
		t.Fatalf("%+v", res)
	}
	moved := filepath.Join(req.OutputDir, "crash", "new-crash.dmp")
	if len(res.CrashDumps) != 1 || res.CrashDumps[0] != moved {
		t.Fatalf("dumps %v", res.CrashDumps)
	}
	if _, err := os.Stat(moved); err != nil {
		t.Error(err)
	}
	if _, err := os.Stat(filepath.Join(dumps, "new-crash.dmp")); !os.IsNotExist(err) {
		t.Error("the dump must have been moved")
	}
	if _, err := os.Stat(filepath.Join(dumps, "old.dmp")); err != nil {
		t.Error("an older dump must stay")
	}
	if _, err := os.Stat(filepath.Join(dumps, "notes.txt")); err != nil {
		t.Error("non-dump files must stay")
	}
}

func TestRunTimeoutIsNotACrashOrCancel(t *testing.T) {
	ex := &fakeExec{fn: func(ctx context.Context, spec ExecSpec) (ExecResult, error) {
		fmt.Fprintln(spec.Stdout, "partial output")
		return blockUntilDone(ctx, spec)
	}}
	r, _ := newRunner(t, ex)
	res, err := r.Run(context.Background(), request(t), WithTimeout(50*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if res.Ending != EndTimedOut || res.Outcome.Code != OutcomeTimedOut || res.Outcome.Name != "timed_out" || res.Crashed || res.Outcome.OK {
		t.Fatalf("%+v", res)
	}
	if res.Stdout != "partial output" || res.Outcome.Hint == "" {
		t.Errorf("%+v", res)
	}
}

func TestRunCancelIsNotATimeout(t *testing.T) {
	ex := &fakeExec{fn: blockUntilDone}
	r, _ := newRunner(t, ex)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	res, err := r.Run(ctx, request(t))
	if err != nil {
		t.Fatal(err)
	}
	if res.Ending != EndCancelled || res.Outcome.Code != OutcomeCanceled || res.Outcome.Name != "cancelled" || res.Crashed {
		t.Fatalf("%+v", res)
	}
}

func TestRunParentDeadlineCountsAsCancel(t *testing.T) {
	// A deadline on the caller's context is the caller giving up, not the
	// run's own timeout.
	ex := &fakeExec{fn: blockUntilDone}
	r, _ := newRunner(t, ex)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	res, err := r.Run(ctx, request(t), WithTimeout(time.Minute))
	if err != nil || res.Ending != EndCancelled {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestRunDoesNotStartOnBadInput(t *testing.T) {
	ex := &fakeExec{fn: func(context.Context, ExecSpec) (ExecResult, error) { return ExecResult{}, nil }}
	r, _ := newRunner(t, ex)
	req := request(t)

	bad := req
	bad.Inputs = nil
	if _, err := r.Run(context.Background(), bad); !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("no input: %v", err)
	}
	missing := req
	missing.OutputDir = filepath.Join(req.OutputDir, "does-not-exist")
	if _, err := r.Run(context.Background(), missing); !errors.Is(err, ErrInvalidRequest) || !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("missing output dir: %v", err)
	}
	if _, err := r.Run(context.Background(), req, WithTimeout(-1)); !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("bad timeout: %v", err)
	}
	if ex.callCount() != 0 {
		t.Errorf("exec was called %d times", ex.callCount())
	}
}

func TestRunStartErrorPropagates(t *testing.T) {
	ex := &fakeExec{fn: func(context.Context, ExecSpec) (ExecResult, error) { return ExecResult{}, errors.New("cannot start") }}
	r, _ := newRunner(t, ex)
	if _, err := r.Run(context.Background(), request(t)); err == nil || err.Error() != "cannot start" {
		t.Errorf("%v", err)
	}
}

func TestRunWithLogStreams(t *testing.T) {
	ex := &fakeExec{fn: func(_ context.Context, spec ExecSpec) (ExecResult, error) {
		fmt.Fprintln(spec.Stdout, "to stdout")
		fmt.Fprintln(spec.Stderr, "to stderr")
		return ExecResult{}, nil
	}}
	r, _ := newRunner(t, ex)
	var log bytes.Buffer
	res, err := r.Run(context.Background(), request(t), WithLog(&log))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(log.String(), "to stdout") || !strings.Contains(log.String(), "to stderr") || res.Stdout != "to stdout" || res.Stderr != "to stderr" {
		t.Errorf("log %q res %+v", log.String(), res)
	}
}

func TestNewRunnerGuards(t *testing.T) {
	if _, err := NewRunner(Install{ReasonCode: ReasonNotInstalled, Reason: "gone"}, nil); err == nil {
		t.Error("not found must fail")
	}
	if _, err := NewRunner(Install{Found: true, Version: "7.3.0", Reason: "nope"}, nil); err == nil {
		t.Error("unsupported must fail")
	}
	r, err := NewRunner(Install{Found: true, Supported: true, Dialect: "v72", Exe: "x.exe"}, nil)
	if err != nil || r.Exe != "x.exe" || r.Dialect.Name() != "v72" {
		t.Errorf("%+v %v", r, err)
	}
	if _, ok := r.Exec.(OSExec); !ok {
		t.Errorf("default exec is %T", r.Exec)
	}
}

func TestCleanOutputAndTail(t *testing.T) {
	in := "OpenGL probe: a\r\n\r\n[2026-09-29 07:03:52.1] [0x1f] [trace] t\n[2026-09-29 07:03:52.1] [0x1f] [debug] d\n[2026-09-29 07:03:52.1] [0x1f] [error] e\n   \nkept\t\n"
	if got := cleanOutput([]byte(in)); got != "[2026-09-29 07:03:52.1] [0x1f] [error] e\nkept" {
		t.Errorf("%q", got)
	}
	tb := newTailBuffer(16)
	for i := 0; i < 50; i++ {
		fmt.Fprintf(tb, "line %02d\n", i)
	}
	got := string(tb.Bytes())
	if !strings.HasSuffix(got, "line 49\n") || len(got) > 16 || !strings.HasPrefix(got, "line ") {
		t.Errorf("tail %q", got)
	}
	small := newTailBuffer(100)
	small.Write([]byte("abc\ndef\n"))
	if string(small.Bytes()) != "abc\ndef\n" {
		t.Errorf("%q", small.Bytes())
	}
}
