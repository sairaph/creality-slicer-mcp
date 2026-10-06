package projects

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/sairaph/creality-slicer-mcp/internal/domain"
)

// View modes of PrepareView.
const (
	ViewPreview = "preview" // the G-code of a sliced plate, for the app's Preview tab
	ViewProject = "project" // the project as a 3MF, for the app's editor
)

const viewDirName = "view"

// ViewRecord is what the server wrote into the view folder: the file name
// (relative to the folder), its size and its sha256. A file whose content
// differs from its record was changed by someone else (the user saved into it).
type ViewRecord struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// ViewFile is a file prepared for the app to open.
type ViewFile struct {
	ProjectID string
	Mode      string
	Plate     int
	// Path is the absolute path of the file to open: <project>/view/plate<N>_r<rev>.gcode
	// or <project>/view/<safe name>_r<rev>.3mf, with _2, _3 ... before the
	// extension when the plain name holds a file the user saved. It is a copy: the
	// app never locks the slice output or the project file.
	Path     string
	Revision int
	Bytes    int64
	// SavedFiles lists the files in the view folder that differ from what this
	// server wrote: the user saved into them. They are never overwritten or
	// deleted. A saved 3MF comes back with open_project into.
	SavedFiles []string
}

// matchesRecord reports whether the file at path still has the content the
// server recorded for name.
func matchesRecord(dir, name string, recs []ViewRecord) bool {
	for _, r := range recs {
		if r.Name == name {
			sum, size, err := sumFile(filepath.Join(dir, name))
			return err == nil && size == r.Size && sum == r.SHA256
		}
	}
	return false
}

