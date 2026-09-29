package doctorchecks

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/sairaph/mcp-wizard/doctor"

	"github.com/sairaph/creality-slicer-mcp/internal/domain"
)

// DataDirCheck reports whether the per-user data folder (cache and projects)
// can be written. The folder itself is not created by the check: when it does
// not exist yet, the nearest existing parent is probed instead, since that is
// where the server would create it.
type DataDirCheck struct {
	// Dir is the folder to check; empty means domain.DataDir().
	Dir string
}

func (DataDirCheck) Name() string { return "Data folder" }

func (c DataDirCheck) Run(_ context.Context) doctor.Result {
	dir := c.Dir
	if dir == "" {
		var err error
		if dir, err = domain.DataDir(); err != nil {
			return doctor.Result{Name: c.Name(), Status: doctor.Fail, Detail: err.Error()}
		}
	}
	probe, exists := nearestExisting(dir)
	if err := probeWritable(probe); err != nil {
		return doctor.Result{Name: c.Name(), Status: doctor.Fail,
			Detail: fmt.Sprintf("%s is not writable: %v (cache and projects are stored there)", probe, err)}
	}
	if exists {
		return doctor.Result{Name: c.Name(), Status: doctor.OK, Detail: dir + " is writable"}
	}
	return doctor.Result{Name: c.Name(), Status: doctor.OK, Detail: dir + " does not exist yet; it is created on first use (" + probe + " is writable)"}
}

// nearestExisting returns dir when it exists, else its nearest existing
// parent, and whether dir itself is an existing folder. An existing path that
// is not a folder is returned as it is, so probing it fails.
func nearestExisting(dir string) (string, bool) {
	dir = filepath.Clean(dir)
	for p := dir; ; p = filepath.Dir(p) {
		if info, err := os.Stat(p); err == nil {
			return p, p == dir && info.IsDir()
		}
		if filepath.Dir(p) == p {
			return p, false
		}
	}
}

// probeWritable creates a temporary file in dir and removes it again.
func probeWritable(dir string) error {
	f, err := os.CreateTemp(dir, ".doctor-probe-*")
	if err != nil {
		return err
	}
	name := f.Name()
	closeErr := f.Close()
	if err := os.Remove(name); err != nil {
		return err
	}
	return closeErr
}
