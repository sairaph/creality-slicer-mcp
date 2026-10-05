package mcpserver

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sairaph/creality-slicer-mcp/internal/projects"
)

// --- group_objects ---

type groupObjectsInput struct {
	Project           string   `json:"project"`
	Objects           []string `json:"objects"`
	Name              *string  `json:"name,omitempty"`
	IncludeScreenshot *bool    `json:"include_screenshot,omitempty"`
}

type groupPartFront struct {
	Name     string `yaml:"name"`
	Kind     string `yaml:"kind"`
	Filament int    `yaml:"filament"`
}

type groupObjectsFront struct {
	baseFront `yaml:",inline"`
	Object    objectFront      `yaml:"object"`
	Parts     []groupPartFront `yaml:"parts"`
	Warnings  []string         `yaml:"warnings,omitempty"`
}

func (s *Server) groupObjects(ctx context.Context, _ *mcp.CallToolRequest, in groupObjectsInput) (*mcp.CallToolResult, any, error) {
	be, fail := s.projectsOrFail(ctx)
	if fail != nil {
		return fail, nil, nil
	}
	before := warnCodesBefore(be, in.Project)
	objects := make([]string, len(in.Objects))
	for i, o := range in.Objects {
		objects[i] = strings.TrimSpace(o)
	}
	res, err := be.Store.GroupObjects(in.Project, projects.GroupRequest{Objects: objects, Name: deref(in.Name)})
	if err != nil {
		return projFailure(err), nil, nil
	}
	front := groupObjectsFront{baseFront: base(res.Info), Object: objectFrontOf(res.Object), Warnings: res.Warnings}
	var b strings.Builder
	fmt.Fprintf(&b, "Grouped %d objects into %s.\n\nParts (name | kind | filament):\n", len(objects), objectLine(res.Object))
	for _, p := range res.Parts {
		front.Parts = append(front.Parts, groupPartFront{Name: p.Name, Kind: partKindName(p.Subtype), Filament: p.Filament})
		fmt.Fprintf(&b, "%s | %s | %d\n", pipeSafe(p.Name), partKindName(p.Subtype), p.Filament)
	}
	b.WriteString(warningLines(withIntroduced(res.Warnings, before, res.Info)))
	out := successResult(front, nextLine(b.String(), "update_settings with scope part to tune one part, get_view to check, or slice_project."))
	return s.withObjectScreenshot(be, out, in.IncludeScreenshot, in.Project, res.Object.Plate, strconv.Itoa(res.Object.ID), false), nil, nil
}
