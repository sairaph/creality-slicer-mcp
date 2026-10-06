package main

// realBackend is the data and the actions of the interactive app (see
// internal/appui) over the same server object the MCP tools run on, and over
// the doctor checks. The app never reads tool replies: it asks for the typed
// data behind them.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/sairaph/mcp-wizard/doctor"

	"github.com/sairaph/creality-slicer-mcp/internal/appui"
	"github.com/sairaph/creality-slicer-mcp/internal/domain"
	"github.com/sairaph/creality-slicer-mcp/internal/mcpserver"
	"github.com/sairaph/creality-slicer-mcp/internal/projects"
)

// envFromApp is set in the environment of the installer the app starts, so
// that its last screen says "press enter to return to the app". It is an
// internal marker, not a setting.
const envFromApp = "CREALITY_SLICER_MCP_FROM_APP"

// maxProblemKeys is how many setting keys a catalog problem names.
const maxProblemKeys = 10

type realBackend struct {
	once sync.Once
	srv  *mcpserver.Server
	err  error
}

// server builds the server once from the environment settings; a bad setting
// is the error, as it is for the MCP server.
func (b *realBackend) server() (*mcpserver.Server, error) {
	b.once.Do(func() {
		settings, err := domain.SettingsFromEnv()
		if err != nil {
			b.err = err
			return
		}
		b.srv = mcpserver.New(mcpserver.Config{Version: version, Settings: settings})
	})
	return b.srv, b.err
}

func (b *realBackend) store(ctx context.Context) (mcpserver.ProjectStore, error) {
	srv, err := b.server()
	if err != nil {
		return nil, err
	}
	return srv.Projects(ctx)
}

func (b *realBackend) Summary(ctx context.Context) (appui.Summary, error) {
	var s appui.Summary
	srv, err := b.server()
	if err != nil {
		s.SlicerLine = err.Error()
		return s, nil
	}
	info, err := srv.StatusInfo(ctx, false)
	switch {
	case err != nil:
		s.SlicerLine = "Creality Print could not be checked: " + err.Error() + ". Choose Slicer status."
	case !info.Install.Found:
		s.SlicerLine = "Creality Print was not found. Choose Slicer status."
	case !info.Install.Supported:
		s.SlicerLine = fmt.Sprintf("Creality Print %s is not supported. Choose Slicer status.", info.Install.Version)
	default:
		s.SlicerOK = true
		s.SlicerLine = fmt.Sprintf("Creality Print %s is ready.", info.Install.Version)
		store, perr := srv.Projects(ctx)
		if perr == nil {
			var items []projects.ListItem
			if items, perr = store.List(); perr == nil {
				s.Projects = len(items)
			}
		}
		if perr != nil {
			s.SlicerOK = false
			s.SlicerLine = fmt.Sprintf("Creality Print %s is ready, but the projects cannot be read: %s", info.Install.Version, appui.ErrText(perr))
		}
	}
	if clients, _, cerr := configuredClients(ctx); cerr == nil {
		for _, c := range clients {
			s.Clients = append(s.Clients, c.Name)
		}
	}
	return s, nil
}

