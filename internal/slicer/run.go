package slicer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// MaxTimeout is the longest a single run may take (an anti-hang guard, not a
// product limit): the slicer never legitimately needs longer, and the tool
// layer allows up to this per call.
const MaxTimeout = 30 * time.Minute

// DefaultTimeout applies when a run gives no timeout.
const DefaultTimeout = MaxTimeout

// Ending is how a run ended. The three endings are never conflated: a run the
// caller cancelled and a run that timed out are not crashes.
type Ending string

// Endings.
const (
	EndExited    Ending = "exited"
	EndTimedOut  Ending = "timed_out"
	EndCancelled Ending = "cancelled"
)

// OutcomeNoOutput names the failure of a run that exited 0 without writing the
// G-code it was asked for.
const OutcomeNoOutput = "NO_OUTPUT"

// Result is what one slice run produced.
type Result struct {
	Ending    Ending
	ExitCode  int32 // meaningful for EndExited (and crashes)
	Outcome   Outcome
	Crashed   bool   // exited with a native crash code
	CrashName string // EXCEPTION_ACCESS_VIOLATION, ...
	// Stdout and Stderr are the cleaned tails of the streams (launcher noise
	// and trace/debug/info log lines removed, error lines kept).
	Stdout   string
	Stderr   string
	Duration time.Duration
	Args     []string // the argv that was run (without the program)
	// GCodeFiles are the plate_N.gcode files present in OutputDir after the
	// run, ordered by plate number. The Runner deletes every plate_*.gcode of
	// OutputDir before it starts the slicer, so these are this run's files.
	GCodeFiles []string
	// CrashDumps are the .dmp files a crashed run left in the temp folder
	// during its run window, moved to <OutputDir>\crash\ (their new paths).
	// Nothing is touched in the temp folder unless the run ended as a crash.
	CrashDumps []string
	// CrashDumpsAmbiguous is true when another run overlapped this one, so a
	// dump may belong to that run instead.
	CrashDumpsAmbiguous bool
}

// Runner runs slice requests through an Exec.
type Runner struct {
	Exe     string  // program to run (CrealityPrint.exe)
	Dialect Dialect // builds argv and classifies exit codes
	Exec    Exec    // the process launcher (OSExec unless a test injects a fake)
	// TempDir names the folder where the crash reporter writes .dmp files
	// (default os.TempDir). Tests point it at a temporary folder.
	TempDir func() string
	// Install is the detected application (its data folder and profile versions
	// decide the mirror of the system presets, see prepareDataDir).
	Install Install
	// DataDir is the data folder the tools own for dialects that need one
	// (v73); NewRunner sets it to DefaultCLIDataDir.
	DataDir string
}

// NewRunner returns a Runner for an install. lookup validates override keys
// (see OverrideLookup); nil disables overrides.
func NewRunner(in Install, lookup OverrideLookup) (*Runner, error) {
	if !in.Found {
		return nil, fmt.Errorf("Creality Print is not available: %s", in.Reason)
	}
	if !in.Supported {
		return nil, fmt.Errorf("Creality Print %s cannot be driven: %s", in.Version, in.Reason)
	}
	d, err := NewDialect(in.Dialect, lookup)
	if err != nil {
		return nil, err
	}
	r := &Runner{Exe: in.Exe, Dialect: d, Exec: OSExec{}, Install: in}
	if _, ok := d.(dataDirDialect); ok {
		r.DataDir, _ = DefaultCLIDataDir()
	}
	return r, nil
}

// RunOption tunes one Run call.
type RunOption func(*runConfig)

type runConfig struct {
	reserved bool // the caller already holds the output folder (Jobs.Start)
	timeout  time.Duration
	log      io.Writer
}

// WithTimeout sets how long the run may take (default DefaultTimeout, at most
// MaxTimeout).
func WithTimeout(d time.Duration) RunOption { return func(c *runConfig) { c.timeout = d } }

// WithLog also streams stdout and stderr, as they arrive, to w (background
// jobs pass their log file).
func WithLog(w io.Writer) RunOption { return func(c *runConfig) { c.log = w } }

