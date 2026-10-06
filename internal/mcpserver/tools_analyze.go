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
	addTool(s.mcpServer, "analyze_toolpaths", withRange(withItemRange(withItemRange(withItemRange(withEnum(inputSchema[analyzeInput](map[string]string{"plate": "1", "detail": `"summary"`}), "detail", "summary", "per_layer"), "layers", 2, 2), "z", 2, 2), "center", 2, 2), 1, 1e6, "plate", "page"), s.analyzeToolpaths)
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
	Objects   []analyzeObjectFront `yaml:"objects,omitempty"`
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
	// An object name that matches nothing is an error that lists the names to use.
	if unknown := unknownObjects(in.Objects, an.AllLabels); len(unknown) > 0 {
		return notFound("No object named "+strings.Join(unknown, ", ")+" on plate "+fmt.Sprint(rep.Plate.Plate),
			"Names as get_project shows them: "+strings.Join(objectNames(an.AllLabels), ", ")+"."), nil, nil
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
	page := 1
	if in.Page != nil {
		page = *in.Page
	}
	if page < 1 {
		page = 1
	}
	for _, o := range an.Objects {
		if page > 1 {
			break // the per object summary is on page 1 only
		}
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
	var empty []string
	var shown []section
	total := 0
	for _, sc := range analysisSections(an, opt, detail == "per_layer") {
		if len(sc.rows) == 0 {
			empty = append(empty, sc.measure)
			continue
		}
		total += len(sc.rows)
		shown = append(shown, sc)
	}
	text, pages := pageOfSections(shown, page)
	if len(shown) > 0 && page > pages {
		front.pageFront = pageFront{Page: page, Total: total, TotalPages: pages}
		return successResult(front, nextLine(head.String()+fmt.Sprintf("\n\nPage %d is past the end: there are %d rows on %d page(s).", page, total, pages), "page=1.")), nil, nil
	}
	front.pageFront = pageFront{Page: page, Total: total, TotalPages: pages}
	body := head.String() + "\n\n" + text
	// a measure whose sections are all empty has no findings
	var noFind []string
	for _, m := range dedupeStr(empty) {
		had := false
		for _, sc := range shown {
			had = had || sc.measure == m
		}
		if !had {
			noFind = append(noFind, m)
		}
	}
	if len(noFind) > 0 {
		body += "no findings: " + strings.Join(noFind, ", ") + "\n"
	}
	// one Next line: the next page when there is one, and how to narrow
	next := "narrow with objects, layers, z, features or one measure; detail per_layer lists every layer."
	if pages > page {
		next = fmt.Sprintf("page=%d of %d for the rest, or %s", page+1, pages, next)
	}
	body += "Next: " + next
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

func dedupeStr(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range in {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// unknownObjects lists the requested names that match no object of the G-code,
// by the same rule the analysis selects with: the name before _id_ or the whole
// label, ignoring case and punctuation.
func unknownObjects(want, labels []string) []string {
	have := map[string]bool{}
	for _, l := range labels {
		have[gcodeinfo.NormObjectName(l)] = true
		have[gcodeinfo.NormObjectName(objectNames([]string{l})[0])] = true
	}
	var out []string
	for _, w := range want {
		if !have[gcodeinfo.NormObjectName(w)] {
			out = append(out, w)
		}
	}
	return out
}

// objectNames turns G-code labels (name_id_N_copy_K) into the names get_project
// shows, each once.
func objectNames(labels []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, l := range labels {
		n := l
		if i := strings.LastIndex(l, "_id_"); i >= 0 {
			n = l[:i]
		}
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out
}

// section is one table of the answer: a measure, its heading and its data rows.
type section struct {
	measure string
	heading string
	rows    []string
	foot    string // printed under the last page of the section
}

// analysisSections renders the measures as tables, findings first.
func analysisSections(an *gcodeinfo.Analysis, opt gcodeinfo.AnalyzeOptions, perLayer bool) []section {
	on := map[string]bool{}
	for _, m := range opt.Measures {
		on[m] = true
	}
	var out []section
	add := func(measure, heading string, rows []string, foot string) {
		out = append(out, section{measure: measure, heading: heading, rows: rows, foot: foot})
	}
	layerCell := func(l int) string {
		if l > 0 {
			return fmt.Sprint(l)
		}
		return "all"
	}
	if on[gcodeinfo.MeasureUnsupportedStart] {
		var rows []string
		for _, r := range an.UnsupportedStarts {
			rows = append(rows, fmt.Sprintf("%s | %d | %s | %s | %s | %s | %s | %s | %.1f | %.0f", pipeSafe(r.Object), r.Layer, f2(r.Z), pipeSafe(strings.Join(r.Features, ", ")), f2(r.MinX), f2(r.MaxX), f2(r.MinY), f2(r.MaxY), r.AreaMM2, r.SupportedPercent))
		}
		add(gcodeinfo.MeasureUnsupportedStart, "unsupported_starts: extrusion that starts where the layer below has under 10% extrusion, bridges excluded (object | layer | z | feature | x min | x max | y min | y max | area mm2 | supported %)", rows, "")
	}
	if on[gcodeinfo.MeasureSupportContacts] {
		var rows []string
		for _, r := range an.SupportContacts {
			rows = append(rows, fmt.Sprintf("%s | %s | %d | %s | %s | %s | %s | %s | %.1f | %s", pipeSafe(r.Object), pipeSafe(r.Feature), r.Layer, f2(r.Z), f2(r.MinX), f2(r.MaxX), f2(r.MinY), f2(r.MaxY), r.AreaMM2, pipeSafe(orDash(strings.Join(r.Above, ", ")))))
		}
		add(gcodeinfo.MeasureSupportContacts, "support_contacts (object | feature | layer | z | x min | x max | y min | y max | area mm2 | objects printed on or beside it (within the support z gap))", rows, "")
	}
	if on[gcodeinfo.MeasureShortRuns] {
		minRun := opt.MinRun
		if minRun <= 0 {
			minRun = 1
		}
		var rows []string
		short := 0
		for _, r := range an.ShortRuns {
			if r.Layer == 0 {
				short += r.Short
				rows = append(rows, fmt.Sprintf("%s | all | - | %d | %d | -", pipeSafe(r.Object), r.Runs, r.Short))
			} else if perLayer {
				var ends []string
				for _, e := range r.Ends {
					ends = append(ends, fmt.Sprintf("%s,%s", f2(e[0]), f2(e[1])))
				}
				more := ""
				if r.Short > len(r.Ends) {
					more = fmt.Sprintf(" and %d more", r.Short-len(r.Ends))
				}
				rows = append(rows, fmt.Sprintf("%s | %d | %s | %d | %d | %s%s", pipeSafe(r.Object), r.Layer, f2(r.Z), r.Runs, r.Short, strings.Join(ends, "; "), more))
			}
		}
		foot := ""
		if !perLayer {
			foot = "(detail per_layer lists the layers and the end positions)"
		}
		if short == 0 {
			rows = nil // every object has a total row; only a short run is a finding
		}
		add(gcodeinfo.MeasureShortRuns, fmt.Sprintf("short_runs: runs shorter than %s mm (object | layer | z | runs | short | end positions x,y)", num(minRun)), rows, foot)
	}
	if on[gcodeinfo.MeasureFirstLayers] {
		var rows, gaps []string
		for _, r := range an.FirstLayers {
			for _, f := range r.Features {
				rows = append(rows, fmt.Sprintf("%s | %s | %d | %d", pipeSafe(r.Object), pipeSafe(f.Feature), f.First, f.Last))
			}
			gaps = append(gaps, fmt.Sprintf("%s | %d-%d | %s", pipeSafe(r.Object), r.FirstLayer, r.LastLayer, ints(r.Gaps)))
		}
		add(gcodeinfo.MeasureFirstLayers, "first_layers (object | feature | first layer | last layer)", rows, "")
		add(gcodeinfo.MeasureFirstLayers, "first_layers, layers with no extrusion inside an object's range (object | range | gap layers)", gaps, "")
	}
	if on[gcodeinfo.MeasureBounds] {
		var rows []string
		for _, r := range an.Bounds {
			if r.Layer == 0 || perLayer {
				z := "-"
				if r.Layer > 0 {
					z = f2(r.Z)
				}
				rows = append(rows, fmt.Sprintf("%s | %s | %s | %s | %s | %s | %s | %s | %s | %s", pipeSafe(r.Object), pipeSafe(r.Feature), layerCell(r.Layer), z, f2(r.MinX), f2(r.MaxX), f2(r.MinY), f2(r.MaxY), f2(r.MaxX-r.MinX), f2(r.MaxY-r.MinY)))
			}
		}
		add(gcodeinfo.MeasureBounds, "bounds in mm (object | feature | layer | z | x min | x max | y min | y max | span x | span y)", rows, "")
	}
	if on[gcodeinfo.MeasureFlow] {
		var rows []string
		for _, r := range an.Flow {
			rows = append(rows, fmt.Sprintf("%s | %s | %s | %d | %.1f | %.2f | %s | %s | %s | %s | %s", pipeSafe(r.Object), pipeSafe(r.Feature), layerCell(r.Layer), r.Segments, r.LengthMM, r.EMM, f3(r.EPerMM), f3(r.Median), f3(r.P01), f3(r.P99), f3(r.Max)))
		}
		add(gcodeinfo.MeasureFlow, "flow (object | feature | layer | segments | length mm | E mm | E per mm | ratio median | p01 | p99 | max); ratio 1 is the flow the width and height ask for", rows, "")
	}
	if on[gcodeinfo.MeasureRadius] {
		c := opt.Center
		var rows []string
		for _, r := range an.Radius {
			if r.Layer == 0 || perLayer {
				rows = append(rows, fmt.Sprintf("%s | %s | %s | %s | %s", pipeSafe(r.Object), pipeSafe(r.Feature), layerCell(r.Layer), f2(r.Min), f2(r.Max)))
			}
		}
		add(gcodeinfo.MeasureRadius, fmt.Sprintf("radius from (%s, %s) in mm (object | feature | layer | min | max)", f2(c[0]), f2(c[1])), rows, "")
	}
	if on[gcodeinfo.MeasureWallOrder] {
		var rows []string
		for _, r := range an.WallOrder {
			rows = append(rows, fmt.Sprintf("%s | %d | %d | %s", pipeSafe(r.Object), r.OuterFirst, r.InnerFirst, ints(r.InnerFirstLayers)))
		}
		add(gcodeinfo.MeasureWallOrder, "wall_order (object | layers outer wall first | layers inner wall first | those layers)", rows, "")
	}
	return out
}

// pageBudget is the size of a page in characters, shared by the measures that
// still have rows.
const pageBudget = 9000

// sectionTake is how many rows of s, from index from, fit in a share.
func sectionTake(s section, from, share int) int {
	used := len(s.heading) + 40
	to := from
	for to < len(s.rows) && (to == from || used+len(s.rows[to])+1 <= share) {
		used += len(s.rows[to]) + 1
		to++
	}
	return to
}

// pageOfSections cuts page n (from 1) out of the sections. Every page gives each
// section that still has rows an equal share of the budget (at least one row), so
// no measure waits behind another. It returns the text of the page n and the
// number of pages.
func pageOfSections(secs []section, n int) (string, int) {
	cursor := make([]int, len(secs))
	var text string
	pages := 0
	for p := 1; ; p++ {
		active := 0
		for i, s := range secs {
			if cursor[i] < len(s.rows) {
				active++
			}
		}
		if active == 0 {
			return text, pages
		}
		pages = p
		share := pageBudget / active
		var b strings.Builder
		for i, s := range secs {
			if cursor[i] >= len(s.rows) {
				continue
			}
			from := cursor[i]
			to := sectionTake(s, from, share)
			cursor[i] = to
			if p == n {
				fmt.Fprintf(&b, "## %s\nrows %d-%d of %d\n%s\n", s.heading, from+1, to, len(s.rows), strings.Join(s.rows[from:to], "\n"))
				if to == len(s.rows) && s.foot != "" {
					b.WriteString(s.foot + "\n")
				}
				b.WriteString("\n")
			}
		}
		if p == n {
			text = b.String()
		}
	}
}
