package slicer

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// helperRunner runs the test binary itself as a fake CrealityPrint through
// the real OSExec. It never starts the real slicer.
func helperRunner(t *testing.T, mode string) (*Runner, string) {
	t.Helper()
	t.Setenv(helperEnv, mode)
	r, dumps := newRunner(t, OSExec{})
	r.Exe = os.Args[0]
	return r, dumps
}

func TestOSExecFakeSlicerSuccess(t *testing.T) {
	r, _ := helperRunner(t, "slice")
	t.Setenv("HELPER_STDERR", "a warning on stderr")
	req := request(t)
	req.Plate = 2
	res, err := r.Run(context.Background(), req, WithTimeout(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if res.Ending != EndExited || res.ExitCode != 0 || !res.Outcome.OK {
		t.Fatalf("%+v", res)
	}
	if strings.Contains(res.Stdout, "OpenGL probe") || strings.Contains(res.Stdout, "[info]") || !strings.Contains(res.Stdout, "[error] something real went wrong") {
		t.Errorf("stdout %q", res.Stdout)
	}
	if res.Stderr != "a warning on stderr" {
		t.Errorf("stderr %q", res.Stderr)
	}
	if len(res.GCodeFiles) != 1 || res.GCodeFiles[0] != filepath.Join(req.OutputDir, "plate_2.gcode") {
		t.Errorf("plates %v", res.GCodeFiles)
	}
}

func TestOSExecNegativeExitCodesRoundTrip(t *testing.T) {
	windowsOnly(t)
	for _, tc := range []struct {
		exit string
		want int32
		name string
	}{
		{"-17", -17, "PROCESS_NOT_COMPATIBLE"},
		{"-2", -2, "INVALID_PARAMS"},
		{"-100", -100, "SLICING_ERROR"},
	} {
		r, _ := helperRunner(t, "slice")
		t.Setenv("HELPER_EXIT", tc.exit)
		res, err := r.Run(context.Background(), request(t), WithTimeout(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		if res.ExitCode != tc.want || res.Outcome.Name != tc.name || res.Crashed {
			t.Errorf("exit %s: %+v", tc.exit, res)
		}
	}
}

func TestOSExecInvalidOptionFromStderr(t *testing.T) {
	windowsOnly(t)
	r, _ := helperRunner(t, "slice")
	t.Setenv("HELPER_EXIT", "-2")
	t.Setenv("HELPER_STDERR", "Invalid option --frobnicate")
	res, err := r.Run(context.Background(), request(t), WithTimeout(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome.Name != "INVALID_OPTION" {
		t.Errorf("%+v", res.Outcome)
	}
}

func TestOSExecCrashAndDump(t *testing.T) {
	windowsOnly(t)
	r, dumps := helperRunner(t, "dump")
	t.Setenv("HELPER_DUMP", filepath.Join(dumps, "helper.dmp"))
	req := request(t)
	res, err := r.Run(context.Background(), req, WithTimeout(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Crashed || res.CrashName != "EXCEPTION_ACCESS_VIOLATION" || res.ExitCode != -1073741819 || res.Outcome.Code != OutcomeCrashed {
		t.Fatalf("%+v", res)
	}
	if len(res.CrashDumps) != 1 || res.CrashDumps[0] != filepath.Join(req.OutputDir, "crash", "helper.dmp") {
		t.Errorf("dumps %v", res.CrashDumps)
	}
}

func TestOSExecTimeout(t *testing.T) {
	r, _ := helperRunner(t, "sleep")
	start := time.Now()
	res, err := r.Run(context.Background(), request(t), WithTimeout(700*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if res.Ending != EndTimedOut || res.Outcome.Code != OutcomeTimedOut || res.Crashed {
		t.Fatalf("%+v", res)
	}
	if elapsed := time.Since(start); elapsed > 20*time.Second {
		t.Errorf("took %s: the process was not killed promptly", elapsed)
	}
}

func TestOSExecCancel(t *testing.T) {
	r, _ := helperRunner(t, "sleep")
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(700 * time.Millisecond); cancel() }()
	start := time.Now()
	res, err := r.Run(ctx, request(t))
	if err != nil {
		t.Fatal(err)
	}
	if res.Ending != EndCancelled || res.Outcome.Code != OutcomeCanceled {
		t.Fatalf("%+v", res)
	}
	if elapsed := time.Since(start); elapsed > 20*time.Second {
		t.Errorf("took %s", elapsed)
	}
}

func TestOSExecMissingProgram(t *testing.T) {
	r, _ := newRunner(t, OSExec{})
	r.Exe = filepath.Join(t.TempDir(), "no-such-slicer.exe")
	if _, err := r.Run(context.Background(), request(t)); err == nil {
		t.Error("a program that cannot start must be an error")
	}
}

// windowsOnly skips tests that depend on how Windows reports exit codes
// (0xFFFFFFEF for -17, 0xC0000005 for a crash).
func windowsOnly(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "windows" {
		t.Skip("Windows exit code semantics")
	}
}

// D-V73-2: under test the real exec path refuses every program but the test
// binary, so no test can start the real slicer.
func TestOSExecRefusesANonTestProgramUnderTest(t *testing.T) {
	for _, exe := range []string{`C:\Program Files\Creality\Creality Print 7.3\CrealityPrint.exe`, filepath.Join(t.TempDir(), "CrealityPrint.exe")} {
		_, err := OSExec{}.Run(context.Background(), ExecSpec{Exe: exe, Args: []string{"--slice", "1", "x.3mf"}})
		if err == nil || !strings.Contains(err.Error(), "refusing to start") {
			t.Errorf("%s: %v", exe, err)
		}
	}
	if err := refuseRealProgramUnderTest(os.Args[0]); err != nil {
		t.Errorf("the test binary itself must be allowed: %v", err)
	}
}
