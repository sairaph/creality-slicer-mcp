package mcpserver

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sairaph/mcp-wizard/render"

	"github.com/sairaph/creality-slicer-mcp/internal/projects"
	"github.com/sairaph/creality-slicer-mcp/internal/threemf"
)

// projFailure turns an error of the projects layer into the error result of a
// tool: its code, message and hint as they are. A slicer failure carries the
// slicer's own output as a second text item.
func projFailure(err error) *toolResult {
	e := projects.AsError(err)
	code := e.Code
	switch code {
	case projects.CodeSlicerError:
		code = codeSlicer
	case projects.CodeInvalidInput, projects.CodeNotFound, projects.CodeConflict, projects.CodeUnavailable, projects.CodeInternal:
	default:
		code = render.CodeInternal
	}
	re := render.Error{Code: code, Message: shortMessage(e.Message), Hint: e.Hint}
	var tail string
	if len(e.Fields) > 0 {
		fields := map[string]any{}
		for k, v := range e.Fields {
			if k == "output_tail" {
				tail, _ = v.(string)
				continue
			}
			fields[k] = v
		}
		if len(fields) > 0 {
			re.Fields = fields
		}
	}
	res := render.ErrorResult(re)
	if strings.TrimSpace(tail) != "" {
		res.Content = append(res.Content, &mcp.TextContent{Text: "Output:\n" + textBlock(tail)})
	}
	return res
}

// baseFront is the front matter every project scoped reply starts with.
type baseFront struct {
	Project  string `yaml:"project"`
	Name     string `yaml:"name"`
	Revision int    `yaml:"revision"`
}

func base(in *projects.Info) baseFront {
	return baseFront{Project: in.ID, Name: in.Name, Revision: in.Revision}
}

// filamentFront is one filament slot in a front matter.
type filamentFront struct {
	Index  int    `yaml:"index"`
	Preset string `yaml:"preset"`
	Type   string `yaml:"type"`
	Colour string `yaml:"colour"`
}

func filamentsFront(in *projects.Info) []filamentFront {
	out := make([]filamentFront, len(in.Filaments))
	for i, f := range in.Filaments {
		out[i] = filamentFront{Index: f.Index, Preset: f.Preset, Type: f.Type, Colour: f.Colour}
	}
	return out
}

// plateFront is one plate in a front matter.
type plateFront struct {
	Index         int    `yaml:"index"`
	Name          string `yaml:"name,omitempty"`
	Objects       int    `yaml:"objects"`
	BedType       string `yaml:"bed_type,omitempty"`
	PrintSequence string `yaml:"print_sequence,omitempty"`
	Locked        bool   `yaml:"locked"`
}

func platesFront(in *projects.Info) []plateFront {
	out := make([]plateFront, len(in.Plates))
	for i, p := range in.Plates {
		out[i] = plateFront{Index: p.Index, Name: p.Name, Objects: p.Objects, BedType: p.BedType, PrintSequence: p.PrintSequence, Locked: p.Locked}
	}
	return out
}

// lastSliceFront summarises the last slice in a front matter.
type lastSliceFront struct {
	Time     string  `yaml:"time"`
	Revision int     `yaml:"revision"`
	Plates   int     `yaml:"plates"`
	TimeS    int     `yaml:"time_s"`
	TotalG   float64 `yaml:"total_g"`
	Stale    bool    `yaml:"stale"`
}

func lastFront(st *projects.SliceStamp) *lastSliceFront {
	if st == nil {
		return nil
	}
	return &lastSliceFront{Time: st.Time.UTC().Format(time.RFC3339), Revision: st.Revision, Plates: st.Plates, TimeS: st.TimeS, TotalG: round1(st.TotalG), Stale: st.Stale}
}

func round1(f float64) float64 { return float64(int(f*10+0.5)) / 10 }

