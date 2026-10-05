package mcpserver

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sairaph/mcp-wizard/render"

	"github.com/sairaph/creality-slicer-mcp/internal/projects"
)

func (s *Server) registerProjectTools() {
	addTool(s.mcpServer, "create_project", withMinItems(withMinItems(inputSchema[createInput](nil), "filaments", 1), "spools", 1), s.createProject)
	addTool(s.mcpServer, "open_project", withEnum(inputSchema[openInput](nil), "preview", "none", "small"), s.openProject)
	addTool(s.mcpServer, "list_projects", withRange(inputSchema[listProjectsInput](nil), 1, 1e6, "page"), s.listProjects)
	addTool(s.mcpServer, "get_project", inputSchema[getProjectInput](nil), s.getProject)
	addTool(s.mcpServer, "export_project", inputSchema[exportInput](map[string]string{"overwrite": "false"}), s.exportProject)
	addTool(s.mcpServer, "delete_project", inputSchema[deleteInput](nil), s.deleteProject)
}

// projects returns the projects layer, or the error result to send.
func (s *Server) projectsOrFail(ctx context.Context) (ProjectBackend, *toolResult) {
	be, err := s.env.projectBackend(ctx)
	if err != nil {
		return ProjectBackend{}, unavailable("use projects", err)
	}
	return be, nil
}

// filamentInput is one filament slot in a request.
type filamentInput struct {
	Preset string `json:"preset"`
	Colour string `json:"colour"`
}

func filamentSpecs(in []filamentInput) []projects.FilamentSpec {
	if in == nil {
		return nil // not given: keep the project's filaments
	}
	out := make([]projects.FilamentSpec, len(in))
	for i, f := range in {
		out[i] = projects.FilamentSpec{Preset: strings.TrimSpace(f.Preset), Colour: strings.TrimSpace(f.Colour)}
	}
	return out
}

// spoolInput is one CFS spool as creality-k2-mcp get_filaments reports it.
type spoolInput struct {
	Slot      string `json:"slot,omitempty"`
	CatalogID string `json:"catalog_id,omitempty"`
	Material  string `json:"material"`
	Colour    string `json:"colour,omitempty"`
	Status    string `json:"status,omitempty"`
	Name      string `json:"name,omitempty"`
}

func spoolSpecs(in []spoolInput) []projects.SpoolSpec {
	out := make([]projects.SpoolSpec, len(in))
	for i, s := range in {
		out[i] = projects.SpoolSpec{Slot: strings.TrimSpace(s.Slot), CatalogID: strings.TrimSpace(s.CatalogID), Material: strings.TrimSpace(s.Material),
			Colour: strings.TrimSpace(s.Colour), Status: strings.TrimSpace(s.Status), Name: s.Name}
	}
	return out
}

// --- create_project ---

type createInput struct {
	Name      string          `json:"name"`
	Printer   *string         `json:"printer,omitempty"`
	Process   *string         `json:"process,omitempty"`
	Filaments []filamentInput `json:"filaments,omitempty"`
	Spools    []spoolInput    `json:"spools,omitempty"`
	BedType   *string         `json:"bed_type,omitempty"`
}

type createFront struct {
	baseFront `yaml:",inline"`
	Printer   string          `yaml:"printer"`
	Process   string          `yaml:"process"`
	Filaments []filamentFront `yaml:"filaments"`
	Plates    int             `yaml:"plates"`
}

func (s *Server) createProject(ctx context.Context, _ *mcp.CallToolRequest, in createInput) (*mcp.CallToolResult, any, error) {
	be, fail := s.projectsOrFail(ctx)
	if fail != nil {
		return fail, nil, nil
	}
	info, err := be.Store.CreateProject(projects.CreateRequest{
		Name: in.Name, Printer: deref(in.Printer), Process: deref(in.Process),
		Filaments: filamentSpecs(in.Filaments), Spools: spoolSpecs(in.Spools), BedType: deref(in.BedType),
	})
	if err != nil {
		return projFailure(err), nil, nil
	}
	front := createFront{baseFront: base(info), Printer: info.Printer, Process: info.Process, Filaments: filamentsFront(info), Plates: len(info.Plates)}
	body := "Created the project.\n\n" + projectBody(info, nil) +
		"\n\nNext: add_model with {\"project\": \"" + info.ID + "\", \"path\": \"<absolute path of an .stl, .obj or .3mf>\"}." +
		"\nFor a multi-colour print, each filament's type must match a spool loaded in the CFS: call get_guide with {\"topic\": \"multicolor-cfs\"}."
	return successResult(front, body), nil, nil
}

// --- open_project ---

type openInput struct {
	Path    string  `json:"path"`
	Name    *string `json:"name,omitempty"`
	Into    *string `json:"into,omitempty"`
	Preview *string `json:"preview,omitempty"`
}

