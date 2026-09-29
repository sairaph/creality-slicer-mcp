package doctorchecks

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/sairaph/mcp-wizard/doctor"

	"github.com/sairaph/creality-slicer-mcp/internal/catalog"
	"github.com/sairaph/creality-slicer-mcp/internal/domain"
	"github.com/sairaph/creality-slicer-mcp/internal/motext"
	"github.com/sairaph/creality-slicer-mcp/internal/profiles"
	"github.com/sairaph/creality-slicer-mcp/internal/slicer"
)

// k2Printer is the printer preset the profile and drift checks look at: the
// K2 family's default and the one every K2 Combo user has.
const k2Printer = "Creality K2 0.4 nozzle"

// catalogVersion is the settings catalog the server ships.
const catalogVersion = "7.2.1"

// maxDriftKeys is how many drifting keys the drift check lists.
const maxDriftKeys = 10

// Profiles is the part of profiles.Store the checks use.
type Profiles interface {
	List(t profiles.Type, f profiles.Filter) ([]profiles.Descriptor, error)
	Get(t profiles.Type, name string) (profiles.Preset, error)
	// KeysSetBy returns the setting keys set by the presets of a printer model.
	KeysSetBy(printerModel string) ([]string, error)
}

// Texts is what motext.Index offers: the wording of the installed app.
type Texts interface {
	catalog.TextSource
	Len() int
	Langs() []string
}

// Deps are the things the Creality Print checks read. Each is a seam a test
// replaces, so no test touches the real install; NewDeps gives the real ones.
type Deps struct {
	// Settings reads the environment settings (domain.SettingsFromEnv).
	Settings func() (domain.Settings, error)
	// Detect finds Creality Print; cmd is CREALITY_SLICER_MCP_CMD or "".
	Detect func(ctx context.Context, cmd string) (slicer.Install, error)
	// ReadDir lists a folder (os.ReadDir).
	ReadDir func(dir string) ([]os.DirEntry, error)
	// OpenProfiles reads the preset bundles.
	OpenProfiles func(r profiles.Roots) (Profiles, error)
	// LoadCatalog loads the embedded settings catalog of the installed version
	// family; a version without a catalog of its own gets the default one.
	LoadCatalog func(version string) (*catalog.Catalog, error)
	// LoadTexts loads the app's message catalogs from a resources\i18n folder.
	LoadTexts func(dir string) (Texts, error)
}

// NewDeps returns the real dependencies.
func NewDeps() Deps {
	return Deps{
		Settings: domain.SettingsFromEnv,
		Detect: func(ctx context.Context, cmd string) (slicer.Install, error) {
			return slicer.Detect(ctx, slicer.Options{Cmd: cmd})
		},
		ReadDir: os.ReadDir,
		OpenProfiles: func(r profiles.Roots) (Profiles, error) {
			return profiles.Open(r)
		},
		LoadCatalog: func(version string) (*catalog.Catalog, error) {
			if c, err := catalog.Load(version); err == nil {
				return c, nil
			}
			return catalog.Load(catalogVersion)
		},
		LoadTexts: func(dir string) (Texts, error) {
			return motext.LoadDir(dir)
		},
	}
}

// creality holds what the checks share: Creality Print is detected once per
// doctor run, however many checks ask.
type creality struct {
	deps Deps
	once sync.Once
	in   slicer.Install
	err  error
}

func (c *creality) install(ctx context.Context) (slicer.Install, error) {
	c.once.Do(func() {
		var cmd string
		if s, err := c.deps.Settings(); err == nil {
			cmd = s.Cmd // a bad value is SettingsCheck's failure, not this one's
		}
		c.in, c.err = c.deps.Detect(ctx, cmd)
	})
	return c.in, c.err
}

// usable returns the install when it was found, or the Warn result saying why
// a check cannot run.
func (c *creality) usable(ctx context.Context, name string) (slicer.Install, *doctor.Result) {
	in, err := c.install(ctx)
	switch {
	case err != nil:
		return in, &doctor.Result{Name: name, Status: doctor.Warn, Detail: "not checked: " + err.Error()}
	case !in.Found:
		return in, &doctor.Result{Name: name, Status: doctor.Warn, Detail: "not checked: Creality Print was not found"}
	}
	return in, nil
}

