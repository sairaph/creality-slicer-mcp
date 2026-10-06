package slicer

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// TailLines is how many of the last output lines a job reports.
const TailLines = 200

// KeepFor is how long a finished job and its log file are kept when nobody
// reads them.
const KeepFor = 24 * time.Hour

// tailBytes bounds how much of a log file is read to find its last lines.
const tailBytes = 4 << 20

// JobIDPrefix starts every slice job id.
const JobIDPrefix = "slice-"

// Job states.
const (
	StateRunning   = "running"
	StateFinished  = "finished"
	StateCancelled = "cancelled"
)

// DoneFunc runs once, in the job's own goroutine, when the slice has ended
// and before the job reports itself finished: the place for post-processing
// that must happen whether or not anyone polls (reading the G-code summary,
// inserting thumbnails, recording the slice in the project). res is the
// slice result; err is set instead when the slicer could not be started (res
// is then the zero Result). While it runs the job still reports "running", so
// a poll never sees a slice whose post-processing is not done. A panic in fn
// is recovered and reported as the job's error.
type DoneFunc func(j *Job, res Result, err error)

// JobOption tunes Jobs.Start.
type JobOption func(*jobConfig)

type jobConfig struct {
	tag    any
	onDone DoneFunc
}

// WithTag attaches a caller value (project id, plate, revision) to the job;
// it is Job.Tag and travels with every snapshot's job.
func WithTag(tag any) JobOption { return func(c *jobConfig) { c.tag = tag } }

// WithOnDone sets the DoneFunc of the job.
func WithOnDone(fn DoneFunc) JobOption { return func(c *jobConfig) { c.onDone = fn } }

// Job is one slice run in the background. Its stdout and stderr stream into
// one log file while it runs.
type Job struct {
	ID      string
	Started time.Time
	Timeout time.Duration
	// Tag is the value given to WithTag.
	Tag any

	logPath string
	stop    context.CancelFunc
	ctx     context.Context
	done    chan struct{}

	mu              sync.Mutex
	finished        time.Time
	result          *Result
	cancelRequested bool
	errText         string // the run could not start, or its post-processing failed
	read            bool   // the final output was handed out and the log removed
	finalTail       string // that output, kept for later reads
}

// Snapshot is what a job reports about itself.
type Snapshot struct {
	ID      string
	Tag     any
	State   string // running, finished or cancelled
	Elapsed time.Duration
	Timeout time.Duration
	// Result is set once the run ended (nil while running, or when the slicer
	// could not be started: see Error).
	Result *Result
	Error  string
	// Output is the last TailLines lines of the log (cleaned).
	Output string
	// OutputFile is the log file path, "" once it was removed.
	OutputFile string
	// Removed reports that this read handed out the final output and removed
	// the log file.
	Removed bool
}

// Jobs runs and tracks background slice jobs.
type Jobs struct {
	runner *Runner
	dir    string

	mu   sync.Mutex
	jobs map[string]*Job
}

// NewJobs returns an empty job list. Logs are written to dir (the caller's
// cache folder; created when the first job starts).
func NewJobs(runner *Runner, dir string) *Jobs {
	return &Jobs{runner: runner, dir: dir, jobs: map[string]*Job{}}
}

// IsJobID reports whether id looks like a slice job id.
func IsJobID(id string) bool { return strings.HasPrefix(id, JobIDPrefix) }

func newJobID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return JobIDPrefix + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return JobIDPrefix + hex.EncodeToString(b)
}

