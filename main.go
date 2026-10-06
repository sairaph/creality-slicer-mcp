package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/sairaph/mcp-wizard/cli"
	"github.com/sairaph/mcp-wizard/command"
	"github.com/sairaph/mcp-wizard/doctor"
	"github.com/sairaph/mcp-wizard/flow"
	"github.com/sairaph/mcp-wizard/harness"
	"github.com/sairaph/mcp-wizard/installer"
	"github.com/sairaph/mcp-wizard/tui"
	"github.com/sairaph/mcp-wizard/update"

	"github.com/sairaph/creality-slicer-mcp/internal/appui"
	"github.com/sairaph/creality-slicer-mcp/internal/clicmd"
	"github.com/sairaph/creality-slicer-mcp/internal/doctorchecks"
	"github.com/sairaph/creality-slicer-mcp/internal/domain"
	"github.com/sairaph/creality-slicer-mcp/internal/mcpserver"
)

// version is set by goreleaser via -ldflags "-X main.version=...".
var version = "dev"

// oneShotCommands are the product's own CLI commands. They call the same
// functions the MCP tools call, through internal/clicmd, so the two never
// disagree. Each is registered in init with a Run that calls a clicmd.Run<Name>
// with clicmd.NewDefaultDeps().
var oneShotCommands = command.New()

func init() {
	oneShotCommands.Register(command.Handler{
		Name:        "status",
		Description: "Show whether Creality Print is installed and usable (the get_slicer_status tool)",
		Usage:       "status [--refresh]",
		Run: func(ctx context.Context, args []string) int {
			return clicmd.RunStatus(ctx, clicmd.NewDefaultDeps(), args)
		},
	})
	oneShotCommands.Register(command.Handler{
		Name:        "presets",
		Description: "List printer, process or filament presets (the list_presets tool, all pages)",
		Usage:       "presets <printer|process|filament> [--printer NAME|all|any] [--filament-type TYPE] [--source system|user|all]",
		Run: func(ctx context.Context, args []string) int {
			return clicmd.RunPresets(ctx, clicmd.NewDefaultDeps(), args)
		},
	})
	oneShotCommands.Register(command.Handler{
		Name:        "slice",
		Description: "Slice a 3MF project file with Creality Print and print the summary (the slice_project tool)",
		Usage:       "slice <project.3mf> [--plate N] [--out DIR [--overwrite]]",
		Run: func(ctx context.Context, args []string) int {
			return clicmd.RunSlice(ctx, clicmd.NewDefaultDeps(), args)
		},
	})
}

