package slicer

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Outcome classifies how a slice run ended.
type Outcome struct {
	// Code is machine text: "ok", "slicer_failed", "slicer_crashed",
	// "timed_out" or "cancelled".
	Code string
	// Name is the CLI exit code name (INVALID_PARAMS, ...), the NTSTATUS name
	// of a crash, or INVALID_OPTION for an option the CLI did not know.
	Name    string
	Message string
	Hint    string
	OK      bool
}

// Outcome codes.
const (
	OutcomeOK       = "ok"
	OutcomeFailed   = "slicer_failed"
	OutcomeCrashed  = "slicer_crashed"
	OutcomeTimedOut = "timed_out"
	OutcomeCanceled = "cancelled"
)

// SliceRequest is everything a slice run needs; a Dialect turns it into argv.
type SliceRequest struct {
	Inputs          []string // input files, a project 3MF first if any
	Plate           int      // 0 = all plates, n = plate n (1-based)
	OutputDir       string   // absolute, must exist; plate_N.gcode files land here. The Runner owns it for the run and deletes its plate_*.gcode first
	LogFile         string   // absolute, in an existing folder: the slicer own --logfile ("" = none)
	DebugLevel      int      // --debug level, 0 means the default 3
	Settings        []string // flattened machine/process JSON paths (--load-settings)
	Filaments       []string // flattened filament JSON paths (--load-filaments)
	FilamentIDs     []int    // filament slot per input, STL/OBJ route (--load-filament-ids)
	FilamentColours []string // "#RRGGBB" per filament (--filament-colour)
	Arrange         *int
	Orient          *int
	Overrides       map[string]string // config key -> CLI value string
	SkipObjects     []int             // identify ids (--skip-objects)
	AllowNewer      bool              // --allow-newer-file=1
	DataDir         string            // --datadir (dialects that use one; the Runner fills it in)
}

// OverrideLookup asks the option catalog how a config key is spelled on the
// command line: the flag ("wall-loops", with or without leading dashes), and
// whether it is a boolean. ok is false when the key has no command line form.
type OverrideLookup func(key string) (cliFlag string, isBool bool, ok bool)

// Dialect knows one version family of the Creality Print CLI.
type Dialect interface {
	// Name is the dialect name ("v72").
	Name() string
	// BuildSliceArgs returns the argv (without the program) for a slice run,
	// or an error naming what is wrong with the request. The result is
	// guaranteed to start the CLI (an action flag) and to contain no
	// forbidden flag.
	BuildSliceArgs(SliceRequest) ([]string, error)
	// Classify turns an exit code and the cleaned stderr into an Outcome.
	Classify(exitCode int32, stderr string) Outcome
}

// NewDialect returns the dialect called name ("v72" or "v73"). lookup validates
// override keys; nil means "no overrides allowed".
func NewDialect(name string, lookup OverrideLookup) (Dialect, error) {
	switch name {
	case "v73":
		return &v73{v72{lookup: lookup}}, nil
	case "v72":
		return &v72{lookup: lookup}, nil
	}
	return nil, fmt.Errorf("no command line dialect %q", name)
}

// ErrInvalidRequest wraps every request error of BuildSliceArgs.
var ErrInvalidRequest = errors.New("invalid slice request")

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidRequest, fmt.Sprintf(format, args...))
}

// v72 is the dialect of Creality Print 7.2.x (dev_docs/10-cli.md sections 15
// and 20).
type v72 struct {
	lookup OverrideLookup
}

func (*v72) Name() string { return "v72" }

// defaultDebugLevel is --debug when the request leaves it 0 (info).
const defaultDebugLevel = 3

// forbiddenFlags are never sent: they export files the MCP produces itself,
// reach outside the sandboxed output folder, or are consumed by the launcher
// and rejected by the CLI (dev_docs/10-cli.md 15.2, 15.3, 20).
var forbiddenFlags = []string{
	"--export-3mf", "--export-stl", "--export-stls", "--pipe", "--datadir",
	"--sw-renderer", "--no-sw-renderer", "--uptodate",
}

