package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/sairaph/creality-slicer-mcp/internal/applaunch"
	"github.com/sairaph/creality-slicer-mcp/internal/catalog"
	"github.com/sairaph/creality-slicer-mcp/internal/domain"
	"github.com/sairaph/creality-slicer-mcp/internal/motext"
	"github.com/sairaph/creality-slicer-mcp/internal/profiles"
	"github.com/sairaph/creality-slicer-mcp/internal/projects"
	"github.com/sairaph/creality-slicer-mcp/internal/slicer"
)

// catalogVersion is the embedded settings catalog the tools describe.
const catalogVersion = "7.2.1"

// k2Model is the printer model whose presets the catalog drift check reads.
const k2Model = "Creality K2"

// maxDriftKeys is how many drifting keys a status body lists.
const maxDriftKeys = 10

// The K2 presets whose values are shown as "K2 default" in search results.
const (
	k2Printer  = "Creality K2 0.4 nozzle"
	k2Process  = "0.20mm Standard @Creality K2 0.4 nozzle"
	k2Filament = "CR-PLA @Creality K2 0.4 nozzle"
)

// InstallSource finds Creality Print. *slicer.Detector is the real one.
type InstallSource interface {
	Get(ctx context.Context) (slicer.Install, error)
	Refresh(ctx context.Context) (slicer.Install, error)
}

// ProfileStore is the part of *profiles.Store the tools use.
type ProfileStore interface {
	List(t profiles.Type, f profiles.Filter) ([]profiles.Descriptor, error)
	Get(t profiles.Type, name string) (profiles.Preset, error)
	Info() profiles.Info
	Warnings() []string
	// KeysSetBy returns the setting keys set by the presets of a printer model.
	KeysSetBy(printerModel string) ([]string, error)
}

// Deps are what the tools read from the machine. Every field is optional:
// New fills the missing ones with the real implementation, and tests replace
// them with fakes (a fake install, a small store in a temporary folder, a
// synthetic text index).
type Deps struct {
	// Install finds Creality Print; default slicer.NewDetector with
	// Config.Settings.Cmd.
	Install InstallSource
	// Catalog loads the settings catalog once; default catalog.Load of the
	// embedded version.
	Catalog func() (*catalog.Catalog, error)
	// Texts loads the app's setting descriptions from its resources\i18n
	// folder; default motext.LoadDir. A failure is fine: descriptions are then
	// reported as missing.
	Texts func(dir string) (catalog.TextSource, error)
	// Profiles opens the preset bundles of an install; default profiles.Open
	// on the install's profile root and data folder.
	Profiles func(in slicer.Install) (ProfileStore, error)
	// ProjectsDir is the store of projects; default domain.ProjectsDir.
	ProjectsDir func() (string, error)
	// NewProjects builds the projects layer for a found install: the store
	// and the slice jobs it runs. Default: a projects.Store in ProjectsDir
	// with the real slicer runner (when the install is supported). Tests
	// build one over a temporary folder with a fake slicer process.
	NewProjects func(in slicer.Install, ps ProfileStore, cat *catalog.Catalog) (ProjectBackend, error)
	// Launcher starts a Creality Print window on a file (open_in_app); default
	// applaunch.Real. Tests inject a fake: the real one refuses under go test.
	Launcher applaunch.Launcher
}

// ProjectStore is the part of *projects.Store the tools use, one method per
// operation: the tools add no logic of their own to them.
type ProjectStore interface {
	CreateProject(req projects.CreateRequest) (*projects.Info, error)
	OpenProject(req projects.OpenRequest) (*projects.OpenResult, error)
	List() ([]projects.ListItem, error)
	GetProject(ref string) (*projects.Info, error)
	AddModel(ref string, req projects.AddModelRequest) (*projects.AddModelResult, error)
	UpdateObject(ref string, req projects.UpdateObjectRequest) (*projects.UpdateObjectResult, error)
	RemoveObject(ref, object string) (*projects.Info, error)
	GroupObjects(ref string, req projects.GroupRequest) (*projects.GroupResult, error)
	RemovePart(ref, object, part string) (*projects.RemovePartResult, error)
	ListJobs() []projects.JobInfo
	ExplainSettings(ref string, used map[string]string) ([]projects.SettingDiff, error)
	Overrides(ref string, plate int) (*projects.OverridesReport, error)
	UpdateSettings(ref string, req projects.SettingsRequest) (*projects.SettingsResult, error)
	SetPresets(ref string, req projects.PresetsRequest) (*projects.PresetsResult, error)
	AddModifier(ref string, req projects.ModifierRequest) (*projects.ModifierResult, error)
	SetHeightRanges(ref, object string, ranges []projects.RangeSpec) (*projects.Info, error)
	SetHeightRangesDetailed(ref, object string, ranges []projects.RangeSpec) (*projects.HeightRangesResult, error)
	SetLayerActions(ref string, plate int, actions []projects.LayerAction) (*projects.Info, error)
	ManagePlates(ref string, req projects.PlatesRequest) (*projects.PlatesResult, error)
	Export(ref, path string, overwrite bool) (*projects.ExportResult, error)
	Delete(ref, confirm string) (string, error)
	Slice(ref string, opts projects.SliceOptions) (*projects.SliceOutcome, error)
	SliceStatus(ref string) (*projects.SliceStatus, error)
	CancelSlice(jobID string) (bool, error)
	Report(ref string, plate int) (*projects.SliceReport, error)
	Preview(ref string, kind projects.PreviewKind, plate int) ([]byte, error)
	StoredThumbnail(ref string, plate int) ([]byte, error)
	SettingValue(ref, key string) (*projects.SettingValue, error)
	View(ref string, req projects.ViewRequest) (*projects.ViewResult, error)
	PrepareView(ref string, plate int, mode string) (*projects.ViewFile, error)
}

