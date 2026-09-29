package mcpserver

import (
	"context"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sairaph/mcp-wizard/render"

	"github.com/sairaph/creality-slicer-mcp/internal/gcodeinfo"
	"github.com/sairaph/creality-slicer-mcp/internal/projects"
	rend "github.com/sairaph/creality-slicer-mcp/internal/render"
	"github.com/sairaph/creality-slicer-mcp/internal/slicer"
	"image/color"
)

func (s *Server) registerSliceTools() {
	addTool(s.mcpServer, "slice_project", withRange(withPositiveMax(withEnum(withRange(inputSchema[sliceInput](map[string]string{"plate": "0", "arrange": "false", "orient": "false", "wait": "60", "thumbnails": "true", "timeout": "1800"}), 0, 1e6, "plate"), "preview", "none", "small", "large"), "timeout", 1800), 1, 600, "wait"), s.sliceProject)
	addTool(s.mcpServer, "get_slice_status", withRange(inputSchema[sliceStatusInput](map[string]string{"cancel": "false", "wait": "0"}), 0, 600, "wait"), s.getSliceStatus)
	addTool(s.mcpServer, "get_slice_report", withRange(withEnum(withEnum(withEnum(inputSchema[reportInput](map[string]string{"plate": "1"}), "section", "summary", "filaments", "objects", "layers", "layer", "settings"), "color_by", "feature", "filament", "speed"), "preview", "none", "small", "large"), 1, 1e6, "plate"), s.getSliceReport)
}

// --- slice_project ---

type sliceInput struct {
	Project    string         `json:"project"`
	Plate      *int           `json:"plate,omitempty"`
	Arrange    *bool          `json:"arrange,omitempty"`
	Orient     *bool          `json:"orient,omitempty"`
	Overrides  map[string]any `json:"overrides,omitempty"`
	Background *bool          `json:"background,omitempty"`
	Wait       *float64       `json:"wait,omitempty"`
	Thumbnails *bool          `json:"thumbnails,omitempty"`
	Timeout    *float64       `json:"timeout,omitempty"`
	Preview    *string        `json:"preview,omitempty"`
}

type toolFront struct {
	Tool       string `yaml:"tool"`
	Filament   int    `yaml:"filament"` // 0 based index: T0 is 0
	Preset     string `yaml:"preset"`
	Type       string `yaml:"type"`
	Colour     string `yaml:"colour"`
	FilamentID string `yaml:"filament_id,omitempty"`
}

type slicePlateFront struct {
	Index       int       `yaml:"index"`
	Bytes       int64     `yaml:"bytes"`
	TimeS       int       `yaml:"time_s"`
	TimeText    string    `yaml:"time_text"`
	FilamentG   []float64 `yaml:"filament_g"`
	TotalG      float64   `yaml:"total_g"`
	Layers      int       `yaml:"layers"`
	Objects     int       `yaml:"objects"`
	Multicolour bool      `yaml:"multicolour"`
	Changes     int       `yaml:"changes"`
}

type handoffFront struct {
	Plate        int         `yaml:"plate"`
	GCodePath    string      `yaml:"gcode_path"`
	UploadName   string      `yaml:"upload_name"`
	Tools        []toolFront `yaml:"tools"`
	ExcludeNames []string    `yaml:"exclude_names,omitempty"`
}

type sliceFront struct {
	baseFront `yaml:",inline"`
	ElapsedS  float64           `yaml:"elapsed_s,omitempty"`
	JobID     string            `yaml:"job_id,omitempty"`
	State     string            `yaml:"state"`
	Stale     bool              `yaml:"stale,omitempty"`
	Plates    []slicePlateFront `yaml:"plates,omitempty"`
	Warnings  []string          `yaml:"warnings,omitempty"`
	Handoff   []handoffFront    `yaml:"handoff,omitempty"`
}

