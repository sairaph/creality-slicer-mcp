package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/sairaph/mcp-wizard/app"
	"github.com/sairaph/mcp-wizard/app/detail"
	"github.com/sairaph/mcp-wizard/app/menu"
	"github.com/sairaph/mcp-wizard/async"
	"github.com/sairaph/mcp-wizard/cli"
	"github.com/sairaph/mcp-wizard/command"
	"github.com/sairaph/mcp-wizard/doctor"
	"github.com/sairaph/mcp-wizard/flow"
	"github.com/sairaph/mcp-wizard/harness"
	"github.com/sairaph/mcp-wizard/installer"
	"github.com/sairaph/mcp-wizard/proxy"
	"github.com/sairaph/mcp-wizard/secret"
	"github.com/sairaph/mcp-wizard/tui"
	"github.com/sairaph/mcp-wizard/update"

	"github.com/sairaph/creality-slicer-mcp/internal/domain"
	"github.com/sairaph/creality-slicer-mcp/internal/mcpserver"
)

// version is set by goreleaser via -ldflags "-X main.version=...".
var version = "dev"

var oneShotCommands = command.New()

func init() {
	// Register one-shot CLI commands that share business logic with the TUI:
	//
	// oneShotCommands.Register(command.Handler{
	//     Name:        "list-items",
	//     Description: "List all items",
	//     Run: func(ctx context.Context, args []string) int {
	//         return 0
	//     },
	// })
}

var usageSpecs = []cli.Spec{
	{Name: "mcp", Description: "Run the MCP server, or bridge to the remote endpoint (default when not in a terminal)"},
	{Name: "install", Description: "Install and configure AI client integration"},
	{Name: "uninstall", Description: "Remove AI client integration"},
	{Name: "add", Description: "Register the server in this project's AI client configs"},
	{Name: "login", Description: "Sign in to creality-slicer-mcp"},
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
	case "login":
		os.Exit(runLogin(ctx, cmd))
	case "doctor":
		os.Exit(runDoctor(ctx))
	case "update":
		os.Exit(runUpdate(ctx, cmd))
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
	Login   installer.LoginState
	Results installer.ResultsState
}

func harnessState(s *AppState) *installer.HarnessState { return &s.Harness }
func loginState(s *AppState) *installer.LoginState     { return &s.Login }
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

// selectIDs picks the harnesses to act on. --clients wins, then --all, and by
// default every selectable client that is not configured yet. The returned
// reason explains an empty selection.
func selectIDs(harnesses []harness.Harness, cmd cli.Command) (ids []harness.ID, reason string) {
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
			return nil, "no detected client matches --clients " + strings.Join(unknown, ",")
		}
		return ids, ""
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
		if cmd.All || !h.Configured {
			ids = append(ids, h.ID)
		}
	}
	if len(ids) == 0 && candidates > 0 {
		return nil, "every detected client is already configured (use --all to re-register)"
	}
	return ids, ""
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

// credentialKey is the store key the bridge reads the remote token from.
func credentialKey() string {
	if r := domain.Remote(); r != nil && r.CredentialKey != "" {
		return r.CredentialKey
	}
	return "token"
}

// saveCredentialsFromFlags stores --email/--token given to an unattended
// install so the server can use them without a login step. The token is
// stored under the key the bridge reads.
func saveCredentialsFromFlags(ctx context.Context, store secret.Store, cmd cli.Command) error {
	if len(cmd.Credentials) == 0 {
		return nil
	}
	sess, _, err := store.Load(ctx)
	if err != nil {
		return err
	}
	for k, v := range cmd.Credentials {
		if k == "token" {
			k = credentialKey()
			v = strings.TrimSpace(v)
		}
		sess.Set(k, v)
	}
	return store.Save(ctx, sess)
}

// loadCredential returns the stored value for key, looking in the current
// project's store first (where `add` saves it) and then the global one.
func loadCredential(ctx context.Context, key string) (string, error) {
	paths := []string{domain.CredentialPath()}
	if cwd, err := os.Getwd(); err == nil {
		paths = append([]string{domain.ProjectCredentialPath(cwd)}, paths...)
	}
	for _, path := range paths {
		sess, exists, err := secret.NewFileStore(path).Load(ctx)
		if err != nil {
			return "", err
		}
		if !exists {
			continue
		}
		if v := strings.TrimSpace(sess.GetString(key)); v != "" {
			return v, nil
		}
	}
	return "", nil
}

