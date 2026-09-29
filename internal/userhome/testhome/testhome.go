// Package testhome isolates a test binary from the developer's real home
// directory. It imports "testing" and is only ever imported from _test.go
// files.
package testhome

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/sairaph/creality-slicer-mcp/internal/userhome"
)

// Run is what every test package's TestMain calls:
//
//	func TestMain(m *testing.M) { os.Exit(testhome.Run(m)) }
//
// It points HOME, USERPROFILE and the other per-user environment variables
// (APPDATA, LOCALAPPDATA, XDG_*) at a fresh temp directory for the whole
// package run and removes it afterwards, and records the real home in
// userhome.RealHomeEnv (overwriting any value already in the environment) so
// userhome.Dir can refuse to resolve it. Tests that
// need their own home keep using t.Setenv.
func Run(m *testing.M) int {
	return RunFunc(m.Run)
}

// RunFunc is Run for a TestMain that wraps m.Run in more setup of its own.
func RunFunc(run func() int) int {
	// Always isolate and always recompute the real home from the OS account
	// (never from HOME or a marker inherited from the environment, which a
	// stale value or an enclosing test process could have left behind).
	realHome, err := userhome.RealHome()
	if err != nil {
		fmt.Fprintf(os.Stderr, "testhome: resolve real home: %v\n", err)
		return 1
	}
	// A short name: some tests derive Unix socket paths from HOME, which
	// have a tight length budget.
	root, err := os.MkdirTemp("", "csh")
	if err != nil {
		fmt.Fprintf(os.Stderr, "testhome: MkdirTemp: %v\n", err)
		return 1
	}
	defer func() {
		if err := os.RemoveAll(root); err != nil {
			fmt.Fprintf(os.Stderr, "testhome: remove %s: %v\n", root, err)
		}
	}()

	env := map[string]string{
		userhome.RealHomeEnv: realHome,
		"HOME":               root,
		"USERPROFILE":        root,
		"APPDATA":            filepath.Join(root, "AppData", "Roaming"),
		"LOCALAPPDATA":       filepath.Join(root, "AppData", "Local"),
		"XDG_CONFIG_HOME":    filepath.Join(root, ".config"),
		"XDG_DATA_HOME":      filepath.Join(root, ".local", "share"),
		"XDG_CACHE_HOME":     filepath.Join(root, ".cache"),
		"XDG_STATE_HOME":     filepath.Join(root, ".local", "state"),
		// Read by the guide skill folders and the shell profile cleanup: empty
		// means unset, so the real values never reach a test.
		"CONTINUE_GLOBAL_DIR": "",
		"ZDOTDIR":             "",
	}
	saved := map[string]*string{}
	for k, v := range env {
		if old, ok := os.LookupEnv(k); ok {
			o := old
			saved[k] = &o
		} else {
			saved[k] = nil
		}
		os.Setenv(k, v)
	}
	defer func() {
		for k, old := range saved {
			if old == nil {
				os.Unsetenv(k)
			} else {
				os.Setenv(k, *old)
			}
		}
	}()
	return run()
}