type openFront struct {
	baseFront    `yaml:",inline"`
	SourcePath   string          `yaml:"source_path,omitempty"`
	AppVersion   string          `yaml:"app_version,omitempty"`
	Printer      string          `yaml:"printer"`
	Process      string          `yaml:"process"`
	Filaments    []filamentFront `yaml:"filaments"`
	Plates       int             `yaml:"plates"`
	Objects      int             `yaml:"objects"`
	Painted      []string        `yaml:"painted,omitempty"`
	SlicedInFile bool            `yaml:"sliced_in_file"`
}

func (s *Server) openProject(ctx context.Context, _ *mcp.CallToolRequest, in openInput) (*mcp.CallToolResult, any, error) {
	be, fail := s.projectsOrFail(ctx)
	if fail != nil {
		return fail, nil, nil
	}
	res, err := be.Store.OpenProject(projects.OpenRequest{Path: in.Path, Name: deref(in.Name), Into: strings.TrimSpace(deref(in.Into))})
	if err != nil {
		return projFailure(err), nil, nil
	}
	info := res.Info
	front := openFront{
		baseFront: base(info), SourcePath: info.SourcePath, AppVersion: res.SourceAppVersion, Printer: info.Printer, Process: info.Process,
		Filaments: filamentsFront(info), Plates: len(info.Plates), Objects: len(info.Objects), Painted: info.Painted, SlicedInFile: info.SlicedInFile,
	}
	var b strings.Builder
	if deref(in.Into) != "" {
		fmt.Fprintf(&b, "Replaced the content of project `%s` with `%s` (revision %d). The id, name and folder are kept; earlier slices are void, slice_project makes a new one. The source file is untouched.\n\n", info.ID, in.Path, info.Revision)
	} else {
		fmt.Fprintf(&b, "Opened `%s` as a copy in this server's store; the source file is untouched.\n\n", in.Path)
	}
	b.WriteString(projectBody(info, nil))
	if res.SpoolsFrom != "" {
		var slots []string
		for _, f := range info.Filaments {
			if f.Spool != nil && f.Spool.Slot != "" {
				slots = append(slots, fmt.Sprintf("filament %d -> %s", f.Index, f.Spool.Slot))
			}
		}
		fmt.Fprintf(&b, "\n\nCFS slots restored from %s: %s. The slice handoff gives start_print its slot_map.", res.SpoolsFrom, strings.Join(slots, ", "))
	}
	if len(info.Painted) > 0 {
		fmt.Fprintf(&b, "\n\nPainted data in %s is kept as it is; these tools never edit painted regions (see get_guide topic gui-handoff).", strings.Join(info.Painted, ", "))
	}
	if info.SlicedInFile {
		b.WriteString("\n\nThe file carries a slice made by the app; slice_project makes a new one.")
	}
	b.WriteString("\n\nNext: get_project to look at it, or update_settings / slice_project to change or slice it.")
	res2 := successResult(front, b.String())
	if deref(in.Preview) == "small" {
		if data, terr := be.Store.StoredThumbnail(info.ID, 1); terr == nil && len(data) > 0 {
			res2 = attachImage(res2, data, "the project's own thumbnail")
		}
	}
	return res2, nil, nil
}

// --- list_projects ---

type listProjectsInput struct {
	Page *int `json:"page,omitempty"`
}

type listProjectsFront struct {
	Count           int `yaml:"count"`
	render.PageMeta `yaml:",inline"`
}

func (s *Server) listProjects(ctx context.Context, _ *mcp.CallToolRequest, in listProjectsInput) (*mcp.CallToolResult, any, error) {
	be, fail := s.projectsOrFail(ctx)
	if fail != nil {
		return fail, nil, nil
	}
	items, err := be.Store.List()
	if err != nil {
		return failure(ctx, "list the projects", err, ""), nil, nil
	}
	page := 1
	if in.Page != nil {
		page = *in.Page
	}
	window, meta, next, err := paginatePage(items, page, func(w []projects.ListItem) (string, error) { return projectRows(w), nil })
	if err != nil {
		return failure(ctx, "page the projects", err, ""), nil, nil
	}
	var b strings.Builder
	switch {
	case len(items) == 0:
		b.WriteString("No projects yet. Call create_project, or open_project on an existing .3mf.")
	case len(window) == 0:
		fmt.Fprintf(&b, "Page %d is past the end: there are %d project(s) on %d page(s).", meta.Page, meta.Total, meta.TotalPages)
	default:
		fmt.Fprintf(&b, "%d project(s), most recently changed first; page %d of %d.\n\nid | name | printer | objects | plates | last slice\n%s\n", len(items), meta.Page, meta.TotalPages, projectRows(window))
		b.WriteString(strings.TrimSpace(next))
		b.WriteString("\nNext: get_project with {\"project\": \"<id>\"}.")
	}
	return successResult(listProjectsFront{Count: len(items), PageMeta: meta}, strings.TrimRight(b.String(), "\n")), nil, nil
}

