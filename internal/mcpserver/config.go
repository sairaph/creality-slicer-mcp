package mcpserver

// Config holds the MCP server configuration.
type Config struct {
	Version   string
	Transport string // "stdio" or "http"
	HTTPAddr  string // listen address for HTTP mode
}