func (c *creality) checks() []doctor.Check {
	return []doctor.Check{
		printCheck{c}, appDataCheck{c}, profilesCheck{c}, tooltipCheck{c}, driftCheck{c},
	}
}

// printCheck reports the detected Creality Print.
type printCheck struct{ c *creality }

func (printCheck) Name() string { return "Creality Print" }

func (k printCheck) Run(ctx context.Context) doctor.Result {
	in, err := k.c.install(ctx)
	if err != nil {
		return doctor.Result{Name: k.Name(), Status: doctor.Warn, Detail: "detection was interrupted: " + err.Error()}
	}
	if !in.Found {
		detail := in.Reason
		if detail == "" {
			detail = "Creality Print was not found"
		}
		return doctor.Result{Name: k.Name(), Status: doctor.Warn,
			Detail: detail + "; slicing disabled (set " + domain.EnvCmd + " if it is installed elsewhere)"}
	}
	desc := fmt.Sprintf("%s (build %s) at %s", in.Version, in.Build, in.Exe)
	if s, err := k.c.deps.Settings(); err == nil && s.Cmd != "" {
		desc += " (from " + domain.EnvCmd + ")"
	}
	if !in.Supported {
		reason := in.Reason
		if reason == "" {
			reason = "version " + in.Version + " is not supported"
		}
		return doctor.Result{Name: k.Name(), Status: doctor.Warn, Detail: desc + "\n" + reason + "; slicing disabled (settings and guides still work)"}
	}
	detail := desc + ", dialect " + in.Dialect
	if in.GUIRunning {
		detail += "; the Creality Print window is open, which does not affect slicing here"
	}
	return doctor.Result{Name: k.Name(), Status: doctor.OK, Detail: detail}
}

// appDataCheck reports whether Creality Print's own data folder can be read.
type appDataCheck struct{ c *creality }

func (appDataCheck) Name() string { return "Creality Print data" }

func (k appDataCheck) Run(ctx context.Context) doctor.Result {
	in, skip := k.c.usable(ctx, k.Name())
	if skip != nil {
		return *skip
	}
	if in.DataDir == "" {
		return doctor.Result{Name: k.Name(), Status: doctor.Warn,
			Detail: "the app's data folder does not exist yet; start Creality Print once so it creates it (the installed profiles are used meanwhile)"}
	}
	entries, err := k.c.deps.ReadDir(in.DataDir)
	if err != nil {
		return doctor.Result{Name: k.Name(), Status: doctor.Warn, Detail: fmt.Sprintf("%s cannot be read: %v", in.DataDir, err)}
	}
	return doctor.Result{Name: k.Name(), Status: doctor.OK, Detail: fmt.Sprintf("%s is readable (%d entries)", in.DataDir, len(entries))}
}

// openProfiles opens the bundles of an install.
func (c *creality) openProfiles(in slicer.Install) (Profiles, error) {
	return c.deps.OpenProfiles(profiles.Roots{
		InstallProfiles: filepath.Join(in.Dir, "resources", "profiles"),
		DataDir:         in.DataDir,
		Bundle:          in.ProfileRoot,
	})
}

// profilesCheck counts the K2 presets the installed bundle offers.
type profilesCheck struct{ c *creality }

func (profilesCheck) Name() string { return "Profiles" }

func (k profilesCheck) Run(ctx context.Context) doctor.Result {
	in, skip := k.c.usable(ctx, k.Name())
	if skip != nil {
		return *skip
	}
	store, err := k.c.openProfiles(in)
	if err != nil {
		return doctor.Result{Name: k.Name(), Status: doctor.Warn, Detail: "the preset bundle cannot be read: " + err.Error() + "; presets and slicing settings are unavailable"}
	}
	filter := profiles.Filter{Printer: k2Printer}
	procs, perr := store.List(profiles.TypeProcess, filter)
	fils, ferr := store.List(profiles.TypeFilament, filter)
	if perr != nil || ferr != nil {
		return doctor.Result{Name: k.Name(), Status: doctor.Warn, Detail: fmt.Sprintf("listing presets failed: %v", errors2(perr, ferr))}
	}
	detail := fmt.Sprintf("%d process and %d filament presets for %s (bundle version %s)", len(procs), len(fils), k2Printer, in.ProfileVersion)
	if len(procs) == 0 || len(fils) == 0 {
		return doctor.Result{Name: k.Name(), Status: doctor.Warn, Detail: detail + "; expected some of each"}
	}
	return doctor.Result{Name: k.Name(), Status: doctor.OK, Detail: detail}
}