// checkRemoteURL refuses to send a credential over plain HTTP to anything
// but a loopback address.
func checkRemoteURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid remote URL %q: %w", raw, err)
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		host := u.Hostname()
		if host == "localhost" || net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback() {
			return nil
		}
		return fmt.Errorf("remote URL %q uses plain http; the credential would be sent in cleartext (use https)", raw)
	default:
		return fmt.Errorf("remote URL %q must use https", raw)
	}
}

// --- Install / add ---

func runInstall(ctx context.Context, cmd cli.Command) int {
	detector, err := newDetector(serverName(cmd))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	credStore := secret.NewFileStore(domain.CredentialPath())

	if tui.IsInteractive() && !cmd.Yes {
		return runWizard(ctx, detector, harness.Scope{}, domain.LoginConfig(credStore), cmd, "creality-slicer-mcp setup")
	}
	return runUnattended(ctx, detector, harness.Scope{}, credStore, cmd, harness.Present)
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
	credStore := secret.NewFileStore(domain.ProjectCredentialPath(dir))

	if tui.IsInteractive() && !cmd.Yes {
		return runWizard(ctx, detector, scope, domain.ProjectLoginConfig(credStore, dir), cmd, "creality-slicer-mcp project setup")
	}
	return runUnattended(ctx, detector, scope, credStore, cmd, harness.Present)
}

// runWizard drives the interactive install: pick clients, sign in, register.
func runWizard(ctx context.Context, detector *harness.Detector, scope harness.Scope, login installer.LoginConfig, cmd cli.Command, title string) int {
	if cmd.DryRun {
		// A dry run must not write anything, credentials included.
		login.Store = nil
	}
	state := &AppState{}
	steps := []flow.Step[AppState]{
		installer.HarnessStep(ctx, detector, harnessState, installer.HarnessStepOptions{AllDetected: true, Scope: scope}),
		installer.LoginStep(ctx, login, loginState),
		installer.ApplyStep(ctx, detector, harnessState, resultsState, installer.ApplyStepOptions{Scope: scope, DryRun: cmd.DryRun}),
	}
	f := flow.New(steps, state)
	code := tui.Run(ctx, f, tui.Options{Title: title})
	if state.Failure != nil {
		fmt.Fprintln(os.Stderr, state.Failure)
	}
	return code
}

func runUnattended(ctx context.Context, detector *harness.Detector, scope harness.Scope, credStore secret.Store, cmd cli.Command, desired harness.DesiredState) int {
	enabling := desired == harness.Present
	harnesses := detector.DetectIn(ctx, scope)

	var ids []harness.ID
	if enabling {
		var reason string
		ids, reason = selectIDs(harnesses, cmd)
		if reason != "" {
			fmt.Fprintf(os.Stderr, "  %s\n", reason)
			if len(cmd.Clients) > 0 {
				return 2
			}
			return 0
		}
	} else {
		for _, h := range harnesses {
			if h.Configured {
				ids = append(ids, h.ID)
			}
		}
	}
	if len(ids) == 0 {
		if enabling {
			installer.PrintNoClients(os.Stdout, domain.BinaryName, false)
		} else {
			fmt.Println("  No clients are configured.")
		}
		return 0
	}
	if cmd.DryRun {
		return printPlan(ctx, detector, scope, ids, desired)
	}

	policy := harness.ConflictReplace
	if !enabling {
		policy = harness.ConflictError
	}
	results := detector.ApplyIn(ctx, scope, ids, desired, policy)
	installer.PrintResultsWithScope(os.Stdout, results, scope, enabling, false)
	installer.PrintReloadHints(os.Stdout, results, byID(harnesses))

	if enabling {
		if err := saveCredentialsFromFlags(ctx, credStore, cmd); err != nil {
			fmt.Fprintf(os.Stderr, "  Could not save credentials: %v\n", err)
			return 1
		}
	}
	return exitCodeFor(results)
}

