package mcpserver

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sairaph/creality-slicer-mcp/internal/gcodeinfo"
)

// --- analyze_toolpaths ---

func (s *Server) registerAnalyzeTools() {
	addTool(s.mcpServer, "analyze_toolpaths", withEnum(inputSchema[analyzeInput](map[string]string{"plate": "1", "detail": `"summary"`}), "detail", "summary", "per_layer"), s.analyzeToolpaths)
}

type analyzeInput struct {
	Project  string    `json:"project"`
	Plate    *int      `json:"plate,omitempty"`
	Objects  []string  `json:"objects,omitempty"`
	Layers   []int     `json:"layers,omitempty"`
	Z        []float64 `json:"z,omitempty"`
	Features []string  `json:"features,omitempty"`
	Measure  []string  `json:"measure,omitempty"`
	Center   []float64 `json:"center,omitempty"`
	MinRun   *float64  `json:"min_run,omitempty"`
	Detail   *string   `json:"detail,omitempty"`
	Page     *int      `json:"page,omitempty"`
}

type analyzeObjectFront struct {
	Name       string `yaml:"name"`
	FirstLayer int    `yaml:"first_layer,omitempty"`
	LastLayer  int    `yaml:"last_layer,omitempty"`
	Gaps       int    `yaml:"gap_layers,omitempty"`
}

type analyzeFront struct {
	baseFront `yaml:",inline"`
	Plate     int                  `yaml:"plate"`
	Stale     bool                 `yaml:"stale,omitempty"`
	Layers    int                  `yaml:"layers"`
	ByObject  bool                 `yaml:"by_object,omitempty"`
	Measure   []string             `yaml:"measure"`
	Objects   []analyzeObjectFront `yaml:"objects"`
	// Findings count what the finding measures found.
	UnsupportedStarts int `yaml:"unsupported_starts,omitempty"`
	ShortRuns         int `yaml:"short_runs,omitempty"`
	SupportClusters   int `yaml:"support_clusters,omitempty"`
	pageFront         `yaml:",inline"`
}

type pageFront struct {
	Page       int `yaml:"page,omitempty"`
	Total      int `yaml:"total,omitempty"`
	TotalPages int `yaml:"total_pages,omitempty"`
}

