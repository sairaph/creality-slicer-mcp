package projects

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/sairaph/creality-slicer-mcp/internal/catalog"
	"github.com/sairaph/creality-slicer-mcp/internal/profiles"
	"github.com/sairaph/creality-slicer-mcp/internal/slicer"
	"github.com/sairaph/creality-slicer-mcp/internal/threemf"
)

// sliceLogName is the slicer's own log of the last slice, in the project's out
// folder (--logfile).
const sliceLogName = "slice.log"

// ObjectLabel maps one label of the G-code (what exclude_object takes on the
// printer) to the object it belongs to.
type ObjectLabel struct {
	ObjectID int
	Name     string
	Label    string
}

// ActionResult says what became of a layer action of the plate in the G-code.
type ActionResult struct {
	Kind  string
	Layer int
	// Z is the height the action was asked for; AtZ where it was found.
	Z     float64
	Found bool
	AtZ   float64
	// Ignored is true for a layer tool change that Creality Print drops because
	// the objects of the plate use several filaments (see layer_tool_change_ignored):
	// the T lines the G-code has at that height are the objects' own swaps.
	Ignored bool
	// Empty names the setting that is empty when the action was written but
	// does nothing: template_custom_gcode (a template), machine_pause_gcode (a
	// pause) or change_filament_gcode (a tool change, which is then a bare T).
	// Found is false then.
	Empty string
}

// labelRE parses <name>_id_<n>_copy_<k>.
var labelRE = regexp.MustCompile(`^(?:(.*)_)?id_(\d+)_copy_(\d+)$`)

var instanceNameRE = regexp.MustCompile(`[ !@#$%^&*()=+\[\]{};:",']+`)

// SanitizeInstanceName is what the slicer makes of an object name for its
// labels (GCode.cpp sanitize_instance_name, the same in 7.2.1 and 7.3.0): every
// run of space or ! @ # $ % ^ & * ( ) = + [ ] { } ; : " , ' becomes one
// underscore, then one leading and one trailing underscore are removed.
func SanitizeInstanceName(name string) string {
	s := instanceNameRE.ReplaceAllString(name, "_")
	s = strings.TrimPrefix(s, "_")
	return strings.TrimSuffix(s, "_")
}

// objectLabels ties each exclusion label of a plate to its object. The number in
// a label counts the objects of the plate in their order.
func objectLabels(p *threemf.Project, plate int, names []string) []ObjectLabel {
	pl := p.Plate(plate)
	if pl == nil {
		return nil
	}
	var order []int
	seen := map[int]bool{}
	for _, in := range pl.Instances {
		if !seen[in.ObjectID] {
			seen[in.ObjectID] = true
			order = append(order, in.ObjectID)
		}
	}
	var out []ObjectLabel
	for _, name := range names {
		m := labelRE.FindStringSubmatch(name)
		if m == nil {
			continue
		}
		n, _ := strconv.Atoi(m[2])
		if n < 0 || n >= len(order) {
			continue
		}
		o := p.Object(order[n])
		if o == nil || SanitizeInstanceName(o.Name) != m[1] {
			continue
		}
		out = append(out, ObjectLabel{ObjectID: o.ID, Name: o.Name, Label: name})
	}
	return out
}

var zLineRE = regexp.MustCompile(`^;Z?:(-?[0-9.]+)\s*$`)
var toolLineRE = regexp.MustCompile(`^T(\d+)\s*$`)