// Start begins the slice in the background and returns at once. The job
// outlives the request that started it and runs until it ends, timeout
// passes, Cancel or StopAll. The error is for a request that cannot start
// (invalid request, missing output or log folder, timeout above MaxTimeout,
// unwritable job folder); a slicer that fails to launch shows up in the job's
// Error. Like Runner.Run, the job swaps the plate files into req.OutputDir
// only when the whole run succeeded.
func (m *Jobs) Start(req SliceRequest, timeout time.Duration, opts ...JobOption) (*Job, error) {
	var cfg jobConfig
	for _, o := range opts {
		o(&cfg)
	}
	// Validate before creating anything so a bad request leaves no log behind.
	if _, err := m.runner.validate(req, timeout); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(m.dir, 0o755); err != nil {
		return nil, fmt.Errorf("could not prepare the job folder: %w", err)
	}
	m.sweep()
	release, err := acquire(req.OutputDir)
	if err != nil {
		return nil, err
	}
	id := newJobID()
	logPath := filepath.Join(m.dir, "job-"+id+".log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		release()
		return nil, fmt.Errorf("could not create the job log: %w", err)
	}
	ctx, stop := context.WithCancel(context.Background())
	j := &Job{ID: id, Started: time.Now(), Timeout: timeout, Tag: cfg.tag, logPath: logPath, stop: stop, ctx: ctx, done: make(chan struct{})}
	m.mu.Lock()
	m.jobs[id] = j
	m.mu.Unlock()
	go j.run(ctx, m.runner, req, logFile, cfg.onDone, release)
	return j, nil
}