// ProjectBackend is the projects layer and the slice jobs it started. The
// server owns the jobs: it stops them all when it shuts down.
type ProjectBackend struct {
	Store ProjectStore
	Jobs  *slicer.Jobs
}

// realProjects builds the production projects layer.
func realProjects(in slicer.Install, ps ProfileStore, cat *catalog.Catalog) (ProjectBackend, error) {
	real, ok := ps.(*profiles.Store)
	if !ok {
		return ProjectBackend{}, fmt.Errorf("the projects layer needs the installed preset bundle")
	}
	root, err := domain.ProjectsDir()
	if err != nil {
		return ProjectBackend{}, err
	}
	cfg := projects.Config{Root: root, Install: in, Profiles: real, Catalog: cat}
	var jobs *slicer.Jobs
	if in.Supported {
		runner, err := slicer.NewRunner(in, cat.CLIFlag)
		if err != nil {
			return ProjectBackend{}, err
		}
		cache, err := domain.CacheDir()
		if err != nil {
			return ProjectBackend{}, err
		}
		jobs = slicer.NewJobs(runner, cache)
		cfg.Runner, cfg.Jobs = runner, jobs
	}
	st, err := projects.New(cfg)
	if err != nil {
		return ProjectBackend{}, err
	}
	return ProjectBackend{Store: st, Jobs: jobs}, nil
}

func (d Deps) withDefaults(cfg Config) Deps {
	if d.Install == nil {
		d.Install = slicer.NewDetector(slicer.Options{Cmd: cfg.Settings.Cmd})
	}
	if d.Catalog == nil {
		// The catalog of the installed version family (7.3 has its own); an install
		// that is not found or has no catalog of its own gets the default one.
		d.Catalog = func() (*catalog.Catalog, error) {
			if in, err := d.Install.Get(context.Background()); err == nil && in.Version != "" {
				if c, err := catalog.Load(in.Version); err == nil {
					return c, nil
				}
			}
			return catalog.Load(catalogVersion)
		}
	}
	if d.Texts == nil {
		d.Texts = func(dir string) (catalog.TextSource, error) { return motext.LoadDir(dir) }
	}
	if d.Profiles == nil {
		d.Profiles = func(in slicer.Install) (ProfileStore, error) {
			return profiles.Open(profiles.Roots{
				InstallProfiles: filepath.Join(in.Dir, "resources", "profiles"),
				DataDir:         in.DataDir,
				Bundle:          in.ProfileRoot,
			})
		}
	}
	if d.ProjectsDir == nil {
		d.ProjectsDir = domain.ProjectsDir
	}
	if d.NewProjects == nil {
		d.NewProjects = realProjects
	}
	if d.Launcher == nil {
		d.Launcher = applaunch.Real{}
	}
	return d
}

// errNoInstall means Creality Print is not there, so anything read from it is
// unavailable; the message says why.
type errNoInstall struct{ reason string }

func (e errNoInstall) Error() string {
	if e.reason == "" {
		return "Creality Print was not found"
	}
	return e.reason
}

