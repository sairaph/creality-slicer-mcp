package mcpserver

// Typed data for the interactive app (internal/appui). The tools and the app
// call the same functions here, so what a tool says and what a screen shows
// cannot disagree: SlicerStatus and openInApp are built on StatusInfo and
// launch.

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/sairaph/mcp-wizard/render"

	"github.com/sairaph/creality-slicer-mcp/internal/applaunch"
	"github.com/sairaph/creality-slicer-mcp/internal/projects"
	"github.com/sairaph/creality-slicer-mcp/internal/slicer"
)

// StatusInfo is what get_slicer_status reports, before it is turned into text.
type StatusInfo struct {
	Install slicer.Install
	// ProfileSource is "install" or "data_dir": where Install.ProfileRoot is;
	// empty when no profiles were found.
	ProfileSource string
	// ProjectsDir is the folder of the project store; empty when it cannot be
	// resolved.
	ProjectsDir string
	// CatalogVersion is the settings catalog in use; empty when it cannot be
	// loaded.
	CatalogVersion string
	// TooltipCoverage is "found/total" setting descriptions of the installed
	// app; empty when none are attached.
	TooltipCoverage string
	// DescriptionsNote is why no descriptions are attached (only set when
	// Creality Print was found and the catalog loaded).
	DescriptionsNote string
	// Drift lists the settings the installed K2 presets set that the catalog
	// does not know; DriftKnown is false when that could not be checked.
	Drift      []string
	DriftKnown bool
}

// StatusInfo detects Creality Print (again when refresh) and gathers what the
// status reply is made of. The error is the failure to run the detection.
func (s *Server) StatusInfo(ctx context.Context, refresh bool) (StatusInfo, error) {
	install, err := s.env.install(ctx, refresh)
	if err != nil {
		return StatusInfo{}, err
	}
	info := StatusInfo{Install: install}
	if install.Found && install.ProfileRoot != "" {
		info.ProfileSource = "data_dir"
		if filepath.Clean(install.ProfileRoot) == filepath.Clean(filepath.Join(install.Dir, "resources", "profiles")) {
			info.ProfileSource = "install"
		}
	}
	if dir, err := s.env.deps.ProjectsDir(); err == nil {
		info.ProjectsDir = dir
	}
	cat, reason, cerr := s.env.catalog(ctx)
	if cerr != nil {
		return info, nil
	}
	st := cat.Stats()
	info.CatalogVersion = st.Version
	if st.TextsAttached {
		info.TooltipCoverage = fmt.Sprintf("%d/%d", st.TooltipsFound, st.TooltipHashes)
	} else if install.Found {
		info.DescriptionsNote = reason
	}
	// Catalog drift: settings the installed K2 presets set that the shipped
	// catalog does not know.
	if install.Found {
		if store, serr := s.env.profileStore(ctx); serr == nil {
			if keys, kerr := store.KeysSetBy(k2Model); kerr == nil && len(keys) > 0 {
				info.Drift = cat.UnknownKeys(keys)
				info.DriftKnown = true
			}
		}
	}
	return info, nil
}

// Projects returns the store of projects, building the projects layer on first
// use. The error says why it cannot be used (no Creality Print, a store folder
// that cannot be opened).
func (s *Server) Projects(ctx context.Context) (ProjectStore, error) {
	be, err := s.env.projectBackend(ctx)
	if err != nil {
		e := projectsError(err)
		return nil, &projects.Error{Code: e.Code, Message: e.Message, Hint: e.Hint}
	}
	return be.Store, nil
}

// LaunchResult is a started Creality Print window.
type LaunchResult struct {
	// Mode is projects.ViewPreview or projects.ViewProject.
	Mode  string
	Plate int
	// File is the copy in the project's view folder the window opened.
	File string
	PID  int
	// Version is the Creality Print version that was started.
	Version string
	// OtherWindow is true when a Creality Print window was already open.
	OtherWindow bool
}

// LaunchView starts a new Creality Print window on a project, as open_in_app
// does: a copy of the preview of a sliced plate (mode "preview", or "") or of
// the project (mode "project"). It never touches a window that is open. A
// failure is a *projects.Error with its code, message and hint.
func (s *Server) LaunchView(ctx context.Context, project string, plate int, mode string) (LaunchResult, error) {
	l, fail := s.launch(ctx, project, plate, mode)
	if fail != nil {
		return LaunchResult{}, fail.err
	}
	return l.LaunchResult, nil
}

// launched is a started window with what the open_in_app reply is made of.
type launched struct {
	LaunchResult
	install slicer.Install
	view    *projects.ViewFile
	store   ProjectStore
}

// launchFail is a failure of launch: the error result open_in_app sends and
// the same error as a value.
type launchFail struct {
	res *toolResult
	err *projects.Error
}

func failFromError(e render.Error) *launchFail {
	return &launchFail{res: render.ErrorResult(e), err: &projects.Error{Code: e.Code, Message: e.Message, Hint: e.Hint}}
}

// launch writes the file to show and starts the application on it. mode "" is
// the preview and plate 0 is plate 1.
func (s *Server) launch(ctx context.Context, project string, plate int, mode string) (launched, *launchFail) {
	install, err := s.env.install(ctx, false)
	if err != nil {
		return launched{}, failFromError(failureError("detect Creality Print", err, ""))
	}
	if !install.Found || !install.Supported || install.Exe == "" {
		reason := install.Reason
		if reason == "" {
			reason = "no supported Creality Print (7.2 or 7.3) was found"
		}
		return launched{}, failFromError(unavailableError("open the app", errors.New(reason)))
	}
	be, err := s.env.projectBackend(ctx)
	if err != nil {
		return launched{}, failFromError(projectsError(err))
	}
	if mode == "" {
		mode = projects.ViewPreview
	}
	if plate == 0 {
		plate = 1
	}
	vf, err := be.Store.PrepareView(project, plate, mode)
	if err != nil {
		code, msg, hint := projErrorParts(projects.AsError(err))
		return launched{}, &launchFail{res: projFailure(err), err: &projects.Error{Code: code, Message: msg, Hint: hint}}
	}
	pid, err := s.env.deps.Launcher.Launch(install.Exe, vf.Path)
	if err != nil {
		hint := "Check that Creality Print starts from its own shortcut, then call open_in_app again."
		if errors.Is(err, applaunch.ErrUnderTest) {
			hint = "The real launcher does not run under go test."
		}
		return launched{}, failFromError(render.Error{Code: render.CodeUnavailable, Message: shortMessage(fmt.Sprintf("Could not start Creality Print: %v", err)), Hint: hint})
	}
	return launched{
		LaunchResult: LaunchResult{Mode: vf.Mode, Plate: vf.Plate, File: vf.Path, PID: pid, Version: install.Version, OtherWindow: install.GUIRunning},
		install:      install, view: vf, store: be.Store,
	}, nil
}