func printPlan(ctx context.Context, detector *harness.Detector, scope harness.Scope, ids []harness.ID, desired harness.DesiredState) int {
	policy := harness.ConflictReplace
	if desired == harness.Absent {
		policy = harness.ConflictError
	}
	changes, err := detector.PlanResultsIn(ctx, scope, ids, desired, policy)
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
		dir, err := projectDir(cmd)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		scope = harness.ProjectScopeDir(dir)
	}
	return runUnattended(ctx, detector, scope, nil, cmd, harness.Absent)
}

// --- Login ---

func runLogin(ctx context.Context, cmd cli.Command) int {
	credStore := secret.NewFileStore(domain.CredentialPath())

	if len(cmd.Credentials) > 0 {
		if err := saveCredentialsFromFlags(ctx, credStore, cmd); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Printf("  Credentials saved to %s\n", credStore.Path())
		return 0
	}
	if !tui.IsInteractive() {
		fmt.Fprintln(os.Stderr, "login requires an interactive terminal, or pass credentials as flags")
		return 1
	}

	state := &AppState{}
	step := installer.LoginStep(ctx, domain.LoginConfig(credStore),
		func(s *AppState) *installer.LoginState { return &s.Login },
	)
	f := flow.New([]flow.Step[AppState]{step}, state)
	return tui.Run(ctx, f, tui.Options{Title: "creality-slicer-mcp login"})
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

func newDoctor(ctx context.Context) *doctor.Runner {
	opts := updateOptions()
	r := doctor.New(
		doctor.ExecutableCheck{},
		doctor.PathCheck{Dir: opts.InstallDir},
		credentialsCheck{path: domain.CredentialPath()},
		clientsCheck{},
	)
	if remote := domain.Remote(); remote != nil {
		r.Add(remoteCheck{remote: remote})
	}
	if version != "dev" {
		r.Add(doctor.UpdateCheck{Opts: opts})
	}
	return r
}

func runDoctor(ctx context.Context) int {
	return newDoctor(ctx).Run(ctx, os.Stdout)
}

// credentialsCheck reports whether stored credentials exist. Missing
// credentials are a warning, not a failure, because login is optional.
type credentialsCheck struct{ path string }

func (c credentialsCheck) Name() string { return "Credentials" }

func (c credentialsCheck) Run(_ context.Context) doctor.Result {
	if _, err := os.Stat(c.path); err != nil {
		return doctor.Result{Name: c.Name(), Status: doctor.Warn, Detail: "not signed in; run `creality-slicer-mcp login`"}
	}
	return doctor.Result{Name: c.Name(), Status: doctor.OK, Detail: c.path}
}

// remoteCheck confirms the remote endpoint accepts the stored credential
// with a single bounded MCP initialize request. It does not open a session,
// so nothing has to be torn down afterwards.
type remoteCheck struct{ remote *domain.RemoteConfig }

func (remoteCheck) Name() string { return "Remote endpoint" }

func (c remoteCheck) Run(ctx context.Context) doctor.Result {
	if err := checkRemoteURL(c.remote.URL); err != nil {
		return doctor.Result{Name: c.Name(), Status: doctor.Fail, Detail: err.Error()}
	}
	token, err := loadCredential(ctx, c.remote.CredentialKey)
	if err != nil {
		return doctor.Result{Name: c.Name(), Status: doctor.Fail, Detail: err.Error()}
	}
	if token == "" {
		return doctor.Result{Name: c.Name(), Status: doctor.Warn, Detail: "no credential stored; run `creality-slicer-mcp login`"}
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"creality-slicer-mcp-doctor","version":"` + version + `"}}}`
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.remote.URL, strings.NewReader(body))
	if err != nil {
		return doctor.Result{Name: c.Name(), Status: doctor.Fail, Detail: err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if c.remote.HeaderName != "" {
		req.Header.Set(c.remote.HeaderName, c.remote.HeaderPrefix+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return doctor.Result{Name: c.Name(), Status: doctor.Fail, Detail: oneLine(fmt.Sprintf("%s: %v", c.remote.URL, err))}
	}
	defer resp.Body.Close()
	// Tell the server we are not keeping the session it may have created.
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" && resp.StatusCode < 300 {
		if del, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.remote.URL, nil); err == nil {
			del.Header.Set("Mcp-Session-Id", sid)
			if c.remote.HeaderName != "" {
				del.Header.Set(c.remote.HeaderName, c.remote.HeaderPrefix+token)
			}
			if r, err := http.DefaultClient.Do(del); err == nil {
				r.Body.Close()
			}
		}
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return doctor.Result{Name: c.Name(), Status: doctor.Fail, Detail: fmt.Sprintf("%s rejected the stored credential (HTTP %d); run `creality-slicer-mcp login`", c.remote.URL, resp.StatusCode)}
	case resp.StatusCode >= 300:
		return doctor.Result{Name: c.Name(), Status: doctor.Fail, Detail: fmt.Sprintf("%s: HTTP %d", c.remote.URL, resp.StatusCode)}
	}
	return doctor.Result{Name: c.Name(), Status: doctor.OK, Detail: c.remote.URL}
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// clientsCheck lists the AI clients that have this server registered.
type clientsCheck struct{}

func (clientsCheck) Name() string { return "AI clients" }

func (clientsCheck) Run(ctx context.Context) doctor.Result {
	detector, err := newDetector(domain.ServerName)
	if err != nil {
		return doctor.Result{Name: "AI clients", Status: doctor.Fail, Detail: err.Error()}
	}
	var configured []string
	for _, h := range detector.Detect(ctx) {
		if h.Configured {
			configured = append(configured, h.Name)
		}
	}
	if len(configured) == 0 {
		return doctor.Result{Name: "AI clients", Status: doctor.Warn, Detail: "no client is configured; run `creality-slicer-mcp install`"}
	}
	return doctor.Result{Name: "AI clients", Status: doctor.OK, Detail: strings.Join(configured, ", ")}
}

// --- Update ---

func runUpdate(ctx context.Context, cmd cli.Command) int {
	opts := updateOptions()

	// `update --from <file>` is used by the install script, which has already
	// downloaded and verified the new binary.
	if len(cmd.Args) >= 2 && cmd.Args[0] == "--from" {
		if err := update.SwapFrom(ctx, cmd.Args[1], opts); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Println("  Updated.")
		return 0
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
		fmt.Printf("  creality-slicer-mcp %s is up to date.\n", version)
		return 0
	}
	fmt.Printf("  Updating creality-slicer-mcp %s -> %s\n", version, latest)
	if err := update.SelfUpdate(ctx, opts); err != nil {
		fmt.Fprintf(os.Stderr, "  Update failed: %v\n", err)
		return 1
	}
	fmt.Printf("  Updated to %s.\n", latest)
	return 0
}

// --- App ---

// The interactive app opens when the binary is run bare in a terminal. It
// starts as a menu; extend appState with your own steps and screens.

const (
	stepMenu app.Step = iota
	stepDoctor
)

type appState struct {
	app.AppModel
	ctx    context.Context
	menu   *menu.Model
	detail *detail.Model
}

func runApp(ctx context.Context) int {
	s := &appState{ctx: ctx}
	s.menu = menu.New(domain.ServerName+" "+version, func() []menu.Item {
		return []menu.Item{
			{Label: "Run doctor", Action: "doctor"},
			{Label: "Quit", Action: "quit"},
		}
	})
	return app.Run(ctx, s, app.Options{Title: domain.ServerName, Version: version})
}

func (m *appState) Init() tea.Cmd { return m.menu.Init() }

func (m *appState) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if handled, cmd := m.HandleGlobalKeys(msg); handled {
		return m, cmd
	}

	switch msg := msg.(type) {
	case app.ActionMsg:
		switch msg.Source {
		case "menu":
			action, _ := msg.Data.(string)
			if msg.Value == "quit" || action == "quit" {
				m.Quit = true
				return m, tea.Quit
			}
			if action == "doctor" {
				m.Step = stepDoctor
				m.Status = "Running checks..."
				m.detail = detail.New("Doctor", m.Status)
				return m, tea.Batch(m.detail.Init(), async.Load(func() (string, error) {
					var buf bytes.Buffer
					newDoctor(m.ctx).Run(m.ctx, &buf)
					return buf.String(), nil
				}))
			}
		case "detail":
			if msg.Value == "back" {
				m.Step = stepMenu
				return m, nil
			}
		}
		return m, nil

	case async.Result[string]:
		m.Status = ""
		if msg.Err != nil {
			m.detail.SetContent("Doctor failed: " + msg.Err.Error())
		} else {
			m.detail.SetContent(msg.Value)
		}
		return m, nil
	}

	switch m.Step {
	case stepMenu:
		return m, m.menu.Update(msg)
	case stepDoctor:
		if m.detail != nil {
			return m, m.detail.Update(msg)
		}
	}
	return m, nil
}

func (m *appState) View() string {
	switch m.Step {
	case stepDoctor:
		if m.detail != nil {
			return m.detail.View()
		}
	}
	return m.menu.View()
}

// --- MCP Server ---

func runMCPServer(ctx context.Context, cmd cli.Command) int {
	transport := os.Getenv("TRANSPORT")

	// --remote, or a configured remote endpoint when TRANSPORT is not set
	// explicitly, turns this binary into a stdio bridge: the AI client talks
	// to us, we talk to the remote with the stored credential. An explicit
	// TRANSPORT always serves the embedded server.
	if remoteURL, remote := bridgeTarget(cmd); remoteURL != "" && (cmd.Remote != "" || transport == "") {
		return runBridge(ctx, remoteURL, remote)
	}

	if transport == "" {
		transport = "stdio"
	}
	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = "127.0.0.1:8080"
	}

	srv := mcpserver.New(mcpserver.Config{
		Version:   version,
		Transport: transport,
		HTTPAddr:  addr,
	})
	err := srv.Run(ctx)
	if err == nil || errors.Is(err, context.Canceled) || isClientHangup(err) {
		return 0
	}
	fmt.Fprintln(os.Stderr, err)
	return 1
}

// bridgeTarget returns the remote URL to bridge to: --remote wins, then the
// project's RemoteConfig. The second value carries the header settings.
func bridgeTarget(cmd cli.Command) (string, *domain.RemoteConfig) {
	remote := domain.Remote()
	if cmd.Remote != "" {
		if remote == nil {
			// Ad-hoc bridge on a project without a configured remote: a
			// bearer token stored with `login --token`.
			remote = &domain.RemoteConfig{HeaderName: "Authorization", HeaderPrefix: "Bearer ", CredentialKey: "token"}
		}
		return cmd.Remote, remote
	}
	if remote != nil {
		return remote.URL, remote
	}
	return "", nil
}

func runBridge(ctx context.Context, remoteURL string, remote *domain.RemoteConfig) int {
	if err := checkRemoteURL(remoteURL); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	err := proxy.Run(ctx, proxy.Config{
		URL: remoteURL,
		HeaderFunc: func(ctx context.Context) (map[string]string, error) {
			if remote.HeaderName == "" || remote.CredentialKey == "" {
				return nil, nil
			}
			token, err := loadCredential(ctx, remote.CredentialKey)
			if err != nil {
				return nil, err
			}
			if token == "" {
				return nil, fmt.Errorf("no credential stored; run `creality-slicer-mcp login` (or `creality-slicer-mcp login --token <token>`) first")
			}
			return map[string]string{remote.HeaderName: remote.HeaderPrefix + token}, nil
		},
	})
	if err == nil || errors.Is(err, context.Canceled) {
		return 0
	}
	fmt.Fprintln(os.Stderr, err)
	if errors.Is(err, proxy.ErrUnauthorized) {
		fmt.Fprintln(os.Stderr, "  The stored credential was rejected; run `creality-slicer-mcp login` to replace it.")
	}
	return 1
}

// isClientHangup reports whether the server stopped because the client closed
// the transport (stdin EOF for stdio), which is how MCP sessions normally end.
// The SDK formats this as "server is closing: EOF" without wrapping io.EOF, so
// the check is textual.
func isClientHangup(err error) bool {
	return strings.HasSuffix(err.Error(), io.EOF.Error())
}
