// Package domain holds the identity, per-user folders and runtime settings of
// creality-slicer-mcp. It does not talk to Creality Print; that is
// internal/slicer.
package domain

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/sairaph/creality-slicer-mcp/internal/userhome"
)

// Identity used by install, doctor and update. Owner and Repo point at the
// GitHub repository that publishes releases. ServerName is the key the AI
// clients' configs use for this server.
const (
	ServerName = "creality-slicer-mcp"
	BinaryName = "creality-slicer-mcp"
	Owner      = "sairaph"
	Repo       = "creality-slicer-mcp"
)

// Environment variables read by the MCP server at startup. TRANSPORT and ADDR
// (see DefaultEnv) belong to the framework and are read in main.go.
const (
	// EnvCmd is the optional absolute path of CrealityPrint.exe. Unset means
	// the installed Creality Print is detected.
	EnvCmd = "CREALITY_SLICER_MCP_CMD"
	// EnvTransport selects how MCP is served: stdio (default) or http.
	EnvTransport = "TRANSPORT"
	// EnvAddr is the listen address when the transport is http.
	EnvAddr = "ADDR"
	// EnvOnlyTextFeedback, when true (1, true, yes or on), leaves the automatic
	// screenshots out of the replies of the tools that change a project;
	// get_view still draws.
	EnvOnlyTextFeedback = "CREALITY_SLICER_MCP_ONLY_TEXT_FEEDBACK"
)

// DefaultHTTPAddr is the listen address of the http transport when ADDR is unset.
const DefaultHTTPAddr = "127.0.0.1:8080"

// AssetName maps a GOOS/GOARCH pair to the release asset published by
// .goreleaser.yml, which names binaries <project>-<os>-<arch>[.exe].
func AssetName(goos, goarch string) string {
	name := fmt.Sprintf("%s-%s-%s", BinaryName, goos, goarch)
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

// DefaultEnv returns the environment variables written into AI client
// configs when the server is registered. The server reads TRANSPORT (stdio
// or http) and ADDR (listen address for http) at startup. Users add the
// settings of docs/configuration.md to the entry's env block themselves;
// install keeps such an edited entry.
func DefaultEnv() map[string]string {
	return map[string]string{
		"TRANSPORT": "stdio",
	}
}

// dataDirName is the per-user directory under the home directory.
const dataDirName = "." + BinaryName

// DataDir is creality-slicer-mcp's per-user directory, ~/.creality-slicer-mcp.
// It resolves the path only: a caller that merely reads must not bring the
// directory into existence.
func DataDir() (string, error) {
	home, err := userhome.Dir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, dataDirName), nil
}

// CacheDir is where per-run scratch files go, DataDir()/cache. Runs remove
// what they put there when they end.
func CacheDir() (string, error) {
	dir, err := DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "cache"), nil
}

// ProjectsDir is the store of projects, DataDir()/projects, one folder per
// project.
func ProjectsDir() (string, error) {
	dir, err := DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "projects"), nil
}

// InstallDir is where install.ps1 and install.sh put the binary:
// %LOCALAPPDATA%\creality-slicer-mcp\bin on Windows, ~/.creality-slicer-mcp/bin
// elsewhere. LOCALAPPDATA is read directly, outside the userhome guard, as the
// install script does; testhome points it into the temporary home, so tests
// never reach the real one.
func InstallDir() (string, error) {
	home, err := userhome.Dir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	if runtime.GOOS == "windows" {
		local := os.Getenv("LOCALAPPDATA")
		if local == "" {
			local = filepath.Join(home, "AppData", "Local")
		}
		return filepath.Join(local, Repo, "bin"), nil
	}
	return filepath.Join(home, dataDirName, "bin"), nil
}

// Settings is the MCP server's runtime configuration.
type Settings struct {
	// Cmd is the CrealityPrint.exe named by CREALITY_SLICER_MCP_CMD; empty
	// means detect the installed Creality Print.
	Cmd string
	// Transport is "stdio" or "http" (TRANSPORT; default stdio).
	Transport string
	// Addr is the listen address of the http transport (ADDR; default
	// DefaultHTTPAddr). It is only read, and validated, for http.
	Addr string
	// OnlyTextFeedback is CREALITY_SLICER_MCP_ONLY_TEXT_FEEDBACK: no automatic
	// screenshots in the replies of the changing tools (default false).
	OnlyTextFeedback bool
}

// SettingsFromEnv reads Settings from the environment. A bad value returns an
// error naming the variable, and the server then exits with status 2.
func SettingsFromEnv() (Settings, error) {
	s := Settings{Transport: "stdio", Addr: DefaultHTTPAddr}
	if v := strings.TrimSpace(os.Getenv(EnvTransport)); v != "" {
		switch t := strings.ToLower(v); t {
		case "stdio", "http":
			s.Transport = t
		default:
			return s, fmt.Errorf("%s: %q is not stdio or http", EnvTransport, v)
		}
	}
	if s.Transport == "http" {
		if v := strings.TrimSpace(os.Getenv(EnvAddr)); v != "" {
			if err := validateListenAddr(v); err != nil {
				return s, fmt.Errorf("%s: %w", EnvAddr, err)
			}
			s.Addr = v
		}
	}
	if v := strings.TrimSpace(os.Getenv(EnvOnlyTextFeedback)); v != "" {
		switch strings.ToLower(v) {
		case "1", "true", "yes", "on":
			s.OnlyTextFeedback = true
		case "0", "false", "no", "off":
		default:
			return s, fmt.Errorf("%s: %q is not 1 or 0 (true, false, yes, no, on, off also work)", EnvOnlyTextFeedback, v)
		}
	}
	if v := strings.TrimSpace(os.Getenv(EnvCmd)); v != "" {
		if err := ValidateCmd(v); err != nil {
			return s, fmt.Errorf("%s: %w", EnvCmd, err)
		}
		s.Cmd = v
	}
	return s, nil
}

// validateListenAddr checks a host:port listen address; the host may be empty
// (every interface).
func validateListenAddr(addr string) error {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("%q is not host:port (%v)", addr, err)
	}
	if n, err := strconv.Atoi(port); err != nil || n < 0 || n > 65535 {
		return fmt.Errorf("%q has no valid port (want 0 to 65535)", addr)
	}
	return nil
}

// ValidateCmd checks a CrealityPrint.exe path override: it must be absolute,
// exist and be a file.
func ValidateCmd(path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("%q is not an absolute path", path)
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%q does not exist", path)
		}
		return fmt.Errorf("%q cannot be read: %v", path, err)
	}
	if info.IsDir() {
		return fmt.Errorf("%q is a folder, not a file", path)
	}
	return nil
}