// scanActions looks for the layer actions of a plate in its G-code: a pause
// leaves ";PAUSE_PRINT", a tool change a T command, a custom action its first
// line of text, a colour change M600.
func scanActions(gcode string, actions []ActionInfo) []ActionResult {
	if len(actions) == 0 {
		return nil
	}
	f, err := os.Open(gcode)
	if err != nil {
		return nil
	}
	defer f.Close()
	type event struct {
		kind string
		tool int
		text string
		z    float64
		// empty: a pause marker with no G-code after it
		empty bool
	}
	var events []event
	firstLine := func(a ActionInfo) string {
		for _, l := range strings.Split(a.GCode, "\n") {
			if l = strings.TrimSpace(l); l != "" {
				return l
			}
		}
		return ""
	}
	customs := map[string]bool{}
	for _, a := range actions {
		if a.Kind == ActionCustom && firstLine(a) != "" {
			customs[firstLine(a)] = true
		}
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	z := 0.0
	firstTool := true
	afterMarker := false // the line before was ;CUSTOM_GCODE
	afterPause := false  // the line before was ;PAUSE_PRINT
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		// 7.3 writes ;CUSTOM_GCODE and then the text of a custom action or the
		// processed template (GCode.cpp emit_custom_gcode_per_print_z). Only the
		// line right after the marker counts: with an empty template it is a
		// blank line, a comment (; OBJECT_ID) or an EXCLUDE_OBJECT line, and
		// nothing was inserted. Other text that is no custom action is the template.
		wasAfter, wasPause := afterMarker, afterPause
		afterMarker, afterPause = false, false
		if wasAfter {
			if line != "" && !strings.HasPrefix(line, ";") && !strings.HasPrefix(line, "EXCLUDE_OBJECT") && !customs[line] &&
				!strings.HasPrefix(line, "M600") && !toolLineRE.MatchString(line) {
				events = append(events, event{kind: ActionTemplate, z: z})
			}
		}
		if wasPause && (line == "" || strings.HasPrefix(line, ";")) {
			// 7.3 writes ";PAUSE_PRINT" and then the processed machine_pause_gcode and
			// a newline: with an empty machine_pause_gcode a blank line follows, and
			// the printer is never told to pause.
			events[len(events)-1].empty = true
		}
		if line == ";CUSTOM_GCODE" {
			afterMarker = true
			continue
		}
		switch {
		case zLineRE.MatchString(line):
			z, _ = strconv.ParseFloat(zLineRE.FindStringSubmatch(line)[1], 64)
		case line == ";PAUSE_PRINT":
			events = append(events, event{kind: ActionPause, z: z})
			afterPause = true
		case strings.HasPrefix(line, "M600"):
			events = append(events, event{kind: ActionColorChange, z: z})
		case toolLineRE.MatchString(line):
			tool, _ := strconv.Atoi(toolLineRE.FindStringSubmatch(line)[1])
			if firstTool {
				firstTool = false // the first tool is the start of the print
				continue
			}
			events = append(events, event{kind: ActionToolChange, tool: tool + 1, z: z})
		case customs[line] && wasAfter:
			// the text itself, right after the marker (the same line elsewhere is not it)
			events = append(events, event{kind: ActionCustom, text: line, z: z})
		}
	}
	used := make([]bool, len(events))
	var out []ActionResult
	for _, a := range actions {
		r := ActionResult{Kind: a.Kind, Layer: a.Layer, Z: a.Z}
		for i, e := range events {
			if used[i] || e.kind != a.Kind || e.z < a.Z-0.011 {
				continue
			}
			if a.Kind == ActionToolChange && e.tool != a.Filament {
				continue
			}
			if a.Kind == ActionCustom && e.text != firstLine(a) {
				continue
			}
			used[i] = true
			if e.empty {
				r.Empty, r.AtZ = "machine_pause_gcode", e.z
			} else {
				r.Found, r.AtZ = true, e.z
			}
			break
		}
		out = append(out, r)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Z < out[j].Z })
	return out
}

var logPrefixRE = regexp.MustCompile(`^\[[^\]]*\]\s+\[[^\]]*\]\s+\[(\w+)\]\s*`)

// logTail returns the last n meaningful lines of the slicer's log: no trace or
// debug lines, the time and thread prefix reduced to the level.
func logTail(path string, n int) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	if info, err := f.Stat(); err == nil && info.Size() > 512<<10 {
		f.Seek(info.Size()-512<<10, 0)
	}
	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 8<<20)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r\x00 ")
		if strings.TrimSpace(line) == "" {
			continue
		}
		if m := logPrefixRE.FindStringSubmatch(line); m != nil {
			switch strings.ToLower(m[1]) {
			case "trace", "debug":
				continue
			}
			line = strings.ToLower(m[1]) + ": " + line[len(m[0]):]
		}
		if len(line) > 300 {
			line = line[:300] + "..."
		}
		lines = append(lines, line)
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

// withLog adds the path of the slicer's log and its last meaningful lines to a
// slicer error.
func withLog(e *Error, logPath string) *Error {
	if e == nil || logPath == "" {
		return e
	}
	if e.Fields == nil {
		e.Fields = map[string]any{}
	}
	if _, err := os.Stat(logPath); err == nil {
		e.Fields["log_file"] = logPath
		if tail := logTail(logPath, 15); len(tail) > 0 {
			e.Fields["log_tail"] = tail
		}
	}
	return e
}

// JobInfo is one slice job of the list.
type JobInfo struct {
	ID        string
	ProjectID string
	// State is running, finished, failed or cancelled: a job that ended in a
	// failed slice is failed, not finished.
	State    string
	ExitName string
	Message  string
	Elapsed  float64
}

