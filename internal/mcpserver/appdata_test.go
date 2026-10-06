package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/sairaph/creality-slicer-mcp/internal/projects"
	"github.com/sairaph/creality-slicer-mcp/internal/slicer"
)

func asProjectsError(t *testing.T, err error) *projects.Error {
	t.Helper()
	var pe *projects.Error
	if !errors.As(err, &pe) {
		t.Fatalf("error = %v (%T), want a *projects.Error", err, err)
	}
	return pe
}

// StatusInfo is what get_slicer_status is made of: the same facts.
func TestStatusInfoIsWhatTheToolReports(t *testing.T) {
	pf := newProjFixture(t)
	ctx := context.Background()
	info, err := pf.srv.StatusInfo(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	front := frontOf(t, pf.ok(t, "get_slicer_status", nil))
	in := info.Install
	if !in.Found || !in.Supported || in.Version != "7.2.2" || front["version"] != in.Version || front["build"] != in.Build {
		t.Errorf("install = %+v, front = %v", in, front)
	}
	if info.ProfileSource != "install" && info.ProfileSource != "data_dir" || front["profile_source"] != info.ProfileSource {
		t.Errorf("profile source %q, front %v", info.ProfileSource, front["profile_source"])
	}
	if info.ProjectsDir == "" || front["projects_dir"] != info.ProjectsDir {
		t.Errorf("projects dir %q, front %v", info.ProjectsDir, front["projects_dir"])
	}
	if info.CatalogVersion == "" || front["catalog_version"] != info.CatalogVersion {
		t.Errorf("catalog %q, front %v", info.CatalogVersion, front["catalog_version"])
	}
	if info.TooltipCoverage == "" || front["tooltip_coverage"] != info.TooltipCoverage || info.DescriptionsNote != "" {
		t.Errorf("descriptions %q (note %q), front %v", info.TooltipCoverage, info.DescriptionsNote, front["tooltip_coverage"])
	}
	drift, has := front["catalog_drift"]
	if has != info.DriftKnown || (has && fmt.Sprint(drift) != fmt.Sprint(len(info.Drift))) {
		t.Errorf("drift %v (known %v), front %v", info.Drift, info.DriftKnown, drift)
	}
}

func TestStatusInfoRefreshDetectsAgain(t *testing.T) {
	pf := newProjFixture(t)
	before := pf.install.refreshes
	if _, err := pf.srv.StatusInfo(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if pf.install.refreshes != before+1 {
		t.Errorf("refreshes: %d -> %d", before, pf.install.refreshes)
	}
}

func TestStatusInfoWithoutCrealityPrint(t *testing.T) {
	f := newFixture(t, func(fi *fakeInstall, d *Deps) { fi.install = slicer.Install{Reason: "Creality Print is not installed"} })
	srv := newServer(f.cfg)
	info, err := srv.StatusInfo(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if info.Install.Found || info.Install.Reason != "Creality Print is not installed" || info.ProfileSource != "" || info.DriftKnown || len(info.Drift) != 0 {
		t.Errorf("info = %+v", info)
	}
	if info.ProjectsDir == "" || info.CatalogVersion == "" {
		t.Errorf("the folder and the catalog do not depend on the install: %+v", info)
	}
}

func TestProjectsAccessorReturnsTheStore(t *testing.T) {
	pf := newProjFixture(t)
	ctx := context.Background()
	store, err := pf.srv.Projects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if items, err := store.List(); err != nil || len(items) != 0 {
		t.Fatalf("a new store: %v, %v", items, err)
	}
	id := pf.create(t, "From the app")
	if items, err := store.List(); err != nil || len(items) != 1 || items[0].ID != id {
		t.Errorf("after create_project: %v, %v", items, err)
	}
	// The tool and the accessor see the same projects.
	if got := pf.ok(t, "list_projects", nil); !strings.Contains(got, id) {
		t.Errorf("list_projects = %s", got)
	}
}

func TestProjectsAccessorSaysWhyItCannotBeUsed(t *testing.T) {
	f := newFixture(t, func(fi *fakeInstall, d *Deps) { fi.install = slicer.Install{Reason: "Creality Print is not installed"} })
	srv := newServer(f.cfg)
	store, err := srv.Projects(context.Background())
	if store != nil || err == nil {
		t.Fatalf("store %v, error %v", store, err)
	}
	pe := asProjectsError(t, err)
	if pe.Code != "unavailable" || !strings.Contains(pe.Message, "Creality Print is not installed") || pe.Hint == "" {
		t.Errorf("error = %+v", pe)
	}
}

// LaunchView is what open_in_app does.
func TestLaunchViewStartsTheAppOnACopy(t *testing.T) {
	pf := newProjFixture(t)
	ctx := context.Background()
	id := pf.withModel(t, "Look")

	r, err := pf.srv.LaunchView(ctx, id, 0, "project")
	if err != nil {
		t.Fatal(err)
	}
	if r.Mode != "project" || r.Plate != 1 || r.PID != 4201 || r.Version != "7.2.2" || r.OtherWindow || !strings.HasSuffix(r.File, ".3mf") {
		t.Errorf("result = %+v", r)
	}
	if pf.launcher.calls() != 1 || pf.launcher.files[0] != r.File {
		t.Errorf("launches: %d, files %v", pf.launcher.calls(), pf.launcher.files)
	}

	pf.install.install.GUIRunning = true
	if r, err = pf.srv.LaunchView(ctx, id, 1, "project"); err != nil || !r.OtherWindow {
		t.Errorf("a window was open: %+v, %v", r, err)
	}
}

func TestLaunchViewErrorsKeepTheirCodeAndHint(t *testing.T) {
	pf := newProjFixture(t)
	ctx := context.Background()
	id := pf.withModel(t, "Look")

	// No slice: a conflict that says to slice first, and nothing is started.
	_, err := pf.srv.LaunchView(ctx, id, 1, "")
	pe := asProjectsError(t, err)
	if pe.Code != "conflict" || !strings.Contains(pe.Hint, "slice_project") {
		t.Errorf("unsliced preview: %+v", pe)
	}
	_, err = pf.srv.LaunchView(ctx, "nope", 1, "project")
	if pe := asProjectsError(t, err); pe.Code != "not_found" {
		t.Errorf("unknown project: %+v", pe)
	}
	if pf.launcher.calls() != 0 {
		t.Errorf("the app was started: %d", pf.launcher.calls())
	}

	pf.launcher.err = errors.New("the file is not an executable")
	_, err = pf.srv.LaunchView(ctx, id, 1, "project")
	pe = asProjectsError(t, err)
	if pe.Code != "unavailable" || !strings.Contains(pe.Message, "Could not start Creality Print") || !strings.Contains(pe.Hint, "starts from its own shortcut") {
		t.Errorf("failed launch: %+v", pe)
	}
}

func TestLaunchViewNeedsASupportedInstall(t *testing.T) {
	fl := &fakeLauncher{}
	f := newFixture(t, func(fi *fakeInstall, d *Deps) {
		fi.install = slicer.Install{Reason: "Creality Print is not installed"}
		d.Launcher = fl
	})
	srv := newServer(f.cfg)
	_, err := srv.LaunchView(context.Background(), "x", 1, "")
	pe := asProjectsError(t, err)
	if pe.Code != "unavailable" || !strings.Contains(pe.Message, "Creality Print is not installed") {
		t.Errorf("error = %+v", pe)
	}
	if fl.calls() != 0 {
		t.Error("the app was started without an install")
	}
}

// The error of the tool and the error of the accessor are the same error.
func TestLaunchViewAndOpenInAppFailTheSameWay(t *testing.T) {
	pf := newProjFixture(t)
	id := pf.withModel(t, "Look")
	_, err := pf.srv.LaunchView(context.Background(), id, 1, "")
	pe := asProjectsError(t, err)
	tool := pf.errText(t, "open_in_app", map[string]any{"project": id})
	for _, part := range []string{pe.Code, pe.Message, pe.Hint} {
		if !strings.Contains(tool, part) {
			t.Errorf("the tool error lacks %q:\n%s", part, tool)
		}
	}
}
