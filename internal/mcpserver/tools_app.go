package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

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
	plate := 1
	if in.Plate != nil {
		plate = *in.Plate
	}
	l, fail := s.launch(ctx, in.Project, plate, deref(in.Mode))
	if fail != nil {
		return fail.res, nil, nil
	}
	install, vf, pid := l.install, l.view, l.PID
	front := openInAppFront{baseFront: baseFront{Project: vf.ProjectID, Revision: vf.Revision}, Mode: vf.Mode, Plate: vf.Plate, File: vf.Path, PID: pid, AppVersion: install.Version, SavedFiles: vf.SavedFiles}
	if info, ierr := l.store.GetProject(vf.ProjectID); ierr == nil {
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
	next := "ask the user what they saw, then change the project or slice again."
	if vf.Mode == projects.ViewProject {
		next = "wait until the user has saved in the app, then open_project with that path and into."
	}
	return successResult(front, nextLine(b.String(), next)), nil, nil
}
