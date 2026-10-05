package projects

import (
	"context"
	"errors"
	"fmt"
	"github.com/gofrs/flock"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/sairaph/creality-slicer-mcp/internal/gcodeinfo"
	"github.com/sairaph/creality-slicer-mcp/internal/render"
	"github.com/sairaph/creality-slicer-mcp/internal/slicer"
	"github.com/sairaph/creality-slicer-mcp/internal/threemf"
)

// snapshotFile is the copy of project.3mf a slice reads, so changes made while
// the slicer runs never reach it.
const snapshotFile = "slice_input.3mf"

// ToolInfo is one filament slot the G-code uses, for the hand-off to printing.
type ToolInfo struct {
	Tool       int // T number in the G-code, 0 based
	Slot       int // filament slot, 1 based
	Preset     string
	Type       string
	Colour     string
	FilamentID string
	// SpoolSlot is the CFS slot (T1A ..) of the spool this filament was made from
	// (create_project or set_presets with spools), empty otherwise.
	SpoolSlot string
}

// PlateResult is what one sliced plate produced.
type PlateResult struct {
	Plate int
	// Revision is the project revision this plate was sliced from.
	Revision int
	// GCodePath is the G-code file inside the project folder.
	GCodePath string
	// UploadName is the file name to store the G-code under on the printer.
	UploadName  string
	TimeSeconds int
	TimeText    string
	Layers      int
	// FilamentG is the weight per filament slot; TotalG the sum.
	FilamentG []float64
	TotalG    float64
	// Tools lists the tools the G-code uses, in order of first use.
	Tools []ToolInfo
	// ExcludeNames are the object names of EXCLUDE_OBJECT_DEFINE (what a
	// printer's exclude_object takes).
	ExcludeNames []string
	Bytes        int64
	// Changes is the number of filament changes in the G-code.
	Changes int
	// ObjectLabels tie each exclusion label to its object.
	ObjectLabels []ObjectLabel
	// Actions says what became of the plate's layer actions in the G-code.
	Actions []ActionResult
	// Multicolour is true when the G-code changes filament.
	Multicolour bool
	// PrimeTowerG and PrimeTowerS are the filament and the printing time of the
	// prime tower (by layer, several filaments); zero without one. The time is
	// the tower's own moves, so the real cost with the tool changes is higher.
	PrimeTowerG float64
	PrimeTowerS int
	// FlushG is the purge of the tool changes (FlushChanges of them), from the
	// G-code footer, or an estimate from the flush matrix when FlushEstimated.
	FlushG         float64
	FlushChanges   int
	FlushEstimated bool
	// PurgeWarning is the note about the purge waste of a multi-filament by-layer
	// plate (see purgeWarning); empty when there is none.
	PurgeWarning string
	// Overrides are the plate, object, part and height range settings at the time
	// of the slice (the project copy the slicer read is deleted).
	Overrides *OverridesReport
}

// LastSlice is the record of the last successful slice, kept in job.json.
type LastSlice struct {
	Time     time.Time
	Revision int
	JobID    string
	Plates   []PlateResult
	Warnings []string
	// Arranged and Oriented say that the tools placed the objects before this
	// slice (written into the project).
	Arranged bool
	Oriented bool
	// ElapsedS is how long the slicer ran, in seconds.
	ElapsedS float64
	// Moved lists the objects arrange or orient moved before this slice (old and
	// new position on the plate).
	Moved []ObjectMove
}

// SliceOptions is slice_project.
type SliceOptions struct {
	// Plate is the plate to slice; 0 slices every plate that has objects.
	Plate int
	// Arrange packs the objects of the plate with the tools' own placement (gap,
	// margin, wipe tower kept free), Orient lays every object on its largest flat
	// face; both are written into the project first (a revision), never left to
	// the slicer, so they work the same with every slicer version.
	Arrange bool
	Orient  bool
	// Overrides are setting overrides for this run only (the project is not
	// changed).
	Overrides map[string]any
	// Wait, when above zero and Background is false, runs the slice as a job and
	// waits for it up to this long: a slice that finishes in time returns its
	// result, a longer one returns the job id and carries on.
	Wait time.Duration
	// SkipThumbnails leaves the thumbnails out of the G-code.
	SkipThumbnails bool
	// Ctx ends the wait (and a foreground run) when the caller gives up: a job
	// that was started goes on. Nil means never.
	Ctx context.Context
	// Background starts the slice and returns at once with a job id.
	Background bool
	// Timeout for the run; 0 means the default (30 minutes).
	Timeout time.Duration
	// Preview asks for a picture of the plate with the reply.
	Preview PreviewKind
}

