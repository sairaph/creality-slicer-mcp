package mcpserver

import "github.com/sairaph/creality-slicer-mcp/internal/domain"

// Config holds the MCP server configuration.
type Config struct {
	Version   string
	Transport string // "stdio" or "http"
	HTTPAddr  string // listen address for HTTP mode
	// Settings are the values read from the environment at startup
	// (domain.SettingsFromEnv).
	Settings domain.Settings
	// Deps are what the tools read from the machine; the zero value means the
	// real ones (see Deps).
	Deps Deps
}