var colourRE = regexp.MustCompile(`^#[0-9A-Fa-f]{6}(?:[0-9A-Fa-f]{2})?$`)

func (d *v72) BuildSliceArgs(r SliceRequest) ([]string, error) {
	if len(r.Inputs) == 0 {
		return nil, invalid("no input file")
	}
	if r.Plate < 0 {
		return nil, invalid("plate must be 0 (all) or a plate number, got %d", r.Plate)
	}
	if strings.TrimSpace(r.OutputDir) == "" {
		return nil, invalid("output directory is required")
	}
	debug := r.DebugLevel
	if debug == 0 {
		debug = defaultDebugLevel
	}
	if debug < 0 || debug > 5 {
		return nil, invalid("debug level must be 1 to 5, got %d", debug)
	}
	project := false
	for _, in := range r.Inputs {
		if err := checkPath("input file", in); err != nil {
			return nil, err
		}
		if strings.EqualFold(pathExt(in), ".3mf") {
			if len(r.Inputs) > 1 {
				return nil, invalid("the project 3MF %q must be the only input: 7.3 rejects a project combined with other files and the tools slice a project on its own", in)
			}
			project = true
		}
	}
	if err := checkPath("output directory", r.OutputDir); err != nil {
		return nil, err
	}
	if r.LogFile != "" {
		if err := checkPath("log file", r.LogFile); err != nil {
			return nil, err
		}
	}
	if len(r.FilamentIDs) > 0 {
		if project {
			return nil, invalid("filament ids apply to STL or OBJ inputs only, not to a project 3MF")
		}
		if len(r.FilamentIDs) != len(r.Inputs) {
			return nil, invalid("%d filament ids for %d inputs: give one per input", len(r.FilamentIDs), len(r.Inputs))
		}
		if len(r.Filaments) == 0 {
			return nil, invalid("filament ids need filament preset files")
		}
	}

	args := []string{"--slice", strconv.Itoa(r.Plate), "--outputdir", r.OutputDir, "--debug", strconv.Itoa(debug)}
	if r.LogFile != "" {
		args = append(args, "--logfile", r.LogFile)
	}
	if len(r.Settings) > 0 {
		joined, err := joinPaths("settings file", r.Settings)
		if err != nil {
			return nil, err
		}
		args = append(args, "--load-settings", joined)
	}
	if len(r.Filaments) > 0 {
		joined, err := joinPaths("filament file", r.Filaments)
		if err != nil {
			return nil, err
		}
		args = append(args, "--load-filaments", joined)
	}
	if len(r.FilamentIDs) > 0 {
		ids := make([]string, len(r.FilamentIDs))
		for i, id := range r.FilamentIDs {
			if id < 1 {
				return nil, invalid("filament ids start at 1, got %d", id)
			}
			ids[i] = strconv.Itoa(id)
		}
		args = append(args, "--load-filament-ids", strings.Join(ids, ","))
	}
	if len(r.FilamentColours) > 0 {
		for _, c := range r.FilamentColours {
			if !colourRE.MatchString(c) {
				return nil, invalid("filament colour %q is not #RRGGBB or #RRGGBBAA", c)
			}
		}
		args = append(args, "--filament-colour", strings.Join(r.FilamentColours, ";"))
	}
	if r.Arrange != nil {
		args = append(args, "--arrange", strconv.Itoa(*r.Arrange))
	}
	if r.Orient != nil {
		args = append(args, "--orient", strconv.Itoa(*r.Orient))
	}
	over, err := d.overrideArgs(r)
	if err != nil {
		return nil, err
	}
	args = append(args, over...)
	if len(r.SkipObjects) > 0 {
		ids := make([]string, len(r.SkipObjects))
		for i, id := range r.SkipObjects {
			ids[i] = strconv.Itoa(id)
		}
		args = append(args, "--skip-objects", strings.Join(ids, ","))
	}
	if r.AllowNewer {
		args = append(args, "--allow-newer-file=1")
	}
	args = append(args, r.Inputs...)
	if err := CheckArgs(args); err != nil {
		return nil, err
	}
	return args, nil
}