// SliceOutcome is the reply of Slice.
type SliceOutcome struct {
	ProjectID string
	// JobID is set for a background slice.
	JobID    string
	Running  bool
	Revision int
	Last     *LastSlice
	Preview  []byte
	Warnings []string
}

// sliceEnd is how a finished job ended, kept for get_slice_status.
type sliceEnd struct {
	last *LastSlice
	err  *Error
}

func (e *sliceEnd) fail(err *Error) { e.err = err }

// sanitizeName makes a printer file name from a project name.
var unsafeNameRE = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func sanitizeName(s string) string {
	s = strings.Trim(unsafeNameRE.ReplaceAllString(strings.TrimSpace(s), "_"), "._")
	if s == "" {
		return "project"
	}
	if len(s) > 60 {
		s = s[:60]
	}
	return s
}

// slicerError turns a failed run into the structured error of the tools.
func slicerError(res slicer.Result, err error, extraHint string) *Error {
	if err != nil {
		if errors.Is(err, slicer.ErrBusy) {
			return busyError()
		}
		return errf(CodeSlicerError, "check that Creality Print is installed (server_status shows what was detected)", "the slicer could not be run: %v", err)
	}
	o := res.Outcome
	hint := o.Hint
	if extraHint != "" {
		hint = extraHint
	}
	e := errf(CodeSlicerError, hint, "%s", firstNonEmpty(o.Message, "the slicer failed"))
	e.Fields = map[string]any{"outcome": o.Code, "exit_name": o.Name, "exit_code": res.ExitCode, "output_tail": strings.TrimSpace(res.Stderr + "\n" + res.Stdout)}
	if res.CrashName != "" {
		e.Fields["crash_name"] = res.CrashName
	}
	if len(res.CrashDumps) > 0 {
		e.Fields["crash_dumps"] = res.CrashDumps
	}
	return e
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

// overrideStrings validates the run overrides against the catalog and turns
// them into the command line value strings.
func (s *Store) overrideStrings(over map[string]any, allowLocked bool) (map[string]string, error) {
	if len(over) == 0 {
		return nil, nil
	}
	out := map[string]string{}
	var errs keyErrors
	h := &handle{s: s}
	for _, key := range sortedValueKeys(over) {
		v := over[key]
		if v == nil {
			errs.add(key, "needs a value")
			continue
		}
		o, ok := h.validate(key, v, "preset", allowLocked, &errs)
		if !ok {
			continue
		}
		if _, _, has := s.cfg.Catalog.CLIFlag(key); !has {
			errs.add(key, "has no command line form; change it with update_settings before slicing")
			continue
		}
		vv := anyToVal(o, v)
		if vv.IsList {
			out[key] = strings.Join(vv.List, ",")
		} else {
			out[key] = vv.Str
		}
	}
	return out, errs.err("overrides apply to this slice only; use update_settings to change the project")
}

func (s *Store) lookup() slicer.OverrideLookup { return s.cfg.Catalog.CLIFlag }

// Slice saves nothing new (every change is already in project.3mf), copies the
// project, runs the installed slicer on the copy and post-processes the
// G-code. With Background it returns at once and the work continues in a job.
func (s *Store) Slice(ref string, opts SliceOptions) (*SliceOutcome, error) {
	if s.cfg.Runner == nil {
		reason := s.cfg.Install.Reason
		if reason == "" {
			reason = "Creality Print was not found"
		}
		return nil, errf(CodeUnavailable, "install Creality Print 7.2 or 7.3; the other tools still work", "slicing is not available: %s", reason)
	}
	over, err := s.overrideStrings(opts.Overrides, false)
	if err != nil {
		return nil, err
	}
	id, err := s.resolve(ref)
	if err != nil {
		return nil, err
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = slicer.DefaultTimeout
	}
	if timeout > slicer.MaxTimeout {
		return nil, invalidf("", "timeout %s is above the maximum of %s", timeout, slicer.MaxTimeout)
	}
	s.mu.Lock()
	if job := s.running[id]; job != "" {
		s.mu.Unlock()
		return nil, conflictf("wait for it with get_slice_status, or cancel it", "project %s is already being sliced (%s)", id, job)
	}
	s.running[id] = "starting"
	s.mu.Unlock()
	callCtx := opts.Ctx
	if callCtx == nil {
		callCtx = context.Background()
	}
	unlockSlice, gerr := s.sliceGuard(id)
	if gerr != nil {
		s.mu.Lock()
		delete(s.running, id)
		s.mu.Unlock()
		return nil, gerr
	}
	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() {
			unlockSlice()
			s.mu.Lock()
			delete(s.running, id)
			s.mu.Unlock()
		})
	}

	// The tools' own orient and arrange come first and become part of the
	// project, so the slicer only ever sees a placed project.
	var moved []ObjectMove
	if opts.Arrange || opts.Orient {
		if err := s.write(id, func(h *handle) error {
			var perr error
			moved, perr = h.autoPlaceScope(opts.Plate, opts.Orient, opts.Arrange)
			return perr
		}); err != nil {
			release()
			return nil, err
		}
	}

	// Snapshot the project.
	var (
		req       slicer.SliceRequest
		startRev  int
		platesOut []int
		crashFil  int // filaments of the first multi-filament plate printed by layer (for the crash hint)
		crashSeq  string
		seqHint   string // the hint of a -63 failure (by object clearance)
		logPath   string // the slicer's own log of this run
		name      string
	)
	err = s.write(id, func(h *handle) error {
		// Nothing changes here (no revision): the writer lock only makes sure the
		// copy never sees a half written save.
		startRev, name = h.meta.Revision, h.meta.Name
		if len(h.p.Objects) == 0 {
			return invalidf("add a model with add_model first", "project %s has no objects to slice", id)
		}
		if opts.Plate != 0 && h.p.Plate(opts.Plate) == nil {
			return notFoundf("call get_project to see the plates", "project %s has no plate %d", id, opts.Plate)
		}
		for _, pl := range h.p.Plates {
			if len(pl.Instances) > 0 && (opts.Plate == 0 || opts.Plate == pl.Index) {
				platesOut = append(platesOut, pl.Index)
				if crashFil == 0 {
					slots := map[int]bool{}
					for _, in := range pl.Instances {
						if o := h.p.Object(in.ObjectID); o != nil {
							slots[max(o.Extruder(), 1)] = true
						}
					}
					seq := pl.Config.Value("print_sequence")
					if seq == "" && h.p.Settings != nil {
						seq = h.p.Settings.String("print_sequence")
					}
					if len(slots) >= 2 && seq != "by object" {
						crashFil, crashSeq = len(slots), seq
					}
				}
			}
		}
		seqHint = h.seqHintText(platesOut, s.cfg.Install.Dialect)
		if len(platesOut) == 0 {
			return invalidf("move an object onto the plate or choose another plate", "plate %d has no objects", opts.Plate)
		}
		outDir := filepath.Join(h.dir, outDirName)
		if err := os.MkdirAll(outDir, 0o755); err != nil {
			return errf(CodeInternal, "", "%v", err)
		}
		snap := filepath.Join(outDir, snapshotFile)
		if err := copyFile(filepath.Join(h.dir, projectFile), snap); err != nil {
			return errf(CodeInternal, "", "copying the project for slicing failed: %v", err)
		}
		logPath = filepath.Join(outDir, sliceLogName)
		_ = os.Remove(logPath) // the log of an earlier slice is not this one's
		req = slicer.SliceRequest{
			Inputs: []string{snap}, Plate: opts.Plate, OutputDir: outDir,
			LogFile:    logPath,
			Overrides:  over,
			AllowNewer: threemfNewer(h.appVersion(), s.version()),
		}
		return nil
	})
	if err != nil {
		release()
		return nil, err
	}

	finish := func(ctx context.Context, res slicer.Result, runErr error, jobID string) (*LastSlice, *Error) {
		defer release()
		defer os.Remove(req.Inputs[0]) // the copy of the project is not kept (it can be as large as the project)
		retried := false
		// -24: the slicer judges the file to come from a newer or foreign
		// application (Bambu Studio, older Creality builds). The project has no
		// dangerous content the slicer could misread, so run it once more with
		// the allow-newer flag and say so.
		if runErr == nil && res.Ending == slicer.EndExited && res.ExitCode == -24 && !req.AllowNewer {
			req2 := req
			req2.AllowNewer = true
			if r2, e2 := s.cfg.Runner.Run(ctx, req2, slicer.WithTimeout(timeout)); e2 == nil {
				res, retried = r2, true
			}
		}
		if runErr != nil || !res.Outcome.OK {
			return nil, withLog(slicerError(res, runErr, failureHint(s.cfg.Install.Dialect, res.Outcome, crashCtx{crashFil, crashSeq, seqHint})), logPath)
		}
		var notes []string
		if retried {
			notes = append(notes, "the file was saved by a newer or other application version; it was sliced with the newer-file check switched off")
		}
		last, ferr := s.postProcess(id, name, req, res, startRev, opts, platesOut, notes, moved)
		if ferr != nil {
			return nil, ferr
		}
		last.JobID = jobID
		return last, nil
	}

	if opts.Background || opts.Wait > 0 {
		if s.cfg.Jobs == nil {
			release()
			return nil, errf(CodeUnavailable, "", "background slicing is not available")
		}
		var job *slicer.Job
		done := func(j *slicer.Job, res slicer.Result, runErr error) {
			last, ferr := finish(j.Context(), res, runErr, j.ID)
			end := &sliceEnd{last: last, err: ferr}
			s.mu.Lock()
			s.ends()[j.ID] = end
			s.mu.Unlock()
		}
		job, err = s.cfg.Jobs.Start(req, timeout, slicer.WithTag(id), slicer.WithOnDone(done))
		if err != nil {
			_ = os.Remove(req.Inputs[0])
			release()
			if errors.Is(err, slicer.ErrBusy) {
				return nil, busyError()
			}
			return nil, invalidf("", "%v", err)
		}
		s.mu.Lock()
		if _, still := s.running[id]; still {
			s.running[id] = job.ID
		}
		s.hints()[job.ID] = crashCtx{crashFil, crashSeq, seqHint}
		s.mu.Unlock()
		if !opts.Background && opts.Wait > 0 {
			// Wait for the job in the foreground up to Wait; a slice that takes
			// longer carries on as a job and the caller gets its id.
			timer := time.NewTimer(opts.Wait)
			defer timer.Stop()
			select {
			case <-job.Done():
				s.mu.Lock()
				end := s.ends()[job.ID]
				s.mu.Unlock()
				if end == nil {
					return nil, errf(CodeInternal, "", "the slice job %s ended without a result", job.ID)
				}
				if end.err != nil {
					return nil, end.err
				}
				o := s.outcome(id, end.last, opts)
				o.JobID = job.ID
				return o, nil
			case <-timer.C:
			case <-callCtx.Done():
			}
		}
		return &SliceOutcome{ProjectID: id, JobID: job.ID, Running: true, Revision: startRev}, nil
	}

	res, runErr := s.cfg.Runner.Run(callCtx, req, slicer.WithTimeout(timeout))
	last, ferr := finish(callCtx, res, runErr, "")
	if ferr != nil {
		return nil, ferr
	}
	return s.outcome(id, last, opts), nil
}

