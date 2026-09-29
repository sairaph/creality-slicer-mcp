package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sairaph/creality-slicer-mcp/internal/projects"
	"github.com/sairaph/creality-slicer-mcp/internal/render"
)

func (s *Server) registerViewTools() {
	names := make([]string, len(render.ViewNames))
	for i, n := range render.ViewNames {
		names[i] = string(n)
	}
	schema := inputSchema[viewInput](map[string]string{
		"plate": "1", "view_name": `"Isometric"`, "show_parts": "true", "show_labels": "true", "show_ranges": "false",
	})
	schema = withEnum(schema, "view_name", names...)
	schema = withRange(schema, 1, 1e6, "plate")
	schema = withRange(schema, 1, render.MaxSize, "width", "height")
	addTool(s.mcpServer, "get_view", schema, s.getView)
}

type viewInput struct {
	Project    string   `json:"project"`
	Plate      *int     `json:"plate,omitempty"`
	ViewName   *string  `json:"view_name,omitempty"`
	Focus      []string `json:"focus,omitempty"`
	Hide       []string `json:"hide,omitempty"`
	Isolate    []string `json:"isolate,omitempty"`
	ShowParts  *bool    `json:"show_parts,omitempty"`
	ShowLabels *bool    `json:"show_labels,omitempty"`
	ShowRanges *bool    `json:"show_ranges,omitempty"`
	Width      *int     `json:"width,omitempty"`
	Height     *int     `json:"height,omitempty"`
}

type viewFront struct {
	baseFront `yaml:",inline"`
	Plate     int      `yaml:"plate"`
	View      string   `yaml:"view"`
	Focus     []string `yaml:"focus,omitempty"`
	Objects   int      `yaml:"objects"`
	Parts     int      `yaml:"parts"`
	Ranges    *int     `yaml:"ranges,omitempty"`
	Width     int      `yaml:"width"`
	Height    int      `yaml:"height"`
}

func (s *Server) getView(ctx context.Context, _ *mcp.CallToolRequest, in viewInput) (*mcp.CallToolResult, any, error) {
	be, fail := s.projectsOrFail(ctx)
	if fail != nil {
		return fail, nil, nil
	}
	req := projects.ViewRequest{
		View: deref(in.ViewName), Focus: in.Focus, Hide: in.Hide, Isolate: in.Isolate,
		ShowParts: boolOr(in.ShowParts, true), ShowLabels: boolOr(in.ShowLabels, true), ShowRanges: boolOr(in.ShowRanges, false),
	}
	if in.Plate != nil {
		req.Plate = *in.Plate
	}
	if in.Width != nil {
		req.Width = *in.Width
	}
	if in.Height != nil {
		req.Height = *in.Height
	}
	v, err := be.Store.View(in.Project, req)
	if err != nil {
		return projFailure(err), nil, nil
	}
	info, err := be.Store.GetProject(v.ProjectID)
	if err != nil {
		return projFailure(err), nil, nil
	}
	front := viewFront{baseFront: base(info), Plate: v.Plate, View: v.View, Focus: v.Focus, Objects: v.Objects, Parts: v.Parts, Width: v.Width, Height: v.Height}
	if req.ShowRanges {
		front.Ranges = &v.Ranges
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Plate %d from the %s view, %d x %d pixels: %d object(s)", v.Plate, v.View, v.Width, v.Height, v.Objects)
	if req.ShowParts {
		fmt.Fprintf(&b, ", %d part(s)", v.Parts)
	}
	if len(v.Focus) > 0 {
		fmt.Fprintf(&b, ", framed on %s", strings.Join(v.Focus, ", "))
	}
	if req.ShowRanges {
		if v.Ranges == 0 {
			b.WriteString("; no object in the picture has height ranges (set_height_ranges adds them)")
		} else {
			fmt.Fprintf(&b, ", %d height range band(s)", v.Ranges)
		}
	}
	b.WriteString(".\n\nLegend:\n")
	for _, l := range v.Legend {
		fmt.Fprintf(&b, "- %s\n", l)
	}
	if len(v.SkippedLabels) > 0 {
		fmt.Fprintf(&b, "\nLabels left out so that none covers another: %s. Use focus, hide or isolate to see them.\n", strings.Join(v.SkippedLabels, ", "))
	}
	b.WriteString("\nNext: get_view with another view_name (Front and Right show heights), or focus on one object; update_object, add_modifier or slice_project when it looks right.")
	res := successResult(front, b.String())
	fitted, ferr := fitPNG(v.PNG, imageBudget(res))
	if ferr != nil {
		if errors.Is(ferr, errImageTooLarge) {
			return invalidInput("The picture does not fit in a reply even at its smallest size.", "Call get_view with a smaller width and height."), nil, nil
		}
		return failure(ctx, "draw the view", ferr, ""), nil, nil
	}
	if len(fitted) != len(v.PNG) {
		res = successResult(front, b.String()+"\n\nThe image was reduced to fit in the reply; ask for a smaller width and height to control it.")
	}
	if img, ok := imageContent(fitted); ok {
		res.Content = append(res.Content, img)
	}
	return res, nil, nil
}

// withScreenshot attaches an isometric picture of the whole plate, 512 pixels
// on the longest edge (4:3), with parts and labels and the changed objects
// outlined, to the
// reply of a tool that changed a project. A picture that cannot be drawn or
// does not fit never fails the call: it is logged and left out. The
// environment setting CREALITY_SLICER_MCP_ONLY_TEXT_FEEDBACK and the tool's
// include_screenshot false both turn it off.
func (s *Server) withScreenshot(be ProjectBackend, res *toolResult, include *bool, project string, plate int, focus []string, ranges bool) *toolResult {
	return s.screenshot(be, res, include, project, plate, focus, false, ranges)
}

// withPartScreenshot is withScreenshot for a change to a part of an object: a
// modifier is a few millimetres on a 260 mm plate, so the picture is framed on
// the object with its parts, which is still outlined.
func (s *Server) withPartScreenshot(be ProjectBackend, res *toolResult, include *bool, project string, plate int, object string) *toolResult {
	return s.screenshot(be, res, include, project, plate, []string{object}, true, false)
}

func (s *Server) screenshot(be ProjectBackend, res *toolResult, include *bool, project string, plate int, focus []string, frame, ranges bool) *toolResult {
	if s.config.Settings.OnlyTextFeedback || !boolOr(include, true) {
		return res
	}
	var framed []string
	if frame {
		framed = focus
	}
	if plate < 1 {
		plate = 1
	}
	v, err := be.Store.View(project, projects.ViewRequest{
		Plate: plate, View: string(render.Isometric), Focus: framed, Highlight: focus, ShowParts: true, ShowLabels: true, ShowRanges: ranges, LongEdge: 512,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "creality-slicer-mcp: the screenshot of %s was left out: %v\n", project, err)
		return res
	}
	return attachImage(res, v.PNG, "the screenshot")
}

// plateOfObject is the plate of the object with the given id or name (ignoring
// case) in info, or fallback.
func plateOfObject(info *projects.Info, ref string, fallback int) int {
	ref = strings.TrimSpace(ref)
	for _, o := range info.Objects {
		if strconv.Itoa(o.ID) == ref || strings.EqualFold(o.Name, ref) {
			return o.Plate
		}
	}
	return fallback
}