func (b *realBackend) Status(ctx context.Context, refresh bool) (appui.Status, error) {
	srv, err := b.server()
	if err != nil {
		return appui.Status{Reason: err.Error()}, nil
	}
	info, err := srv.StatusInfo(ctx, refresh)
	if err != nil {
		return appui.Status{}, err
	}
	in := info.Install
	st := appui.Status{
		Found: in.Found, Supported: in.Supported, Version: in.Version, Build: in.Build, Reason: in.Reason,
		GUIRunning: in.GUIRunning, ProfileVersion: in.ProfileVersion, ProfileSource: info.ProfileSource,
		ProjectsDir: info.ProjectsDir, Exe: in.Exe, DataDir: in.DataDir,
	}
	if exe, err := os.Executable(); err == nil {
		st.Program = exe
	}
	switch {
	case !in.Found:
		reason := in.Reason
		if reason == "" {
			reason = "Creality Print was not found"
		}
		st.Problems = append(st.Problems, fmt.Sprintf("%s. Presets, setting descriptions and slicing need it. If it is installed in an unusual place, set %s to the full path of CrealityPrint.exe.", strings.TrimRight(reason, "."), domain.EnvCmd))
	case !in.Supported:
		reason := in.Reason
		if reason == "" {
			reason = "only versions 7.2 and 7.3 are supported"
		}
		st.Problems = append(st.Problems, fmt.Sprintf("This version cannot slice (%s). Install Creality Print 7.3 or 7.2.", strings.TrimRight(reason, ".")))
	}
	if len(info.Drift) > 0 {
		shown := info.Drift[:min(len(info.Drift), maxProblemKeys)]
		keys := strings.Join(shown, ", ")
		if len(info.Drift) > len(shown) {
			keys += fmt.Sprintf(" and %d more", len(info.Drift)-len(shown))
		}
		st.Problems = append(st.Problems, fmt.Sprintf("The installed Creality Print sets %s that this server does not know yet (catalog %s, app %s): %s.",
			plural(len(info.Drift), "setting", "settings"), info.CatalogVersion, in.Version, keys))
	}
	if in.Found && info.TooltipCoverage == "" && info.DescriptionsNote != "" {
		st.Problems = append(st.Problems, fmt.Sprintf("Setting descriptions are not available (%s); describe_setting will say so.", strings.TrimRight(info.DescriptionsNote, ".")))
	}
	if info.ProjectsDir == "" {
		st.Problems = append(st.Problems, "The projects folder cannot be resolved.")
	}
	return st, nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func (b *realBackend) Projects(ctx context.Context) ([]projects.ListItem, error) {
	store, err := b.store(ctx)
	if err != nil {
		return nil, err
	}
	return store.List()
}

func (b *realBackend) Project(ctx context.Context, id string) (appui.ProjectView, error) {
	store, err := b.store(ctx)
	if err != nil {
		return appui.ProjectView{}, err
	}
	info, err := store.GetProject(id)
	if err != nil {
		return appui.ProjectView{}, err
	}
	pv := appui.ProjectView{Info: info}
	if info.LastSlice == nil {
		return pv, nil
	}
	for _, p := range info.Plates {
		if err := ctx.Err(); err != nil {
			return appui.ProjectView{}, err
		}
		// A plate without a slice answers "not sliced": it has no slice line.
		r, err := store.Report(id, p.Index)
		if err != nil {
			continue
		}
		pv.Slices = append(pv.Slices, appui.PlateSlice{
			Plate: p.Index, Duration: r.Plate.TimeText, Layers: r.Plate.Layers, Grams: r.Plate.TotalG,
			GCodePath: r.Plate.GCodePath, Stale: r.Stale,
		})
	}
	return pv, nil
}

func (b *realBackend) Launch(ctx context.Context, id string) (appui.Launched, error) {
	srv, err := b.server()
	if err != nil {
		return appui.Launched{}, err
	}
	// The preview of the first plate whose slice is fresh, else the editor.
	plate, mode := 1, projects.ViewProject
	if pv, err := b.Project(ctx, id); err == nil {
		for _, s := range pv.Slices {
			if !s.Stale {
				plate, mode = s.Plate, projects.ViewPreview
				break
			}
		}
	}
	r, err := srv.LaunchView(ctx, id, plate, mode)
	if err != nil {
		return appui.Launched{}, err
	}
	return appui.Launched{Mode: r.Mode, Plate: r.Plate, PID: r.PID, Version: r.Version, OtherWindow: r.OtherWindow}, nil
}

func (b *realBackend) Export(ctx context.Context, id, path string, overwrite bool) (appui.ExportDone, error) {
	store, err := b.store(ctx)
	if err != nil {
		return appui.ExportDone{}, err
	}
	res, err := store.Export(id, path, overwrite)
	if err != nil {
		var pe *projects.Error
		if !overwrite && errors.As(err, &pe) && pe.Code == projects.CodeConflict && strings.Contains(pe.Message, "already exists") {
			return appui.ExportDone{}, &appui.ExistsError{Path: path}
		}
		return appui.ExportDone{}, err
	}
	return appui.ExportDone{File: res.File, Bytes: res.Bytes}, nil
}

func (b *realBackend) Delete(ctx context.Context, id string) error {
	store, err := b.store(ctx)
	if err != nil {
		return err
	}
	_, err = store.Delete(id, id)
	return err
}

// Doctor runs the health checks one by one, in order, and stops between two of
// them when ctx ends.
func (b *realBackend) Doctor(ctx context.Context) []appui.DoctorRow {
	var rows []appui.DoctorRow
	for _, c := range doctorChecks() {
		if ctx.Err() != nil {
			break
		}
		r := c.Run(ctx)
		level := appui.LevelOK
		switch r.Status {
		case doctor.Warn:
			level = appui.LevelWarn
		case doctor.Fail:
			level = appui.LevelFail
		}
		name := r.Name
		if name == "" {
			name = c.Name()
		}
		rows = append(rows, appui.DoctorRow{Name: name, Level: level, Detail: r.Detail})
	}
	return rows
}

// configureCmd is the installer, run as a child process in the terminal of the
// app: the wizard owns the terminal until it ends.
func configureCmd() *exec.Cmd {
	exe, err := os.Executable()
	if err != nil {
		exe = domain.BinaryName
	}
	cmd := exec.Command(exe, "install")
	cmd.Env = append(os.Environ(), envFromApp+"=1")
	return cmd
}
