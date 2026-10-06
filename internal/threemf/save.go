package threemf

import (
	"archive/zip"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sairaph/creality-slicer-mcp/internal/domain"
)

func (p *Project) removeMember(name string) {
	delete(p.byName, name)
	kept := p.members[:0]
	for _, m := range p.members {
		if m.name != name {
			kept = append(kept, m)
		}
	}
	p.members = kept
	delete(p.objectFiles, name)
}

// setMember replaces or adds a generated member.
func (p *Project) setMember(name string, data []byte) {
	store := strings.HasSuffix(name, ".png")
	if m := p.byName[name]; m != nil {
		m.data, m.touched, m.store = data, true, store
		return
	}
	m := &member{name: name, data: data, touched: true, store: store}
	p.members = append(p.members, m)
	p.byName[name] = m
}

// finalize regenerates the members whose content changed.
func (p *Project) finalize() error {
	// Changed project settings can change every plate, so no slice result of an
	// existing project survives them.
	if p.Settings != nil && p.Settings.dirty && !p.isNew {
		p.resetAllSliceResults()
	}
	if p.modelDirty {
		if err := p.requireSplit(); err != nil {
			return err
		}
		// New sub model files for the meshes added in this session, one file
		// per path holding all the fresh parts that point at it.
		type group struct {
			obj   *Object
			parts []*Part
		}
		files := map[string]*group{}
		var order []string
		for _, o := range p.Objects {
			for _, part := range o.Parts {
				if part.fresh == nil {
					continue
				}
				g := files[part.Mesh.Path]
				if g == nil {
					g = &group{obj: o}
					files[part.Mesh.Path] = g
					order = append(order, part.Mesh.Path)
				}
				g.parts = append(g.parts, part)
			}
		}
		for _, path := range order {
			g := files[path]
			p.setMember(strings.TrimPrefix(path, "/"), p.genObjectModel(g.obj, g.parts))
		}
		if !p.isNew {
			if _, ok := p.Metadata.Get("ModificationDate"); ok {
				p.Metadata.Set("ModificationDate", p.Now().Format("2006-01-02"))
			}
		}
		p.setMember(memberModel, p.genModel())
		p.setMember(memberModelRels, genModelRels(p.objectFileNames()))
	}
	if p.settingsDirty || p.isNew {
		p.setMember(memberModelSettings, p.genModelSettings())
	}
	if p.Settings != nil && (p.Settings.dirty || p.isNew) {
		p.setMember(memberProjectConfig, p.Settings.Marshal())
	}
	if p.rangesDirty || p.isNew {
		if data := p.genLayerRanges(); data != nil {
			p.setMember(memberLayerRanges, data)
		} else if p.byName[memberLayerRanges] != nil {
			p.removeMember(memberLayerRanges)
		}
	}
	if p.hasIndexed && (p.modelDirty || p.rangesDirty) {
		p.saveIndexed()
	}
	if p.gcodesDirty || (p.isNew && len(p.GCodes) > 0) {
		p.syncGCodes()
		p.setMember(memberCustomGCode, p.genGCodes())
	}
	if p.isNew {
		// Fixed members; a thumbnail call may already have written the
		// relationships or patched the content types.
		if p.byName[memberContentTypes] == nil {
			p.setMember(memberContentTypes, []byte(contentTypes))
		}
		if p.byName[memberRels] == nil {
			p.setMember(memberRels, []byte(rootRels))
		}
		p.setMember(memberCreality, genCreality(p.Creality))
		if p.byName[memberSliceInfo] == nil {
			p.setMember(memberSliceInfo, genSliceInfo(p.Creality.Value("AppVersion")))
		}
	}
	return nil
}

// saveOrder puts the members in the order the app writes them; members that
// are not in the list keep their relative order after it.
func (p *Project) saveOrder() []*member {
	if !p.isNew {
		return p.members
	}
	want := []string{memberContentTypes, memberModel, memberModelRels}
	want = append(want, p.objectFileNames()...)
	want = append(want, memberLayerRanges, memberCustomGCode, memberProjectConfig, memberModelSettings, memberSliceInfo, memberCreality, memberRels)
	seen := map[string]bool{}
	var out []*member
	for _, name := range want {
		if m := p.byName[name]; m != nil && !seen[name] {
			out = append(out, m)
			seen[name] = true
		}
	}
	for _, m := range p.members {
		if !seen[m.name] {
			out = append(out, m)
		}
	}
	return out
}