func (s *Server) analyzeToolpaths(ctx context.Context, _ *mcp.CallToolRequest, in analyzeInput) (*mcp.CallToolResult, any, error) {
	be, fail := s.projectsOrFail(ctx)
	if fail != nil {
		return fail, nil, nil
	}
	opt := gcodeinfo.AnalyzeOptions{Objects: in.Objects, Features: in.Features, Measures: in.Measure}
	for _, m := range opt.Measures {
		ok := false
		for _, k := range gcodeinfo.AllMeasures {
			ok = ok || k == m
		}
		if !ok {
			return invalidInput(fmt.Sprintf("Unknown measure %q", m), "measure values: "+strings.Join(gcodeinfo.AllMeasures, ", ")+"."), nil, nil
		}
	}
	if len(opt.Measures) == 0 {
		opt.Measures = []string{gcodeinfo.MeasureFirstLayers, gcodeinfo.MeasureBounds}
	}
	if len(in.Layers) > 0 && len(in.Z) > 0 {
		return invalidInput("Give layers or z, not both", "layers is [from, to] by layer number, z is [from, to] in mm."), nil, nil
	}
	if len(in.Layers) > 0 {
		if len(in.Layers) != 2 || in.Layers[0] < 1 || in.Layers[1] < in.Layers[0] {
			return invalidInput("layers needs [from, to], both from 1, from not above to", "Example: {\"layers\": [1, 10]}."), nil, nil
		}
		opt.LayerFrom, opt.LayerTo = in.Layers[0], in.Layers[1]
	}
	if len(in.Z) > 0 {
		if len(in.Z) != 2 || in.Z[0] < 0 || in.Z[1] < in.Z[0] {
			return invalidInput("z needs [from, to] in mm, from not above to", "Example: {\"z\": [0.2, 2]}."), nil, nil
		}
		opt.UseZ, opt.ZFrom, opt.ZTo = true, in.Z[0], in.Z[1]
	}
	hasMeasure := func(m string) bool {
		for _, k := range opt.Measures {
			if k == m {
				return true
			}
		}
		return false
	}
	if hasMeasure(gcodeinfo.MeasureRadius) {
		if len(in.Center) != 2 {
			return invalidInput("The radius measure needs center", "Give center as [x, y] in plate mm, for example the middle of a round part."), nil, nil
		}
		opt.Center = &[2]float64{in.Center[0], in.Center[1]}
	}
	if in.MinRun != nil {
		if *in.MinRun <= 0 {
			return invalidInput("min_run must be above 0", "It is the run length in mm below which a run counts as short (default 1)."), nil, nil
		}
		opt.MinRun = *in.MinRun
	}
	detail := deref(in.Detail)
	switch detail {
	case "", "summary":
		detail = "summary"
	case "per_layer":
		opt.PerLayer = true
	default:
		return invalidInput(fmt.Sprintf("Unknown detail %q", detail), "detail is summary (default) or per_layer."), nil, nil
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
	an, err := gcodeinfo.Analyze(rep.Plate.GCodePath, opt)
	if err != nil {
		return notFound("The G-code cannot be analysed: "+err.Error(), "Slice again with slice_project."), nil, nil
	}
	front := analyzeFront{baseFront: base(info), Plate: rep.Plate.Plate, Stale: rep.Stale, Layers: an.Layers, ByObject: an.ByObject, Measure: opt.Measures,
		UnsupportedStarts: len(an.UnsupportedStarts), SupportClusters: len(an.SupportContacts)}
	for _, r := range an.ShortRuns {
		if r.Layer == 0 {
			front.ShortRuns += r.Short
		}
	}
	rangeOf := map[string]gcodeinfo.FirstLayersRow{}
	for _, r := range an.FirstLayers {
		rangeOf[r.Object] = r
	}
	for _, o := range an.Objects {
		of := analyzeObjectFront{Name: o.Display}
		if r, ok := rangeOf[o.Display]; ok {
			of.FirstLayer, of.LastLayer, of.Gaps = r.FirstLayer, r.LastLayer, len(r.Gaps)
		}
		front.Objects = append(front.Objects, of)
	}
	var head strings.Builder
	fmt.Fprintf(&head, "Plate %d, %d layer(s), %d object(s) analysed.", rep.Plate.Plate, an.Layers, len(an.Objects))
	if rep.Stale {
		fmt.Fprintf(&head, "\nNote: the project changed after this slice (slice revision %d, now %d); this is the old toolpath. Slice again for the current one.", rep.Revision, info.Revision)
	}
	if an.ByObject {
		head.WriteString("\nThis plate is printed by object: a layer number is the layer within its object (each object counts from 1), and the layer below is the object's own.")
	}
	if len(an.Objects) == 0 {
		head.WriteString("\nNo object matched. Names are the ones get_project shows; the objects in the G-code are " + strings.Join(an.AllLabels, ", ") + ".")
	}
	rows := analysisRows(an, opt, detail == "per_layer")
	page := 1
	if in.Page != nil {
		page = *in.Page
	}
	window, meta, next, err := paginatePage(rows, page, func(w []string) (string, error) { return strings.Join(w, "\n"), nil })
	if err != nil {
		return failure(ctx, "page the analysis", err, ""), nil, nil
	}
	if len(window) == 0 && len(rows) > 0 {
		return invalidInput(fmt.Sprintf("Page %d is past the end: %d page(s)", meta.Page, meta.TotalPages), "Ask for a smaller page number."), nil, nil
	}
	front.pageFront = pageFront{Page: meta.Page, Total: meta.Total, TotalPages: meta.TotalPages}
	body := head.String() + "\n\n" + strings.Join(window, "\n")
	body += "\n" + strings.TrimSpace(next)
	body += "\nNext: narrow with objects, layers, z, features or one measure; detail per_layer lists every layer."
	return successResult(front, strings.TrimRight(body, "\n")), nil, nil
}

func f2(v float64) string {
	if math.IsInf(v, 0) || math.IsNaN(v) {
		return "-"
	}
	return fmt.Sprintf("%.2f", v)
}

func f3(v float64) string {
	if math.IsInf(v, 0) || math.IsNaN(v) {
		return "-"
	}
	return fmt.Sprintf("%.3f", v)
}

func ints(l []int) string {
	if len(l) == 0 {
		return "-"
	}
	var parts []string
	for i, v := range l {
		if i == 20 {
			parts = append(parts, fmt.Sprintf("... %d more", len(l)-20))
			break
		}
		parts = append(parts, fmt.Sprint(v))
	}
	return strings.Join(parts, ", ")
}

// analysisRows renders the measures as lines (headings, table rows, blanks) so
// that the paging can cut between any two of them.
func analysisRows(an *gcodeinfo.Analysis, opt gcodeinfo.AnalyzeOptions, perLayer bool) []string {
	var rows []string
	add := func(format string, a ...any) { rows = append(rows, fmt.Sprintf(format, a...)) }
	on := map[string]bool{}
	for _, m := range opt.Measures {
		on[m] = true
	}
	if on[gcodeinfo.MeasureFirstLayers] {
		add("## first_layers (object | feature | first layer | last layer)")
		for _, r := range an.FirstLayers {
			for _, f := range r.Features {
				add("%s | %s | %d | %d", pipeSafe(r.Object), pipeSafe(f.Feature), f.First, f.Last)
			}
		}
		add("")
		add("Layers with no extrusion inside an object's range (object | range | gap layers):")
		for _, r := range an.FirstLayers {
			add("%s | %d-%d | %s", pipeSafe(r.Object), r.FirstLayer, r.LastLayer, ints(r.Gaps))
		}
		add("")
	}
	if on[gcodeinfo.MeasureBounds] {
		add("## bounds in mm (object | feature | layer | z | x min | x max | y min | y max | span x | span y)")
		for _, r := range an.Bounds {
			if r.Layer == 0 || perLayer {
				layer, z := "all", "-"
				if r.Layer > 0 {
					layer, z = fmt.Sprint(r.Layer), f2(r.Z)
				}
				add("%s | %s | %s | %s | %s | %s | %s | %s | %s | %s", pipeSafe(r.Object), pipeSafe(r.Feature), layer, z, f2(r.MinX), f2(r.MaxX), f2(r.MinY), f2(r.MaxY), f2(r.MaxX-r.MinX), f2(r.MaxY-r.MinY))
			}
		}
		add("")
	}
	if on[gcodeinfo.MeasureFlow] {
		add("## flow (object | feature | layer | segments | length mm | E mm | E per mm | ratio median | p01 | p99 | max); ratio 1 is the flow the width and height ask for")
		for _, r := range an.Flow {
			layer := "all"
			if r.Layer > 0 {
				layer = fmt.Sprint(r.Layer)
			}
			add("%s | %s | %s | %d | %.1f | %.2f | %s | %s | %s | %s | %s", pipeSafe(r.Object), pipeSafe(r.Feature), layer, r.Segments, r.LengthMM, r.EMM, f3(r.EPerMM), f3(r.Median), f3(r.P01), f3(r.P99), f3(r.Max))
		}
		add("")
	}
	if on[gcodeinfo.MeasureRadius] {
		c := opt.Center
		add("## radius from (%s, %s) in mm (object | feature | layer | min | max)", f2(c[0]), f2(c[1]))
		for _, r := range an.Radius {
			if r.Layer == 0 || perLayer {
				layer := "all"
				if r.Layer > 0 {
					layer = fmt.Sprint(r.Layer)
				}
				add("%s | %s | %s | %s | %s", pipeSafe(r.Object), pipeSafe(r.Feature), layer, f2(r.Min), f2(r.Max))
			}
		}
		add("")
	}
	if on[gcodeinfo.MeasureShortRuns] {
		minRun := opt.MinRun
		if minRun <= 0 {
			minRun = 1
		}
		add("## short_runs: runs shorter than %s mm (object | layer | z | runs | short | end positions x,y)", num(minRun))
		for _, r := range an.ShortRuns {
			if r.Layer == 0 {
				add("%s | all | - | %d | %d | -", pipeSafe(r.Object), r.Runs, r.Short)
			} else if perLayer {
				var ends []string
				for _, e := range r.Ends {
					ends = append(ends, fmt.Sprintf("%s,%s", f2(e[0]), f2(e[1])))
				}
				more := ""
				if r.Short > len(r.Ends) {
					more = fmt.Sprintf(" and %d more", r.Short-len(r.Ends))
				}
				add("%s | %d | %s | %d | %d | %s%s", pipeSafe(r.Object), r.Layer, f2(r.Z), r.Runs, r.Short, strings.Join(ends, "; "), more)
			}
		}
		if !perLayer {
			add("(detail per_layer lists the layers and the end positions)")
		}
		add("")
	}
	if on[gcodeinfo.MeasureUnsupportedStart] {
		add("## unsupported_starts: extrusion that starts where the layer below has under 10%% extrusion, bridges excluded (object | layer | z | feature | x min | x max | y min | y max | area mm2 | supported %%)")
		for _, r := range an.UnsupportedStarts {
			add("%s | %d | %s | %s | %s | %s | %s | %s | %.1f | %.0f", pipeSafe(r.Object), r.Layer, f2(r.Z), pipeSafe(strings.Join(r.Features, ", ")), f2(r.MinX), f2(r.MaxX), f2(r.MinY), f2(r.MaxY), r.AreaMM2, r.SupportedPercent)
		}
		if len(an.UnsupportedStarts) == 0 {
			add("none")
		}
		add("")
	}
	if on[gcodeinfo.MeasureSupportContacts] {
		add("## support_contacts (object | feature | layer | z | x min | x max | y min | y max | area mm2 | printed on it by the next layer)")
		for _, r := range an.SupportContacts {
			add("%s | %s | %d | %s | %s | %s | %s | %s | %.1f | %s", pipeSafe(r.Object), pipeSafe(r.Feature), r.Layer, f2(r.Z), f2(r.MinX), f2(r.MaxX), f2(r.MinY), f2(r.MaxY), r.AreaMM2, pipeSafe(orDash(strings.Join(r.Above, ", "))))
		}
		if len(an.SupportContacts) == 0 {
			add("none")
		}
		add("")
	}
	if on[gcodeinfo.MeasureWallOrder] {
		add("## wall_order (object | layers outer wall first | layers inner wall first | those layers)")
		for _, r := range an.WallOrder {
			add("%s | %d | %d | %s", pipeSafe(r.Object), r.OuterFirst, r.InnerFirst, ints(r.InnerFirstLayers))
		}
		add("")
	}
	return rows
}