// outcome is the reply of a finished slice, with the preview when asked for.
func (s *Store) outcome(id string, last *LastSlice, opts SliceOptions) *SliceOutcome {
	out := &SliceOutcome{ProjectID: id, Last: last, Revision: last.Revision, Warnings: last.Warnings}
	if opts.Preview != "" && opts.Preview != PreviewNone && len(last.Plates) > 0 {
		if data, perr := s.Preview(id, opts.Preview, last.Plates[0].Plate); perr == nil {
			out.Preview = data
		} else {
			out.Warnings = append(out.Warnings, "the preview could not be rendered: "+perr.Error())
		}
	}
	return out
}

// crashCtx is what the crash hint of a job needs: the filaments of the first
// multi-filament plate printed by layer, and its print sequence.
type crashCtx struct {
	filaments int
	sequence  string
	seqHint   string
}

// failureHint is the extra hint of a failed slice: the crash of 7.2 on several
// filaments by layer, or the clearance advice of a by-object collision.
func failureHint(dialect string, o slicer.Outcome, c crashCtx) string {
	if h := slicer.CrashHint(dialect, o, c.filaments, c.sequence); h != "" {
		return h
	}
	if o.Name == "OBJECT_COLLISION_IN_SEQ_PRINT" {
		return c.seqHint
	}
	return ""
}