// validate checks everything Run and Jobs.Start can check before anything
// starts: the timeout, the argv, the output folder and the folder of the log.
func (r *Runner) validate(req SliceRequest, timeout time.Duration) ([]string, error) {
	if timeout <= 0 {
		return nil, fmt.Errorf("%w: timeout must be positive", ErrInvalidRequest)
	}
	if timeout > MaxTimeout {
		return nil, fmt.Errorf("%w: timeout %s is above the maximum of %s", ErrInvalidRequest, timeout.Round(time.Second), MaxTimeout)
	}
	args, err := r.Dialect.BuildSliceArgs(r.fill(req))
	if err != nil {
		return nil, err
	}
	if info, err := os.Stat(req.OutputDir); err != nil || !info.IsDir() {
		return nil, fmt.Errorf("%w: output directory %q does not exist", ErrInvalidRequest, req.OutputDir)
	}
	if req.LogFile != "" {
		if info, err := os.Stat(filepath.Dir(req.LogFile)); err != nil || !info.IsDir() {
			return nil, fmt.Errorf("%w: the folder of the log file %q does not exist", ErrInvalidRequest, req.LogFile)
		}
	}
	return args, nil
}

// Run slices req and reports how it went. The error is only for a run that
// did not start: an invalid request, a missing output folder or a program
// that cannot be launched. Everything that happens once the slicer runs, its
// failure, a crash, a timeout, a cancellation, is a Result.
//
// The Runner owns req.OutputDir for the run: it deletes the plate_N.gcode of the
// requested plate (every plate for plate 0)
// there before starting the slicer, so the files found afterwards are this
// run's own and a failed run can never report an earlier run's output.
func (r *Runner) Run(ctx context.Context, req SliceRequest, opts ...RunOption) (Result, error) {
	cfg := runConfig{timeout: DefaultTimeout}
	for _, o := range opts {
		o(&cfg)
	}
	args, err := r.validate(req, cfg.timeout)
	if err != nil {
		return Result{}, err
	}
	if ctx.Err() != nil {
		// Cancelled before it began (a request cancelled while queued): a
		// result, not an error, and nothing was touched.
		return Result{Ending: EndCancelled, Args: args, Outcome: cancelledOutcome()}, nil
	}
	if !cfg.reserved {
		release, err := acquire(req.OutputDir)
		if err != nil {
			return Result{}, err
		}
		defer release()
	}
	releaseData, err := r.prepareDataDir()
	if err != nil {
		return Result{}, err
	}
	defer releaseData() // the slicer reads the data folder for the whole run
	if err := clearPlateFiles(req.OutputDir, req.Plate); err != nil {
		return Result{}, err
	}
	tempDir := os.TempDir
	if r.TempDir != nil {
		tempDir = r.TempDir
	}
	dumpsBefore := listDumps(tempDir())

	stdout, stderr := newTailBuffer(tailLimit), newTailBuffer(tailLimit)
	var outW, errW io.Writer = stdout, stderr
	if cfg.log != nil {
		log := &syncWriter{w: cfg.log}
		outW, errW = io.MultiWriter(stdout, log), io.MultiWriter(stderr, log)
	}

	runCtx, cancel := context.WithTimeout(ctx, cfg.timeout)
	defer cancel()
	win := runs.begin()
	er, err := r.Exec.Run(runCtx, ExecSpec{Exe: r.Exe, Args: args, Stdout: outW, Stderr: errW})
	end := time.Now()
	overlapped := runs.finish(win)
	res := Result{Duration: end.Sub(win.start), Args: args}
	if err != nil {
		return Result{}, err
	}
	res.Stdout, res.Stderr = cleanOutput(stdout.Bytes()), cleanOutput(stderr.Bytes())
	res.ExitCode = er.ExitCode
	switch {
	case er.Killed && ctx.Err() != nil:
		res.Ending = EndCancelled
		res.Outcome = cancelledOutcome()
	case er.Killed:
		res.Ending = EndTimedOut
		res.Outcome = Outcome{
			Code:    OutcomeTimedOut,
			Message: fmt.Sprintf("Creality Print did not finish within %s", cfg.timeout.Round(time.Second)),
			Hint:    "Slice again with a longer timeout, or reduce the work (fewer objects or plates, a coarser layer height).",
		}
	default:
		res.Ending = EndExited
		res.Outcome = r.Dialect.Classify(er.ExitCode, res.Stderr)
		res.CrashName, res.Crashed = CrashName(er.ExitCode)
	}
	res.GCodeFiles = plateFiles(req.OutputDir, req.Plate)
	if res.Ending == EndExited && res.Outcome.OK && !hasPlateOutput(req.Plate, res.GCodeFiles) {
		res.Outcome = noOutputOutcome(req.Plate)
	}
	if res.Crashed {
		res.CrashDumps = collectDumps(tempDir(), dumpsBefore, req.OutputDir, win.start, end)
		res.CrashDumpsAmbiguous = overlapped
	}
	return res, nil
}