func (s *Server) sliceProject(ctx context.Context, _ *mcp.CallToolRequest, in sliceInput) (*mcp.CallToolResult, any, error) {
	be, fail := s.projectsOrFail(ctx)
	if fail != nil {
		return fail, nil, nil
	}
	// timeout is the anti-hang limit of the slicer run; wait is how long this
	// call waits for the result before it hands over the job id.
	timeout := 1800.0
	if in.Timeout != nil {
		timeout = *in.Timeout
	}
	wait := 60.0
	if in.Wait != nil {
		wait = math.Min(math.Max(*in.Wait, 1), 600)
	}
	opts := projects.SliceOptions{
		Arrange: boolOr(in.Arrange, false), Orient: boolOr(in.Orient, false), Overrides: in.Overrides,
		Background: boolOr(in.Background, false), Wait: time.Duration(wait * float64(time.Second)),
		SkipThumbnails: !boolOr(in.Thumbnails, true), Timeout: time.Duration(timeout * float64(time.Second)),
	}
	if in.Plate != nil {
		opts.Plate = *in.Plate
	}
	preview := deref(in.Preview)

	// The store waits for the result; run it aside so a call the client
	// gave up on ends at once. A job that was still running by then is
	// cancelled when the store hands it over.
	type sliced struct {
		out *projects.SliceOutcome
		err error
	}
	done := make(chan sliced, 1)
	go func() {
		out, err := be.Store.Slice(in.Project, opts)
		if ctx.Err() != nil && err == nil && out != nil && out.Running {
			_, _ = be.Store.CancelSlice(out.JobID)
		}
		done <- sliced{out, err}
	}()
	var r sliced
	select {
	case r = <-done:
	case <-ctx.Done():
		fmt.Fprintf(os.Stderr, "creality-slicer-mcp: slice_project of %s was cancelled by the client\n", in.Project)
		return failure(ctx, "slice the project", ctx.Err(), ""), nil, nil
	}
	if r.err != nil {
		return projFailure(r.err), nil, nil
	}
	out := r.out
	info, ierr := be.Store.GetProject(out.ProjectID)
	if ierr != nil {
		return projFailure(ierr), nil, nil
	}
	if out.Running {
		front := sliceFront{baseFront: base(info), JobID: out.JobID, State: "running"}
		poll := fmt.Sprintf("get_slice_status with {\"job_id\": \"%s\", \"wait\": 60} until it says finished (pass cancel true to stop it). The finished status reply is the same as a slice that finished in time.", out.JobID)
		body := fmt.Sprintf("Slicing started in the background as job `%s`.\n\nNext: %s", out.JobID, poll)
		if !boolOr(in.Background, false) {
			body = fmt.Sprintf("The slice is still running after %d s, so it continues in the background as job `%s`.\n\nNext: %s", int(wait), out.JobID, poll)
		}
		return successResult(front, body), nil, nil
	}
	res := sliceReply(info, out.Last, out.Warnings, false)
	if preview != "" && preview != "none" && out.Last != nil && len(out.Last.Plates) > 0 {
		res = s.attachSlicePreview(res, out.Last.Plates[0], preview)
	}
	return res, nil, nil
}

// attachSlicePreview adds what the slicer made of a plate: its toolpaths from
// the isometric view, layer upon layer in the colour of each filament, and the
// first layer of all objects (a plate printed by object has one per object).
// small is 512 and 384 pixels, large 1024 and 768 (the layer picture is square).
func (s *Server) attachSlicePreview(res *toolResult, p projects.PlateResult, preview string) *toolResult {
	w, h, sq := 512, 384, 384
	if preview == "large" {
		w, h, sq = 1024, 768, 1024
	}
	layers, err := gcodeinfo.Layers(p.GCodePath)
	if err != nil || len(layers) == 0 {
		fmt.Fprintf(os.Stderr, "creality-slicer-mcp: the slice preview was left out: %v\n", err)
		return res
	}
	var all []gcodeinfo.Move
	for _, l := range layers {
		moves, err := gcodeinfo.LayerMoves(p.GCodePath, l)
		if err != nil {
			fmt.Fprintf(os.Stderr, "creality-slicer-mcp: the slice preview was left out: %v\n", err)
			return res
		}
		all = append(all, moves...)
	}
	if data, err := rend.GCodeIso(all, w, h, layerOptions(p, fmt.Sprintf("PLATE %d TOOLPATHS", p.Plate))); err == nil {
		res = attachImage(res, data, "the toolpaths picture")
	} else {
		fmt.Fprintf(os.Stderr, "creality-slicer-mcp: the toolpaths picture was left out: %v\n", err)
	}
	if set, err := layersAtZ(p.GCodePath, layers, layers[0].Z); err == nil {
		if data, err := rend.GCodeLayer(set.moves, rend.ByFeature, sq, layerOptions(p, "FIRST LAYER")); err == nil {
			res = attachImage(res, data, "the first layer picture")
		}
	}
	return res
}