// env is the lazily loaded state behind the tools: the detected install, the
// catalog with its texts, the profile store and the K2 default values. All of
// it is loaded on first use and dropped again by refresh.
type env struct {
	deps Deps

	mu sync.Mutex
	// gen counts refreshes. A lazy loader reads it before it reads the install
	// and stores what it loaded only if it is unchanged: a load that started on
	// the old install must not repopulate a cache the refresh has emptied.
	gen       uint64
	baseCat   *catalog.Catalog // loaded once, without texts
	baseErr   error
	baseDone  bool
	cat       *catalog.Catalog // with the texts of the current install, when they load
	catDone   bool
	textsErr  error
	store     ProfileStore
	defaults  *k2Defaults
	defaultsD bool
	backend   *ProjectBackend
	retired   []ProjectBackend // backends replaced by a refresh that still have jobs
}

func newEnv(d Deps) *env { return &env{deps: d} }

// install returns the detected install; refresh re-detects and drops what was
// loaded from the old one.
func (e *env) install(ctx context.Context, refresh bool) (slicer.Install, error) {
	if refresh {
		// Detect first, then drop what was loaded from the old install: a
		// call in between would otherwise cache the old install's data again.
		in, err := e.deps.Install.Refresh(ctx)
		e.mu.Lock()
		e.gen++
		e.cat, e.catDone, e.textsErr, e.store, e.defaults, e.defaultsD = nil, false, nil, nil, nil, false
		// The catalog family follows the install: choose it again.
		e.baseCat, e.baseErr, e.baseDone = nil, nil, false
		// The backend is replaced at once: every call after the refresh
		// uses the new presets and catalog. One that still has jobs (running
		// or recent) is kept aside, only so those jobs can be polled and
		// cancelled (jobBackends) and stopped at shutdown.
		if e.backend != nil {
			if e.backend.Jobs != nil && len(e.backend.Jobs.Snapshots()) > 0 {
				e.retired = append(e.retired, *e.backend)
			}
			e.backend = nil
		}
		e.mu.Unlock()
		return in, err
	}
	return e.deps.Install.Get(ctx)
}

// snapshot returns the install together with the generation it belongs to. The
// generation is read first: if a refresh completes after that, the caller sees
// the generation has moved and starts again, so nothing it loaded from an
// install read before the refresh is kept.
func (e *env) snapshot(ctx context.Context) (slicer.Install, uint64, error) {
	e.mu.Lock()
	gen := e.gen
	e.mu.Unlock()
	in, err := e.install(ctx, false)
	return in, gen, err
}

// current says whether no refresh has completed since gen was read. Callers
// hold e.mu.
func (e *env) current(gen uint64) bool { return e.gen == gen }

// catalog returns the settings catalog. Once Creality Print is found, its
// descriptions are attached (loaded once); without them the catalog still
// works and texts report as missing. The second result is the reason texts are
// missing, "" when they are attached.
func (e *env) catalog(ctx context.Context) (*catalog.Catalog, string, error) {
	for {
		in, gen, err := e.snapshot(ctx)
		if err != nil {
			return nil, "", err
		}
		e.mu.Lock()
		if !e.current(gen) {
			e.mu.Unlock()
			continue // a refresh completed while the install was read
		}
		cat, reason, err := e.catalogLocked(in)
		e.mu.Unlock()
		return cat, reason, err
	}
}

// catalogLocked loads the catalog for in; e.mu is held.
func (e *env) catalogLocked(in slicer.Install) (*catalog.Catalog, string, error) {
	if !e.baseDone {
		e.baseCat, e.baseErr = e.deps.Catalog()
		e.baseDone = true
	}
	if e.baseErr != nil {
		return nil, "", fmt.Errorf("the embedded settings catalog cannot be loaded: %w", e.baseErr)
	}
	if !e.catDone {
		e.catDone = true
		e.cat = e.baseCat
		switch {
		case !in.Found:
			e.textsErr = errors.New("Creality Print was not found")
		default:
			ts, terr := e.deps.Texts(filepath.Join(in.Dir, "resources", "i18n"))
			if terr != nil {
				e.textsErr = terr
			} else {
				e.cat = e.baseCat.WithTexts(ts)
			}
		}
	}
	reason := ""
	if e.textsErr != nil {
		reason = e.textsErr.Error()
	}
	return e.cat, reason, nil
}

// profileStore opens the preset bundles of the installed Creality Print.
func (e *env) profileStore(ctx context.Context) (ProfileStore, error) {
	for {
		in, gen, err := e.snapshot(ctx)
		if err != nil {
			return nil, err
		}
		if !in.Found {
			return nil, errNoInstall{reason: in.Reason}
		}
		e.mu.Lock()
		if !e.current(gen) {
			e.mu.Unlock()
			continue
		}
		if e.store != nil {
			st := e.store
			e.mu.Unlock()
			return st, nil
		}
		st, err := e.deps.Profiles(in)
		if err != nil {
			e.mu.Unlock()
			return nil, fmt.Errorf("the preset bundle cannot be read: %w", err)
		}
		e.store = st
		e.mu.Unlock()
		return st, nil
	}
}