// run is the job's goroutine: it runs the slice, runs the DoneFunc and then
// records how it ended.
func (j *Job) run(ctx context.Context, runner *Runner, req SliceRequest, log *os.File, onDone DoneFunc, release func()) {
	res, err := runner.Run(ctx, req, WithTimeout(j.Timeout), WithLog(log), reservedFolder())
	release() // the folder is free for the DoneFunc to run the slicer again
	log.Close()
	defer close(j.done)
	defer j.stop()
	var doneErr string
	if onDone != nil {
		doneErr = j.callOnDone(onDone, res, err)
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.finished = time.Now()
	switch {
	case err != nil:
		j.errText = err.Error()
	default:
		j.result = &res
	}
	if doneErr != "" {
		if j.errText != "" {
			j.errText += "; "
		}
		j.errText += doneErr
	}
}

// callOnDone runs fn and turns a panic into an error text.
func (j *Job) callOnDone(fn DoneFunc, res Result, err error) (failure string) {
	defer func() {
		if r := recover(); r != nil {
			failure = fmt.Sprintf("post-processing of the slice failed: %v", r)
		}
	}()
	fn(j, res, err)
	return ""
}

// Done is closed when the job has ended and its DoneFunc has returned.
func (j *Job) Done() <-chan struct{} { return j.done }

// LogFile is the file the job streams its output to.
func (j *Job) LogFile() string { return j.logPath }

// readTail returns the last n cleaned lines of the file at path.
func readTail(path string, n int) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	if info, err := f.Stat(); err == nil && info.Size() > tailBytes {
		f.Seek(info.Size()-tailBytes, io.SeekStart)
	}
	data, _ := io.ReadAll(f)
	lines := strings.Split(cleanOutput(data), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// state derives the state from how the run ended, not from whether a cancel
// was asked for: a run that finished normally in the instant of a Cancel stays
// finished. Call with j.mu held.
func (j *Job) state() string {
	switch {
	case j.finished.IsZero():
		return StateRunning
	case j.result != nil && j.result.Ending == EndCancelled,
		j.result == nil && j.cancelRequested:
		return StateCancelled
	case j.cancelRequested && j.result != nil && !j.result.Outcome.OK:
		// The cancel reached the work after the run (the -24 retry, post-processing)
		// and that work did not complete: cancelled. A run that completed anyway
		// stays finished.
		return StateCancelled
	}
	return StateFinished
}

// Snapshot reports the job. The first snapshot of a finished job hands out its
// final output and removes the log file; later ones repeat that output.
func (j *Job) Snapshot() Snapshot {
	j.mu.Lock()
	defer j.mu.Unlock()
	s := Snapshot{ID: j.ID, Tag: j.Tag, Timeout: j.Timeout, State: j.state()}
	if j.finished.IsZero() {
		s.Elapsed = time.Since(j.Started)
		s.Output = readTail(j.logPath, TailLines)
		s.OutputFile = j.logPath
		return s
	}
	s.Elapsed = j.finished.Sub(j.Started)
	s.Result, s.Error = j.result, j.errText
	if !j.read {
		j.finalTail = readTail(j.logPath, TailLines)
		os.Remove(j.logPath)
		j.read = true
		s.Removed = true
		s.OutputFile = j.logPath
	}
	s.Output = j.finalTail
	return s
}

// Cancel stops the job's process tree and waits briefly for it to end. It
// reports true when the job ended cancelled (or is still stopping), false when
// it had already finished or finished anyway.
func (j *Job) Cancel() bool {
	j.mu.Lock()
	if !j.finished.IsZero() {
		j.mu.Unlock()
		return false
	}
	j.cancelRequested = true
	j.mu.Unlock()
	j.stop()
	select {
	case <-j.done:
		// The true outcome: a job whose run finished in the instant of the cancel
		// (it was in post-processing) is finished, not cancelled.
		j.mu.Lock()
		defer j.mu.Unlock()
		return j.state() == StateCancelled
	case <-time.After(10 * time.Second):
	}
	return true
}

// Get returns the job called id, or nil.
func (m *Jobs) Get(id string) *Job {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.jobs[id]
}

// Snapshots reports every job, newest first, without consuming any output.
func (m *Jobs) Snapshots() []Snapshot {
	m.mu.Lock()
	list := make([]*Job, 0, len(m.jobs))
	for _, j := range m.jobs {
		list = append(list, j)
	}
	m.mu.Unlock()
	sort.Slice(list, func(a, b int) bool { return list[a].Started.After(list[b].Started) })
	out := make([]Snapshot, 0, len(list))
	for _, j := range list {
		j.mu.Lock()
		s := Snapshot{ID: j.ID, Tag: j.Tag, Timeout: j.Timeout, State: j.state(), Elapsed: time.Since(j.Started)}
		if !j.finished.IsZero() {
			s.Elapsed = j.finished.Sub(j.Started)
			s.Result, s.Error = j.result, j.errText
		}
		j.mu.Unlock()
		out = append(out, s)
	}
	return out
}

// Running is the number of jobs still running.
func (m *Jobs) Running() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, j := range m.jobs {
		j.mu.Lock()
		if j.finished.IsZero() {
			n++
		}
		j.mu.Unlock()
	}
	return n
}

// StopAll ends every running job (the server is exiting), best effort.
func (m *Jobs) StopAll() {
	m.mu.Lock()
	list := make([]*Job, 0, len(m.jobs))
	for _, j := range m.jobs {
		list = append(list, j)
	}
	m.mu.Unlock()
	var wg sync.WaitGroup
	for _, j := range list {
		wg.Add(1)
		go func(j *Job) {
			defer wg.Done()
			j.Cancel()
		}(j)
	}
	wg.Wait()
}

// sweep forgets finished jobs older than KeepFor and removes job logs in the
// job folder that old, which also covers jobs of an earlier server process.
func (m *Jobs) sweep() {
	cutoff := time.Now().Add(-KeepFor)
	m.mu.Lock()
	for id, j := range m.jobs {
		j.mu.Lock()
		old := !j.finished.IsZero() && j.finished.Before(cutoff)
		j.mu.Unlock()
		if old {
			os.Remove(j.logPath)
			delete(m.jobs, id)
		}
	}
	m.mu.Unlock()
	entries, err := os.ReadDir(m.dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, "job-"+JobIDPrefix) || !strings.HasSuffix(name, ".log") {
			continue
		}
		if info, err := e.Info(); err == nil && info.ModTime().Before(cutoff) {
			os.Remove(filepath.Join(m.dir, name))
		}
	}
}

// Context is the job's own context: it ends when the job is cancelled or
// stopped, and stays alive while the DoneFunc runs, so work started from the
// hook (a second run) can be cancelled with the job.
func (j *Job) Context() context.Context { return j.ctx }