var usageSpecs = []cli.Spec{
	{Name: "mcp", Description: "Run the MCP server (default when not in a terminal)"},
	{Name: "install", Description: "Register the server and the guide skill with AI clients"},
	{Name: "uninstall", Description: "Remove AI client integration (--all: also the guide skill, cache and program)"},
	{Name: "add", Description: "Register the server in this project's AI client configs"},
	{Name: "doctor", Description: "Diagnose the installation"},
	{Name: "update", Description: "Update to the latest release"},
	{Name: "version", Description: "Print the version"},
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Bare invocation in a terminal opens the app; everywhere else (an AI
	// client spawning us, a pipe) it is the MCP server, which cli.Parse
	// reports as "mcp".
	if opts := updateOptions(); opts.InstallDir != "" {
		update.RemoveStaleBinaries(opts.InstallDir, opts.BinaryName)
	}
	if len(os.Args) == 1 && tui.IsInteractive() {
		os.Exit(runApp(ctx))
	}

	cmd, err := cli.Parse(os.Args[1:])
	if err != nil {
		if errors.Is(err, cli.ErrUsage) {
			printUsage(os.Stdout)
			os.Exit(0)
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(2)
	}

	switch cmd.Name {
	case "mcp":
		os.Exit(runMCPServer(ctx, cmd))
	case "install", "configure":
		if cmd.Scope == string(harness.ScopeProject) {
			os.Exit(runAdd(ctx, cmd))
		}
		os.Exit(runInstall(ctx, cmd))
	case "uninstall":
		os.Exit(runUninstall(ctx, cmd))
	case "add":
		os.Exit(runAdd(ctx, cmd))
	case "doctor":
		os.Exit(runDoctor(ctx))
	case "update":
		os.Exit(runUpdate(ctx, cmd))
	case "refresh-guide":
		// Hidden: not in usageSpecs or oneShotCommands, so it never appears
		// in --help. update runs it with the binary it just installed, whose
		// embedded guide is the new one.
		os.Exit(runRefreshGuide(cmd))
	case "help":
		printUsage(os.Stdout)
	case "version":
		fmt.Println(version)
	default:
		if ok, code := oneShotCommands.Dispatch(ctx, cmd.Name, cmd.Args); ok {
			os.Exit(code)
		}
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", cmd.Name)
		printUsage(os.Stderr)
		os.Exit(2)
	}
}

func printUsage(w *os.File) {
	cli.Usage(w, usageSpecs)
	oneShotCommands.PrintUsage(w)
}

// --- Shared helpers ---

// AppState is shared across install wizard steps.
type AppState struct {
	flow.BaseState
	Harness installer.HarnessState
	Results installer.ResultsState
	// UntickedClients names the clients left unticked because their entry
	// was edited or runs another program; harnessDetecting is set while the
	// client list is being detected (see harnessSelection).
	UntickedClients  []string
	harnessDetecting bool
	// cancelled is set when the client list was left with q or ctrl+c.
	cancelled bool

	// Skills is the guide skill, written after the registration; ShowPaths and
	// Scroll belong to the screens of the wizard, FromApp is set when the app
	// started the wizard (see envFromApp).
	Skills    skillState
	ShowPaths bool
	Scroll    appui.Scroller
	FromApp   bool
	// runSkills writes the guide skill (planSkills when nil); tests replace it.
	runSkills func(scope harness.Scope, ids []harness.ID, dryRun bool) skillPlan
}

func harnessState(s *AppState) *installer.HarnessState { return &s.Harness }
func resultsState(s *AppState) *installer.ResultsState { return &s.Results }

func serverName(cmd cli.Command) string {
	if cmd.ServerName != "" {
		return cmd.ServerName
	}
	return domain.ServerName
}

func newDetector(name string) (*harness.Detector, error) {
	exe, err := harness.ResolveExecutable()
	if err != nil {
		return nil, err
	}
	return harness.New(harness.ServerSpec{
		Name:    name,
		Command: exe,
		Args:    []string{"mcp"},
		Env:     domain.DefaultEnv(),
	})
}

// selectIDs picks the harnesses to register. --clients wins, then --all, and
// by default every selectable client that is not configured yet. Replacing
// an entry that was edited, or that runs another program (see clients.go),
// would drop what is there, so those are returned in kept instead; --all
// replaces edited entries too, and only --clients replaces another
// program's. The returned reason explains an empty selection.
func selectIDs(harnesses []harness.Harness, entries map[harness.ID]clientEntry, cmd cli.Command) (ids []harness.ID, kept []harness.Harness, reason string) {
	if len(cmd.Clients) > 0 {
		var unknown []string
		for _, want := range cmd.Clients {
			found := false
			for _, h := range harnesses {
				if strings.EqualFold(string(h.ID), want) && h.Selectable() {
					ids = append(ids, h.ID)
					found = true
				}
			}
			if !found {
				unknown = append(unknown, want)
			}
		}
		if len(unknown) > 0 {
			return nil, nil, "no detected client matches --clients " + strings.Join(unknown, ",")
		}
		return ids, nil, ""
	}
	// Default: installed clients that are not configured yet. --all: every
	// installed or configured client (in project scope that means every
	// client found on this machine gets a project entry).
	candidates := 0
	for _, h := range harnesses {
		if !h.Selectable() || !h.Relevant() {
			continue
		}
		candidates++
		e := entries[h.ID]
		switch {
		case e.kind == entryForeign, !cmd.All && e.kind == entryEdited:
			kept = append(kept, h)
		case cmd.All, !h.Configured:
			ids = append(ids, h.ID)
		}
	}
	if len(ids) == 0 && len(kept) == 0 && candidates > 0 {
		return nil, nil, "every detected client is already configured (use --all to re-register)"
	}
	return ids, kept, ""
}

func exitCodeFor(results []harness.Result) int {
	for _, r := range results {
		if r.State == harness.ApplyFailed {
			return 1
		}
	}
	return 0
}

func byID(harnesses []harness.Harness) map[harness.ID]harness.Harness {
	m := make(map[harness.ID]harness.Harness, len(harnesses))
	for _, h := range harnesses {
		m[h.ID] = h
	}
	return m
}

// warnUnusedCredentials tells the user that --email/--token, which cli.Parse
// still accepts for install/add/uninstall, do nothing for this server: it has
// no login concept.
func warnUnusedCredentials(cmd cli.Command) {
	if len(cmd.Credentials) > 0 {
		fmt.Println("  --email and --token are not used by this server; ignoring them.")
	}
}

// --- Install / add ---

func runInstall(ctx context.Context, cmd cli.Command) int {
	detector, err := newDetector(serverName(cmd))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	if tui.IsInteractive() && !runsUnattended(cmd) {
		warnUnusedCredentials(cmd)
		return runWizard(ctx, detector, harness.Scope{}, cmd)
	}
	return runUnattended(ctx, detector, harness.Scope{}, cmd, harness.Present)
}

// projectDir resolves --dir (default: the working directory) to an absolute path.
func projectDir(cmd cli.Command) (string, error) {
	dir := cmd.Dir
	if dir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		dir = cwd
	}
	return filepath.Abs(dir)
}

func runAdd(ctx context.Context, cmd cli.Command) int {
	dir, err := projectDir(cmd)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	detector, err := newDetector(serverName(cmd))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	scope := harness.ProjectScopeDir(dir)

	if tui.IsInteractive() && !runsUnattended(cmd) {
		warnUnusedCredentials(cmd)
		return runWizard(ctx, detector, scope, cmd)
	}
	return runUnattended(ctx, detector, scope, cmd, harness.Present)
}

// runWizard drives the interactive install: pick clients, register, write the
// guide skill. Nothing is written before the registration step. The wizard
// runs in the alternate screen; what stays in the terminal afterwards is a
// summary line and the next step.
func runWizard(ctx context.Context, detector *harness.Detector, scope harness.Scope, cmd cli.Command) int {
	state := &AppState{FromApp: os.Getenv(envFromApp) == "1"}
	steps := []flow.Step[AppState]{
		harnessSelection{
			Step: installer.HarnessStep(ctx, detector, harnessState, installer.HarnessStepOptions{AllDetected: true, Scope: scope}),
			name: serverName(cmd),
			findUnticked: func(hs []harness.Harness) map[harness.ID]bool {
				return untickedClients(clientEntries(ctx, detector, scope, hs))
			},
		},
		applyGuard{installer.ApplyStep(ctx, detector, harnessState, resultsState, installer.ApplyStepOptions{Scope: scope, DryRun: cmd.DryRun}), cmd.DryRun},
	}
	applyIndex := stepIndex(steps, "apply")
	f := flow.New(steps, state)
	code := runFlow(ctx, f, os.Stdin, os.Stdout)
	return finishWizard(ctx, f, state, applyIndex, code, cmd.DryRun, os.Stdout, os.Stderr)
}

// finishWizard maps the end of the flow to the exit code and the text left in
// the terminal. When the app started the wizard it shows the notice itself
// from the exit code, so nothing is printed then: text printed here would
// land on the main screen and stay there after the app exits.
func finishWizard(ctx context.Context, f *flow.Flow[AppState], state *AppState, applyIndex, code int, dryRun bool, stdout, stderr io.Writer) int {
	quiet := state.FromApp
	say := func(w io.Writer, text string) {
		if !quiet {
			fmt.Fprintln(w, text)
		}
	}
	switch classifyWizard(&state.BaseState, code, f.Current() >= applyIndex, dryRun, ctx.Err() != nil) {
	case outcomeCancelled:
		say(stdout, "  Setup cancelled; nothing was changed.")
		return exitCancelled
	case outcomeInterrupted:
		say(stderr, interruptedMessage)
		if quiet {
			return exitInterrupted
		}
		return 1
	case outcomeFailed:
		if state.Failure != nil {
			say(stderr, state.Failure.Error())
		} else {
			say(stderr, "  The setup wizard could not run in this terminal; nothing was changed.\n"+
				"  Run `"+domain.BinaryName+" install --yes` to install without it.")
		}
		return 1
	}
	if state.Failure != nil {
		// Registration failed for some clients; the rest still got the guide.
		say(stderr, state.Failure.Error())
	}
	if !quiet {
		printScrollback(stdout, state, dryRun)
	}
	if state.Skills.Started && !state.Skills.Done {
		say(stderr, fmt.Sprintf("  The guide skill may not have been written; run `%s install --yes` to write it.", domain.BinaryName))
		return 1
	}
	return max(code, state.Skills.Code)
}

func runUnattended(ctx context.Context, detector *harness.Detector, scope harness.Scope, cmd cli.Command, desired harness.DesiredState) int {
	warnUnusedCredentials(cmd)
	harnesses := detector.DetectIn(ctx, scope)

	entries := clientEntries(ctx, detector, scope, harnesses)
	name := serverName(cmd)

	if desired == harness.Present {
		ids, kept, reason := selectIDs(harnesses, entries, cmd)
		if reason != "" {
			fmt.Fprintf(os.Stderr, "  %s\n", reason)
			if len(cmd.Clients) > 0 {
				return 2
			}
			// Every client is registered already; the guide skill is still
			// written for them.
			return installSkills(os.Stdout, scope, skillClients(harnesses, entries, nil), cmd.DryRun, true)
		}
		printKept(os.Stdout, kept, entries, scope, name, cmd.DryRun)
		code := 0
		if len(ids) > 0 || len(kept) == 0 {
			code = registerIDs(ctx, detector, scope, harnesses, ids, cmd.DryRun, desired)
		}
		return max(code, installSkills(os.Stdout, scope, skillClients(harnesses, entries, ids), cmd.DryRun, true))
	}
	// Removal targets every entry that runs creality-slicer-mcp, edited ones
	// too; another program's entry under the same name only when named.
	wanted, unknown := matchClients(harnesses, cmd.Clients)
	if len(unknown) > 0 {
		fmt.Fprintf(os.Stderr, "  no known client matches --clients %s\n", strings.Join(unknown, ","))
		return 2
	}
	var ids []harness.ID
	var foreign []harness.Harness
	for _, h := range harnesses {
		e, found := entries[h.ID]
		switch {
		case !found:
		case len(cmd.Clients) > 0:
			if wanted[h.ID] {
				ids = append(ids, h.ID)
			}
		case e.ours():
			ids = append(ids, h.ID)
		default:
			foreign = append(foreign, h)
		}
	}
	printSkippedForeign(os.Stdout, foreign, entries, scope, name)
	if len(ids) == 0 && len(foreign) > 0 {
		return 0
	}
	code := registerIDs(ctx, detector, scope, harnesses, ids, cmd.DryRun, desired)
	// The guide skill goes with the clients it was written for, except from a
	// folder another client that stays registered still reads.
	removing := map[harness.ID]bool{}
	for _, id := range ids {
		removing[id] = true
	}
	var remaining []harness.ID
	for id, e := range entries {
		if e.ours() && !removing[id] {
			remaining = append(remaining, id)
		}
	}
	return max(code, removeSkills(os.Stdout, scope, ids, remaining, cmd.DryRun))
}

// registerIDs adds or removes the server in the given harnesses, replacing a
// differing same-name entry: callers only pass harnesses meant to change.
func registerIDs(ctx context.Context, detector *harness.Detector, scope harness.Scope, harnesses []harness.Harness, ids []harness.ID, dryRun bool, desired harness.DesiredState) int {
	enabling := desired == harness.Present
	if len(ids) == 0 {
		if enabling {
			installer.PrintNoClients(os.Stdout, domain.BinaryName, false)
		} else {
			fmt.Println("  No clients are configured.")
		}
		return 0
	}
	if dryRun {
		return printPlan(ctx, detector, scope, ids, desired)
	}

	results := detector.ApplyIn(ctx, scope, ids, desired, harness.ConflictReplace)
	installer.PrintResultsWithScope(os.Stdout, results, scope, enabling, false)
	installer.PrintReloadHints(os.Stdout, results, byID(harnesses))
	return exitCodeFor(results)
}

func printPlan(ctx context.Context, detector *harness.Detector, scope harness.Scope, ids []harness.ID, desired harness.DesiredState) int {
	changes, err := detector.PlanResultsIn(ctx, scope, ids, desired, harness.ConflictReplace)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	installer.PrintChanges(os.Stdout, changes, scope)
	return 0
}

// --- Uninstall ---

func runUninstall(ctx context.Context, cmd cli.Command) int {
	detector, err := newDetector(serverName(cmd))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	scope := harness.Scope{}
	if cmd.Scope == string(harness.ScopeProject) {
		// --all removes the program and everything installed for the user,
		// which a project-scoped uninstall must not touch.
		if cmd.All {
			fmt.Fprintln(os.Stderr, "  --all removes the program itself, so it cannot be combined with --scope project.\n"+
				"  Run `"+domain.BinaryName+" uninstall --scope project` to remove this project's client entries,\n"+
				"  or `"+domain.BinaryName+" uninstall --all` to remove everything installed for your user.")
			return 2
		}
		dir, err := projectDir(cmd)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		scope = harness.ProjectScopeDir(dir)
	} else if cmd.All {
		// --all removes everything, not only the client registrations, so
		// it cannot leave some clients registered to the deleted program.
		if len(cmd.Clients) > 0 {
			fmt.Fprintln(os.Stderr, "  --all removes the program itself, so it cannot be combined with --clients.\n"+
				"  Run `"+domain.BinaryName+" uninstall --clients ...` to remove only some registrations.")
			return 2
		}
		return runUninstallAll(ctx, detector, cmd)
	}
	return runUnattended(ctx, detector, scope, cmd, harness.Absent)
}

// --- Doctor ---

func updateOptions() update.Options {
	opts := update.Options{
		Owner:          domain.Owner,
		Repo:           domain.Repo,
		CurrentVersion: version,
		AssetName:      domain.AssetName,
		BinaryName:     domain.BinaryName,
	}
	if exe, err := harness.ResolveExecutable(); err == nil {
		opts.InstallDir = filepath.Dir(exe)
		opts.BinaryName = filepath.Base(exe)
	}
	return opts
}

// doctorChecks are the health checks, in the order doctor reports them.
func doctorChecks() []doctor.Check {
	opts := updateOptions()
	checks := []doctor.Check{
		executableCheck{},
		doctor.PathCheck{Dir: opts.InstallDir},
		clientsCheck{},
	}
	checks = append(checks, doctorchecks.Checks()...)
	if version != "dev" {
		checks = append(checks, doctor.UpdateCheck{Opts: opts})
	}
	return checks
}

func newDoctor() *doctor.Runner { return doctor.New(doctorChecks()...) }

func runDoctor(ctx context.Context) int {
	return newDoctor().Run(ctx, os.Stdout)
}

// executableCheck is doctor.ExecutableCheck, except on Windows: there Go
// reports no execute permission bits, so the library check always fails, and
// a file that exists and is running is executable by definition.
type executableCheck struct{}

func (executableCheck) Name() string { return doctor.ExecutableCheck{}.Name() }

func (c executableCheck) Run(ctx context.Context) doctor.Result {
	if runtime.GOOS != "windows" {
		return doctor.ExecutableCheck{}.Run(ctx)
	}
	path, err := os.Executable()
	if err != nil {
		return doctor.Result{Name: c.Name(), Status: doctor.Fail, Detail: fmt.Sprintf("cannot determine executable: %v", err)}
	}
	if _, err := os.Stat(path); err != nil {
		return doctor.Result{Name: c.Name(), Status: doctor.Fail, Detail: fmt.Sprintf("cannot stat %s: %v", path, err)}
	}
	return doctor.Result{Name: c.Name(), Status: doctor.OK, Detail: path}
}

// configuredClient is a client that has this server registered: its name and
// the name with what is wrong with its entry.
type configuredClient struct{ Name, Label string }

// configuredClients lists the AI clients that have this server registered,
// including entries the user has edited (see clients.go). outdated is true
// when one of them runs another copy of the program.
func configuredClients(ctx context.Context) (clients []configuredClient, outdated bool, err error) {
	detector, err := newDetector(domain.ServerName)
	if err != nil {
		return nil, false, err
	}
	harnesses := detector.Detect(ctx)
	entries := clientEntries(ctx, detector, harness.Scope{}, harnesses)
	for _, h := range harnesses {
		switch e := entries[h.ID]; e.kind {
		case entryConfigured:
			clients = append(clients, configuredClient{h.Name, h.Name})
		case entryEdited:
			clients = append(clients, configuredClient{h.Name, h.Name + " (entry edited)"})
		case entryOutdated:
			clients = append(clients, configuredClient{h.Name, h.Name + " (runs " + e.command + ")"})
			outdated = true
		}
	}
	return clients, outdated, nil
}

// clientsCheck lists the AI clients that have this server registered,
// including entries the user has edited (see clients.go).
type clientsCheck struct{}

func (clientsCheck) Name() string { return "AI clients" }

func (clientsCheck) Run(ctx context.Context) doctor.Result {
	clients, outdated, err := configuredClients(ctx)
	if err != nil {
		return doctor.Result{Name: "AI clients", Status: doctor.Fail, Detail: err.Error()}
	}
	var configured []string
	for _, c := range clients {
		configured = append(configured, c.Label)
	}
	if len(configured) == 0 {
		return doctor.Result{Name: "AI clients", Status: doctor.Warn, Detail: "no client is configured; run `" + domain.BinaryName + " install`"}
	}
	if outdated {
		return doctor.Result{Name: "AI clients", Status: doctor.Warn, Detail: strings.Join(configured, ", ") + "; run `" + domain.BinaryName + " install --yes` to register this copy instead"}
	}
	return doctor.Result{Name: "AI clients", Status: doctor.OK, Detail: strings.Join(configured, ", ")}
}

// --- Update ---

func runUpdate(ctx context.Context, cmd cli.Command) int {
	opts := updateOptions()

	// `update --from <file>` swaps in a binary that was already downloaded
	// and verified by other means, then refreshes the guide skill, as a
	// download does.
	if len(cmd.Args) >= 2 && cmd.Args[0] == "--from" {
		if err := update.SwapFrom(ctx, cmd.Args[1], opts); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Println("  Updated.")
		return runNewBinaryGuideRefresh(ctx, filepath.Join(opts.InstallDir, opts.BinaryName))
	}

	if version == "dev" {
		fmt.Println("  This is a development build; build from source to update.")
		return 0
	}
	latest, available, err := update.Check(ctx, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  Update check failed: %v\n", err)
		return 1
	}
	if !available {
		fmt.Printf("  %s %s is up to date.\n", domain.BinaryName, version)
		return 0
	}
	fmt.Printf("  Updating %s %s -> %s\n", domain.BinaryName, version, latest)
	if err := update.SelfUpdate(ctx, opts); err != nil {
		fmt.Fprintf(os.Stderr, "  Update failed: %v\n", err)
		return 1
	}
	fmt.Printf("  Updated to %s.\n", latest)
	// The new binary carries the matching guide; let it refresh the copies.
	return runNewBinaryGuideRefresh(ctx, filepath.Join(opts.InstallDir, opts.BinaryName))
}

// runNewBinaryGuideRefresh runs `refresh-guide` with the binary an update just
// installed, since this process still holds the old guide.
func runNewBinaryGuideRefresh(ctx context.Context, exe string) int {
	cmd := exec.CommandContext(ctx, exe, "refresh-guide")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "  Could not update the guide skill (%v); run `%s install --yes` to write it again.\n", err, domain.BinaryName)
		return 1
	}
	return 0
}