// projectBody renders the summary every project reply carries: presets,
// filaments, plates and objects, overrides and warnings.
func projectBody(in *projects.Info, labels map[int][]string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Project `%s` (id `%s`), revision %d. Printer `%s`, process `%s`.\n", in.Name, in.ID, in.Revision, in.Printer, in.Process)
	b.WriteString("\nFilaments (slot | preset | type | colour):\n")
	for _, f := range in.Filaments {
		fmt.Fprintf(&b, "%d | %s | %s | %s\n", f.Index, pipeSafe(f.Preset), f.Type, f.Colour)
	}
	b.WriteString("\nPlates:\n")
	for _, p := range in.Plates {
		name := p.Name
		if name == "" {
			name = "-"
		}
		lock := ""
		if p.Locked {
			lock = ", locked"
		}
		fmt.Fprintf(&b, "%d | %s | %d object(s) | %s | %s%s\n", p.Index, pipeSafe(name), p.Objects, orDash(p.BedType), orDash(p.PrintSequence), lock)
	}
	if len(in.Objects) > 0 {
		b.WriteString("\nObjects (name | id | plate | size mm | position x,y,z | rotation | filament | overrides | parts" + labelHeader(labels) + "):\n")
		for _, o := range in.Objects {
			extra := ""
			if o.Instances > 1 {
				extra += fmt.Sprintf(" (%d instances)", o.Instances)
			}
			if o.Painted {
				extra += " painted"
			}
			if o.Outside {
				extra += " OUTSIDE"
			}
			fmt.Fprintf(&b, "%s | %d | %d | %s | %s | %s | %d | %d | %d%s%s\n", pipeSafe(o.Name), o.ID, o.Plate, vec3(o.Size), vec3(o.Position), vec3(o.Rotation), o.Filament, o.Overrides, len(o.Parts), labelCell(labels, o.ID), extra)
		}
		writeParts(&b, in)
	} else {
		b.WriteString("\nNo objects yet: call add_model.\n")
	}
	if in.Overrides > 0 {
		keys := in.OverrideKeys
		more := ""
		if len(keys) > 20 {
			keys, more = keys[:20], fmt.Sprintf(" and %d more", len(keys)-20)
		}
		fmt.Fprintf(&b, "\nChanged from the presets (%d): %s%s.\n", in.Overrides, strings.Join(keys, ", "), more)
	}
	if in.FlushMode != "" && len(in.Filaments) > 1 {
		fmt.Fprintf(&b, "\nFlush matrix: %s (multiplier %s).\n", in.FlushMode, orDash(in.FlushMultiplier))
	}
	if in.LastSlice != nil {
		stale := ""
		if in.LastSlice.Stale {
			stale = " (older than the current revision: slice again)"
		}
		fmt.Fprintf(&b, "\nLast slice: %d plate(s), %s, %.1f g%s.\n", in.LastSlice.Plates, durText(in.LastSlice.TimeS), in.LastSlice.TotalG, stale)
	}
	if len(in.Warnings) > 0 {
		b.WriteString("\nWarnings:\n")
		for _, w := range in.Warnings {
			fmt.Fprintf(&b, "- %s\n", w.Message)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return pipeSafe(s)
}

func vec3(v [3]float64) string {
	return fmt.Sprintf("%s,%s,%s", num(v[0]), num(v[1]), num(v[2]))
}

func num(f float64) string {
	s := fmt.Sprintf("%.2f", f)
	s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	if s == "" || s == "-0" {
		return "0"
	}
	return s
}

// durText renders seconds as 1h 02m 03s.
func durText(s int) string {
	if s <= 0 {
		return "0s"
	}
	h, m, sec := s/3600, s%3600/60, s%60
	switch {
	case h > 0:
		return fmt.Sprintf("%dh %02dm %02ds", h, m, sec)
	case m > 0:
		return fmt.Sprintf("%dm %02ds", m, sec)
	}
	return fmt.Sprintf("%ds", sec)
}

func sortedStrings(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

// objectLine is one added or changed object in a body.
func objectLine(o projects.ObjectInfo) string {
	return fmt.Sprintf("`%s` (id %d) on plate %d: size %s mm, position %s, rotation %s, filament %d", o.Name, o.ID, o.Plate, vec3(o.Size), vec3(o.Position), vec3(o.Rotation), o.Filament)
}

// nextLine ends a reply body with the call to make next, unless it already
// has one.
func nextLine(body, next string) string {
	if strings.Contains(body, "\nNext:") {
		return body
	}
	return strings.TrimRight(body, "\n") + "\n\nNext: " + next
}

// projectNext is the call to make after reading a project.
func projectNext(in *projects.Info) string {
	switch {
	case len(in.Objects) == 0:
		return "add_model to put a model on a plate."
	case in.LastSlice == nil:
		return "get_view to look at the plate, slice_project, or update_settings to change settings first."
	case in.LastSlice.Stale:
		return "get_view to look at the plate, or slice_project again (the last slice is older than the project)."
	}
	return "get_view to look at the plate, get_slice_report to read the last slice, or update_settings and slice_project again."
}

// labelsOf returns the exclusion labels of the objects of a project, taken
// from its last slice; nil when it was never sliced.
func labelsOf(be ProjectBackend, in *projects.Info) map[int][]string {
	if in.LastSlice == nil {
		return nil
	}
	st, err := be.Store.SliceStatus(in.ID)
	if err != nil || st.Last == nil {
		return nil
	}
	return exclusionLabels(in, st.Last)
}

// exclusionLabels matches the objects of a project to the labels the G-code
// of a slice gives them (<name>_id_<n>_copy_<k>, what exclude_object takes).
func exclusionLabels(in *projects.Info, last *projects.LastSlice) map[int][]string {
	out := map[int][]string{}
	for _, o := range in.Objects {
		for _, p := range last.Plates {
			if p.Plate != o.Plate {
				continue
			}
			for _, name := range p.ExcludeNames {
				if strings.HasPrefix(name, o.Name+"_id_") {
					out[o.ID] = append(out[o.ID], name)
				}
			}
		}
	}
	return out
}

func labelHeader(labels map[int][]string) string {
	if labels == nil {
		return ""
	}
	return " | exclusion label"
}

func labelCell(labels map[int][]string, id int) string {
	if labels == nil {
		return ""
	}
	if l := labels[id]; len(l) > 0 {
		return " | " + strings.Join(l, ", ")
	}
	return " | -"
}

// writeParts lists the modifiers, negative parts, support enforcers and
// support blockers of the objects with their size and centre on the plate.
func writeParts(b *strings.Builder, in *projects.Info) {
	first := true
	for _, o := range in.Objects {
		for _, p := range o.Parts {
			if p.Subtype == threemf.SubtypeNormal {
				continue
			}
			if first {
				b.WriteString("\nParts (object | part id | kind | name | size mm | centre x,y,z | overrides):\n")
				first = false
			}
			fmt.Fprintf(b, "%s | %d | %s | %s | %s | %s | %d\n", pipeSafe(o.Name), p.ID, partKindName(p.Subtype), orDash(p.Name), vec3(p.Size), vec3(p.Center), p.Overrides)
		}
	}
}

// partKindName is the kind of a part as the tools name it.
func partKindName(subtype string) string {
	switch subtype {
	case threemf.SubtypeModifier:
		return "modifier"
	case threemf.SubtypeNegative:
		return "negative_part"
	}
	return subtype
}
