package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sairaph/mcp-wizard/render"

	"github.com/sairaph/creality-slicer-mcp/internal/applaunch"
	"github.com/sairaph/creality-slicer-mcp/internal/projects"
)

func (s *Server) registerAppTools() {
	schema := inputSchema[openInAppInput](map[string]string{"plate": "1", "mode": `"preview"`})
	schema = withRange(schema, 1, 1e6, "plate")
	schema = withEnum(schema, "mode", "preview", "project")
	addTool(s.mcpServer, "open_in_app", schema, s.openInApp)
}

type openInAppInput struct {
	Project string  `json:"project"`
	Plate   *int    `json:"plate,omitempty"`
	Mode    *string `json:"mode,omitempty"`
}

type openInAppFront struct {
	baseFront  `yaml:",inline"`
	Mode       string `yaml:"mode"`
	Plate      int    `yaml:"plate"`
	File       string `yaml:"file"`
	PID        int    `yaml:"pid"`
	AppVersion string `yaml:"app_version"`
	// SavedFiles are files in the view folder the user saved changes into.
	SavedFiles []string `yaml:"saved_files,omitempty"`
}

// openInApp writes the file to show (a copy in the project's view folder) and
// starts a new Creality Print window on it. It never closes, signals or replaces
// a window that is open, and it starts the application with that one file and
// no option.
func (s *Server) openInApp(ctx context.Context, _ *mcp.CallToolRequest, in openInAppInput) (*mcp.CallToolResult, any, error) {
	install, err := s.env.install(ctx, false)
	if err != nil {
		return failure(ctx, "detect Creality Print", err, ""), nil, nil
	}
	if !install.Found || !install.Supported || install.Exe == "" {
		reason := install.Reason
		if reason == "" {
			reason = "no supported Creality Print (7.2 or 7.3) was found"
		}
		return unavailable("open the app", errors.New(reason)), nil, nil
	}
	be, fail := s.projectsOrFail(ctx)
	if fail != nil {
		return fail, nil, nil
	}
	mode := deref(in.Mode)
	if mode == "" {
		mode = projects.ViewPreview
	}
	plate := 1
	if in.Plate != nil {
		plate = *in.Plate
	}
	vf, err := be.Store.PrepareView(in.Project, plate, mode)
	if err != nil {
		return projFailure(err), nil, nil
	}
	pid, err := s.env.deps.Launcher.Launch(install.Exe, vf.Path)
	if err != nil {
		hint := "Check that Creality Print starts from its own shortcut, then call open_in_app again."
		if errors.Is(err, applaunch.ErrUnderTest) {
			hint = "The real launcher does not run under go test."
		}
		return render.ErrorResult(render.Error{Code: render.CodeUnavailable, Message: shortMessage(fmt.Sprintf("Could not start Creality Print: %v", err)), Hint: hint}), nil, nil
	}
	front := openInAppFront{baseFront: baseFront{Project: vf.ProjectID, Revision: vf.Revision}, Mode: vf.Mode, Plate: vf.Plate, File: vf.Path, PID: pid, AppVersion: install.Version, SavedFiles: vf.SavedFiles}
	if info, ierr := be.Store.GetProject(vf.ProjectID); ierr == nil {
		front.baseFront = base(info)
	}

	var b strings.Builder
	if vf.Mode == projects.ViewProject {
		fmt.Fprintf(&b, "Started a new Creality Print %s window (process %d) with the project in the 3D editor: `%s`. The app takes a while to start.\n\n", install.Version, pid, vf.Path)
	} else {
		fmt.Fprintf(&b, "Started a new Creality Print %s window (process %d) with the sliced plate %d in the Preview tab: `%s`. The app takes a while to start.\n\n", install.Version, pid, vf.Plate, vf.Path)
	}
	b.WriteString("Other Creality Print windows are untouched, and this server never closes or signals one: the user closes the new window when done. The file is a copy in the project's view folder.")
	if vf.Mode == projects.ViewProject {
		fmt.Fprintf(&b, "\n\nTo bring changes back, the user saves the project in the app (File > Save Project), then call open_project with path `%s` and into `%s`.", vf.Path, vf.ProjectID)
	} else {
		b.WriteString("\n\nTo edit instead, call open_in_app with mode project.")
	}
	for _, saved := range vf.SavedFiles {
		fmt.Fprintf(&b, "\n\nYou saved changes in `%s`: bring them back with open_project with {\"path\": \"%s\", \"into\": \"%s\"}. That file is kept as it is.", saved, saved, vf.ProjectID)
	}
	return successResult(front, b.String()), nil, nil
}