func cancelledOutcome() Outcome {
	return Outcome{Code: OutcomeCanceled, Message: "the slice was stopped because the request was cancelled"}
}

// noOutputOutcome is the outcome of a run that exited 0 without writing the
// G-code it was asked for.
func noOutputOutcome(plate int) Outcome {
	what := "any plate"
	if plate > 0 {
		what = fmt.Sprintf("plate %d", plate)
	}
	return Outcome{
		Code:    OutcomeFailed,
		Name:    OutcomeNoOutput,
		Message: fmt.Sprintf("Creality Print finished (exit code 0) but wrote no G-code for %s", what),
		Hint:    "the slicer finished without writing G-code; check the log and that the plate has printable objects",
	}
}

// hasPlateOutput reports whether the run produced the G-code it was asked
// for: plate_<n>.gcode for plate n, any plate file for plate 0 (all).
func hasPlateOutput(plate int, files []string) bool {
	if plate == 0 {
		return len(files) > 0
	}
	want := "plate_" + strconv.Itoa(plate) + ".gcode"
	for _, f := range files {
		if filepath.Base(f) == want {
			return true
		}
	}
	return false
}

// runTracker notes which runs overlapped, so crash dump attribution can say
// when it is ambiguous.
type runTracker struct {
	mu     sync.Mutex
	active map[*runWindow]bool
}

type runWindow struct {
	start      time.Time
	overlapped bool
}

var runs = &runTracker{active: map[*runWindow]bool{}}

func (t *runTracker) begin() *runWindow {
	t.mu.Lock()
	defer t.mu.Unlock()
	w := &runWindow{start: time.Now()}
	for other := range t.active {
		other.overlapped = true
		w.overlapped = true
	}
	t.active[w] = true
	return w
}

func (t *runTracker) finish(w *runWindow) (overlapped bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.active, w)
	return w.overlapped
}

var plateRE = regexp.MustCompile(`^plate_(\d+)\.gcode$`)

// clearPlateFiles deletes every plate_N.gcode (and a leftover .tmp of the
// slicer's write-then-rename) in dir.
func clearPlateFiles(dir string, plate int) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("could not read the output folder: %w", err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !ownsPlateFile(name, plate) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("could not clear the output folder (%s is in use?): %w", name, err)
		}
	}
	return nil
}

// plateFiles lists plate_N.gcode in dir, by plate number.
func plateFiles(dir string, only int) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	type plate struct {
		n    int
		path string
	}
	var plates []plate
	for _, e := range entries {
		m := plateRE.FindStringSubmatch(e.Name())
		if m == nil || e.IsDir() {
			continue
		}
		n, _ := strconv.Atoi(m[1])
		if only > 0 && n != only {
			continue // only the requested plate belongs to this run
		}
		plates = append(plates, plate{n, filepath.Join(dir, e.Name())})
	}
	sort.Slice(plates, func(a, b int) bool { return plates[a].n < plates[b].n })
	out := make([]string, len(plates))
	for i, p := range plates {
		out[i] = p.path
	}
	return out
}