// Save writes the project to dest through a temporary file in the same
// folder and a rename. Members that were not changed are copied byte for
// byte (compressed data included); only changed members are regenerated.
// dest may be the file the project was opened from.
func (p *Project) Save(dest string) error {
	if err := p.finalize(); err != nil {
		return err
	}
	dir := filepath.Dir(dest)
	tmp, err := os.CreateTemp(dir, ".project-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	fail := func(err error) error {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	zw := zip.NewWriter(tmp)
	now := p.Now()
	for _, m := range p.saveOrder() {
		if m.zf != nil && !m.touched {
			if err := zw.Copy(m.zf); err != nil {
				return fail(fmt.Errorf("copying %s: %w", m.name, err))
			}
			continue
		}
		method := zip.Deflate
		if m.store {
			method = zip.Store
		}
		hdr := &zip.FileHeader{Name: m.name, Method: method, Modified: now}
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			return fail(err)
		}
		if _, err := w.Write(m.data); err != nil {
			return fail(err)
		}
	}
	if err := zw.Close(); err != nil {
		return fail(err)
	}
	if err := tmp.Sync(); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	// The old archive must be closed before it can be replaced on Windows.
	if p.zr != nil {
		p.zr.Close()
		p.zr = nil
	}
	// Another reader can hold the file for a moment (a virus scanner, the app):
	// try the replacement again, for about renameBudget in all, while the
	// finished temporary file is kept. This is the only retry layer of a save
	// (replaceFile has its own short one for one attempt).
	var err2 error
	deadline := time.Now().Add(renameBudget)
	for {
		if err2 = renameFile(tmpName, dest); err2 == nil || !time.Now().Add(renameRetryDelay).Before(deadline) {
			break
		}
		time.Sleep(renameRetryDelay)
	}
	if err2 != nil {
		os.Remove(tmpName)
		// The project keeps every pending change: only the reader of the old
		// file is reopened, so a later Save writes the edit.
		err2 = fmt.Errorf("%w: %v", ErrReplace, err2)
		if reopenErr := p.reopenOld(); reopenErr != nil && p.path != "" {
			return fmt.Errorf("%w (and the project could not be reopened: %v)", err2, reopenErr)
		}
		return err2
	}
	return p.rebind(dest)
}

// ErrReplace is wrapped in the error of a Save whose file could not be replaced
// (another program holds it); the project still has every pending change.
var ErrReplace = errors.New("the project file could not be replaced")

// renameBudget is the total time Save keeps trying to replace the file.
var renameBudget = 3 * time.Second

const renameRetryDelay = 100 * time.Millisecond

var renameFile = domain.ReplaceFile

// reopenOld opens the file the project was read from again after a failed
// save and points the members that were not changed at it. Changed members,
// their data and every dirty flag stay as they are.
func (p *Project) reopenOld() error {
	if p.path == "" {
		return nil
	}
	zr, err := zip.OpenReader(p.path)
	if err != nil {
		return err
	}
	byName := map[string]*zip.File{}
	for _, f := range zr.File {
		byName[f.Name] = f
	}
	for _, m := range p.members {
		if m.zf == nil {
			continue
		}
		if f := byName[m.name]; f != nil {
			m.zf = f
		}
	}
	p.zr = zr
	return nil
}

// rebind opens the saved file and points every member at its entry, so the
// project keeps working (and later saves copy from the new file).
func (p *Project) rebind(file string) error {
	if file == "" {
		return nil
	}
	zr, err := zip.OpenReader(file)
	if err != nil {
		return err
	}
	byName := map[string]*zip.File{}
	for _, f := range zr.File {
		byName[f.Name] = f
	}
	for _, m := range p.members {
		if f := byName[m.name]; f != nil {
			m.zf, m.data, m.touched = f, nil, false
		}
	}
	p.zr, p.path = zr, file
	p.modelDirty, p.settingsDirty, p.rangesDirty, p.gcodesDirty, p.isNew = false, false, false, false, false
	if p.Settings != nil {
		p.Settings.dirty = false
	}
	for _, o := range p.Objects {
		for _, part := range o.Parts {
			part.fresh = nil
		}
	}
	p.objectFiles = map[string]*modelFile{}
	if _, err := p.Read(memberModel); err != nil {
		return err
	}
	return nil
}
