package mcpserver

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sairaph/creality-slicer-mcp/internal/domain"
	"github.com/sairaph/creality-slicer-mcp/internal/projects"
)

// SliceFileArgs is the command line `slice`: a project file, optionally one
// plate, optionally a folder for the G-code.
type SliceFileArgs struct {
	// Path is a .3mf project file. It is copied, never changed.
	Path string
	// Plate is the plate to slice; 0 slices every plate.
	Plate int
	// Out, when set, is the folder the G-code is copied to (upload names as
	// file names); the temporary project is then deleted.
	Out string
	// Overwrite replaces G-code files that already exist in Out.
	Overwrite bool
}

// sliceFileTimeout is how long the command lets the slicer run.
const sliceFileTimeout = 30 * time.Minute

// sliceFilePoll is how often the command looks at its slice job.
const sliceFilePoll = 250 * time.Millisecond

// SliceFile imports a 3MF into the store as a project, slices it and returns
// the same reply as slice_project. The source file is never touched. Without
// Out the project stays in the store and the reply points at its G-code. When
// ctx ends (Ctrl+C) the slice is cancelled.
func (s *Server) SliceFile(ctx context.Context, a SliceFileArgs) *mcp.CallToolResult {
	be, fail := s.projectsOrFail(ctx)
	if fail != nil {
		return fail
	}
	name := strings.TrimSuffix(filepath.Base(a.Path), filepath.Ext(a.Path))
	opened, err := be.Store.OpenProject(projects.OpenRequest{Path: a.Path, Name: name})
	if err != nil {
		return projFailure(err)
	}
	id := opened.Info.ID
	discard := func() { _, _ = be.Store.Delete(id, id) }
	started, err := be.Store.Slice(id, projects.SliceOptions{Plate: a.Plate, Background: true, Timeout: sliceFileTimeout})
	if err != nil {
		discard()
		return projFailure(err)
	}
	// The slice runs as a job so that a cancelled context can stop it.
	var last *projects.LastSlice
	for {
		st, err := be.Store.SliceStatus(started.JobID)
		if err != nil {
			discard()
			return projFailure(err)
		}
		if st.State != "running" {
			if st.Error != nil {
				discard()
				return projFailure(st.Error)
			}
			if st.State == "cancelled" {
				discard()
				return failure(ctx, "slice the project", fmt.Errorf("the slice was cancelled"), "")
			}
			last = st.Last
			break
		}
		select {
		case <-ctx.Done():
			_, _ = be.Store.CancelSlice(started.JobID)
			discard()
			return failure(ctx, "slice the project", fmt.Errorf("the slice was cancelled"), "")
		case <-time.After(sliceFilePoll):
		}
	}
	info, err := be.Store.GetProject(id)
	if err != nil {
		return projFailure(err)
	}
	if a.Out != "" && last != nil {
		copied := *last
		copied.Plates = append([]projects.PlateResult(nil), last.Plates...)
		dests := make([]string, len(copied.Plates))
		for i, p := range copied.Plates {
			dests[i] = filepath.Join(a.Out, p.UploadName)
			if _, err := os.Stat(dests[i]); err == nil && !a.Overwrite {
				return invalidInput(fmt.Sprintf("%s already exists", dests[i]),
					fmt.Sprintf("Pass --overwrite to replace it, or another --out folder. The sliced G-code is kept in %s.", filepath.Dir(p.GCodePath)))
			}
		}
		if err := os.MkdirAll(a.Out, 0o755); err != nil {
			return failure(ctx, "create the output folder", err, "Pass --out with a folder that can be written.")
		}
		for i, p := range copied.Plates {
			data, err := os.ReadFile(p.GCodePath)
			if err == nil {
				err = domain.WriteFileAtomic(dests[i], data, 0o644)
			}
			if err != nil {
				return failure(ctx, "copy the G-code", err, "Pass --out with a folder that can be written.")
			}
			copied.Plates[i].GCodePath = dests[i]
		}
		last = &copied
		discard()
	}
	return sliceReply(info, last, started.Warnings, false)
}

// RecentProjects is the project list as text, newest first: the "Recent
// projects" entry of the interactive app.
func (s *Server) RecentProjects(ctx context.Context) *mcp.CallToolResult {
	be, fail := s.projectsOrFail(ctx)
	if fail != nil {
		return fail
	}
	items, err := be.Store.List()
	if err != nil {
		return failure(ctx, "list the projects", err, "")
	}
	if len(items) == 0 {
		return successResult(listProjectsFront{Count: 0}, "No projects yet. Create one with the create_project tool or open a 3MF with open_project.")
	}
	return successResult(listProjectsFront{Count: len(items)}, fmt.Sprintf("%d project(s), newest first:\n\n%s", len(items), projectRows(items)))
}