// listDumps returns the .dmp files directly inside dir.
func listDumps(dir string) map[string]bool {
	found := map[string]bool{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return found
	}
	for _, e := range entries {
		if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ".dmp") {
			found[e.Name()] = true
		}
	}
	return found
}

// dumpMu serialises dump collection: two crashed runs must not both claim (and
// race to move) the same file.
var dumpMu sync.Mutex

// collectDumps moves .dmp files that were not in before and were written
// during [start, end] from dir into <outputDir>\crash and returns their new
// paths. A copy that succeeded counts as moved even when the source cannot be
// removed (the crash reporter may still hold it).
func collectDumps(dir string, before map[string]bool, outputDir string, start, end time.Time) []string {
	dumpMu.Lock()
	defer dumpMu.Unlock()
	var moved []string
	for name := range listDumps(dir) {
		if before[name] {
			continue
		}
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil || info.ModTime().Before(start.Add(-time.Second)) || info.ModTime().After(end.Add(time.Second)) {
			continue
		}
		crashDir := filepath.Join(outputDir, "crash")
		if err := os.MkdirAll(crashDir, 0o755); err != nil {
			continue
		}
		dst := filepath.Join(crashDir, name)
		if moveFile(filepath.Join(dir, name), dst) {
			moved = append(moved, dst)
		}
	}
	sort.Strings(moved)
	return moved
}

// moveFile renames, falling back to copy and delete across volumes. It
// reports whether dst now holds the file.
func moveFile(src, dst string) bool {
	if err := os.Rename(src, dst); err == nil {
		return true
	}
	in, err := os.Open(src)
	if err != nil {
		return false
	}
	out, err := os.Create(dst)
	if err != nil {
		in.Close()
		return false
	}
	_, cerr := io.Copy(out, in)
	in.Close()
	if err := out.Close(); cerr == nil {
		cerr = err
	}
	if cerr != nil {
		os.Remove(dst)
		return false
	}
	os.Remove(src) // best effort: the copy is what counts
	return true
}

// fill gives a request the data folder of the tools for dialects that need
// one; the caller never sets it.
func (r *Runner) fill(req SliceRequest) SliceRequest {
	if _, ok := r.Dialect.(dataDirDialect); ok && req.DataDir == "" {
		req.DataDir = r.DataDir
	}
	return req
}

// ErrBusy means another run is already using the output folder.
var ErrBusy = errors.New("slicer: another run is using this output folder")

var (
	busyMu  sync.Mutex
	busyDir = map[string]bool{}
)

// acquire reserves an output folder for one run at a time: two runs into one
// folder would delete and read each other's G-code. It returns the release
// function.
func acquire(dir string) (func(), error) {
	key := strings.ToLower(filepath.Clean(dir))
	busyMu.Lock()
	defer busyMu.Unlock()
	if busyDir[key] {
		return nil, fmt.Errorf("%w: %s", ErrBusy, dir)
	}
	busyDir[key] = true
	return func() {
		busyMu.Lock()
		delete(busyDir, key)
		busyMu.Unlock()
	}, nil
}

// ownsPlateFile reports whether a file name in the output folder belongs to a
// run for the given plate (0 = all plates): plate_N.gcode and a leftover
// plate_N.gcode.tmp of the slicer's write-then-rename. A run for one plate
// leaves the G-code of the other plates alone.
func ownsPlateFile(name string, plate int) bool {
	base := strings.TrimSuffix(name, ".tmp")
	if base != name && !strings.HasSuffix(base, ".gcode") {
		return false
	}
	m := plateRE.FindStringSubmatch(base)
	if m == nil {
		return false
	}
	if plate == 0 {
		return true
	}
	n, _ := strconv.Atoi(m[1])
	return n == plate
}

// reservedFolder tells Run that the caller already holds the output folder.
func reservedFolder() RunOption { return func(c *runConfig) { c.reserved = true } }
