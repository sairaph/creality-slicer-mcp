package appui

import (
	"context"
	"os/exec"

	"github.com/sairaph/creality-slicer-mcp/internal/projects"
)

// Backend is what the screens read and do. Every method takes the context of
// the load, which ctrl+c cancels. The real one is in the main package.
type Backend interface {
	Summary(ctx context.Context) (Summary, error)
	Status(ctx context.Context, refresh bool) (Status, error)
	Projects(ctx context.Context) ([]projects.ListItem, error)
	Project(ctx context.Context, id string) (ProjectView, error)
	// Launch starts Creality Print on the project: the preview of the first
	// plate whose slice is fresh, else the 3D editor.
	Launch(ctx context.Context, id string) (Launched, error)
	// Export saves a copy of the project; an *ExistsError when the file exists
	// and overwrite is false.
	Export(ctx context.Context, id, path string, overwrite bool) (ExportDone, error)
	Delete(ctx context.Context, id string) error
	Doctor(ctx context.Context) []DoctorRow
}

// Summary is the two rows under the menu.
type Summary struct {
	// SlicerLine is "Creality Print 7.3.0 is ready." when SlicerOK, else why
	// Creality Print cannot be used.
	SlicerLine string
	SlicerOK   bool
	Projects   int
	Clients    []string
}

// Status is the slicer status in the words of a person.
type Status struct {
	Found, Supported       bool
	Version, Build, Reason string
	GUIRunning             bool
	ProfileVersion         string
	// ProfileSource is "install" or "data_dir".
	ProfileSource string
	ProjectsDir   string
	// Exe and DataDir are Creality Print's program and data folder; Program is
	// this program's own file.
	Exe, DataDir, Program string
	Problems              []string
}

// ProjectView is a project with the facts of the slice of each plate.
type ProjectView struct {
	Info   *projects.Info
	Slices []PlateSlice
}

// PlateSlice is the last slice of one plate.
type PlateSlice struct {
	Plate     int
	Duration  string
	Layers    int
	Grams     float64
	GCodePath string
	Stale     bool
}

// Launched is a Creality Print window that was started.
type Launched struct {
	// Mode is "preview" or "project".
	Mode        string
	Plate       int
	PID         int
	Version     string
	OtherWindow bool
}

// ExportDone is a finished export.
type ExportDone struct {
	File  string
	Bytes int64
}

// ExistsError is the answer of Export when the file is there already.
type ExistsError struct{ Path string }

func (e *ExistsError) Error() string { return e.Path + " already exists" }

// Level is the outcome of a doctor check.
type Level int

const (
	LevelOK Level = iota
	LevelWarn
	LevelFail
)

// DoctorRow is one finished check.
type DoctorRow struct {
	Name   string
	Level  Level
	Detail string
}

// Options configure the app.
type Options struct {
	Backend Backend
	Version string
	// ConfigureCmd builds the command that runs the installer in this
	// terminal; nil when the app cannot hand off.
	ConfigureCmd func() *exec.Cmd
	// Unicode selects the spinner frames; Cwd is the folder an export starts in.
	Unicode bool
	Cwd     func() string
}
