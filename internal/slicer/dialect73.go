package slicer

import (
	"strconv"
	"strings"
)

// v73 is the dialect of Creality Print 7.3.x (dev_docs/10-cli.md sections 1 to
// 14 and dev_docs/22-implementation-plan.md V73). The 7.3 command line is a
// rewrite of the 7.2 one: it needs --cli (without it the program opens its
// window), keeps the G-code only with --need-gcode-file, has no transform or
// export flags, and always runs with a data folder of its own that the tools
// own, so it never reads or writes the application's.
type v73 struct{ v72 }

func (*v73) Name() string { return "v73" }

// DataSubdir is the version folder the CLI appends to --datadir.
func (*v73) DataSubdir() string { return "7.3" }

// forbiddenFlags73 are never sent to 7.3: file exports and pipes, the flags 7.3
// no longer has (transforms, the STL route), and flags the launcher consumes.
var forbiddenFlags73 = []string{
	"--pipe", "--need-business-report", "--diagnostics", "--sw-renderer", "--no-sw-renderer",
	"--arrange", "--orient", "--rotate", "--rotate-x", "--rotate-y", "--scale", "--repetitions", "--ensure-on-bed",
	"--assemble", "--convert-unit", "--clone-objects", "--min-save", "--info", "--allow-rotations",
	"--load-settings", "--load-filaments", "--load-filament-ids",
}

// BuildSliceArgs builds the 7.3 command line: --cli first, then --datadir,
// --slice, --need-gcode-file, --outputdir, --debug and the optional flags, the
// project 3MF last. The request must be a single project 3MF; the tools' own
// arrange and orient are done to the project before it gets here.
func (d *v73) BuildSliceArgs(r SliceRequest) ([]string, error) {
	switch {
	case len(r.Inputs) != 1:
		return nil, invalid("7.3 slices exactly one project 3MF, got %d input(s)", len(r.Inputs))
	case !strings.EqualFold(pathExt(r.Inputs[0]), ".3mf"):
		return nil, invalid("7.3 slices a project 3MF; %q is not a .3mf file", r.Inputs[0])
	case r.Arrange != nil || r.Orient != nil:
		return nil, invalid("the 7.3 command line has no arrange or orient; place the objects in the project first")
	case len(r.Settings) > 0 || len(r.Filaments) > 0 || len(r.FilamentIDs) > 0:
		return nil, invalid("preset files and filament ids belong to the STL route, which 7.3 does not use for projects")
	case r.Plate < 0:
		return nil, invalid("plate must be 0 (all) or a plate number, got %d", r.Plate)
	case strings.TrimSpace(r.OutputDir) == "":
		return nil, invalid("output directory is required")
	case strings.TrimSpace(r.DataDir) == "":
		return nil, invalid("7.3 needs a data folder of its own (--datadir); the Runner supplies it")
	}
	debug := r.DebugLevel
	if debug == 0 {
		debug = defaultDebugLevel
	}
	if debug < 0 || debug > 5 {
		return nil, invalid("debug level must be 1 to 5, got %d", debug)
	}
	for what, p := range map[string]string{"input file": r.Inputs[0], "output directory": r.OutputDir, "data directory": r.DataDir} {
		if err := checkPath(what, p); err != nil {
			return nil, err
		}
	}
	args := []string{"--cli", "--datadir", r.DataDir, "--slice", strconv.Itoa(r.Plate), "--need-gcode-file", "--outputdir", r.OutputDir, "--debug", strconv.Itoa(debug)}
	if r.LogFile != "" {
		if err := checkPath("log file", r.LogFile); err != nil {
			return nil, err
		}
		args = append(args, "--logfile", r.LogFile)
	}
	if len(r.FilamentColours) > 0 {
		for _, c := range r.FilamentColours {
			if !colourRE.MatchString(c) {
				return nil, invalid("filament colour %q is not #RRGGBB or #RRGGBBAA", c)
			}
		}
		args = append(args, "--filament-colour", strings.Join(r.FilamentColours, ";"))
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
	args = append(args, r.Inputs[0])
	if err := d.checkArgs(args); err != nil {
		return nil, err
	}
	return args, nil
}

// checkArgs rejects a 7.3 argv that would open the window, export something or
// use a flag 7.3 does not have.
func (*v73) checkArgs(args []string) error { return checkArgs73(args) }

func checkArgs73(args []string) error {
	if len(args) == 0 || args[0] != "--cli" {
		return invalid("the 7.3 command line must start with --cli; without it the slicer opens its window")
	}
	hasSlice, hasData := false, false
	for _, a := range args[1:] {
		if !strings.HasPrefix(a, "-") {
			continue
		}
		name := strings.ToLower(a)
		if i := strings.Index(name, "="); i >= 0 {
			name = name[:i]
		}
		switch {
		case name == "--slice":
			hasSlice = true
		case name == "--datadir":
			hasData = true
		case strings.HasPrefix(name, "--export-") || strings.HasPrefix(name, "--uptodate") || strings.HasPrefix(name, "--downward"):
			return invalid("flag %s is not allowed", name)
		}
		for _, f := range forbiddenFlags73 {
			if name == f {
				return invalid("flag %s is not allowed", name)
			}
		}
	}
	if !hasSlice {
		return invalid("the command line has no --slice action")
	}
	if !hasData {
		return invalid("the command line has no --datadir: the slicer would use the application's own data folder")
	}
	return nil
}

// Classify implements Dialect with the 7.3 exit code table.
func (*v73) Classify(exitCode int32, stderr string) Outcome {
	return classify(exitCode, stderr, LookupExitCode73, "7.3")
}
