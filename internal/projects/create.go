package projects

import (
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/sairaph/creality-slicer-mcp/internal/profiles"
	"github.com/sairaph/creality-slicer-mcp/internal/threemf"
)

// DefaultPrinter is the printer of a project that names none.
const DefaultPrinter = "Creality K2 0.4 nozzle"

var colourRE = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

// FilamentSpec is one filament slot of a request: a filament preset and the
// colour the model parts printed with it should be shown in.
type FilamentSpec struct {
	Preset string
	Colour string // #RRGGBB
}

// CreateRequest describes a new project.
type CreateRequest struct {
	Name    string
	Printer string // default DefaultPrinter
	Process string // default: the printer's default process
	// Filaments are the slots in order; at least one, each with a colour.
	Filaments []FilamentSpec
	BedType   string // curr_bed_type (a plate type name); default from the catalog
	// Spools makes the filaments from CFS spools instead (exclusive with Filaments).
	Spools []SpoolSpec
}

// alternatives names up to n compatible presets of a type for a printer, for
// the hint of an error.
func (s *Store) alternatives(t profiles.Type, printer string, n int) string {
	ds, err := s.cfg.Profiles.List(t, profiles.Filter{Printer: printer, Source: "system"})
	if err != nil || len(ds) == 0 {
		return ""
	}
	var names []string
	for _, d := range ds {
		names = append(names, d.Name)
		if len(names) == n {
			break
		}
	}
	return "compatible " + string(t) + " presets include: " + strings.Join(names, "; ") + " (list_presets shows all)"
}

func (s *Store) getPreset(t profiles.Type, name, printerForHint string) (profiles.Preset, error) {
	p, err := s.cfg.Profiles.Get(t, name)
	if err == nil {
		return p, nil
	}
	hint := "call list_presets to see the presets"
	if printerForHint != "" {
		if alt := s.alternatives(t, printerForHint, 8); alt != "" {
			hint = alt
		}
	}
	return p, invalidf(hint, "the %s preset %q was not found", t, name)
}

// checkCompatible refuses a process or filament that does not fit the printer.
func (s *Store) checkCompatible(printer, process profiles.Preset, filaments []profiles.Preset) error {
	if ok, _ := profiles.Compatible(printer, process); !ok {
		return invalidf(s.alternatives(profiles.TypeProcess, printer.Name, 8),
			"the process preset %q is not compatible with the printer %q", process.Name, printer.Name)
	}
	for _, f := range filaments {
		if ok, _ := profiles.Compatible(printer, f); !ok {
			return invalidf(s.alternatives(profiles.TypeFilament, printer.Name, 8),
				"the filament preset %q is not compatible with the printer %q", f.Name, printer.Name)
		}
		if ok, _ := profiles.CompatiblePrint(process, f); !ok {
			return invalidf(s.alternatives(profiles.TypeFilament, printer.Name, 8),
				"the filament preset %q does not go with the process preset %q", f.Name, process.Name)
		}
	}
	return nil
}

// resolveFilaments loads the filament presets and normalises the colours.
func (s *Store) resolveFilaments(printer string, specs []FilamentSpec) ([]profiles.Preset, []string, error) {
	if len(specs) == 0 {
		return nil, nil, invalidf("give at least one filament: {preset, colour}", "a project needs at least one filament")
	}
	var presets []profiles.Preset
	var colours []string
	for i, sp := range specs {
		if strings.TrimSpace(sp.Preset) == "" {
			return nil, nil, invalidf("", "filament %d has no preset", i+1)
		}
		if !colourRE.MatchString(sp.Colour) {
			return nil, nil, invalidf("write the colour as #RRGGBB, for example #FFFFFF", "filament %d (%s) needs a colour, got %q", i+1, sp.Preset, sp.Colour)
		}
		f, err := s.getPreset(profiles.TypeFilament, sp.Preset, printer)
		if err != nil {
			return nil, nil, err
		}
		presets = append(presets, f)
		colours = append(colours, strings.ToUpper(sp.Colour))
	}
	return presets, colours, nil
}