// projectBackend returns the projects layer, built on first use from the
// installed Creality Print's presets and the catalog.
func (e *env) projectBackend(ctx context.Context) (ProjectBackend, error) {
	for {
		in, gen, err := e.snapshot(ctx)
		if err != nil {
			return ProjectBackend{}, err
		}
		if !in.Found {
			return ProjectBackend{}, errNoInstall{reason: in.Reason}
		}
		ps, err := e.profileStore(ctx)
		if err != nil {
			return ProjectBackend{}, err
		}
		cat, _, err := e.catalog(ctx)
		if err != nil {
			return ProjectBackend{}, err
		}
		e.mu.Lock()
		if !e.current(gen) {
			e.mu.Unlock()
			continue // the install changed while the parts were loaded
		}
		if e.backend != nil {
			be := *e.backend
			e.mu.Unlock()
			return be, nil
		}
		be, err := e.deps.NewProjects(in, ps, cat)
		if err != nil {
			e.mu.Unlock()
			return ProjectBackend{}, fmt.Errorf("%w: %w", errProjectsStart, err)
		}
		e.backend = &be
		e.mu.Unlock()
		return be, nil
	}
}

// jobBackends returns the backends whose jobs can be asked about: the current
// one (when it can start) first, then the ones replaced by a refresh that
// still have jobs. Only job polling and cancelling use the old ones.
func (e *env) jobBackends(ctx context.Context) ([]ProjectBackend, error) {
	cur, err := e.projectBackend(ctx)
	e.mu.Lock()
	kept := e.retired[:0]
	for _, be := range e.retired {
		if be.Jobs != nil && len(be.Jobs.Snapshots()) > 0 {
			kept = append(kept, be)
		}
	}
	e.retired = kept
	old := append([]ProjectBackend(nil), kept...)
	e.mu.Unlock()
	if err != nil {
		if len(old) == 0 {
			return nil, err
		}
		return old, nil
	}
	return append([]ProjectBackend{cur}, old...), nil
}

// stopJobs stops every slice job this server started: none outlives it.
func (e *env) stopJobs() {
	e.mu.Lock()
	var all []*slicer.Jobs
	for _, be := range e.retired {
		if be.Jobs != nil {
			all = append(all, be.Jobs)
		}
	}
	if e.backend != nil && e.backend.Jobs != nil {
		all = append(all, e.backend.Jobs)
	}
	e.mu.Unlock()
	for _, j := range all {
		j.StopAll()
	}
}

// k2Defaults are the flattened K2 presets that supply the "K2 default"
// column; a preset that cannot be read is nil.
type k2Defaults struct {
	printer, process, filament *profiles.Preset
}

// k2 returns the K2 default presets, or nil when no store is available.
func (e *env) k2(ctx context.Context) *k2Defaults {
	st, err := e.profileStore(ctx)
	if err != nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	// The store was read before the lock: after a refresh it may be an old one,
	// whose defaults must not be cached for the new install.
	current := e.store == st
	if current && e.defaultsD {
		return e.defaults
	}
	if current {
		e.defaultsD = true
	}
	d := &k2Defaults{}
	get := func(t profiles.Type, name string) *profiles.Preset {
		p, err := st.Get(t, name)
		if err != nil {
			return nil
		}
		return &p
	}
	d.printer, d.process, d.filament = get(profiles.TypePrinter, k2Printer), get(profiles.TypeProcess, k2Process), get(profiles.TypeFilament, k2Filament)
	if d.printer == nil && d.process == nil && d.filament == nil {
		return nil
	}
	if current {
		e.defaults = d
	}
	return d
}

// value returns the K2 preset value of an option: the string form of its value
// in the preset of its type, and false when no K2 preset sets it.
func (d *k2Defaults) value(o *catalog.Option) (string, bool) {
	if d == nil {
		return "", false
	}
	types := o.PresetTypes
	if len(types) == 0 {
		types = []string{o.Owner}
	}
	for _, t := range types {
		var p *profiles.Preset
		switch t {
		case "process", "print":
			p = d.process
		case "filament":
			p = d.filament
		case "printer":
			p = d.printer
		}
		if p == nil {
			continue
		}
		if v, ok := p.Values[o.Key]; ok {
			return showPresetValue(v), true
		}
	}
	return "", false
}

// errProjectsStart is wrapped in the error of a projects layer that could not be
// built (the store folder, for example).
var errProjectsStart = errors.New("the projects layer cannot start")