// sliceReply renders a finished slice: a summary table, the tools and the
// exclusion labels once, the warnings and the steps on the printer side. The
// handoff is in the front matter for a program and in one compact table for a
// reader.
func sliceReply(info *projects.Info, last *projects.LastSlice, warnings []string, viaStatus bool) *toolResult {
	front := sliceFront{baseFront: base(info), State: "finished"}
	if last == nil {
		return successResult(front, "The slice produced no result.")
	}
	front.Warnings = dedupe(append(append([]string(nil), last.Warnings...), warnings...))
	front.Stale = last.Revision < info.Revision
	labels := exclusionLabels(info, last)
	var b strings.Builder
	front.ElapsedS = round1(last.ElapsedS)
	if last.ElapsedS > 0 {
		fmt.Fprintf(&b, "Sliced %d plate(s) from revision %d in %s (%.0f s).\n\n", len(last.Plates), last.Revision, durText(int(last.ElapsedS+0.5)), last.ElapsedS)
	} else {
		fmt.Fprintf(&b, "Sliced %d plate(s) from revision %d.\n\n", len(last.Plates), last.Revision)
	}
	b.WriteString("Plates (plate | time | grams | layers | upload name | G-code path):\n")
	var tools, objects, actions strings.Builder
	for _, p := range last.Plates {
		front.Plates = append(front.Plates, slicePlateFront{
			Index: p.Plate, Bytes: p.Bytes, TimeS: p.TimeSeconds, TimeText: p.TimeText,
			FilamentG: roundAll(p.FilamentG), TotalG: round2(p.TotalG), Layers: p.Layers, Objects: len(p.ExcludeNames), Multicolour: p.Multicolour, Changes: p.Changes,
		})
		h := handoffFront{Plate: p.Plate, GCodePath: p.GCodePath, UploadName: p.UploadName, ExcludeNames: p.ExcludeNames}
		for _, t := range p.Tools {
			h.Tools = append(h.Tools, toolFront{Tool: fmt.Sprintf("T%d", t.Tool), Filament: t.Tool, Preset: t.Preset, Type: t.Type, Colour: t.Colour, FilamentID: t.FilamentID})
			fmt.Fprintf(&tools, "%d | T%d | %d | %s | %s | %s\n", p.Plate, t.Tool, t.Tool, pipeSafe(t.Preset), t.Type, t.Colour)
		}
		front.Handoff = append(front.Handoff, h)
		for _, a := range p.Actions {
			kind := strings.ReplaceAll(a.Kind, "_", " ")
			if a.Found {
				fmt.Fprintf(&actions, "%d | %s at layer %d (z %s mm) | found in the G-code at z %s mm\n", p.Plate, kind, a.Layer, num(a.Z), num(a.AtZ))
			} else {
				fmt.Fprintf(&actions, "%d | %s at layer %d (z %s mm) | not found in the G-code: it did nothing\n", p.Plate, kind, a.Layer, num(a.Z))
			}
		}
		fmt.Fprintf(&b, "%d | %s | %.1f | %d | %s | %s\n", p.Plate, orDefault(p.TimeText, durText(p.TimeSeconds)), p.TotalG, p.Layers, p.UploadName, p.GCodePath)
		for _, o := range info.Objects {
			if o.Plate == p.Plate {
				if l := labels[o.ID]; len(l) > 0 {
					fmt.Fprintf(&objects, "%d | %s | %s\n", p.Plate, pipeSafe(o.Name), strings.Join(l, ", "))
				}
			}
		}
	}
	if tools.Len() > 0 {
		b.WriteString("\nTools (plate | tool | filament index, 0-based | preset | type | colour):\n" + tools.String())
	}
	if actions.Len() > 0 {
		b.WriteString("\nLayer actions (plate | action | result in the G-code):\n" + actions.String())
	}
	if objects.Len() > 0 {
		b.WriteString("\nExclusion labels (plate | object | label for exclude_object):\n" + objects.String())
	}
	if front.Stale {
		fmt.Fprintf(&b, "\nThe project changed after this slice (now revision %d): slice again for current results.\n", info.Revision)
	}
	if len(front.Warnings) > 0 {
		b.WriteString("\nWarnings:\n- " + strings.Join(front.Warnings, "\n- ") + "\n")
	}
	b.WriteString("\n" + handoffText())
	return successResult(front, strings.TrimRight(b.String(), "\n"))
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func roundAll(in []float64) []float64 {
	out := make([]float64, len(in))
	for i, v := range in {
		out[i] = round2(v)
	}
	return out
}

func fmtBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d bytes", n)
}

// handoffText says how to print a slice with the creality-k2-mcp server.
func handoffText() string {
	var b strings.Builder
	b.WriteString("Next, to print (the creality-k2-mcp server does that; this server never talks to the printer):\n")
	b.WriteString("1. get_filaments: check that each tool above has a loaded spool of the same type and a close colour.\n")
	b.WriteString("2. upload_gcode_file with path = the G-code path and filename = the upload name.\n")
	b.WriteString("3. start_print with filename = the upload name, source = cfs and slot_map = a list of {filament, slot}: filament is the tool's 0-based index above, slot is the CFS slot (T1A to T4D) whose spool has the same material type. Ask the user before starting.\n")
	b.WriteString("4. During the print, exclude_object with object_name = one of the exclusion labels skips a failed object.")
	return b.String()
}

