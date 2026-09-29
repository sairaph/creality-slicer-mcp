//go:build !windows

package main

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/sairaph/creality-slicer-mcp/internal/domain"
	"github.com/sairaph/creality-slicer-mcp/internal/userhome"
)

// removeFromUserPath removes the lines install.sh appended to the shell
// profiles; that is the only place it put the install directory on PATH.
func removeFromUserPath(dir string) (bool, error) {
	home, err := userhome.Dir()
	if err != nil {
		return false, err
	}
	removedAny := false
	var errs []error
	paths := []string{filepath.Join(home, ".zshrc")}
	// zsh reads .zshrc from ZDOTDIR when that is set, and install.sh wrote there.
	if zdot := os.Getenv("ZDOTDIR"); zdot != "" && !samePath(zdot, home) {
		paths = append(paths, filepath.Join(zdot, ".zshrc"))
	}
	for _, name := range []string{".bashrc", ".profile", ".bash_profile"} {
		paths = append(paths, filepath.Join(home, name))
	}
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		updated, removed := removeRCBlock(string(data), dir)
		if !removed {
			continue
		}
		if err := domain.WriteFileAtomic(path, []byte(updated), info.Mode().Perm()); err != nil {
			errs = append(errs, err)
			continue
		}
		removedAny = true
	}
	return removedAny, errors.Join(errs...)
}

// removeInstallRoot deletes the install directory, and its parent when that is
// left empty: on Unix the install directory sits inside the data folder, which
// keeps the projects and must stay while it holds anything. A running program
// may delete its own file on Unix.
func removeInstallRoot(installDir string) error {
	if err := os.RemoveAll(installDir); err != nil {
		return err
	}
	_ = os.Remove(filepath.Dir(installDir)) // fails, harmlessly, while it holds projects
	return nil
}
