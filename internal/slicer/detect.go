// Package slicer finds the installed Creality Print, builds its command line
// and runs it as a child process to slice, in the foreground or as a
// background job. It is the analogue of freecad-mcp's internal/headless.
//
// Every access to the operating system goes through a small interface (the
// registry, file versions, the process list, the file system, the process
// launcher), so tests use fakes and never touch the real machine or start the
// real slicer.
package slicer

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Reason codes (Install.ReasonCode) reported by Detect; empty when usable.
const (
	ReasonUnsupportedPlatform = "unsupported_platform"
	ReasonNotInstalled        = "not_installed"
	ReasonUnsupportedVersion  = "unsupported_version"
)

// ProductName is the DisplayName prefix of the installer's uninstall entry.
const ProductName = "Creality Print"

// GUIProcessName is the process name of the Creality Print window.
const GUIProcessName = "CrealityPrint.exe"

// Install describes the Creality Print found on this machine.
type Install struct {
	Found bool
	// ReasonCode is "unsupported_platform", "not_installed" or
	// "unsupported_version", empty when Creality Print is usable. Reason is
	// the human sentence for it.
	ReasonCode string
	Reason     string
	Version    string // "7.2.2"
	Build      string // "5483", from the exe FileVersion
	Dir        string // install directory
	Exe        string // <Dir>\CrealityPrint.exe
	DataDir    string // %APPDATA%\Creality\Creality Print\<major>.0, "" when it does not exist

	// ProfileVersionInstall and ProfileVersionData are the "version" of
	// Creality.json in the install's resources\profiles and in
	// <DataDir>\system ("" when absent). ProfileVersion is the higher of the
	// two and ProfileRoot is the profiles directory it comes from.
	ProfileVersionInstall string
	ProfileVersionData    string
	ProfileVersion        string
	ProfileRoot           string

	Dialect   string // "v72" for 7.2.*, "" otherwise
	Supported bool

	// GUIRunning is a snapshot taken when Detect ran: some CrealityPrint.exe
	// process exists. Informational only; the CLI never touches a running GUI.
	GUIRunning bool
	GUIPIDs    []int

	// Others lists the directories of further installs that were found.
	Others []string
}

// UninstallEntry is one entry of the Windows uninstall registry keys.
type UninstallEntry struct {
	Key             string // sub key name, "CrealityPrint-7"
	DisplayName     string
	DisplayVersion  string
	InstallLocation string
	UninstallString string
}

// RegistryReader lists the uninstall entries under HKLM, HKLM WOW6432Node and
// HKCU (in that order).
type RegistryReader interface {
	UninstallEntries() ([]UninstallEntry, error)
}

// FileVersionReader reads the FileVersion string of an executable from its
// version resource ("5483" for the installed CrealityPrint.exe).
type FileVersionReader interface {
	FileVersion(path string) (string, error)
}

// ProcessLister lists the process ids of the processes with the given image
// name (read-only).
type ProcessLister interface {
	PIDsByName(name string) ([]int, error)
}

// FileSystem is the small part of the file system Detect reads.
type FileSystem interface {
	IsFile(path string) bool
	IsDir(path string) bool
	ReadFile(path string) ([]byte, error)
	Glob(pattern string) []string
}

// Platform bundles the operating system access of Detect.
type Platform struct {
	Supported   bool // false: Detect reports unsupported_platform
	Registry    RegistryReader
	FileVersion FileVersionReader
	Processes   ProcessLister
	FS          FileSystem
	Getenv      func(string) string
}

// OSPlatform is the real machine.
func OSPlatform() Platform { return osPlatform() }

// osFS is the real file system.
type osFS struct{}