// --- get_slice_status ---

type sliceStatusInput struct {
	JobID  *string  `json:"job_id,omitempty"`
	Cancel *bool    `json:"cancel,omitempty"`
	Wait   *float64 `json:"wait,omitempty"`
}

type jobFront struct {
	JobID          string  `yaml:"job_id"`
	Project        string  `yaml:"project,omitempty"`
	State          string  `yaml:"state"`
	ElapsedSeconds float64 `yaml:"elapsed_seconds"`
	Cancelled      *bool   `yaml:"cancelled,omitempty"`
}

type jobListFront struct {
	Jobs int `yaml:"jobs"`
}

func (s *Server) getSliceStatus(ctx context.Context, _ *mcp.CallToolRequest, in sliceStatusInput) (*mcp.CallToolResult, any, error) {
	// Jobs started before a refresh of the install live in the backend that
	// started them; every other call uses the current one.
	bes, err := s.env.jobBackends(ctx)
	if err != nil {
		return unavailable("use projects", err), nil, nil
	}
	id := strings.TrimSpace(deref(in.JobID))
	if id == "" {
		if boolOr(in.Cancel, false) {
			return invalidInput("cancel needs a job_id", "Call get_slice_status with {} to list the jobs, then again with {\"job_id\": \"<id>\", \"cancel\": true}."), nil, nil
		}
		return listJobs(bes), nil, nil
	}
	if !slicer.IsJobID(id) {
		return noSuchJob(id), nil, nil
	}
	var be ProjectBackend
	found := false
	for _, cand := range bes {
		if cand.Jobs != nil && cand.Jobs.Get(id) != nil {
			be, found = cand, true
			break
		}
	}
	if !found {
		return noSuchJob(id), nil, nil
	}
	if boolOr(in.Cancel, false) {
		ok, err := be.Store.CancelSlice(id)
		if err != nil {
			return noSuchJob(id), nil, nil
		}
		st, _ := be.Store.SliceStatus(id)
		front := jobFront{JobID: id, State: "cancelled", Cancelled: &ok}
		body := "The job was cancelled.\n\nNext: slice_project to start it again."
		if !ok {
			body = "The job had already ended, so nothing was cancelled.\n\nNext: get_slice_status with {\"job_id\": \"" + id + "\"} to read how it ended."
			if st != nil {
				front.State = st.State
			}
		}
		return successResult(front, body), nil, nil
	}
	st, err := be.Store.SliceStatus(id)
	if err != nil {
		if projects.AsError(err).Code == projects.CodeNotFound {
			return noSuchJob(id), nil, nil
		}
		return projFailure(err), nil, nil
	}
	// wait holds the call until the job leaves running or the time is up.
	if in.Wait != nil && *in.Wait > 0 && st.State == "running" {
		deadline := time.Now().Add(time.Duration(math.Min(*in.Wait, 600) * float64(time.Second)))
		for st.State == "running" && time.Now().Before(deadline) {
			select {
			case <-ctx.Done():
				return failure(ctx, "wait for the slice", ctx.Err(), ""), nil, nil
			case <-time.After(500 * time.Millisecond):
			}
			if st, err = be.Store.SliceStatus(id); err != nil {
				return noSuchJob(id), nil, nil
			}
		}
	}
	front := jobFront{JobID: st.JobID, Project: st.ProjectID, State: st.State, ElapsedSeconds: round1(st.Elapsed.Seconds())}
	switch st.State {
	case "running":
		body := fmt.Sprintf("Job `%s` is running (%s so far).", st.JobID, durText(int(st.Elapsed.Seconds())))
		if strings.TrimSpace(st.Output) != "" {
			body += "\n\nLast output:\n" + textBlock(st.Output)
		}
		body += fmt.Sprintf("\n\nNext: get_slice_status with {\"job_id\": \"%s\", \"wait\": 60} to wait for it, or with cancel true to stop it.", st.JobID)
		return successResult(front, body), nil, nil
	case "failed":
		if st.Error != nil {
			return projFailure(st.Error), nil, nil
		}
		return successResult(front, "The slice failed.\n\nNext: get_project to check the project, then slice_project again."), nil, nil
	case "cancelled":
		return successResult(front, "The job was cancelled.\n\nNext: slice_project to start it again."), nil, nil
	case "none":
		return successResult(front, "This project has not been sliced yet.\n\nNext: slice_project."), nil, nil
	}
	info, err := be.Store.GetProject(st.ProjectID)
	if err != nil {
		return projFailure(err), nil, nil
	}
	return sliceReply(info, st.Last, nil, true), nil, nil
}

