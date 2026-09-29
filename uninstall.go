package main

// `uninstall --all` removes what `install` and the install scripts put on the
// machine: the user-level AI client registrations, the guide skill, the cache,
// and the binary the install script placed together with its PATH entry. The
// projects folder holds the user's work, not something installed, so it stays,
// and so does what `add` wrote into a project (its client entries: `uninstall
// --scope project` removes those, per project).

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/sairaph/mcp-wizard/cli"
	"github.com/sairaph/mcp-wizard/harness"

	"github.com/sairaph/creality-slicer-mcp/internal/domain"
)

func runUninstallAll(ctx context.Context, detector *harness.Detector, cmd cli.Command) int {
	w := os.Stdout
	verb := "removed"
	if cmd.DryRun {
		verb = "would remove"
	}
	fmt.Fprintln(w, "  AI clients")
	clientsCode := runUnattended(ctx, detector, harness.Scope{}, cmd, harness.Absent)
	code := clientsCode

	fmt.Fprintln(w, "\n  Guide skill")
	code = max(code, removeAllUserSkills(w, cmd.DryRun))

	fmt.Fprintln(w, "\n  Cache")
	cache, err := domain.CacheDir()
	if err != nil {
		fmt.Fprintf(w, "  [fail] %v\n", err)
		code = 1
	} else {
		code = max(code, removePaths(w, []string{cache}, verb, cmd.DryRun))
	}
	if projects, err := domain.ProjectsDir(); err == nil {
		if _, err := os.Stat(projects); err == nil {
			fmt.Fprintf(w, "  Your projects are kept: they are your work, not part of the installation.\n"+
				"  They are in %s; delete that folder yourself to remove them too.\n", projects)
		}
	}

	fmt.Fprintln(w, "\n  Program")
	code = max(code, removeProgram(w, clientsCode != 0, cmd.DryRun))
	return code
}

// removeProgram removes the installed binary unless removing a client
// registration failed: that client still runs the program, so deleting it
// would leave the client pointing at a missing file.
func removeProgram(w io.Writer, clientsFailed, dryRun bool) int {
	if clientsFailed {
		fmt.Fprintln(w, "  [skip] keeping the program and its PATH entry: an AI client registration could not be")
		fmt.Fprintln(w, "         removed (see above), and that client would be left pointing at a deleted program.")
		fmt.Fprintln(w, "         Fix the reported problem (close the client if it holds its config file open),")
		fmt.Fprintf(w, "         then run `%s uninstall --all` again.\n", domain.BinaryName)
		return 1
	}
	return removeInstalledBinary(w, dryRun)
}

// removePaths removes each existing path and says so when none exists.
func removePaths(w io.Writer, paths []string, verb string, dryRun bool) int {
	code, found := 0, false
	for _, p := range paths {
		if _, err := os.Lstat(p); err == nil {
			found = true
		}
		code = max(code, removePath(w, p, verb, dryRun))
	}
	if !found {
		fmt.Fprintln(w, "  nothing to remove")
	}
	return code
}

// removePath deletes a file or directory, reporting what it did.
func removePath(w io.Writer, path, verb string, dryRun bool) int {
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return 0
	}
	if !dryRun {
		if err := os.RemoveAll(path); err != nil {
			fmt.Fprintf(w, "  [fail] %s: %v\n", path, err)
			return 1
		}
		// The data folder is left behind once empty; drop it then. Remove
		// fails on a folder that still holds the user's projects.
		if dir := filepath.Dir(path); filepath.Base(dir) == "."+domain.BinaryName {
			_ = os.Remove(dir)
		}
	}
	fmt.Fprintf(w, "  [ok]   %s %s\n", verb, path)
	return 0
}

// installRoot is what removing the installed program deletes: on Windows the
// folder that only holds bin, on Unix the bin folder itself, because there the
// install folder sits inside the data folder that holds the projects.
func installRoot(installDir string) string {
	if runtime.GOOS == "windows" {
		return filepath.Dir(installDir)
	}
	return installDir
}