func (osFS) IsFile(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

func (osFS) IsDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

func (osFS) ReadFile(p string) ([]byte, error) { return os.ReadFile(p) }

func (osFS) Glob(pattern string) []string {
	m, _ := filepath.Glob(pattern)
	return m
}

// Options selects what Detect looks for.
type Options struct {
	// Cmd, when not empty, is the absolute path of CrealityPrint.exe and wins
	// over the registry walk. The version is then read from the exe file and
	// the registry entry is matched by directory.
	Cmd string
	// Platform is the operating system access; the zero value means the real
	// machine.
	Platform *Platform
}

func (o Options) platform() Platform {
	if o.Platform != nil {
		return *o.Platform
	}
	return OSPlatform()
}

// Detect finds Creality Print. It never fails for "not installed": that is a
// result (Found false, Reason set). The error is only for a cancelled context.
func Detect(ctx context.Context, opts Options) (Install, error) {
	if err := ctx.Err(); err != nil {
		return Install{}, err
	}
	p := opts.platform()
	if p.FS == nil {
		p.FS = osFS{}
	}
	if !p.Supported {
		return Install{ReasonCode: ReasonUnsupportedPlatform, Reason: "Creality Print is only driven on Windows; this platform is not supported"}, nil
	}
	getenv := p.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	var entries []UninstallEntry
	if p.Registry != nil {
		entries, _ = p.Registry.UninstallEntries()
	}
	var cands []candidate
	if opts.Cmd != "" {
		c, reason := commandCandidate(opts.Cmd, entries, p)
		if reason != "" {
			return Install{ReasonCode: ReasonNotInstalled, Reason: reason}, nil
		}
		cands = []candidate{c}
	} else {
		cands = registryCandidates(entries, p)
		if len(cands) == 0 {
			cands = fallbackCandidates(p, getenv)
		}
	}
	if len(cands) == 0 {
		return Install{ReasonCode: ReasonNotInstalled, Reason: "Creality Print was not found; install it or set CREALITY_SLICER_MCP_CMD to the path of CrealityPrint.exe"}, nil
	}
	for i := range cands {
		cands[i].resolveVersion(p)
	}
	// The highest supported version wins (7.2 and 7.3 side by side pick 7.3);
	// when none is supported the highest overall is reported with its reason.
	sort.SliceStable(cands, func(a, b int) bool {
		sa, sb := dialectFor(cands[a].version) != "", dialectFor(cands[b].version) != ""
		if sa != sb {
			return sa
		}
		return compareVersions(cands[a].version, cands[b].version) > 0
	})

	best := cands[0]
	in := Install{
		Found:   true,
		Version: best.version,
		Build:   best.build,
		Dir:     best.dir,
		Exe:     best.exe,
	}
	for _, c := range cands[1:] {
		in.Others = append(in.Others, c.dir)
	}
	major, minor := versionParts(in.Version)
	if major >= 0 && getenv("APPDATA") != "" {
		dataDir := filepath.Join(getenv("APPDATA"), "Creality", "Creality Print", appDataVersion(major, minor))
		if p.FS.IsDir(dataDir) {
			in.DataDir = dataDir
		}
	}
	readProfiles(&in, p.FS)
	if d := dialectFor(in.Version); d != "" {
		in.Dialect = d
		in.Supported = true
	} else {
		in.ReasonCode = ReasonUnsupportedVersion
		in.Reason = fmt.Sprintf("version %s is not supported yet; supported: 7.2.x and 7.3.x", displayVersion(in.Version))
	}
	if p.Processes != nil {
		if pids, err := p.Processes.PIDsByName(GUIProcessName); err == nil && len(pids) > 0 {
			sort.Ints(pids)
			in.GUIRunning, in.GUIPIDs = true, pids
		}
	}
	return in, nil
}

func displayVersion(v string) string {
	if v == "" {
		return "unknown"
	}
	return v
}

type candidate struct {
	dir, exe string
	regVer   string // DisplayVersion of the matching registry entry
	version  string
	build    string
}

var dirVersionRE = regexp.MustCompile(`\d+\.\d+`)

// resolveVersion fills version and build. On the installed 7.2.2 the exe's
// FileVersion string is only the build number ("5483"; its fixed numeric
// version says 7.0.0.0), so the version comes from, in order: a dotted
// FileVersion with three or more parts (7.2.2.5483), the registry
// DisplayVersion, and the "<major>.<minor>" in the install folder name
// ("Creality Print 7.2").
func (c *candidate) resolveVersion(p Platform) {
	c.version = ""
	if p.FileVersion != nil {
		if fv, err := p.FileVersion.FileVersion(c.exe); err == nil {
			fv = strings.TrimSpace(fv)
			parts := strings.Split(fv, ".")
			switch {
			case len(parts) >= 3 && len(parseVersion(fv)) == len(parts):
				c.version = strings.Join(parts[:3], ".")
				c.build = strings.Join(parts[3:], ".")
			case len(parts) == 1 && len(parseVersion(fv)) == 1:
				c.build = fv
			}
		}
	}
	if c.version == "" {
		c.version = strings.TrimSpace(c.regVer)
	}
	if c.version == "" && p.FS != nil {
		// A portable or hand-placed install has no registry entry: the version is
		// in the slicer library next to the build number.
		c.version = findBuildVersion(p.FS, filepath.Join(c.dir, "CrealityPrint_Slicer.dll"), c.build)
	}
	if c.version == "" {
		c.version = dirVersionRE.FindString(filepath.Base(c.dir))
	}
}

// commandCandidate validates an explicit exe path.
func commandCandidate(cmd string, entries []UninstallEntry, p Platform) (candidate, string) {
	if !filepath.IsAbs(cmd) {
		return candidate{}, fmt.Sprintf("the configured command %q is not an absolute path", cmd)
	}
	if !p.FS.IsFile(cmd) {
		return candidate{}, fmt.Sprintf("the configured command %q does not exist or is not a file", cmd)
	}
	dir := filepath.Dir(cmd)
	c := candidate{dir: dir, exe: cmd}
	for _, e := range entries {
		if !hasProductPrefix(e.DisplayName) {
			continue
		}
		for _, d := range entryDirs(e) {
			if sameDir(d, dir) {
				c.regVer = e.DisplayVersion
			}
		}
	}
	return c, ""
}

// registryCandidates turns uninstall entries into installs whose exe exists.
func registryCandidates(entries []UninstallEntry, p Platform) []candidate {
	var out []candidate
	seen := map[string]bool{}
	for _, e := range entries {
		if !hasProductPrefix(e.DisplayName) {
			continue
		}
		for _, dir := range entryDirs(e) {
			exe := filepath.Join(dir, "CrealityPrint.exe")
			key := strings.ToLower(filepath.Clean(dir))
			if seen[key] || !p.FS.IsFile(exe) {
				continue
			}
			seen[key] = true
			out = append(out, candidate{dir: filepath.Clean(dir), exe: exe, regVer: e.DisplayVersion})
			break
		}
	}
	return out
}

// fallbackCandidates looks for "<Program Files>\Creality\Creality Print <maj.min>".
func fallbackCandidates(p Platform, getenv func(string) string) []candidate {
	roots := []string{}
	for _, env := range []string{"ProgramFiles", "ProgramW6432"} {
		if v := getenv(env); v != "" {
			roots = append(roots, v)
		}
	}
	if len(roots) == 0 {
		roots = append(roots, `C:\Program Files`)
	}
	var out []candidate
	seen := map[string]bool{}
	for _, root := range roots {
		for _, dir := range p.FS.Glob(filepath.Join(root, "Creality", ProductName+" *")) {
			exe := filepath.Join(dir, "CrealityPrint.exe")
			key := strings.ToLower(filepath.Clean(dir))
			if seen[key] || !p.FS.IsFile(exe) {
				continue
			}
			seen[key] = true
			out = append(out, candidate{dir: filepath.Clean(dir), exe: exe})
		}
	}
	return out
}

func hasProductPrefix(name string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(name)), strings.ToLower(ProductName))
}