// overrideArgs renders config overrides in a deterministic (sorted) order:
// "--key-with-dashes value", booleans as "--flag=0|1" (the next token is not
// consumed as their value, dev_docs/10-cli.md 20 P3).
func (d *v72) overrideArgs(r SliceRequest) ([]string, error) {
	overrides := r.Overrides
	if len(overrides) == 0 {
		return nil, nil
	}
	if d.lookup == nil {
		return nil, invalid("setting overrides are not available: no option catalog")
	}
	keys := make([]string, 0, len(overrides))
	for k := range overrides {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var args []string
	for _, key := range keys {
		value := overrides[key]
		if err := overrideCollision(key, r); err != nil {
			return nil, err
		}
		flag, isBool, ok := d.lookup(key)
		if !ok {
			return nil, invalid("setting %q cannot be changed on the command line", key)
		}
		flag = cliSpelling(flag, key)
		if err := checkValue("value of "+key, value); err != nil {
			return nil, err
		}
		if isBool {
			// Booleans (scalar or per slot vectors) are one token, --flag=v or
			// --flag=v1,v2,...: the CLI reads no separate value for coBool and
			// coBools, and splits vector items on commas (Config.hpp deserialize).
			b, err := boolList(value)
			if err != nil {
				return nil, invalid("setting %q is a boolean: %v", key, err)
			}
			args = append(args, "--"+flag+"="+b)
			continue
		}
		// An empty value would make the CLI swallow the next token.
		if strings.TrimSpace(value) == "" {
			return nil, invalid("setting %q needs a value", key)
		}
		args = append(args, "--"+flag, value)
	}
	return args, nil
}

// cliSpelling normalises a catalog flag to its dashed name without leading
// dashes, taking the first spelling of "a|b" and deriving it from the key when
// the catalog gives none.
func cliSpelling(flag, key string) string {
	if i := strings.Index(flag, "|"); i >= 0 {
		flag = flag[:i]
	}
	flag = strings.TrimLeft(strings.TrimSpace(flag), "-")
	if flag == "" {
		flag = strings.ReplaceAll(key, "_", "-")
	}
	return flag
}

func boolValue(v string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return "1", nil
	case "0", "false", "no", "off":
		return "0", nil
	}
	return "", fmt.Errorf("%q is not 0/1 or true/false", v)
}

// CheckArgs rejects an argv that would not run as a plain slice: no action
// flag, or a forbidden flag.
func CheckArgs(args []string) error {
	if len(args) > 0 && args[0] == "--cli" {
		return checkArgs73(args) // the 7.3 command line
	}
	hasSlice := false
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			continue
		}
		name := a
		if i := strings.Index(name, "="); i >= 0 {
			name = name[:i]
		}
		name = strings.ToLower(name)
		if name == "--slice" {
			hasSlice = true
		}
		for _, f := range forbiddenFlags {
			if name == f || (f == "--uptodate" && strings.HasPrefix(name, "--uptodate")) {
				return invalid("flag %s is not allowed", name)
			}
		}
	}
	if !hasSlice {
		return invalid("the command line has no --slice action; the slicer would open its window")
	}
	return nil
}

func checkValue(what, v string) error {
	if strings.ContainsAny(v, "\r\n\x00") {
		return invalid("%s contains a line break or NUL", what)
	}
	return nil
}

// checkPath rejects an empty, non absolute or dash leading path: a relative
// path would resolve against the server's arbitrary working directory, and a
// leading dash would be read as an option.
func checkPath(what, p string) error {
	if strings.TrimSpace(p) == "" {
		return invalid("%s is empty", what)
	}
	if err := checkValue(what, p); err != nil {
		return err
	}
	if !filepath.IsAbs(p) {
		return invalid("%s %q is not an absolute path", what, p)
	}
	return nil
}

// joinPaths joins file paths with ";" for the CLI's list options; a path that
// contains ";" would split into two.
func joinPaths(what string, paths []string) (string, error) {
	for _, p := range paths {
		if err := checkPath(what, p); err != nil {
			return "", err
		}
		if strings.Contains(p, ";") {
			return "", invalid("%s %q contains a semicolon, which the slicer reads as a list separator", what, p)
		}
	}
	return strings.Join(paths, ";"), nil
}