func projectRows(items []projects.ListItem) string {
	lines := make([]string, len(items))
	for i, it := range items {
		last := "-"
		if it.LastSlice != nil {
			stale := ""
			if it.LastSlice.Stale {
				stale = ", stale"
			}
			last = fmt.Sprintf("%s (%s, %.1f g%s)", it.LastSlice.Time.Format("2006-01-02 15:04"), durText(it.LastSlice.TimeS), it.LastSlice.TotalG, stale)
		}
		lines[i] = fmt.Sprintf("%s | %s | %s | %d | %d | %s", it.ID, pipeSafe(it.Name), orDash(it.Printer), it.Objects, it.Plates, last)
	}
	return strings.Join(lines, "\n")
}

// --- get_project ---

type getProjectInput struct {
	Project string `json:"project"`
}

type getProjectFront struct {
	baseFront `yaml:",inline"`
	Printer   string          `yaml:"printer"`
	Process   string          `yaml:"process"`
	Filaments []filamentFront `yaml:"filaments"`
	Plates    []plateFront    `yaml:"plates"`
	Overrides int             `yaml:"overrides"`
	LastSlice *lastSliceFront `yaml:"last_slice,omitempty"`
}

func (s *Server) getProject(ctx context.Context, _ *mcp.CallToolRequest, in getProjectInput) (*mcp.CallToolResult, any, error) {
	be, fail := s.projectsOrFail(ctx)
	if fail != nil {
		return fail, nil, nil
	}
	info, err := be.Store.GetProject(in.Project)
	if err != nil {
		return projFailure(err), nil, nil
	}
	front := getProjectFront{baseFront: base(info), Printer: info.Printer, Process: info.Process, Filaments: filamentsFront(info),
		Plates: platesFront(info), Overrides: info.Overrides, LastSlice: lastFront(info.LastSlice)}
	return successResult(front, nextLine(projectBody(info, labelsOf(be, info)), projectNext(info))), nil, nil
}

// --- export_project ---

type exportInput struct {
	Project   string `json:"project"`
	Path      string `json:"path"`
	Overwrite *bool  `json:"overwrite,omitempty"`
}

type exportFront struct {
	Project       string `yaml:"project"`
	File          string `yaml:"file"`
	Bytes         int64  `yaml:"bytes"`
	CreatedFolder bool   `yaml:"created_folder,omitempty"`
}

func (s *Server) exportProject(ctx context.Context, _ *mcp.CallToolRequest, in exportInput) (*mcp.CallToolResult, any, error) {
	be, fail := s.projectsOrFail(ctx)
	if fail != nil {
		return fail, nil, nil
	}
	res, err := be.Store.Export(in.Project, in.Path, boolOr(in.Overwrite, false))
	if err != nil {
		return projFailure(err), nil, nil
	}
	body := fmt.Sprintf("Exported the project to `%s` (%d bytes).", res.File, res.Bytes)
	if res.CreatedFolder {
		body += " The folder did not exist, so it was created."
	}
	body += "\n\nOpen it in Creality Print for painting or visual checks. After saving there, call open_project on the saved file to bring it back (see get_guide topic gui-handoff)."
	return successResult(exportFront{Project: res.ProjectID, File: res.File, Bytes: res.Bytes, CreatedFolder: res.CreatedFolder}, nextLine(body, "slice_project, or open_project on the file after you saved changes in the app.")), nil, nil
}

// --- delete_project ---

type deleteInput struct {
	Project string `json:"project"`
	Confirm string `json:"confirm"`
}

type deleteFront struct {
	Deleted string `yaml:"deleted"`
}

func (s *Server) deleteProject(ctx context.Context, _ *mcp.CallToolRequest, in deleteInput) (*mcp.CallToolResult, any, error) {
	be, fail := s.projectsOrFail(ctx)
	if fail != nil {
		return fail, nil, nil
	}
	id, err := be.Store.Delete(in.Project, in.Confirm)
	if err != nil {
		return projFailure(err), nil, nil
	}
	return successResult(deleteFront{Deleted: id}, fmt.Sprintf("Deleted project `%s` from this server's store (its folder, with the G-code of its slices). Files you exported are not touched.\n\nNext: list_projects, or create_project to start another.", id)), nil, nil
}

// timeText formats a time for a body.
func timeText(t time.Time) string { return t.UTC().Format("2006-01-02 15:04") }
