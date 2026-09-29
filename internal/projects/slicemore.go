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

var zLineRE = regexp.MustCompile(`^;:(-?[0-9.]+)\s*$`)
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
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case zLineRE.MatchString(line):
			z, _ = strconv.ParseFloat(zLineRE.FindStringSubmatch(line)[1], 64)
		case line == ";PAUSE_PRINT":
			events = append(events, event{kind: ActionPause, z: z})
		case strings.HasPrefix(line, "M600"):
			events = append(events, event{kind: ActionColorChange, z: z})
		case toolLineRE.MatchString(line):
			tool, _ := strconv.Atoi(toolLineRE.FindStringSubmatch(line)[1])
			if firstTool {
				firstTool = false // the first tool is the start of the print
				continue
			}
			events = append(events, event{kind: ActionToolChange, tool: tool + 1, z: z})
		case customs[line]:
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
			r.Found, r.AtZ = true, e.z
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
			switch {
			case changed[key]:
				d.Origin, d.Why = "project", "changed in this project"
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
		sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
		return nil
	})
	return out, err
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
