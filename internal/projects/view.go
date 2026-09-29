package projects

import (
	"fmt"
	"strings"

	"github.com/sairaph/creality-slicer-mcp/internal/render"
)

// ViewRequest is get_view: a picture of a plate from a named camera.
type ViewRequest struct {
	// Plate is the plate to draw; 0 means 1.
	Plate int
	// View is a render.ViewName (any case); empty means Isometric.
	View string
	// Focus, Hide and Isolate are object ids or names. Focus frames the picture
	// on those objects; Hide leaves objects out; Isolate draws only those.
	Focus, Hide, Isolate []string
	// ShowParts draws modifier, negative part, support enforcer and support
	// blocker volumes; ShowLabels each object's id and name; ShowRanges the
	// height ranges as bands.
	ShowParts, ShowLabels, ShowRanges bool
	// Highlight lists objects (ids or names) drawn with a bright outline: the
	// ones a change touched.
	Highlight []string
	// MinExtent is the least width and height in mm of an area framed by Focus.
	MinExtent float64
	// Width and Height are pixels; both zero means a 4:3 frame with a longest
	// edge of 1024, one given keeps the 4:3 shape. At most render.MaxSize.
	Width, Height int
	// LongEdge is the longest edge when Width and Height are zero (default 1024).
	LongEdge int
}

// ViewResult is the picture and what is in it.
type ViewResult struct {
	ProjectID string
	Plate     int
	View      string
	PNG       []byte
	// Objects, Parts and Ranges count what was drawn.
	Objects, Parts, Ranges int
	Width, Height          int
	// Focus lists the names of the objects the picture is framed on.
	Focus []string
	// Legend lists what the colours and marks of the picture mean.
	Legend []string
	// SkippedLabels are the labels left out so that none covers another.
	SkippedLabels []string
}

// viewLabel is the text drawn at the top of an object: its id and a short name.
func viewLabel(id int, name string) string {
	r := []rune(name)
	if len(r) > 10 {
		r = append(r[:9], '.')
	}
	return fmt.Sprintf("%d %s", id, string(r))
}

// View renders a plate of the project from a named view.
func (s *Store) View(ref string, req ViewRequest) (*ViewResult, error) {
	name := render.Isometric
	if strings.TrimSpace(req.View) != "" {
		v, ok := render.ParseViewName(req.View)
		if !ok {
			names := make([]string, len(render.ViewNames))
			for i, n := range render.ViewNames {
				names[i] = string(n)
			}
			return nil, invalidf("view_name is one of "+strings.Join(names, ", "), "unknown view %q", req.View)
		}
		name = v
	}
	if req.Width < 0 || req.Height < 0 || req.Width > render.MaxSize || req.Height > render.MaxSize {
		return nil, invalidf(fmt.Sprintf("give width and height from 1 to %d, or leave them out", render.MaxSize), "the size %dx%d is out of range", req.Width, req.Height)
	}
	plate := req.Plate
	if plate == 0 {
		plate = 1
	}
	res := &ViewResult{Plate: plate, View: string(name)}
	err := s.read(ref, func(h *handle) error {
		res.ProjectID = h.id
		resolve := func(refs []string) (map[int]bool, error) {
			set := map[int]bool{}
			for _, r := range refs {
				o, err := h.objectByRef(r)
				if err != nil {
					return nil, err
				}
				set[o.ID] = true
			}
			return set, nil
		}
		hide, err := resolve(req.Hide)
		if err != nil {
			return err
		}
		isolate, err := resolve(req.Isolate)
		if err != nil {
			return err
		}
		focus, err := resolve(req.Focus)
		if err != nil {
			return err
		}
		highlight, err := resolve(req.Highlight)
		if err != nil {
			return err
		}
		keep := func(id int) bool {
			if hide[id] {
				return false
			}
			return len(isolate) == 0 || isolate[id]
		}
		sc, ids, err := h.sceneWith(plate, sceneExtras{parts: req.ShowParts, ranges: req.ShowRanges, labels: req.ShowLabels, keep: keep})
		if err != nil {
			return err
		}
		opts := render.ViewOptions{View: name, ShowParts: req.ShowParts, ShowLabels: req.ShowLabels, ShowRanges: req.ShowRanges, Width: req.Width, Height: req.Height, LongEdge: req.LongEdge, MinExtent: req.MinExtent}
		seen := map[string]bool{}
		for i, id := range ids {
			if highlight[id] {
				opts.Highlight = append(opts.Highlight, i)
			}
			if focus[id] {
				opts.Focus = append(opts.Focus, i)
				if n := h.p.Object(id).Name; !seen[n] {
					seen[n] = true
					res.Focus = append(res.Focus, n)
				}
			}
		}
		if len(focus) > 0 && len(opts.Focus) == 0 {
			return invalidf("focus objects that are on this plate and not hidden; get_project shows the plates", "none of the focus objects is drawn on plate %d", plate)
		}
		if len(isolate) > 0 && len(ids) == 0 {
			return invalidf("isolate objects that are on this plate; get_project shows the plates", "none of the isolated objects is on plate %d", plate)
		}
		png, st, err := render.RenderView(sc, opts)
		if err != nil {
			return errf(CodeInternal, "", "drawing the view failed: %v", err)
		}
		res.PNG, res.Objects, res.Parts, res.Ranges, res.Width, res.Height = png, st.Objects, st.Parts, st.Ranges, st.Width, st.Height
		res.SkippedLabels = st.SkippedLabels
		res.Legend = viewLegend(req, sc)
		if req.ShowRanges {
			res.Legend = append(res.Legend, h.rangeLegend(ids)...)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

// viewLegend explains the colours in the picture.
func viewLegend(req ViewRequest, sc *render.Scene) []string {
	leg := []string{"grey grid: the bed (10 mm), dark outline: the bed edge", "red arrow X and green arrow Y: the plate axes at the plate origin"}
	if sc.WipeTower != nil {
		leg = append(leg, "blue rectangle (PRIME TOWER): the wipe tower footprint kept free; it is printed only with two or more filaments on a plate printed by layer")
	}
	leg = append(leg, "objects in their filament colour; tinted red when outside the printable area")
	if req.ShowParts {
		leg = append(leg, "yellow: modifier, red: negative part, green: support enforcer, blue-grey: support blocker (solid outline, faint where the model hides them)")
	}
	if req.ShowLabels {
		leg = append(leg, "labels: object id and short name at each object's top")
	}
	return leg
}

// rangeColourNames are the names of the band colours of render, in order.
var rangeColourNames = []string{"orange", "teal", "violet"}

// rangeLegend ties each band colour to its object, its heights and its
// settings.
func (h *handle) rangeLegend(ids []int) []string {
	var out []string
	seen := map[int]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		o := h.p.Object(id)
		if o == nil {
			continue
		}
		for k, lr := range o.LayerRanges {
			var opts []string
			for _, kv := range lr.Options {
				opts = append(opts, kv.Key+" "+kv.Value)
			}
			out = append(out, fmt.Sprintf("%s band: %s from z %s to %s mm (%s)", rangeColourNames[k%len(rangeColourNames)], o.Name, trimNum(lr.MinZ), trimNum(lr.MaxZ), strings.Join(opts, ", ")))
		}
	}
	return out
}

func trimNum(f float64) string {
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", f), "0"), ".")
}