// sumFile is the sha256 and the size of a file, read in a stream (a G-code can
// be hundreds of megabytes).
func sumFile(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// copyFileAtomic copies src to dst through a temporary file next to dst and a
// replace, so the app never sees half a file. It streams the data.
func copyFileAtomic(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	_ = tmp.Chmod(0o644)
	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := domain.ReplaceFile(name, dst); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}

// PrepareView writes the file the app should open and returns its path.
//
// ViewPreview needs a slice of the plate that is not stale (else conflict:
// slice_project first) and copies its G-code. ViewProject exports the current
// project. Either is refused (conflict) while a slice of the project runs.
// mode "" means ViewPreview; plate 0 means 1.
//
// The server records what it writes. A file in the view folder whose content
// differs from its record (the user saved into it from the app) is never
// overwritten or deleted: the new file gets the next free numbered name and the
// file is listed in SavedFiles. Files that still match their record and are from
// earlier revisions are removed (best effort).
func (s *Store) PrepareView(ref string, plate int, mode string) (*ViewFile, error) {
	if mode == "" {
		mode = ViewPreview
	}
	if mode != ViewPreview && mode != ViewProject {
		return nil, invalidf("mode is preview or project", "%q is not a mode", mode)
	}
	if plate == 0 {
		plate = 1
	}
	h, err := s.open(ref, true)
	if err != nil {
		return nil, err
	}
	defer h.close()
	s.mu.Lock()
	job := s.running[h.id]
	s.mu.Unlock()
	if job != "" {
		return nil, conflictf("wait for the slice to end (get_slice_status), then open the view", "project %s is being sliced (job %s)", h.id, job)
	}
	exists := false
	for _, pl := range h.p.Plates {
		exists = exists || pl.Index == plate
	}
	if !exists {
		return nil, invalidf("manage_plates lists the plates", "the project has no plate %d", plate)
	}
	dir := filepath.Join(h.dir, viewDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, errf(CodeInternal, "check that the projects folder is writable", "could not create %s: %v", dir, err)
	}
	rev := h.meta.Revision
	vf := &ViewFile{ProjectID: h.id, Mode: mode, Plate: plate, Revision: rev}
	var srcPath string
	var stem, ext string
	switch mode {
	case ViewPreview:
		var pr *PlateResult
		if l := h.meta.LastSlice; l != nil {
			for i := range l.Plates {
				if l.Plates[i].Plate == plate {
					pr = &l.Plates[i]
				}
			}
		}
		if pr == nil {
			return nil, conflictf("slice_project first", "plate %d has not been sliced", plate)
		}
		if h.meta.plateStale(*pr) {
			return nil, conflictf("slice_project again", "plate %d changed after it was sliced (slice revision %d, project revision %d)", plate, pr.Revision, rev)
		}
		srcPath = pr.GCodePath
		if _, err = os.Stat(srcPath); err != nil {
			return nil, conflictf("slice_project again", "the G-code of plate %d is not readable: %v", plate, err)
		}
		stem, ext = "plate"+strconv.Itoa(plate)+"_r"+strconv.Itoa(rev), ".gcode"
	default:
		srcPath = filepath.Join(h.dir, projectFile)
		if _, err = os.Stat(srcPath); err != nil {
			return nil, errf(CodeInternal, "", "%v", err)
		}
		stem, ext = uploadBase(h.meta.Name, h.id)+"_r"+strconv.Itoa(rev), ".3mf"
	}
	sum, size, err := sumFile(srcPath)
	if err != nil {
		return nil, conflictf("slice_project again", "the file to show is not readable: %v", err)
	}
	vf.Bytes = size

	// The first name that is free, or holds a file this server wrote and that is
	// unchanged, gets the content. A changed file (or one that cannot be written)
	// is skipped.
	recs := h.meta.Views
	var chosen string
	for n := 1; n < 1000 && chosen == ""; n++ {
		name := stem + ext
		if n > 1 {
			name = stem + "_" + strconv.Itoa(n) + ext
		}
		path := filepath.Join(dir, name)
		if _, serr := os.Stat(path); serr == nil {
			if !matchesRecord(dir, name, recs) {
				continue // the user saved into it
			}
			if cur, _, rerr := sumFile(path); rerr == nil && cur == sum {
				chosen = name // the same content is there already
				break
			}
		}
		if werr := copyFileAtomic(srcPath, path); werr != nil {
			continue // held open by the app, perhaps
		}
		chosen = name
	}
	if chosen == "" {
		return nil, errf(CodeInternal, "close the app windows that show older copies, then try again", "no free name for the view file in %s", dir)
	}
	vf.Path = filepath.Join(dir, chosen)

	// Update the records: drop the ones whose file is gone, add the new one, then
	// remove the unchanged files of earlier revisions and list the changed ones.
	var kept []ViewRecord
	for _, r := range recs {
		if r.Name != chosen {
			if _, serr := os.Stat(filepath.Join(dir, r.Name)); serr == nil {
				kept = append(kept, r)
			}
		}
	}
	kept = append(kept, ViewRecord{Name: chosen, Size: vf.Bytes, SHA256: sum})
	recs = kept
	current := "_r" + strconv.Itoa(rev)
	entries, _ := os.ReadDir(dir)
	var left []ViewRecord
	for _, e := range entries {
		if e.IsDir() || e.Name() == chosen {
			continue
		}
		name := e.Name()
		if matchesRecord(dir, name, recs) {
			old := !strings.Contains(name, current+".") && !strings.Contains(name, current+"_")
			if old && os.Remove(filepath.Join(dir, name)) == nil {
				continue
			}
			for _, r := range recs {
				if r.Name == name {
					left = append(left, r)
				}
			}
			continue
		}
		vf.SavedFiles = append(vf.SavedFiles, filepath.Join(dir, name))
	}
	for _, r := range recs {
		if r.Name == chosen {
			left = append(left, r)
		}
	}
	h.meta.Views = left
	if err := s.writeMeta(h.id, h.meta); err != nil {
		return nil, errf(CodeInternal, "", "saving the project metadata failed: %v", err)
	}
	return vf, nil
}