// checkBedType validates a plate type name against the catalog.
func (s *Store) checkBedType(name string) error {
	if name == "" {
		return nil
	}
	o, ok := s.cfg.Catalog.Get("curr_bed_type")
	if !ok || o.Enum == nil {
		return nil
	}
	for _, v := range o.Enum.Values {
		if v == name {
			return nil
		}
	}
	return invalidf("valid bed types: "+strings.Join(o.Enum.Values, ", "), "%q is not a bed type", name)
}

// CreateProject makes a new project: one plate, no objects, the settings
// composed from the presets.
func (s *Store) CreateProject(req CreateRequest) (*Info, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, invalidf("give the project a name", "a project needs a name")
	}
	printerName := req.Printer
	if printerName == "" {
		printerName = DefaultPrinter
	}
	printer, err := s.getPreset(profiles.TypePrinter, printerName, "")
	if err != nil {
		if alt, lerr := s.cfg.Profiles.List(profiles.TypePrinter, profiles.Filter{PrinterModel: K2Model, Source: "system"}); lerr == nil && len(alt) > 0 {
			var names []string
			for _, d := range alt {
				names = append(names, d.Name)
			}
			return nil, invalidf("K2 printers: "+strings.Join(names, "; "), "the printer preset %q was not found", printerName)
		}
		return nil, err
	}
	processName := req.Process
	if processName == "" {
		processName = printer.String("default_print_profile")
	}
	process, err := s.getPreset(profiles.TypeProcess, processName, printer.Name)
	if err != nil {
		return nil, err
	}
	if len(req.Spools) > 0 && len(req.Filaments) > 0 {
		return nil, invalidf("give spools or filaments, not both", "spools and filaments contradict each other")
	}
	var links []SpoolLink
	if len(req.Spools) > 0 {
		if req.Filaments, links, err = s.resolveSpools(printer.Name, req.Spools); err != nil {
			return nil, err
		}
	}
	filaments, colours, err := s.resolveFilaments(printer.Name, req.Filaments)
	if err != nil {
		return nil, err
	}
	if err := s.checkCompatible(printer, process, filaments); err != nil {
		return nil, err
	}
	if err := s.checkBedType(req.BedType); err != nil {
		return nil, err
	}

	in := composeInput{Cat: s.cfg.Catalog, Printer: printer, Process: process, Filaments: filaments, Colours: colours,
		Version: s.version(), BedType: req.BedType, Plates: 1}
	c, err := compose(in)
	if err != nil {
		return nil, errf(CodeInternal, "", "composing the project settings failed: %v", err)
	}

	id, err := s.makeProjectDir(name)
	if err != nil {
		return nil, err
	}
	dir := s.dir(id)
	fail := func(err error) (*Info, error) {
		_ = removeAll(dir)
		return nil, err
	}
	p := threemf.New(threemf.NewOptions{AppVersion: s.version(), Now: s.now})
	p.SetSettings(c.Cfg)
	mode := threemf.ModeSingleExtruder
	if len(filaments) > 1 {
		mode = threemf.ModeMultiExtruder
	}
	if err := p.SetCustomGCodes(1, mode, nil); err != nil {
		return fail(errf(CodeInternal, "", "%v", err))
	}
	m := &meta{Name: name, Created: s.now(), Updated: s.now(), Revision: 0, Spools: links}
	// The first save goes through a handle so the thumbnails and the revision
	// follow the same path as every later change.
	if err := p.Save(filepath.Join(dir, projectFile)); err != nil {
		p.Close()
		return fail(errf(CodeInternal, "", "saving the new project failed: %v", err))
	}
	p.Close()
	if err := s.writeMeta(id, m); err != nil {
		return fail(errf(CodeInternal, "", "%v", err))
	}
	// The first change renders the thumbnails and takes the revision to 1.
	if err := s.write(id, func(h *handle) error { h.touchAll(); return nil }); err != nil {
		return fail(err)
	}
	var info *Info
	err = s.read(id, func(h *handle) error {
		var ierr error
		info, ierr = h.info()
		return ierr
	})
	if err != nil {
		return fail(err)
	}
	return info, nil
}