// ListJobs lists the slice jobs, newest first, with how each really ended.
func (s *Store) ListJobs() []JobInfo {
	if s.cfg.Jobs == nil {
		return nil
	}
	var out []JobInfo
	for _, sn := range s.cfg.Jobs.Snapshots() {
		ji := JobInfo{ID: sn.ID, State: sn.State, Elapsed: sn.Elapsed.Seconds()}
		if id, ok := sn.Tag.(string); ok {
			ji.ProjectID = id
		}
		if sn.State == slicer.StateFinished {
			s.mu.Lock()
			end := s.ends()[sn.ID]
			s.mu.Unlock()
			switch {
			case end != nil && end.err != nil:
				ji.State, ji.Message = "failed", end.err.Message
				if v, ok := end.err.Fields["exit_name"].(string); ok {
					ji.ExitName = v
				}
			case end == nil && sn.Result != nil && !sn.Result.Outcome.OK:
				ji.State, ji.ExitName, ji.Message = "failed", sn.Result.Outcome.Name, sn.Result.Outcome.Message
			case end == nil && sn.Error != "":
				ji.State, ji.Message = "failed", sn.Error
			}
		}
		out = append(out, ji)
	}
	return out
}

// SettingDiff is one setting of a slice that differs from the process preset.
type SettingDiff struct {
	Key    string
	Preset string
	Used   string
	// Origin is "project" (changed in this project), "app" (the app switches it
	// automatically) or "other" (nothing the tools know explains it).
	Origin string
	Why    string
}

func normSetting(s string) string {
	return strings.TrimSuffix(strings.Trim(strings.TrimSpace(s), "\""), "%")
}

// ExplainSettings compares the settings a slice used (the config block of the
// G-code) with the process preset of the project and says where each difference
// comes from: a change made in the project, a rule of the app (a setting it
// switches on or off when another one has a value, like the prime tower for
// printing by object), or nothing the tools know.
func (s *Store) ExplainSettings(ref string, used map[string]string) ([]SettingDiff, error) {
	var out []SettingDiff
	err := s.read(ref, func(h *handle) error {
		cfg, err := h.cfg()
		if err != nil {
			return err
		}
		proc, err := s.cfg.Profiles.Get(profiles.TypeProcess, cfg.String("print_settings_id"))
		if err != nil {
			return notFoundf("", "the process preset %q is not available", cfg.String("print_settings_id"))
		}
		changed := map[string]bool{}
		if diffs := cfg.List("different_settings_to_system"); len(diffs) > 0 {
			for _, k := range strings.Split(diffs[0], ";") {
				changed[k] = true
			}
		}
		// A setting the user set on a plate (print sequence, bed type, spiral mode)
		// is a change of this project too.
		for _, pl := range h.p.Plates {
			for _, e := range pl.Config {
				if mapped, ok := plateOverrideKeys[e.Key]; ok && e.Value != "" {
					changed[e.Key], changed[mapped] = true, true
				}
			}
		}
		env := condEnv{get: func(key string) (string, bool) { v, ok := used[key]; return v, ok }}
		cat := s.cfg.Catalog
		for key, raw := range proc.Values {
			if strings.HasSuffix(key, "_settings_id") {
				continue
			}
			got, ok := used[key]
			if !ok {
				continue
			}
			want := showAny(raw)
			if normSetting(got) == normSetting(want) {
				continue
			}
			d := SettingDiff{Key: key, Preset: want, Used: got}
			filamentName := ""
			if l := cfg.List("filament_settings_id"); len(l) > 0 {
				filamentName = l[0]
			}
			switch {
			case appRuleWhy(key, used, cat, filamentName) != "":
				d.Origin, d.Why = "app", appRuleWhy(key, used, cat, filamentName)
			case changed[key]:
				d.Origin, d.Why = "project", "changed in this project"
			case filamentOwnedWhy(key, cat, filamentName) != "":
				d.Origin, d.Why = "app", filamentOwnedWhy(key, cat, filamentName)
			default:
				d.Origin, d.Why = "other", "the slicer used this value and neither the preset nor a change in the project explains it (the preset may differ from the one the app merged, or the slicer normalises the value)"
				if o, ok := cat.Get(key); ok {
					if why := forcedWhy(o, env, got); why != "" {
						d.Origin, d.Why = "app", why
					}
				}
			}
			out = append(out, d)
		}
		out = append(out, h.explainOtherLevels(used, cfg, changed, out)...)
		sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
		return nil
	})
	return out, err
}