// hints is the map of crash contexts by job id; call with s.mu held.
func (s *Store) hints() map[string]crashCtx {
	if s.crashCtxs == nil {
		s.crashCtxs = map[string]crashCtx{}
	}
	return s.crashCtxs
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// ends is the map of finished jobs; call with s.mu held.
func (s *Store) ends() map[string]*sliceEnd {
	if s.finished == nil {
		s.finished = map[string]*sliceEnd{}
	}
	return s.finished
}

var excludeNameRE = regexp.MustCompile(`^(.*)_id_(\d+)_copy_(\d+)$`)

// postProcess reads the G-code of every sliced plate, embeds the thumbnails,
// writes the arrangement back when it applies and records the slice.
func (s *Store) postProcess(id, projectName string, req slicer.SliceRequest, res slicer.Result, startRev int, opts SliceOptions, wanted []int, notes []string, moved []ObjectMove) (*LastSlice, *Error) {
	snapPath := req.Inputs[0]
	sp, err := threemf.Open(snapPath)
	if err != nil {
		return nil, errf(CodeInternal, "", "the sliced project cannot be read: %v", err)
	}
	defer sp.Close()
	snap := &handle{s: s, id: id, dir: filepath.Dir(filepath.Dir(snapPath)), meta: &meta{Name: projectName}, p: sp}

	last := &LastSlice{Time: s.now(), Revision: startRev, Arranged: opts.Arrange, Oriented: opts.Orient, Warnings: notes, ElapsedS: res.Duration.Seconds(), Moved: moved}
	sizes, sizeErr := render.ParseThumbnailSizes(sp.Settings.String("thumbnails"))
	if sizeErr != nil {
		last.Warnings = append(last.Warnings, "the printer's thumbnail sizes are not readable, so the G-code has no thumbnails: "+sizeErr.Error())
		sizes = nil
	}
	summaries := map[int]gcodeinfo.Summary{}
	var spoolLinks []SpoolLink
	if m, err := s.readMeta(id); err == nil {
		spoolLinks = m.Spools
	}
	for _, file := range res.GCodeFiles {
		plate, ok := plateOfFile(file)
		if !ok {
			continue
		}
		sum, err := gcodeinfo.ReadSummary(file)
		if err != nil {
			return nil, errf(CodeInternal, "the slicer ran but its G-code cannot be read; slice again", "reading %s failed: %v", filepath.Base(file), err)
		}
		if len(sizes) > 0 && !opts.SkipThumbnails {
			if msg := s.embedThumbnails(snap, file, plate, sizes); msg != "" {
				last.Warnings = append(last.Warnings, msg)
			}
		}
		summaries[plate] = sum
		pr := PlateResult{
			Plate: plate, Revision: startRev, GCodePath: file, UploadName: fmt.Sprintf("%s_plate%d.gcode", uploadBase(projectName, id), plate),
			TimeSeconds: sum.TimeSeconds, TimeText: sum.TimeText, Layers: firstPositive(sum.TotalLayerNumber, sum.LayerCount),
			FilamentG: sum.FilamentUsedG, TotalG: sum.TotalFilamentG, Multicolour: sum.M8200 || len(sum.Tools) > 1,
		}
		if st, err := os.Stat(file); err == nil {
			pr.Bytes = st.Size()
		}
		for _, o := range sum.Objects {
			pr.ExcludeNames = append(pr.ExcludeNames, o.Name)
		}
		pr.Tools = toolTable(sp, sum)
		for i := range pr.Tools {
			if t := pr.Tools[i].Tool; t < len(spoolLinks) && spoolLinks[t].Preset == pr.Tools[i].Preset {
				pr.Tools[i].SpoolSlot = spoolLinks[t].Slot
			}
		}
		pr.ObjectLabels = objectLabels(sp, plate, pr.ExcludeNames)
		pr.Actions = scanActions(file, snap.layerActions(plate))
		pr.PrimeTowerG, pr.PrimeTowerS = primeTower(file, sum)
		pr.FlushG, pr.FlushChanges, pr.FlushEstimated = flushCost(file, sum, sp.Settings)
		pr.PurgeWarning = snap.purgeWarning(plate, pr)
		ov := &OverridesReport{Plate: plate}
		snap.fillOverrides(ov)
		pr.Overrides = ov
		pr.Changes = sum.TotalFilamentChange
		if pr.Changes == 0 {
			uses := 0
			for _, t := range sum.Tools {
				uses += t.Count
			}
			pr.Changes = max(uses-1, 0)
		}
		last.Plates = append(last.Plates, pr)
	}
	sort.Slice(last.Plates, func(i, j int) bool { return last.Plates[i].Plate < last.Plates[j].Plate })
	// The purge waste of multi-filament by-layer plates goes first: it is the
	// number the owner pays for.
	last.Warnings = append(last.PurgeNotes(), last.Warnings...)
	for _, w := range wanted {
		found := false
		for _, p := range last.Plates {
			if p.Plate == w {
				found = true
			}
		}
		if !found {
			last.Warnings = append(last.Warnings, fmt.Sprintf("plate %d produced no G-code", w))
		}
	}
	if len(last.Plates) == 0 {
		return nil, errf(CodeSlicerError, "check the objects are on the plate and inside the printable area", "the slicer finished but wrote no G-code")
	}

	// Record the slice in the project metadata (no project change, no revision).
	err = s.write(id, func(h *handle) error {
		// A slice replaces the records of the plates it sliced and keeps the others,
		// each with the revision it was made from.
		byPlate := map[int]PlateResult{}
		if old := h.meta.LastSlice; old != nil {
			for _, p := range old.Plates {
				byPlate[p.Plate] = p
			}
		}
		for _, p := range last.Plates {
			byPlate[p.Plate] = p
		}
		stored := *last
		stored.Plates = nil
		for _, p := range byPlate {
			stored.Plates = append(stored.Plates, p)
		}
		sort.Slice(stored.Plates, func(i, j int) bool { return stored.Plates[i].Plate < stored.Plates[j].Plate })
		h.meta.LastSlice = &stored
		return s.writeMeta(id, h.meta)
	})
	if err != nil {
		return nil, AsError(err)
	}
	return last, nil
}

func firstPositive(vs ...int) int {
	for _, v := range vs {
		if v > 0 {
			return v
		}
	}
	return 0
}

var plateFileRE = regexp.MustCompile(`^plate_(\d+)\.gcode$`)

func plateOfFile(path string) (int, bool) {
	m := plateFileRE.FindStringSubmatch(filepath.Base(path))
	if m == nil {
		return 0, false
	}
	n, _ := strconv.Atoi(m[1])
	return n, true
}

// toolTable maps the tools a G-code uses to the filament slots of the project.
func toolTable(p *threemf.Project, sum gcodeinfo.Summary) []ToolInfo {
	var out []ToolInfo
	cfg := p.Settings
	for _, t := range sum.Tools {
		ti := ToolInfo{Tool: t.Tool, Slot: t.Tool + 1}
		if cfg != nil {
			at := func(key string) string {
				l := cfg.List(key)
				if t.Tool < len(l) {
					return l[t.Tool]
				}
				return ""
			}
			ti.Preset, ti.Type, ti.Colour, ti.FilamentID = at("filament_settings_id"), at("filament_type"), at("filament_colour"), at("filament_ids")
		}
		out = append(out, ti)
	}
	return out
}

// embedThumbnails renders the pictures the printer preset asks for and inserts
// them into the G-code. A failure is reported as a warning: the G-code is good
// without thumbnails.
func (s *Store) embedThumbnails(h *handle, gcode string, plate int, sizes []render.SizeSpec) string {
	sc, err := h.scene(plate)
	if err != nil {
		return fmt.Sprintf("plate %d: thumbnails were not embedded: %v", plate, err)
	}
	pngs := map[render.Size][]byte{}
	for _, sz := range sizes {
		if sz.Format != "PNG" || sz.W != sz.H {
			continue // only square PNG sizes are drawn
		}
		if _, done := pngs[sz.Size]; done {
			continue
		}
		data, err := render.Preview(sc, render.ViewIso, sz.W)
		if err != nil {
			return fmt.Sprintf("plate %d: rendering the %dx%d thumbnail failed: %v", plate, sz.W, sz.H, err)
		}
		pngs[sz.Size] = data
	}
	if len(pngs) == 0 {
		return ""
	}
	if err := render.InsertThumbnails(gcode, pngs); err != nil {
		return fmt.Sprintf("plate %d: embedding the thumbnails failed: %v", plate, err)
	}
	return ""
}

// SliceStatus reports a slice job (ref is a job id) or the slice state of a
// project (ref is a project id or name).
type SliceStatus struct {
	ProjectID string
	JobID     string
	// State is "running", "finished", "cancelled", "failed" or "none" (the
	// project was never sliced).
	State   string
	Elapsed time.Duration
	// Output is the tail of the slicer's output while it runs or after a failure.
	Output string
	Last   *LastSlice
	Error  *Error
	Stale  bool
}

// SliceStatus looks a job or a project up.
func (s *Store) SliceStatus(ref string) (*SliceStatus, error) {
	if isJobRef(ref) {
		if s.cfg.Jobs == nil {
			return nil, notFoundf("", "no slice job %q", ref)
		}
		j := s.cfg.Jobs.Get(ref)
		if j == nil {
			return nil, notFoundf("jobs are forgotten after a day; call get_project for the last slice", "no slice job %q", ref)
		}
		snap := j.Snapshot()
		st := &SliceStatus{JobID: snap.ID, Elapsed: snap.Elapsed, Output: snap.Output, State: snap.State}
		if id, ok := snap.Tag.(string); ok {
			st.ProjectID = id
		}
		if snap.State == slicer.StateFinished {
			s.mu.Lock()
			end := s.ends()[snap.ID]
			s.mu.Unlock()
			switch {
			case end != nil && end.err != nil:
				st.State, st.Error = "failed", end.err
			case end != nil:
				st.Last = end.last
			case snap.Result != nil && !snap.Result.Outcome.OK:
				st.State, st.Error = "failed", s.failedJobError(snap.ID, *snap.Result)
			case snap.Error != "":
				st.State, st.Error = "failed", errf(CodeSlicerError, "", "%s", snap.Error)
			}
			if st.Last != nil && st.ProjectID != "" {
				st.Stale = s.isStale(st.ProjectID, st.Last)
			}
		}
		return st, nil
	}
	id, err := s.resolve(ref)
	if err != nil {
		return nil, err
	}
	st := &SliceStatus{ProjectID: id, State: "none"}
	s.mu.Lock()
	job := s.running[id]
	s.mu.Unlock()
	if job != "" && isJobRef(job) {
		return s.SliceStatus(job)
	}
	if job != "" {
		st.State = "running"
	}
	m, err := s.readMeta(id)
	if err != nil {
		return nil, notFoundf("", "project %s cannot be read: %v", id, err)
	}
	if m.LastSlice != nil {
		if st.State == "none" {
			st.State = "finished"
		}
		st.Last = m.LastSlice
		st.Stale = m.LastSlice.stamp(m).Stale
	}
	return st, nil
}

func (s *Store) isStale(id string, l *LastSlice) bool {
	m, err := s.readMeta(id)
	if err != nil {
		return false
	}
	for _, p := range l.Plates {
		if m.plateStale(p) {
			return true
		}
	}
	return false
}

// CancelSlice stops a running slice job.
func (s *Store) CancelSlice(jobID string) (bool, error) {
	if s.cfg.Jobs == nil || !isJobRef(jobID) {
		return false, notFoundf("", "no slice job %q", jobID)
	}
	j := s.cfg.Jobs.Get(jobID)
	if j == nil {
		return false, notFoundf("", "no slice job %q", jobID)
	}
	return j.Cancel(), nil
}

// SliceReport is the detail of one sliced plate.
type SliceReport struct {
	ProjectID string
	Revision  int
	Stale     bool
	Plate     PlateResult
	Summary   gcodeinfo.Summary
	Warnings  []string
}

// Report reads the G-code of the last slice of a plate again for the details:
// bounds, tools, features, objects, filament use.
func (s *Store) Report(ref string, plate int) (*SliceReport, error) {
	id, err := s.resolve(ref)
	if err != nil {
		return nil, err
	}
	m, err := s.readMeta(id)
	if err != nil {
		return nil, notFoundf("", "project %s cannot be read: %v", id, err)
	}
	if m.LastSlice == nil {
		return nil, notFoundf("call slice_project first", "project %s has not been sliced", id)
	}
	if len(m.LastSlice.Plates) == 0 {
		return nil, notFoundf("call slice_project first", "project %s has no sliced plate", id)
	}
	if plate == 0 {
		plate = m.LastSlice.Plates[0].Plate
	}
	for _, p := range m.LastSlice.Plates {
		if p.Plate != plate {
			continue
		}
		sum, err := gcodeinfo.ReadSummary(p.GCodePath)
		if err != nil {
			return nil, notFoundf("slice again", "the G-code of plate %d is gone: %v", plate, err)
		}
		sum.Config = nil
		sum.TotalFilamentChange = p.Changes // one source for the count of filament changes (D4)
		r := &SliceReport{ProjectID: id, Revision: p.Revision, Stale: m.plateStale(p), Plate: p, Summary: sum, Warnings: m.LastSlice.Warnings}
		return r, nil
	}
	var have []string
	for _, p := range m.LastSlice.Plates {
		have = append(have, strconv.Itoa(p.Plate))
	}
	return nil, notFoundf("sliced plates: "+strings.Join(have, ", ")+"; slice the plate with slice_project", "plate %d has not been sliced in project %s", plate, id)
}

var jobRefRE = regexp.MustCompile(`^slice-[0-9a-f]{8}$`)

// isJobRef reports whether ref is a slice job id. Project ids also start with
// a slug, so the whole shape is checked (a job id has eight hex digits, a
// project id six).
func isJobRef(ref string) bool { return jobRefRE.MatchString(ref) }

// failedJobError is the error of a job that failed before its own end record
// was written, with the same crash hint the record would carry.
func (s *Store) failedJobError(jobID string, res slicer.Result) *Error {
	s.mu.Lock()
	c := s.hints()[jobID]
	s.mu.Unlock()
	return slicerError(res, nil, failureHint(s.cfg.Install.Dialect, res.Outcome, c))
}

// plateStale reports whether a sliced plate changed after its slice.
func (m *meta) plateStale(p PlateResult) bool { return p.Revision < m.PlateRev[p.Plate] }

// stamp summarises the record for the list and for get_project: the totals of
// the sliced plates and which of them are stale.
func (l *LastSlice) stamp(m *meta) *SliceStamp {
	st := &SliceStamp{Time: l.Time, Revision: l.Revision, Plates: len(l.Plates)}
	for _, p := range l.Plates {
		st.TimeS += p.TimeSeconds
		st.TotalG += p.TotalG
		if m.plateStale(p) {
			st.StalePlates = append(st.StalePlates, p.Plate)
		}
	}
	st.Stale = len(st.StalePlates) > 0
	return st
}

func intList(v []int) string {
	s := make([]string, len(v))
	for i, n := range v {
		s[i] = strconv.Itoa(n)
	}
	return strings.Join(s, ", ")
}

// uploadBase is the file name stem G-code is uploaded under: the project name
// made safe. A name that loses letters (another script) or ends up empty gets
// the id suffix, so two such projects never replace each other on the printer.
func uploadBase(name, id string) string {
	s := sanitizeName(name)
	lossy := strings.TrimSpace(name) == ""
	for _, r := range name {
		if r > 127 && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
			lossy = true
		}
	}
	if lossy {
		if i := strings.LastIndex(id, "-"); i >= 0 && i+1 < len(id) {
			s += "_" + id[i+1:]
		} else {
			s += "_" + id
		}
	}
	return s
}