// boolList normalises a boolean or a list of booleans (separated by "," or
// ";") to "0" / "1" items joined by commas.
func boolList(v string) (string, error) {
	parts := strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ';' })
	if len(parts) == 0 {
		return "", fmt.Errorf("no value")
	}
	out := make([]string, len(parts))
	for i, p := range parts {
		b, err := boolValue(p)
		if err != nil {
			return "", err
		}
		out[i] = b
	}
	return strings.Join(out, ","), nil
}

// identityKeys name the selected presets; they are never overridden on the
// command line (the project or the preset files carry them).
var identityKeys = map[string]bool{
	"printer_settings_id": true, "print_settings_id": true, "filament_settings_id": true,
	"preset_name": true, "preset_names": true, "printer_select_mac": true, "filament_ids": true,
}

// overrideCollision rejects an override that the request already sets through
// a dedicated field: the CLI chains repeated vector options, so the value
// would be doubled.
func overrideCollision(key string, r SliceRequest) error {
	switch {
	case identityKeys[key]:
		return invalid("setting %q names a selected preset and cannot be overridden", key)
	case key == "filament_colour" && len(r.FilamentColours) > 0:
		return invalid("setting filament_colour is already given through the filament colours of the request")
	case key == "arrange" && r.Arrange != nil, key == "orient" && r.Orient != nil:
		return invalid("setting %q is already given through the request", key)
	case key == "allow_newer_file" && r.AllowNewer:
		return invalid("setting allow_newer_file is already given through the request")
	}
	return nil
}

func pathExt(p string) string {
	i := strings.LastIndexAny(p, `./\`)
	if i < 0 || p[i] != '.' {
		return ""
	}
	return p[i:]
}

// Classify implements Dialect.
func (*v72) Classify(exitCode int32, stderr string) Outcome {
	return classify(exitCode, stderr, LookupExitCode, "7.2")
}

// classify turns an exit code into an Outcome with the exit code table of one
// version family.
func classify(exitCode int32, stderr string, lookup func(int32) (ExitCode, bool), version string) Outcome {
	first := firstLine(stderr)
	if exitCode == 0 {
		return Outcome{Code: OutcomeOK, OK: true, Message: "Creality Print finished"}
	}
	if name, ok := CrashName(exitCode); ok {
		return Outcome{
			Code: OutcomeCrashed, Name: name,
			Message: fmt.Sprintf("Creality Print crashed with %s (exit code %d, 0x%08X)", name, exitCode, uint32(exitCode)),
			Hint:    "The slicer crashed natively; a crash dump is kept next to the output if one was written. Try again, or simplify the model or the settings that changed.",
		}
	}
	e, known := lookup(exitCode)
	out := Outcome{Code: OutcomeFailed}
	switch {
	case strings.HasPrefix(first, "Invalid option"):
		out.Name = "INVALID_OPTION"
		out.Message = fmt.Sprintf("the slicer rejected an option (exit code %d): %s", exitCode, first)
		out.Hint = "The " + version + " command line does not know this option; a setting cannot be passed that way. Report it with the slicer message."
		return out
	case strings.HasPrefix(first, "No such file"):
		out.Name = "FILE_NOTFOUND"
		out.Message = fmt.Sprintf("a file the slicer needs does not exist (exit code %d): %s", exitCode, first)
		out.Hint = "Check that every input and preset path exists and is absolute."
		return out
	case known:
		out.Name = e.Name
		out.Message = fmt.Sprintf("%s (exit code %d): %s", e.Name, exitCode, e.Meaning)
		out.Hint = e.Hint
	default:
		out.Message = fmt.Sprintf("Creality Print failed with unknown exit code %d", exitCode)
		out.Hint = "Read the slicer output for the reason."
	}
	if first != "" {
		out.Message += "; the slicer said: " + first
	}
	return out
}

func firstLine(text string) string {
	for _, l := range strings.Split(text, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	return ""
}
