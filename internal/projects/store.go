// Package projects is the layer the MCP tools call: a store of projects, each a
// folder holding a Creality Print project (project.3mf, the source of truth)
// and a little metadata (job.json), with operations that build and change
// projects (presets, models, settings, plates, height ranges, layer actions),
// render their thumbnails, and slice them with the installed Creality Print.
//
// Every operation takes a project reference (id, else a unique name), locks
// the project (an in-process mutex and a lock file, so two MCP processes never
// write one project at once), works on the parsed project and saves it
// atomically. The revision of a project rises by one per successful change;
// a slice records the revision it was made from.
package projects

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gofrs/flock"

	"github.com/sairaph/creality-slicer-mcp/internal/catalog"
	"github.com/sairaph/creality-slicer-mcp/internal/domain"
	"github.com/sairaph/creality-slicer-mcp/internal/profiles"
	"github.com/sairaph/creality-slicer-mcp/internal/slicer"
	"github.com/sairaph/creality-slicer-mcp/internal/threemf"
)

// File names inside a project folder.
const (
	projectFile = "project.3mf"
	metaFile    = "job.json"
	lockFile    = ".lock"
	outDirName  = "out"
)

// lockTimeout bounds the wait for another process that holds a project (an
// anti-hang guard).
var lockTimeout = 60 * time.Second

// Config wires a Store to its dependencies.
type Config struct {
	// Root is the store folder (domain.ProjectsDir()); created on first use.
	Root string
	// Install is the detected Creality Print; version and build go into new
	// projects, and slicing needs Install.Supported.
	Install slicer.Install
	// Runner and Jobs run slices. Either may be nil when Creality Print is not
	// usable: everything except slicing still works.
	Runner *slicer.Runner
	Jobs   *slicer.Jobs
	// Profiles reads the presets, Catalog validates settings.
	Profiles *profiles.Store
	Catalog  *catalog.Catalog
	// Now is the clock (default time.Now); tests set it.
	Now func() time.Time
}

// Store is the collection of projects.
type Store struct {
	cfg Config

	mu        sync.Mutex
	locks     map[string]*sync.RWMutex
	running   map[string]string // project id -> slice job id
	finished  map[string]*sliceEnd
	crashCtxs map[string]crashCtx
	sweepOnce sync.Once
}

// New returns a Store. It does not touch the disk.
func New(cfg Config) (*Store, error) {
	if cfg.Root == "" {
		return nil, fmt.Errorf("projects: no store folder")
	}
	if cfg.Profiles == nil || cfg.Catalog == nil {
		return nil, fmt.Errorf("projects: the presets and the setting catalog are required")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	s := &Store{cfg: cfg, locks: map[string]*sync.RWMutex{}, running: map[string]string{}}
	s.sweepTrash()
	return s, nil
}

// NewDefault is New with the store folder of the current user.
func NewDefault(cfg Config) (*Store, error) {
	root, err := domain.ProjectsDir()
	if err != nil {
		return nil, err
	}
	cfg.Root = root
	return New(cfg)
}

func (s *Store) now() time.Time { return s.cfg.Now() }

// version is the installed version and build, "7.3.0.6149", for the
// Application metadata and project_settings of new projects.
func (s *Store) version() string {
	v := s.cfg.Install.Version
	if v == "" {
		// No installed application (slicing is unavailable): the catalog names the
		// version the setting definitions were built from.
		v = s.cfg.Catalog.Stats().Version
	}
	if b := s.cfg.Install.Build; b != "" {
		v += "." + b
	}
	return v
}

// meta is job.json: metadata only, the project is project.3mf.
type meta struct {
	Name       string     `json:"name"`
	Created    time.Time  `json:"created"`
	Updated    time.Time  `json:"updated"`
	Revision   int        `json:"revision"`
	SourcePath string     `json:"source_path,omitempty"`
	LastSlice  *LastSlice `json:"last_slice,omitempty"`
	// PlateRev is, per plate, the revision at which the plate last changed: a
	// slice result of the plate from an earlier revision is stale.
	PlateRev map[int]int `json:"plate_rev,omitempty"`
	// Objects, Plates and Printer summarise the project for the list, so listing
	// never opens the 3MF. They are written by every save.
	Objects int    `json:"objects"`
	Plates  int    `json:"plates"`
	Printer string `json:"printer,omitempty"`
	// Spools ties filaments to the spools they were made from (create_project
	// and set_presets with spools), by position; see SpoolLink.
	Spools []SpoolLink `json:"spools,omitempty"`
	// Views records the files PrepareView wrote into the view folder (name, size,
	// sha256), so a file the user changed by saving into it is never overwritten
	// or deleted.
	Views []ViewRecord `json:"views,omitempty"`
}

func (s *Store) dir(id string) string { return filepath.Join(s.cfg.Root, id) }

func (s *Store) readMeta(id string) (*meta, error) {
	data, err := os.ReadFile(filepath.Join(s.dir(id), metaFile))
	if err != nil {
		return nil, err
	}
	var m meta
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("%s of project %s is damaged: %w", metaFile, id, err)
	}
	return &m, nil
}