// sliceLockFile is the lock a slice holds for its whole run (from the
// snapshot to the end of the post processing), so two servers never slice one
// project at once.
const sliceLockFile = ".slice.lock"

// sliceGuard takes the cross process slice lock of a project without waiting.
func (s *Store) sliceGuard(id string) (unlock func(), err error) {
	out := filepath.Join(s.dir(id), outDirName)
	if err := os.MkdirAll(out, 0o755); err != nil {
		return nil, errf(CodeInternal, "", "%v", err)
	}
	fl := flock.New(filepath.Join(out, sliceLockFile))
	ok, lerr := fl.TryLock()
	if lerr != nil || !ok {
		return nil, conflictf("wait for that slice to end, or use get_slice_status in the other session", "project %s is being sliced by another process", id)
	}
	return func() { _ = fl.Unlock() }, nil
}

// primeTower is what the prime tower of a sliced plate costs: grams (from the
// filament diameter and density of each tool) and printing seconds.
func primeTower(file string, sum gcodeinfo.Summary) (grams float64, seconds int) {
	has := false
	for _, f := range sum.Features {
		has = has || f == "Prime tower"
	}
	if !has {
		return 0, 0
	}
	use, err := gcodeinfo.FeatureUsage(file, "Prime tower")
	if err != nil {
		return 0, 0
	}
	for tool, mm := range use.FilamentMM {
		d, rho := 1.75, 1.24
		if tool < len(sum.FilamentDiameter) && sum.FilamentDiameter[tool] > 0 {
			d = sum.FilamentDiameter[tool]
		}
		if tool < len(sum.FilamentDensity) && sum.FilamentDensity[tool] > 0 {
			rho = sum.FilamentDensity[tool]
		}
		grams += mm * math.Pi * d * d / 4 / 1000 * rho
	}
	return grams, int(use.Seconds + 0.5)
}