// noSuchJob is the reply for a job id nobody knows.
func noSuchJob(id string) *toolResult {
	return notFound(fmt.Sprintf("no slice job with id %s", id),
		"Call get_slice_status without job_id to list the jobs, or get_project to read the last slice of a project (jobs are forgotten after a day).")
}

func listJobs(bes []ProjectBackend) *toolResult {
	var snaps []slicerSnapshot
	for _, be := range bes {
		if be.Store == nil {
			continue
		}
		// The store says how each job really ended: a crashed or failed slice is failed.
		for _, j := range be.Store.ListJobs() {
			snaps = append(snaps, slicerSnapshot{id: j.ID, project: j.ProjectID, state: j.State, exit: j.ExitName, elapsed: time.Duration(j.Elapsed * float64(time.Second))})
		}
	}
	if len(snaps) == 0 {
		return successResult(jobListFront{Jobs: 0}, "No slice jobs are running or recent. Call slice_project to start one.")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d slice job(s), newest first (job | project | state | elapsed):\n", len(snaps))
	for _, sn := range snaps {
		fmt.Fprintf(&b, "%s | %s | %s | %s\n", sn.id, orDash(sn.project), sn.state, durText(int(sn.elapsed.Seconds())))
	}
	b.WriteString("\nNext: get_slice_status with {\"job_id\": \"<job>\"} for one job.")
	return successResult(jobListFront{Jobs: len(snaps)}, strings.TrimRight(b.String(), "\n"))
}

type slicerSnapshot struct {
	id, project, state, exit string
	elapsed                  time.Duration
}

// --- get_slice_report ---

type reportInput struct {
	Project string   `json:"project"`
	Plate   *int     `json:"plate,omitempty"`
	Section *string  `json:"section,omitempty"`
	Layer   *int     `json:"layer,omitempty"`
	Z       *float64 `json:"z,omitempty"`
	ColorBy *string  `json:"color_by,omitempty"`
	Preview *string  `json:"preview,omitempty"`
}

type reportFront struct {
	baseFront `yaml:",inline"`
	Plate     int    `yaml:"plate"`
	Section   string `yaml:"section"`
	Stale     bool   `yaml:"stale,omitempty"`
	Layer     int    `yaml:"layer,omitempty"`
	LayerZ    string `yaml:"layer_z,omitempty"`
	Layers    int    `yaml:"layers,omitempty"`
}

func (s *Server) getSliceReport(ctx context.Context, _ *mcp.CallToolRequest, in reportInput) (*mcp.CallToolResult, any, error) {
	be, fail := s.projectsOrFail(ctx)
	if fail != nil {
		return fail, nil, nil
	}
	plate := 1
	if in.Plate != nil {
		plate = *in.Plate
	}
	rep, err := be.Store.Report(in.Project, plate)
	if err != nil {
		return projFailure(err), nil, nil
	}
	info, err := be.Store.GetProject(rep.ProjectID)
	if err != nil {
		return projFailure(err), nil, nil
	}
	section := deref(in.Section)
	if section == "" {
		section = "summary"
	}
	front := reportFront{baseFront: base(info), Plate: rep.Plate.Plate, Section: section, Stale: rep.Stale}
	var b strings.Builder
	if rep.Stale {
		fmt.Fprintf(&b, "Note: the project changed after this slice (slice revision %d, now %d); slice again for current results.\n\n", rep.Revision, info.Revision)
	}
	p := rep.Plate
	switch section {
	case "summary":
		fmt.Fprintf(&b, "Plate %d: %s, %.1f g in total, %d layer(s), %d object(s), %s.\nG-code: %s\n", p.Plate, orDefault(p.TimeText, durText(p.TimeSeconds)), p.TotalG, p.Layers, len(p.ExcludeNames), fmtBytes(p.Bytes), p.GCodePath)
		sm := rep.Summary
		fmt.Fprintf(&b, "Generated by %s. Filament changes: %d.\n", orDefault(sm.Generator, "-"), sm.TotalFilamentChange)
		if sm.Bounds != nil {
			fmt.Fprintf(&b, "Bounds (mm): x %s to %s, y %s to %s, z 0 to %s.\n", num(sm.Bounds.MinX), num(sm.Bounds.MaxX), num(sm.Bounds.MinY), num(sm.Bounds.MaxY), num(sm.Bounds.MaxZ))
		}
		if len(rep.Warnings) > 0 {
			b.WriteString("\nWarnings:\n- " + strings.Join(rep.Warnings, "\n- ") + "\n")
		}
		b.WriteString("\nNext: get_slice_report with section filaments, objects, layers, layer or settings; the handoff steps are in the slice_project reply.")
	case "filaments":
		if len(p.Tools) == 0 {
			b.WriteString("This G-code uses no tool changes: one filament.\n")
		}
		b.WriteString("tool | slot | preset | type | colour | grams | mm | cm3\n")
		for _, t := range p.Tools {
			g, mm, cm3 := 0.0, 0.0, 0.0
			if t.Slot >= 1 && t.Slot <= len(rep.Summary.FilamentUsedG) {
				g = rep.Summary.FilamentUsedG[t.Slot-1]
			}
			if t.Slot >= 1 && t.Slot <= len(rep.Summary.FilamentUsedMM) {
				mm = rep.Summary.FilamentUsedMM[t.Slot-1]
			}
			if t.Slot >= 1 && t.Slot <= len(rep.Summary.FilamentUsedCM3) {
				cm3 = rep.Summary.FilamentUsedCM3[t.Slot-1]
			}
			fmt.Fprintf(&b, "T%d | %d | %s | %s | %s | %.2f | %.0f | %.2f\n", t.Tool, t.Slot, pipeSafe(t.Preset), t.Type, t.Colour, g, mm, cm3)
		}
		fmt.Fprintf(&b, "\nTotal %.2f g. Filament changes: %d. Purge and prime tower are part of these totals; the flush share is not reported separately.", rep.Summary.TotalFilamentG, rep.Summary.TotalFilamentChange)
	case "objects":
		if len(rep.Summary.Objects) == 0 {
			b.WriteString("The G-code lists no objects (object labels are written when the printer supports exclusion).")
			break
		}
		b.WriteString("label | centre x,y | bounding box x,y (mm)\n")
		for _, o := range rep.Summary.Objects {
			x0, y0, x1, y1 := polyBox(o)
			fmt.Fprintf(&b, "%s | %s,%s | %s to %s, %s to %s\n", pipeSafe(o.Name), num(o.CenterX), num(o.CenterY), num(x0), num(x1), num(y0), num(y1))
		}
		b.WriteString("\nThe labels are what exclude_object takes on the printer.")
	case "layers", "layer", "settings":
		return s.reportFromFile(ctx, be, info, rep, in, front, &b)
	}
	front.Layers = p.Layers
	return successResult(front, strings.TrimRight(b.String(), "\n")), nil, nil
}

func polyBox(o gcodeinfo.Object) (x0, y0, x1, y1 float64) {
	if len(o.Polygon) == 0 {
		return o.CenterX, o.CenterY, o.CenterX, o.CenterY
	}
	x0, y0, x1, y1 = math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for _, pt := range o.Polygon {
		x0, x1 = math.Min(x0, pt[0]), math.Max(x1, pt[0])
		y0, y1 = math.Min(y0, pt[1]), math.Max(y1, pt[1])
	}
	return
}

// layerMoves reads the moves of a layer (1 based; or the layer at z when
// layer is 0 and z is set) and returns the layer's index and Z.
// layerSet is the moves of every layer at one height. A plate printed by
// object has a layer for each object at the same height; a plate printed by
// layer has one.
type layerSet struct {
	moves    []gcodeinfo.Move
	first    gcodeinfo.Layer // the layer asked for (or the nearest to z)
	z        float64
	count    int  // layers at this height
	total    int  // layers in the G-code
	byObject bool // the layers go back down: one object after another
}

// printsByObject says whether the layer heights go back down, which is what a
// plate printed one object after another does.
func printsByObject(layers []gcodeinfo.Layer) bool {
	for i := 1; i < len(layers); i++ {
		if layers[i].Z < layers[i-1].Z-1e-6 {
			return true
		}
	}
	return false
}

// layersAtZ reads the moves of every layer at height z (within 0.01 mm).
func layersAtZ(path string, layers []gcodeinfo.Layer, z float64) (layerSet, error) {
	set := layerSet{z: z, total: len(layers), byObject: printsByObject(layers)}
	for _, l := range layers {
		if math.Abs(l.Z-z) > 0.011 {
			continue
		}
		moves, err := gcodeinfo.LayerMoves(path, l)
		if err != nil {
			return set, err
		}
		if set.count == 0 {
			set.first = l
		}
		set.count++
		set.moves = append(set.moves, moves...)
	}
	if set.count == 0 {
		return set, fmt.Errorf("no layer at z %s mm", num(z))
	}
	return set, nil
}

// layerMoves picks a layer by its number (1 based, counted across all objects)
// or by height and returns the moves of every layer at that height, so a plate
// printed by object shows all its objects.
func layerMoves(path string, layer int, z float64) (layerSet, error) {
	layers, err := gcodeinfo.Layers(path)
	if err != nil {
		return layerSet{}, err
	}
	if len(layers) == 0 {
		return layerSet{}, fmt.Errorf("the G-code has no layers")
	}
	idx := layer - 1
	if layer == 0 {
		best := math.Inf(1)
		for i, l := range layers {
			if d := math.Abs(l.Z - z); d < best {
				best, idx = d, i
			}
		}
	}
	if idx < 0 || idx >= len(layers) {
		return layerSet{}, fmt.Errorf("layer %d does not exist: the G-code has %d layer(s)", layer, len(layers))
	}
	set, err := layersAtZ(path, layers, layers[idx].Z)
	if err == nil {
		set.first = layers[idx]
	}
	return set, err
}

func layerOptions(p projects.PlateResult, title string) rend.LayerOptions {
	opts := rend.LayerOptions{Title: title, Bed: rend.DefaultBed}
	for _, t := range p.Tools {
		for len(opts.ToolColours) <= t.Tool {
			opts.ToolColours = append(opts.ToolColours, color.NRGBA{})
		}
		opts.ToolColours[t.Tool] = hexColour(t.Colour)
	}
	return opts
}

func hexColour(s string) color.NRGBA {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(s) < 6 {
		return color.NRGBA{}
	}
	v, err := strconv.ParseUint(s[:6], 16, 32)
	if err != nil {
		return color.NRGBA{}
	}
	return color.NRGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 255}
}