func (s *Store) writeMeta(id string, m *meta) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return domain.WriteFileAtomic(filepath.Join(s.dir(id), metaFile), append(data, '\n'), 0o600)
}

var slugRE = regexp.MustCompile(`[^a-z0-9]+`)

// newID makes a folder name from the project name: a short slug and six hex
// digits.
func newID(name string) string {
	slug := strings.Trim(slugRE.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if len(slug) > 32 {
		slug = strings.Trim(slug[:32], "-")
	}
	if slug == "" {
		slug = "project"
	}
	b := make([]byte, 3)
	if _, err := rand.Read(b); err != nil {
		b = []byte{byte(time.Now().UnixNano()), 1, 2}
	}
	return slug + "-" + hex.EncodeToString(b)
}

// Ref is how a project is named by the tools: its id, else its unique name.

// resolve maps a project reference to its id.
func (s *Store) resolve(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", invalidf("call list_projects to see the projects", "no project given")
	}
	if strings.ContainsAny(ref, `/\`) || ref == "." || ref == ".." || strings.HasPrefix(ref, trashPrefix) {
		return "", invalidf("give the project id or name from list_projects", "%q is not a project id or name", ref)
	}
	if info, err := os.Stat(filepath.Join(s.dir(ref), metaFile)); err == nil && !info.IsDir() {
		return ref, nil
	}
	items, err := s.List()
	if err != nil {
		return "", err
	}
	var hits []string
	for _, it := range items {
		if strings.EqualFold(it.Name, ref) {
			hits = append(hits, it.ID)
		}
	}
	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		return "", notFoundf("call list_projects to see the projects", "no project with id or name %q", ref)
	}
	return "", invalidf("use the project id", "%d projects are named %q: %s", len(hits), ref, strings.Join(hits, ", "))
}

// projLock is the in-process lock of a project: writers hold it exclusively,
// readers share it, so a save never renames over a file a reader still has
// open (Windows refuses that).
func (s *Store) projLock(id string) *sync.RWMutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	mu := s.locks[id]
	if mu == nil {
		mu = &sync.RWMutex{}
		s.locks[id] = mu
	}
	return mu
}

// lock takes the in-process lock (exclusively) and the lock file of a project.
func (s *Store) lock(id string) (unlock func(), err error) {
	mu := s.projLock(id)
	mu.Lock()
	fl := flock.New(filepath.Join(s.dir(id), lockFile))
	ctx, cancel := context.WithTimeout(context.Background(), lockTimeout)
	defer cancel()
	ok, err := fl.TryLockContext(ctx, 25*time.Millisecond)
	if err != nil || !ok {
		mu.Unlock()
		if err == nil {
			err = ctx.Err()
		}
		return nil, conflictf("another process is changing the project; try again in a moment", "the project %s is locked: %v", id, err)
	}
	return func() {
		_ = fl.Unlock()
		mu.Unlock()
	}, nil
}

// ListItem is one row of the project list.
type ListItem struct {
	ID        string
	Name      string
	Printer   string
	Objects   int
	Plates    int
	Revision  int
	Created   time.Time
	Updated   time.Time
	LastSlice *SliceStamp
}

// SliceStamp says when a project was last sliced and from which revision.
type SliceStamp struct {
	Time     time.Time
	Revision int
	Plates   int
	TimeS    int
	TotalG   float64
	Stale    bool
	// StalePlates lists the sliced plates that changed after their slice.
	StalePlates []int
}

// List returns every project, most recently changed first (ties by id), so a
// page of the list is stable while nothing changes.
func (s *Store) List() ([]ListItem, error) {
	entries, err := os.ReadDir(s.cfg.Root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []ListItem
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), trashPrefix) {
			continue
		}
		m, err := s.readMeta(e.Name())
		if err != nil {
			continue
		}
		it := ListItem{ID: e.Name(), Name: m.Name, Revision: m.Revision, Created: m.Created, Updated: m.Updated, Objects: m.Objects, Plates: m.Plates, Printer: m.Printer}
		if m.LastSlice != nil {
			it.LastSlice = m.LastSlice.stamp(m)
		}
		if m.Printer != "" {
			out = append(out, it) // the summary is in job.json: no need to open the project
			continue
		}
		if p, err := threemf.Open(filepath.Join(s.dir(e.Name()), projectFile)); err == nil {
			it.Objects, it.Plates = len(p.Objects), len(p.Plates)
			if p.Settings != nil {
				it.Printer = p.Settings.String("printer_settings_id")
			}
			p.Close()
		}
		out = append(out, it)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Updated.Equal(out[j].Updated) {
			return out[i].Updated.After(out[j].Updated)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// handle is an open project during one operation.
type handle struct {
	s      *Store
	id     string
	dir    string
	meta   *meta
	p      *threemf.Project
	unlock func()

	changed      bool         // the project was modified and must be saved
	plates       map[int]bool // plates whose thumbnails must be rendered again
	allPlates    bool
	notes        []string // side effects to report
	meshes       map[int]*meshEntry
	takenHeights []float64 // heights of the rects the last occupied call returned
	// placeHeight is the height of the object findSpot is placing (0 when unknown):
	// it decides whether the plate counts as all short for the by-object clearance.
	placeHeight float64
}

func (h *handle) touchPlate(idx int) {
	if h.plates == nil {
		h.plates = map[int]bool{}
	}
	h.plates[idx] = true
	h.changed = true
}

func (h *handle) touchAll() { h.allPlates = true; h.changed = true }

// touchObject marks the plates that hold instances of an object.
func (h *handle) touchObject(objectID int) {
	h.changed = true
	found := false
	for _, pl := range h.p.Plates {
		for _, in := range pl.Instances {
			if in.ObjectID == objectID {
				h.touchPlate(pl.Index)
				found = true
			}
		}
	}
	if !found {
		h.changed = true
	}
}

func (s *Store) open(ref string, write bool) (*handle, error) {
	id, err := s.resolve(ref)
	if err != nil {
		return nil, err
	}
	var unlock func()
	if write {
		if unlock, err = s.lock(id); err != nil {
			return nil, err
		}
	} else {
		mu := s.projLock(id)
		mu.RLock()
		unlock = mu.RUnlock
	}
	h, err := s.openLocked(id)
	if err != nil {
		if unlock != nil {
			unlock()
		}
		return nil, err
	}
	h.unlock = unlock
	return h, nil
}

func (s *Store) openLocked(id string) (*handle, error) {
	m, err := s.readMeta(id)
	if err != nil {
		return nil, notFoundf("call list_projects to see the projects", "project %s cannot be read: %v", id, err)
	}
	p, err := threemf.Open(filepath.Join(s.dir(id), projectFile))
	if err != nil {
		return nil, errf(CodeInternal, "the project file may be damaged; export what you can or delete the project", "project %s: %v", id, err)
	}
	p.Now = s.now
	return &handle{s: s, id: id, dir: s.dir(id), meta: m, p: p}, nil
}

func (h *handle) close() {
	if h.p != nil {
		h.p.Close()
		h.p = nil
	}
	if h.unlock != nil {
		h.unlock()
		h.unlock = nil
	}
}

// commit renders the thumbnails of the changed plates, saves the project
// atomically and records the new revision.
func (h *handle) commit() error {
	if !h.changed {
		return nil
	}
	if err := h.renderThumbnails(); err != nil {
		return err
	}
	path := filepath.Join(h.dir, projectFile)
	var err error
	for attempt := 0; attempt < 30; attempt++ {
		if err = h.p.Save(path); err == nil {
			break
		}
		// A virus scanner can hold the file for a moment on Windows.
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		return errf(CodeInternal, "the project file may be in use by another reader (another program or server); try again in a moment", "saving the project failed: %v", err)
	}
	h.meta.Revision++
	h.meta.Updated = h.s.now()
	h.recordSaved()
	if err := h.s.writeMeta(h.id, h.meta); err != nil {
		return errf(CodeInternal, "", "saving the project metadata failed: %v", err)
	}
	h.changed = false
	return nil
}

// write runs fn on the locked, opened project and saves it when fn changed it.
func (s *Store) write(ref string, fn func(h *handle) error) error {
	h, err := s.open(ref, true)
	if err != nil {
		return err
	}
	defer h.close()
	if err := fn(h); err != nil {
		return err
	}
	return h.commit()
}

// read runs fn on the opened project without locking for write.
func (s *Store) read(ref string, fn func(h *handle) error) error {
	h, err := s.open(ref, false)
	if err != nil {
		return err
	}
	defer h.close()
	return fn(h)
}

// Delete removes a project's folder. confirm must be the project id.
func (s *Store) Delete(ref, confirm string) (id string, err error) {
	id, err = s.resolve(ref)
	if err != nil {
		return "", err
	}
	if confirm != id {
		return "", invalidf("pass confirm equal to the project id "+id, "deleting a project needs confirmation")
	}
	s.mu.Lock()
	if job := s.running[id]; job != "" {
		s.mu.Unlock()
		return "", conflictf("cancel it with get_slice_status or wait", "project %s is being sliced (job %s)", id, job)
	}
	s.mu.Unlock()
	// A slice of another process holds the slice lock: never delete under it.
	if _, statErr := os.Stat(filepath.Join(s.dir(id), outDirName)); statErr == nil {
		unlockSlice, gerr := s.sliceGuard(id)
		if gerr != nil {
			return "", gerr
		}
		unlockSlice() // released at once: its lock file must not be open while the folder goes
	}
	// Delete is all or nothing. The folder is first renamed to a hidden trash
	// name: on Windows that fails while any file inside is open (a Creality Print
	// window showing a view copy), and then nothing has changed. Only after the
	// rename works is the trash removed; a removal that fails leaves it hidden for
	// the next sweep. The in-process lock is held; the lock file is opened only
	// to see that no other process holds it, then closed (it would block the rename).
	mu := s.projLock(id)
	mu.Lock()
	fl := flock.New(filepath.Join(s.dir(id), lockFile))
	ctx, cancel := context.WithTimeout(context.Background(), lockTimeout)
	ok, lerr := fl.TryLockContext(ctx, 25*time.Millisecond)
	cancel()
	if lerr != nil || !ok {
		mu.Unlock()
		return "", conflictf("another process is changing the project; try again in a moment", "the project %s is locked", id)
	}
	_ = fl.Unlock()
	trash := filepath.Join(s.cfg.Root, fmt.Sprintf("%s%s-%x", trashPrefix, id, time.Now().UnixNano()))
	var rerr error
	for attempt := 0; attempt < 10; attempt++ {
		if rerr = os.Rename(s.dir(id), trash); rerr == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	mu.Unlock()
	if rerr != nil {
		return "", conflictf("close the program that holds the file, then call delete_project again",
			"a file of this project is open in another program (for example a Creality Print window opened by open_in_app): close it, then call delete_project again (%v)", rerr)
	}
	s.mu.Lock()
	delete(s.locks, id) // the lock table does not grow with deleted projects
	s.mu.Unlock()
	_ = os.RemoveAll(trash) // best effort: what stays is hidden and swept later
	s.sweepTrash()
	return id, nil
}

// trashPrefix marks the folders of deleted projects that are being removed;
// they are never listed or opened.
const trashPrefix = ".deleting-"

// sweepTrash removes what earlier deletes left in the trash, best effort.
func (s *Store) sweepTrash() {
	entries, err := os.ReadDir(s.cfg.Root)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), trashPrefix) {
			_ = os.RemoveAll(filepath.Join(s.cfg.Root, e.Name()))
		}
	}
}

// removeAll deletes a folder tree, retrying briefly (Windows, scanners, the
// lock file that is still open).
func removeAll(dir string) error {
	var err error
	for attempt := 0; attempt < 10; attempt++ {
		if err = os.RemoveAll(dir); err == nil {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return err
}

// Export copies the project to a .3mf path. An existing file is replaced only
// with overwrite.
func (s *Store) Export(ref, path string, overwrite bool) (*ExportResult, error) {
	if !filepath.IsAbs(path) {
		return nil, invalidf("give an absolute path such as "+filepath.Join(os.TempDir(), "project.3mf"), "%q is not an absolute path", path)
	}
	if !strings.EqualFold(filepath.Ext(path), ".3mf") {
		return nil, invalidf("use a .3mf file name", "%q does not end in .3mf", path)
	}
	var res *ExportResult
	err := s.read(ref, func(h *handle) error {
		if info, err := os.Stat(path); err == nil {
			if info.IsDir() {
				return invalidf("give a file path", "%q is a folder", path)
			}
			if !overwrite {
				return conflictf("pass overwrite true to replace it, or choose another path", "%q already exists", path)
			}
		}
		data, err := os.ReadFile(filepath.Join(h.dir, projectFile))
		if err != nil {
			return errf(CodeInternal, "", "%v", err)
		}
		created := false
		if _, err := os.Stat(filepath.Dir(path)); os.IsNotExist(err) {
			created = true
		}
		if err := domain.WriteFileAtomic(path, data, 0o644); err != nil {
			return errf(CodeInvalidInput, "check that the folder is writable", "could not write %q: %v", path, err)
		}
		res = &ExportResult{ProjectID: h.id, File: path, Bytes: int64(len(data)), CreatedFolder: created}
		return nil
	})
	return res, err
}

// ExportResult describes an exported project.
type ExportResult struct {
	ProjectID     string
	File          string
	Bytes         int64
	CreatedFolder bool
}

// recordSaved updates the parts of job.json that follow a saved project: the
// revision of every plate that changed (a slice result of an earlier revision
// is then stale), the plates that no longer exist, and the list summary.
func (h *handle) recordSaved() {
	if h.meta.PlateRev == nil {
		h.meta.PlateRev = map[int]int{}
	}
	exists := map[int]bool{}
	for _, pl := range h.p.Plates {
		exists[pl.Index] = true
		if h.allPlates || h.plates[pl.Index] {
			h.meta.PlateRev[pl.Index] = h.meta.Revision
		}
	}
	for idx := range h.meta.PlateRev {
		if !exists[idx] {
			delete(h.meta.PlateRev, idx)
		}
	}
	if ls := h.meta.LastSlice; ls != nil {
		kept := ls.Plates[:0]
		for _, p := range ls.Plates {
			if exists[p.Plate] {
				kept = append(kept, p)
			}
		}
		ls.Plates = kept
		if len(kept) == 0 {
			h.meta.LastSlice = nil
		}
	}
	h.meta.Objects, h.meta.Plates = len(h.p.Objects), len(h.p.Plates)
	if h.p.Settings != nil {
		h.meta.Printer = h.p.Settings.String("printer_settings_id")
	}
}