// flushCost is the purge of the tool changes of a plate, in grams, and the
// number of changes. Creality Print 7.3 writes no flush line of its own, but its
// footer counts each change's purge (charged to the filament loaded) while the
// G-code's E words do not contain it, so footer minus E words is the G-code's own
// number. Without a usable footer it is estimated from the project's flush
// matrix times the multiplier for each change in the T command sequence.
func flushCost(file string, sum gcodeinfo.Summary, cfg *threemf.Config) (grams float64, changes int, estimated bool) {
	ex, err := gcodeinfo.ReadExtrusion(file)
	if err != nil || len(ex.ToolSeq) < 2 {
		return 0, 0, false
	}
	changes = len(ex.ToolSeq) - 1
	if n := len(sum.FilamentUsedMM); n > 0 && len(sum.FilamentUsedG) == n {
		for tool, mm := range sum.FilamentUsedMM {
			if extra := mm - ex.NetMM[tool]; extra > 0.5 && mm > 0 {
				grams += extra * sum.FilamentUsedG[tool] / mm
			}
		}
		return grams, changes, false // a usable footer is the answer, also when it says 0
	}
	if cfg == nil {
		return 0, changes, true
	}
	matrix := cfg.List("flush_volumes_matrix")
	n := int(math.Round(math.Sqrt(float64(len(matrix)))))
	mult, err := strconv.ParseFloat(cfg.String("flush_multiplier"), 64)
	if err != nil || n*n != len(matrix) {
		return 0, changes, true
	}
	for i := 1; i < len(ex.ToolSeq); i++ {
		from, to := ex.ToolSeq[i-1], ex.ToolSeq[i]
		if from >= n || to >= n {
			continue
		}
		v, err := strconv.ParseFloat(matrix[from*n+to], 64)
		if err != nil {
			continue
		}
		rho := 1.24
		if to < len(sum.FilamentDensity) && sum.FilamentDensity[to] > 0 {
			rho = sum.FilamentDensity[to]
		}
		// The app raises every nonzero purge to at least 100 mm3 (GCode.cpp
		// g_min_purge_volume, v7.3.0 line 127 and 2163; not when the tower has an
		// inner wall box, which this estimate cannot see).
		vol := v * mult
		if vol > 0 && vol < 100 {
			vol = 100
		}
		grams += vol / 1000 * rho
	}
	return grams, changes, true
}