// OpenRequest imports an existing project file.
type OpenRequest struct {
	Path string // .3mf written by Creality Print (or Bambu Studio / Orca)
	Name string // optional; default the project title or the file name
	// Into is an existing project id: its content is replaced by the file (a
	// project the app saved), keeping id, name and folder. Name is ignored then.
	Into string
}

// OpenResult reports what was imported.
type OpenResult struct {
	Info *Info
	// SourceAppVersion is the application version the file was written by.
	SourceAppVersion string
}

// OpenProject copies a 3MF into the store (the source is never touched) and
// reports it. A file no slicer wrote (no model_settings.config) is refused.
func (s *Store) OpenProject(req OpenRequest) (*OpenResult, error) {
	path := req.Path
	if !filepath.IsAbs(path) {
		return nil, invalidf("give an absolute path", "%q is not an absolute path", path)
	}
	if !strings.EqualFold(filepath.Ext(path), ".3mf") {
		return nil, invalidf("open_project reads .3mf project files", "%q is not a .3mf file", path)
	}
	src, err := os.Open(path)
	if err != nil {
		return nil, invalidf("check the path", "cannot read %q: %v", path, err)
	}
	defer src.Close()

	name := strings.TrimSpace(req.Name)
	tmpID, err := s.makeProjectDir("opening")
	if err != nil {
		return nil, err
	}
	dir := s.dir(tmpID)
	fail := func(err error) (*OpenResult, error) {
		_ = removeAll(dir)
		return nil, err
	}
	dst, err := os.Create(filepath.Join(dir, projectFile))
	if err != nil {
		return fail(errf(CodeInternal, "", "%v", err))
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		return fail(errf(CodeInternal, "", "copying %q failed: %v", path, err))
	}
	if err := dst.Close(); err != nil {
		return fail(errf(CodeInternal, "", "%v", err))
	}
	p, err := threemf.Open(filepath.Join(dir, projectFile))
	if err != nil {
		return fail(invalidf("open_project reads Creality Print, Bambu Studio and Orca project files", "%q is not a readable 3MF project: %v", path, err))
	}
	isSlicer := p.IsSlicerProject
	nObjects, nPlates, printerName := len(p.Objects), len(p.Plates), ""
	if p.Settings != nil {
		printerName = p.Settings.String("printer_settings_id")
	}
	title := strings.TrimSpace(p.Metadata.Value("Title"))
	p.Close()
	if !isSlicer {
		return fail(invalidf("use create_project and add_model to import the meshes of this file into a new project",
			"%q is a plain 3MF (no slicer settings), not a slicer project", path))
	}
	if req.Into != "" {
		defer func() { _ = removeAll(dir) }()
		return s.openInto(req.Into, path, filepath.Join(dir, projectFile))
	}
	if name == "" {
		name = title
	}
	if name == "" {
		name = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	id := newID(name)
	for try := 0; try < 8; try++ {
		if _, statErr := os.Stat(s.dir(id)); os.IsNotExist(statErr) {
			break
		}
		id = newID(name) // never place a project over another one
	}
	if err := os.Rename(dir, s.dir(id)); err != nil {
		return fail(errf(CodeInternal, "", "could not place the project: %v", err))
	}
	dir = s.dir(id)
	m := &meta{Name: name, Created: s.now(), Updated: s.now(), Revision: 1, SourcePath: path, Objects: nObjects, Plates: nPlates, Printer: printerName}
	if err := s.writeMeta(id, m); err != nil {
		return fail(errf(CodeInternal, "", "%v", err))
	}
	res := &OpenResult{}
	err = s.read(id, func(h *handle) error {
		var ierr error
		res.SourceAppVersion = h.appVersion()
		res.Info, ierr = h.info()
		return ierr
	})
	if err != nil {
		return fail(err)
	}
	return res, nil
}

// makeProjectDir creates the folder of a new project (with its out folder)
// under a fresh id. A folder that exists already is never reused: a new id is
// drawn instead. Half made "opening" folders of an interrupted open_project
// are swept once per process.
func (s *Store) makeProjectDir(name string) (string, error) {
	if err := os.MkdirAll(s.cfg.Root, 0o755); err != nil {
		return "", errf(CodeInternal, "check that the projects folder is writable", "could not create %s: %v", s.cfg.Root, err)
	}
	s.sweepOnce.Do(s.sweepOpening)
	for try := 0; try < 16; try++ {
		id := newID(name)
		err := os.Mkdir(s.dir(id), 0o755)
		if os.IsExist(err) {
			continue
		}
		if err == nil {
			err = os.Mkdir(filepath.Join(s.dir(id), outDirName), 0o755)
		}
		if err != nil {
			return "", errf(CodeInternal, "check that the projects folder is writable", "could not create %s: %v", s.dir(id), err)
		}
		return id, nil
	}
	return "", errf(CodeInternal, "", "could not find a free project id")
}

// sweepOpening removes the "opening-xxxxxx" folders an interrupted
// open_project left behind (they have no job.json and are older than an hour).
func (s *Store) sweepOpening() {
	entries, err := os.ReadDir(s.cfg.Root)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "opening-") {
			continue
		}
		if _, err := os.Stat(filepath.Join(s.dir(e.Name()), metaFile)); err == nil {
			continue
		}
		if info, err := e.Info(); err == nil && s.now().Sub(info.ModTime()) > time.Hour {
			_ = removeAll(s.dir(e.Name()))
		}
	}
}

