package slicer

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/gofrs/flock"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sairaph/creality-slicer-mcp/internal/domain"
)

// dataDirDialect is implemented by dialects whose command line needs a data
// folder (--datadir): the CLI appends "Creality Print\<DataSubdir>" to it.
type dataDirDialect interface {
	DataSubdir() string
}

// CLIDataDirName is the folder under the cache folder that holds the data
// folder the tools give the slicer.
const CLIDataDirName = "cli-data"

// DefaultCLIDataDir is the data folder the tools own for the slicer's runs,
// <cache>\cli-data. It is never the application's own folder.
func DefaultCLIDataDir() (string, error) {
	c, err := domain.CacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(c, CLIDataDirName), nil
}

// dataLockTimeout bounds the wait for the data folder lock (an anti-hang guard).
const dataLockTimeout = 60 * time.Second

// prepareDataDir makes the data folder of a run ready and returns the function
// that lets go of it after the run. The folder exists, and its system presets
// are the ones the application would use: when the application's own data
// folder holds a newer profile bundle than the install (a hot update of the
// presets), that bundle is mirrored into the tools' folder, so the slicer
// re-merges projects with exactly the presets the application uses; otherwise
// a stale mirror is removed and the slicer reads the install's own profiles.
// The application's folder is only read.
//
// The folder is shared by every run of every server: a run holds a shared lock
// (<data>\.lock) for its whole life, and the mirror step, which replaces or
// removes the system folder a running slicer reads, takes the lock
// exclusively, so it waits for runs that are going and no two mirrors overlap.
func (r *Runner) prepareDataDir() (release func(), err error) {
	dd, ok := r.Dialect.(dataDirDialect)
	if !ok {
		return func() {}, nil
	}
	if r.DataDir == "" {
		return nil, fmt.Errorf("%w: this Creality Print version needs a data folder of its own and none is configured", ErrInvalidRequest)
	}
	if app := r.Install.DataDir; app != "" && sameDir(app, r.DataDir) {
		return nil, fmt.Errorf("%w: the data folder of the tools must not be the application's own folder %q", ErrInvalidRequest, app)
	}
	base := filepath.Join(r.DataDir, "Creality Print", dd.DataSubdir())
	if err := os.MkdirAll(base, 0o755); err != nil {
		return nil, fmt.Errorf("could not create the slicer data folder: %w", err)
	}
	dst := filepath.Join(base, "system")
	lockPath := filepath.Join(r.DataDir, ".lock")
	lockOnce := func(exclusive bool) (*flock.Flock, error) {
		fl := flock.New(lockPath)
		ctx, cancel := context.WithTimeout(context.Background(), dataLockTimeout)
		defer cancel()
		var got bool
		var lerr error
		if exclusive {
			got, lerr = fl.TryLockContext(ctx, 25*time.Millisecond)
		} else {
			got, lerr = fl.TryRLockContext(ctx, 25*time.Millisecond)
		}
		if lerr != nil || !got {
			if lerr == nil {
				lerr = ctx.Err()
			}
			return nil, fmt.Errorf("the slicer data folder is in use by another run that does not end: %w", lerr)
		}
		return fl, nil
	}
	for attempt := 0; attempt < 5; attempt++ {
		shared, err := lockOnce(false)
		if err != nil {
			return nil, err
		}
		if mirrorCurrent(r.Install, dst) {
			return func() { _ = shared.Unlock() }, nil
		}
		_ = shared.Unlock()
		excl, err := lockOnce(true)
		if err != nil {
			return nil, err
		}
		serr := syncSystemPresets(r.Install, dst)
		_ = excl.Unlock()
		if serr != nil {
			return nil, serr
		}
	}
	return nil, fmt.Errorf("could not bring the slicer data folder up to date")
}

// mirrorCurrent reports whether the system folder of the tools' data folder is
// already what the mirror rule wants.
func mirrorCurrent(in Install, dst string) bool {
	if !mirrorNeeded(in) {
		_, err := os.Stat(dst)
		return os.IsNotExist(err)
	}
	v := bundleVersion(dst)
	return v != "" && v == bundleVersion(filepath.Join(in.DataDir, "system"))
}

// mirrorNeeded reports whether the application's system folder must be
// mirrored: its bundle exists and is newer than the install's.
func mirrorNeeded(in Install) bool {
	return in.DataDir != "" && in.ProfileVersionData != "" && compareVersions(in.ProfileVersionData, in.ProfileVersionInstall) > 0
}

func bundleVersion(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, "Creality.json"))
	if err != nil {
		return ""
	}
	var v struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(data, &v) != nil {
		return ""
	}
	return strings.TrimSpace(v.Version)
}

// syncSystemPresets implements the mirror rule for dst, the "system" folder of
// the tools' data folder.
func syncSystemPresets(in Install, dst string) error {
	if !mirrorNeeded(in) {
		if _, err := os.Stat(dst); err == nil {
			return os.RemoveAll(dst)
		}
		return nil
	}
	src := filepath.Join(in.DataDir, "system")
	if bundleVersion(dst) == bundleVersion(src) && bundleVersion(dst) != "" {
		return nil
	}
	tmp := dst + ".new"
	_ = os.RemoveAll(tmp)
	if err := copyTree(src, tmp); err != nil {
		_ = os.RemoveAll(tmp)
		return fmt.Errorf("could not mirror the application's system presets: %w", err)
	}
	old := dst + ".old"
	_ = os.RemoveAll(old)
	if _, err := os.Stat(dst); err == nil {
		if err := os.Rename(dst, old); err != nil {
			_ = os.RemoveAll(tmp)
			return fmt.Errorf("could not replace the mirrored system presets: %w", err)
		}
	}
	if err := os.Rename(tmp, dst); err != nil {
		return fmt.Errorf("could not place the mirrored system presets: %w", err)
	}
	_ = os.RemoveAll(old)
	return nil
}

func copyTree(src, dst string) error {
	return filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.Create(target)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	})
}