// reportFromFile answers the sections that read the G-code again.
func (s *Server) reportFromFile(ctx context.Context, be ProjectBackend, info *projects.Info, rep *projects.SliceReport, in reportInput, front reportFront, b *strings.Builder) (*mcp.CallToolResult, any, error) {
	p := rep.Plate
	switch front.Section {
	case "layers":
		layers, err := gcodeinfo.Layers(p.GCodePath)
		if err != nil {
			return notFound("The G-code cannot be read: "+err.Error(), "Slice again with slice_project."), nil, nil
		}
		front.Layers = len(layers)
		if len(layers) == 0 {
			b.WriteString("The G-code has no layers.")
			break
		}
		minH, maxH := math.Inf(1), math.Inf(-1)
		for _, l := range layers {
			minH, maxH = math.Min(minH, l.Height), math.Max(maxH, l.Height)
		}
		fmt.Fprintf(b, "%d layer(s). First layer: z %s mm, height %s mm. Last layer: z %s mm. Layer heights range from %s to %s mm.\n", len(layers), num(layers[0].Z), num(layers[0].Height), num(layers[len(layers)-1].Z), num(minH), num(maxH))
		if printsByObject(layers) {
			b.WriteString("This plate is printed by object: layers are counted across all objects, one object after another, so the same height comes up once for each object and layer N is not the Nth height.\n")
		}
		b.WriteString("Time per layer is not reported. Next: get_slice_report with section layer and layer N (1 to " + strconv.Itoa(len(layers)) + ") for one layer with a picture.")
	case "layer":
		if in.Layer == nil && in.Z == nil {
			return invalidInput("section layer needs layer or z", "Call get_slice_report with {\"section\": \"layer\", \"layer\": 1} (layers start at 1) or a z in mm."), nil, nil
		}
		layer, z := 0, 0.0
		if in.Layer != nil {
			layer = *in.Layer
		} else {
			z = *in.Z
		}
		set, err := layerMoves(p.GCodePath, layer, z)
		if err != nil {
			return invalidInput(err.Error(), "Call get_slice_report with section layers to see how many layers there are."), nil, nil
		}
		l, moves := set.first, set.moves
		front.Layer, front.LayerZ = l.Index+1, num(l.Z)
		fmt.Fprintf(b, "Layer %d at z %s mm (height %s mm).\n", l.Index+1, num(l.Z), num(l.Height))
		if set.byObject {
			fmt.Fprintf(b, "This plate is printed by object, so its %d layers are counted across all objects and one height comes up once for each. The picture and the numbers below show all %d layer(s) at z %s mm together: pick a height with z to see every object at it.\n", set.total, set.count, num(l.Z))
		}
		b.WriteString("\n" + layerSummary(moves))
		colorBy := deref(in.ColorBy)
		if colorBy == "" {
			colorBy = "feature"
		}
		size := 0
		switch deref(in.Preview) {
		case "", "small":
			size = 384
		case "large":
			size = 1024
		}
		res := successResult(front, strings.TrimRight(b.String(), "\n"))
		if size > 0 {
			opts := layerOptions(p, fmt.Sprintf("LAYER %d Z %s", l.Index+1, num(l.Z)))
			data, err := rend.GCodeLayer(moves, rend.ColorBy(colorBy), size, opts)
			if err != nil {
				return failure(ctx, "draw the layer", err, ""), nil, nil
			}
			return imageResult(front, strings.TrimRight(b.String(), "\n")+fmt.Sprintf("\n\nThe picture is coloured by %s.", colorBy), data), nil, nil
		}
		return res, nil, nil
	case "settings":
		sum, err := gcodeinfo.ReadSummary(p.GCodePath)
		if err != nil {
			return notFound("The G-code cannot be read: "+err.Error(), "Slice again with slice_project."), nil, nil
		}
		b.WriteString(s.settingsDigest(ctx, be, info, sum))
	}
	return successResult(front, strings.TrimRight(b.String(), "\n")), nil, nil
}