// openInto replaces the content of an existing project with a validated 3MF
// (tmpFile, a copy of the source path): the id, name, folder and job.json data
// stay, the revision goes up by one and every earlier slice result is dropped.
func (s *Store) openInto(ref, source, tmpFile string) (*OpenResult, error) {
	id, err := s.resolve(ref)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	job := s.running[id]
	s.mu.Unlock()
	if job != "" {
		return nil, conflictf("cancel it with get_slice_status or wait", "project %s is being sliced (job %s)", id, job)
	}
	unlock, err := s.lock(id)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if _, err := s.readMeta(id); err != nil {
		return nil, notFoundf("call list_projects to see the projects", "project %s cannot be read: %v", id, err)
	}
	dst := filepath.Join(s.dir(id), projectFile)
	old := dst + ".old"
	// The file and job.json change together: the old file is kept aside until the
	// metadata is written, and put back when anything fails.
	retry := func(from, to string) (err error) {
		for attempt := 0; attempt < 30; attempt++ {
			if err = os.Rename(from, to); err == nil {
				return nil
			}
			time.Sleep(100 * time.Millisecond) // a virus scanner can hold the file for a moment
		}
		return err
	}
	_ = os.Remove(old)
	if err := retry(dst, old); err != nil {
		return nil, errf(CodeInternal, "the project file may be in use by another reader; try again in a moment", "replacing the project failed: %v", err)
	}
	done := false
	defer func() {
		if done {
			_ = os.Remove(old)
			return
		}
		_ = os.Remove(dst)
		_ = retry(old, dst)
	}()
	if err := retry(tmpFile, dst); err != nil {
		return nil, errf(CodeInternal, "the project file may be in use by another reader; try again in a moment", "replacing the project failed: %v", err)
	}
	h, err := s.openLocked(id)
	if err != nil {
		return nil, err
	}
	defer h.close()
	h.meta.Revision++
	h.meta.Updated = s.now()
	h.meta.SourcePath = source
	h.meta.LastSlice = nil
	h.meta.PlateRev = nil
	h.allPlates = true
	// A spool link stays while its filament keeps the same preset.
	var names []string
	if h.p.Settings != nil {
		names = h.p.Settings.List("filament_settings_id")
	}
	var links []SpoolLink
	for i, l := range h.meta.Spools {
		if i < len(names) && names[i] == l.Preset {
			links = append(links, l)
		} else {
			break // links are by position: keep only the unbroken start
		}
	}
	h.meta.Spools = links
	h.recordSaved()
	res := &OpenResult{SourceAppVersion: h.appVersion()}
	if res.Info, err = h.info(); err != nil {
		return nil, err
	}
	if err := s.writeMeta(id, h.meta); err != nil {
		return nil, errf(CodeInternal, "", "saving the project metadata failed: %v", err)
	}
	done = true
	return res, nil
}