// explainOtherLevels lists the changes the project holds on the filament and
// printer presets (different_settings_to_system, entries after the process one):
// the process preset does not know those keys, so the process comparison skips
// them. Each is compared with the value of the preset it belongs to, per filament.
func (h *handle) explainOtherLevels(used map[string]string, cfg *threemf.Config, processChanged map[string]bool, done []SettingDiff) []SettingDiff {
	diffs := cfg.List("different_settings_to_system")
	have := map[string]bool{}
	for _, d := range done {
		have[d.Key] = true
	}
	fils := cfg.List("filament_settings_id")
	var out []SettingDiff
	for slot := 1; slot < len(diffs); slot++ {
		for _, k := range strings.Split(diffs[slot], ";") {
			if k == "" || have[k] || processChanged[k] {
				continue
			}
			got, ok := used[k]
			if !ok {
				continue
			}
			var preset string
			if slot <= len(fils) {
				// the value of every filament preset, as the config block writes a vector
				var vals []string
				for _, name := range fils {
					v := ""
					if fp, err := h.s.cfg.Profiles.Get(profiles.TypeFilament, name); err == nil {
						if raw, ok := fp.Values[k]; ok {
							v = strings.Split(showAny(raw), ",")[0]
						}
					}
					vals = append(vals, v)
				}
				preset = strings.Join(vals, ",")
			} else if pp, err := h.s.cfg.Profiles.Get(profiles.TypePrinter, cfg.String("printer_settings_id")); err == nil {
				if raw, ok := pp.Values[k]; ok {
					preset = showAny(raw)
				}
			}
			if normSetting(got) == normSetting(preset) {
				continue
			}
			have[k] = true
			why := "changed in this project"
			if slot <= len(fils) {
				why = "changed in this project (a filament setting)"
			}
			out = append(out, SettingDiff{Key: k, Preset: preset, Used: got, Origin: "project", Why: why})
		}
	}
	return out
}

// forcedWhy names the rule of the app that sets an option to the used value.
func forcedWhy(o *catalog.Option, env condEnv, used string) string {
	for _, f := range o.ForcedBy {
		if !env.holds(f.When) {
			continue
		}
		if nv, ok := forcedValue(o, f.Set); ok && normSetting(valString(nv)) == normSetting(used) || normSetting(used) == normSetting(f.Set) {
			var conds []string
			for _, c := range f.When {
				conds = append(conds, describeCond(c))
			}
			return "the app sets this automatically when " + strings.Join(conds, " and ")
		}
	}
	return ""
}

// showAny renders a preset value (string or list) as text.
func showAny(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []string:
		return strings.Join(x, ",")
	}
	return fmt.Sprint(v)
}

// appRuleWhy names the automatic switches of the slicer itself that make the
// settings of a slice differ from the process preset, from the source of
// Creality Print (checked in v7.2.1 and v7.3.0):
//
//   - enable_prime_tower goes off when the plate uses one filament or is
//     printed by object with more than one object, unless the timelapse is
//     smooth; independent_support_layer_height then goes off when the tower
//     stays on. DynamicPrintConfig::normalize_fdm_2 (v7.2.1 PrintConfig.cpp
//     7837-7860, v7.3.0 8508-8532; the older single step normalize_fdm, 7717 and
//     8388, turns the tower off for any by object plate).
//   - spiral (vase) mode sets wall_loops 1, alternate_extra_wall off,
//     top_shell_layers 0, sparse_infill_density 0 and retract_when_changing_layer
//     off: DynamicPrintConfig::normalize_fdm_1 (v7.2.1 7790, v7.3.0 8461).
//   - material_flow_temp_graph and the nozzle temperatures are in both the print
//     and the filament option lists (Preset.cpp s_Preset_print_options and
//     s_Preset_filament_options: v7.3.0 962 and 1066, v7.2.1 851 and 950), so the
//     process preset can carry a copy; the slice uses the filament of the slot.
func appRuleWhy(key string, used map[string]string, cat *catalog.Catalog, filament string) string {
	switch key {
	case "enable_prime_tower":
		if used[key] == "0" || used[key] == "false" {
			if used["print_sequence"] == "by object" {
				return "the slicer turns the prime tower off when a plate with several objects is printed by object (one object after another has no tower); it also does so for one filament"
			}
			return "the slicer turns the prime tower off when the plate uses only one filament"
		}
	case "independent_support_layer_height":
		if used["enable_prime_tower"] == "1" && (used[key] == "0" || used[key] == "false") {
			return "the slicer turns this off when the prime tower is on (support and object layers must line up)"
		}
	case "wall_loops", "alternate_extra_wall", "top_shell_layers", "sparse_infill_density", "retract_when_changing_layer":
		if used["spiral_mode"] == "1" {
			return "vase (spiral) mode: the slicer sets one wall, no extra wall, no top layers, no infill and no retraction at layer change"
		}
	}
	return ""
}

// filamentOwnedWhy explains a difference in a setting that belongs to the filament preset.
func filamentOwnedWhy(key string, cat *catalog.Catalog, filament string) string {
	if o, ok := cat.Get(key); ok && hasType(o, "filament") {
		src := "the filament preset"
		if filament != "" {
			src = "the filament preset `" + filament + "`"
		}
		return "a filament setting: the slice takes it from " + src + ", not from the process preset"
	}
	return ""
}