// layerSummary lists the moves of a layer by feature with their lengths.
func layerSummary(moves []gcodeinfo.Move) string {
	type agg struct {
		n   int
		mm  float64
		ext bool
	}
	by := map[string]*agg{}
	var order []string
	travelMM, travelN := 0.0, 0
	for _, m := range moves {
		l := math.Hypot(m.X1-m.X0, m.Y1-m.Y0)
		if !m.Extruding {
			travelMM += l
			travelN++
			continue
		}
		name := strings.TrimSpace(m.Feature)
		if name == "" {
			name = "unknown"
		}
		a := by[name]
		if a == nil {
			a = &agg{}
			by[name] = a
			order = append(order, name)
		}
		a.n++
		a.mm += l
	}
	var b strings.Builder
	b.WriteString("Extrusion by feature (moves | length mm):\n")
	for _, name := range order {
		fmt.Fprintf(&b, "%s | %d | %.0f\n", name, by[name].n, by[name].mm)
	}
	if len(order) == 0 {
		b.WriteString("none in this layer\n")
	}
	fmt.Fprintf(&b, "Travel: %d move(s), %.0f mm.\n", travelN, travelMM)
	return b.String()
}

// settingsDigest lists the settings of the slice that differ from the process
// preset, grouped by where the difference comes from: a change made in the
// project, a rule of the app, or something the tools cannot explain.
func (s *Server) settingsDigest(ctx context.Context, be ProjectBackend, info *projects.Info, sum gcodeinfo.Summary) string {
	diffs, err := be.Store.ExplainSettings(info.ID, sum.Config)
	if err != nil {
		return "The presets cannot be read, so there is nothing to compare with: " + projects.AsError(err).Message
	}
	if len(diffs) == 0 {
		return fmt.Sprintf("The slice used the settings of `%s` unchanged (compared over the settings that preset sets).", info.Process)
	}
	groups := []struct{ origin, title string }{
		{"project", "Changed in this project (update_settings or the process changes of the file)"},
		{"app", "Switched by rules of Creality Print, not by you"},
		{"other", "Differences no change of this project explains (a preset of another version, or a value the slicer normalises)"},
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Settings of the slice that differ from `%s` (preset -> used), %d in all. Each group says why:\n", info.Process, len(diffs))
	shown := 0
	for _, g := range groups {
		var lines []string
		for _, d := range diffs {
			if d.Origin == g.origin {
				lines = append(lines, fmt.Sprintf("%s: %s -> %s", d.Key, d.Preset, d.Used))
				if g.origin == "app" {
					lines[len(lines)-1] += " (" + strings.TrimPrefix(d.Why, "the app sets this automatically ") + ")"
				}
			}
		}
		if len(lines) == 0 {
			continue
		}
		more := ""
		if len(lines) > 30 {
			lines, more = lines[:30], fmt.Sprintf("\n... and %d more.", len(lines)-30)
		}
		shown += len(lines)
		fmt.Fprintf(&b, "\n%s:\n- %s%s\n", g.title, strings.Join(lines, "\n- "), more)
	}
	return strings.TrimRight(b.String(), "\n")
}

func sameSetting(a, b string) bool {
	norm := func(s string) string { return strings.TrimSuffix(strings.Trim(strings.TrimSpace(s), "\""), "%") }
	return norm(a) == norm(b)
}

var _ = render.CodeInternal