// runRefreshGuide rewrites the copies of the guide skill this program wrote
// before with the guide embedded in this binary. --dry-run only reports them.
func runRefreshGuide(cmd cli.Command) int {
	dryRun := false
	for _, a := range cmd.Args {
		if a == "--dry-run" || a == "-dry-run" {
			dryRun = true
		}
	}
	return refreshSkills(os.Stdout, dryRun)
}

// --- MCP Server ---

func runMCPServer(ctx context.Context, cmd cli.Command) int {
	// This server only ever serves its own MCP tools over the configured
	// transport; it never bridges to another MCP endpoint.
	if cmd.Remote != "" {
		fmt.Fprintf(os.Stderr, "  %s does not bridge to other MCP servers, so `mcp --remote %s` is refused.\n"+
			"  Run `%s mcp` without --remote.\n", domain.BinaryName, cmd.Remote, domain.BinaryName)
		return 2
	}

	settings, err := domain.SettingsFromEnv()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	srv := mcpserver.New(mcpserver.Config{
		Version:   version,
		Transport: settings.Transport,
		HTTPAddr:  settings.Addr,
		Settings:  settings,
	})
	err = srv.Run(ctx)
	if err == nil || errors.Is(err, context.Canceled) || isClientHangup(err) {
		return 0
	}
	fmt.Fprintln(os.Stderr, err)
	return 1
}

// isClientHangup reports whether the server stopped because the client closed
// the transport (stdin EOF for stdio), which is how MCP sessions normally end.
// The SDK formats this as "server is closing: EOF" without wrapping io.EOF, so
// the check is textual.
func isClientHangup(err error) bool {
	return strings.HasSuffix(err.Error(), io.EOF.Error())
}