// entryDirs are the directories an uninstall entry names: InstallLocation
// first, then the folder of the uninstaller.
func entryDirs(e UninstallEntry) []string {
	var dirs []string
	if loc := strings.Trim(strings.TrimSpace(e.InstallLocation), `"`); loc != "" {
		dirs = append(dirs, loc)
	}
	if exe := uninstallExe(e.UninstallString); exe != "" {
		dirs = append(dirs, filepath.Dir(exe))
	}
	return dirs
}

// uninstallExe extracts the program path of an UninstallString: a quoted path
// (with or without arguments after it) or an unquoted path ending in ".exe".
func uninstallExe(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if s[0] == '"' {
		if end := strings.Index(s[1:], `"`); end >= 0 {
			return s[1 : 1+end]
		}
		return strings.Trim(s, `"`)
	}
	if i := strings.Index(strings.ToLower(s), ".exe"); i >= 0 {
		return s[:i+4]
	}
	return s
}

func sameDir(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

// versionParts returns major and minor of "7.2.2"; -1 when unparsable.
func versionParts(v string) (major, minor int) {
	nums := parseVersion(v)
	if len(nums) < 2 {
		return -1, -1
	}
	return nums[0], nums[1]
}

func parseVersion(v string) []int {
	var nums []int
	for _, part := range strings.Split(strings.TrimSpace(v), ".") {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil {
			break
		}
		nums = append(nums, n)
	}
	return nums
}

// compareVersions compares dotted numeric versions; missing parts count as 0.
func compareVersions(a, b string) int {
	x, y := parseVersion(a), parseVersion(b)
	for i := 0; i < len(x) || i < len(y); i++ {
		var p, q int
		if i < len(x) {
			p = x[i]
		}
		if i < len(y) {
			q = y[i]
		}
		if p != q {
			if p > q {
				return 1
			}
			return -1
		}
	}
	return 0
}

// readProfiles fills the profile version fields.
func readProfiles(in *Install, fs FileSystem) {
	read := func(root string) string {
		data, err := fs.ReadFile(filepath.Join(root, "Creality.json"))
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
	installRoot := filepath.Join(in.Dir, "resources", "profiles")
	in.ProfileVersionInstall = read(installRoot)
	dataRoot := ""
	if in.DataDir != "" {
		dataRoot = filepath.Join(in.DataDir, "system")
		in.ProfileVersionData = read(dataRoot)
	}
	switch {
	case in.ProfileVersionInstall == "" && in.ProfileVersionData == "":
	case in.ProfileVersionData != "" && compareVersions(in.ProfileVersionData, in.ProfileVersionInstall) > 0:
		in.ProfileVersion, in.ProfileRoot = in.ProfileVersionData, dataRoot
	default:
		in.ProfileVersion, in.ProfileRoot = in.ProfileVersionInstall, installRoot
	}
}

// Detector caches the result of Detect for the life of the process.
type Detector struct {
	opts Options

	mu     sync.Mutex
	cached *Install
}

// NewDetector returns a Detector for opts.
func NewDetector(opts Options) *Detector { return &Detector{opts: opts} }

// Get returns the cached result, detecting on first use.
func (d *Detector) Get(ctx context.Context) (Install, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.cached != nil {
		return *d.cached, nil
	}
	return d.refreshLocked(ctx)
}

// Refresh detects again and replaces the cached result.
func (d *Detector) Refresh(ctx context.Context) (Install, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.refreshLocked(ctx)
}

func (d *Detector) refreshLocked(ctx context.Context) (Install, error) {
	in, err := Detect(ctx, d.opts)
	if err != nil {
		return in, err
	}
	d.cached = &in
	return in, nil
}

// opener is implemented by file systems that can stream a file; the version
// search in the slicer library needs it (the library is over 100 MB).
type opener interface {
	Open(path string) (io.ReadCloser, error)
}

func (osFS) Open(p string) (io.ReadCloser, error) { return os.Open(p) }

// findBuildVersion searches the slicer library for "<major>.<minor>.<patch>.<build>"
// (the build is the FileVersion string of the executable) and returns the
// dotted version without the build, "" when nothing matches. The file is
// streamed in blocks, never read whole.
func findBuildVersion(fs FileSystem, dll, build string) string {
	op, ok := fs.(opener)
	if !ok || build == "" || !fs.IsFile(dll) {
		return ""
	}
	f, err := op.Open(dll)
	if err != nil {
		return ""
	}
	defer f.Close()
	re := regexp.MustCompile(`(?:^|[^0-9.])(7\.\d{1,2}\.\d{1,2})\.` + regexp.QuoteMeta(build) + `(?:[^0-9]|$)`)
	const block = 1 << 20
	keep := len("7.99.99.") + len(build) + 2
	buf := make([]byte, block+keep)
	tail := 0
	for {
		n, err := f.Read(buf[tail:])
		if n > 0 {
			data := buf[:tail+n]
			if m := re.FindSubmatch(data); m != nil {
				return string(m[1])
			}
			tail = keep
			if len(data) < keep {
				tail = len(data)
			}
			copy(buf, data[len(data)-tail:])
		}
		if err != nil {
			return ""
		}
	}
}

// dialectFor names the command line dialect of a version, "" when the version
// is not supported.
func dialectFor(version string) string {
	major, minor := versionParts(version)
	switch {
	case major == 7 && minor == 2:
		return "v72"
	case major == 7 && minor == 3:
		return "v73"
	}
	return ""
}

// appDataVersion is the version folder the application keeps its data in under
// %APPDATA%\Creality\Creality Print: 7.2 still uses "7.0", 7.3 uses "7.3".
func appDataVersion(major, minor int) string {
	if major == 7 && minor >= 3 {
		return strconv.Itoa(major) + "." + strconv.Itoa(minor)
	}
	return strconv.Itoa(major) + ".0"
}