// removeInstalledBinary removes the install directory and its PATH entry,
// but only for a binary the install scripts placed: a development build
// elsewhere is left alone.
func removeInstalledBinary(w io.Writer, dryRun bool) int {
	installDir, err := domain.InstallDir()
	if err != nil {
		fmt.Fprintf(w, "  [fail] %v\n", err)
		return 1
	}
	exe, err := harness.ResolveExecutable()
	if err != nil {
		fmt.Fprintf(w, "  [fail] cannot locate this program: %v\n", err)
		return 1
	}
	if !inInstallDir(exe, installDir) {
		fmt.Fprintf(w, "  %s is not in the install directory %s; leaving it in place.\n", exe, installDir)
		return 0
	}
	root := installRoot(installDir)
	if dryRun {
		fmt.Fprintf(w, "  [ok]   would remove %s\n", root)
		fmt.Fprintf(w, "  [ok]   would remove %s from PATH\n", installDir)
		return 0
	}
	code := 0
	removed, err := removeFromUserPath(installDir)
	switch {
	case err != nil:
		fmt.Fprintf(w, "  [fail] PATH: %v\n", err)
		code = 1
	case removed:
		fmt.Fprintf(w, "  [ok]   removed %s from PATH\n", installDir)
	}
	if err := removeInstallRoot(installDir); err != nil {
		fmt.Fprintf(w, "  [fail] %s: %v\n", root, err)
		return 1
	}
	if runtime.GOOS == "windows" {
		// A running program cannot delete itself on Windows; a helper does
		// it once this process has exited.
		fmt.Fprintf(w, "  [ok]   scheduled removal of %s once this command exits\n", root)
		fmt.Fprintf(w, "         If an AI client is still running %s, close it first, or the folder stays.\n", domain.BinaryName)
	} else {
		fmt.Fprintf(w, "  [ok]   removed %s\n", root)
	}
	fmt.Fprintln(w, "  Open a new terminal for the PATH change to apply.")
	return code
}

// inInstallDir reports whether exe, a path with its symlinks resolved, sits
// in installDir. installDir is resolved the same way, so a home directory
// reached through a symlink still matches; when it cannot be resolved (it
// does not exist) it is compared as given.
func inInstallDir(exe, installDir string) bool {
	if resolved, err := filepath.EvalSymlinks(installDir); err == nil {
		installDir = resolved
	}
	return samePath(filepath.Dir(exe), installDir)
}

func samePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// removePathListEntry drops every entry equal to dir from a PATH value.
func removePathListEntry(value, dir string, sep string, fold bool) (string, bool) {
	clean := func(s string) string {
		s = strings.TrimRight(strings.TrimSpace(s), `\/`)
		if fold {
			s = strings.ToLower(s)
		}
		return s
	}
	want := clean(dir)
	var kept []string
	removed := false
	for _, entry := range strings.Split(value, sep) {
		if entry != "" && clean(entry) == want {
			removed = true
			continue
		}
		kept = append(kept, entry)
	}
	return strings.Join(kept, sep), removed
}

// removeRCBlock drops the two lines install.sh appends to a shell profile:
// "# added by creality-slicer-mcp installer" and the export line naming dir.
func removeRCBlock(content, dir string) (string, bool) {
	lines := strings.Split(content, "\n")
	marker := "# added by " + domain.BinaryName + " installer"
	var out []string
	removed := false
	for i := 0; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == marker && i+1 < len(lines) && strings.Contains(lines[i+1], dir) {
			i++
			removed = true
			// install.sh writes a blank line before the marker.
			if n := len(out); n > 0 && strings.TrimSpace(out[n-1]) == "" {
				out = out[:n-1]
			}
			continue
		}
		out = append(out, lines[i])
	}
	return strings.Join(out, "\n"), removed
}