func errors2(a, b error) error {
	if a != nil {
		return a
	}
	return b
}

// tooltipCheck reports how many setting descriptions the app's message
// catalogs supply.
type tooltipCheck struct{ c *creality }

func (tooltipCheck) Name() string { return "Setting descriptions" }

func (k tooltipCheck) Run(ctx context.Context) doctor.Result {
	in, skip := k.c.usable(ctx, k.Name())
	if skip != nil {
		return *skip
	}
	cat, err := k.c.deps.LoadCatalog(in.Version)
	if err != nil {
		return doctor.Result{Name: k.Name(), Status: doctor.Fail, Detail: "the embedded settings catalog cannot be loaded: " + err.Error()}
	}
	dir := filepath.Join(in.Dir, "resources", "i18n")
	texts, err := k.c.deps.LoadTexts(dir)
	if err != nil {
		return doctor.Result{Name: k.Name(), Status: doctor.Warn,
			Detail: "no message catalogs in " + dir + ": " + err.Error() + "; describe_setting will say a setting has no description"}
	}
	st := cat.WithTexts(texts).Stats()
	detail := fmt.Sprintf("%d of %d settings have a description (%.0f%%), from %d languages", st.TooltipsFound, st.TooltipHashes, st.Coverage()*100, len(texts.Langs()))
	if st.TooltipHashes == 0 || st.TooltipsFound == 0 {
		return doctor.Result{Name: k.Name(), Status: doctor.Warn, Detail: detail + "; describe_setting will say a setting has no description"}
	}
	return doctor.Result{Name: k.Name(), Status: doctor.OK, Detail: detail}
}

// k2Model is the printer model whose presets the drift check reads.
const k2Model = "Creality K2"

// driftCheck finds keys the installed K2 presets set that the shipped catalog
// does not know: the app moved on since the catalog was made.
type driftCheck struct{ c *creality }

func (driftCheck) Name() string { return "Settings catalog" }

func (k driftCheck) Run(ctx context.Context) doctor.Result {
	in, skip := k.c.usable(ctx, k.Name())
	if skip != nil {
		return *skip
	}
	cat, err := k.c.deps.LoadCatalog(in.Version)
	if err != nil {
		return doctor.Result{Name: k.Name(), Status: doctor.Fail, Detail: "the embedded settings catalog cannot be loaded: " + err.Error()}
	}
	store, err := k.c.openProfiles(in)
	if err != nil {
		return doctor.Result{Name: k.Name(), Status: doctor.Warn, Detail: "not checked: the preset bundle cannot be read: " + err.Error()}
	}
	keys, err := store.KeysSetBy(k2Model)
	if err != nil {
		return doctor.Result{Name: k.Name(), Status: doctor.Warn, Detail: "not checked: " + err.Error()}
	}
	if len(keys) == 0 {
		return doctor.Result{Name: k.Name(), Status: doctor.Warn, Detail: "not checked: no " + k2Model + " presets were found"}
	}
	unknown := cat.UnknownKeys(keys)
	if len(unknown) == 0 {
		return doctor.Result{Name: k.Name(), Status: doctor.OK,
			Detail: fmt.Sprintf("catalog %s knows all %d settings the installed %s presets set", cat.Version, len(keys), k2Model)}
	}
	shown := unknown[:min(len(unknown), maxDriftKeys)]
	detail := fmt.Sprintf("the installed app (%s) sets %d setting(s) that catalog %s does not know: %s",
		in.Version, len(unknown), cat.Version, strings.Join(shown, ", "))
	if len(unknown) > len(shown) {
		detail += fmt.Sprintf(" and %d more", len(unknown)-len(shown))
	}
	return doctor.Result{Name: k.Name(), Status: doctor.Warn, Detail: detail + "; they are passed through untouched"}
}
