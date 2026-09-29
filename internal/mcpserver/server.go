// Package mcpserver provides the MCP server setup and tool registration.
package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sairaph/creality-slicer-mcp/internal/domain"
)

// Server wraps the MCP server.
type Server struct {
	mcpServer *mcp.Server
	config    Config
	env       *env
}

// New creates a new MCP server with the given config and registers its tools.
func New(config Config) *Server {
	return newServer(config)
}

// newServer is New's implementation, taking optional extra registration funcs
// run after every real tool group. Tests use it (never New directly) to
// register the sample tools of sample_tools_test.go, so they never ship.
func newServer(config Config, extra ...func(*Server)) *Server {
	config.Deps = config.Deps.withDefaults(config)
	srv := &Server{
		config: config,
		env:    newEnv(config.Deps),
		mcpServer: mcp.NewServer(
			&mcp.Implementation{
				Name:    domain.ServerName,
				Title:   "Creality Print slicer",
				Version: config.Version,
			},
			&mcp.ServerOptions{
				Instructions: serverInstructions,
				// The tools capability is advertised while the tool list is
				// still empty, so clients can always list tools.
				Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{}},
			},
		),
	}

	srv.registerStatusTools()
	srv.registerSettingsTools()
	srv.registerPresetTools()
	srv.registerProjectTools()
	srv.registerEditTools()
	srv.registerSliceTools()
	srv.registerViewTools()

	for _, register := range extra {
		register(srv)
	}
	srv.mcpServer.AddReceivingMiddleware(invalidArguments)
	srv.mcpServer.AddReceivingMiddleware(recoverPanics)

	return srv
}

// MCPServer exposes the underlying server, for tests.
func (s *Server) MCPServer() *mcp.Server { return s.mcpServer }

// Run starts the server and blocks until ctx is cancelled.
func (s *Server) Run(ctx context.Context) error {
	// Slice jobs end with the server, on either transport.
	defer s.env.stopJobs()
	switch s.config.Transport {
	case "http":
		return s.runHTTP(ctx)
	default:
		return s.runStdio(ctx)
	}
}

func (s *Server) runStdio(ctx context.Context) error {
	return s.mcpServer.Run(ctx, &mcp.StdioTransport{})
}

func (s *Server) runHTTP(ctx context.Context) error {
	addr := s.config.HTTPAddr
	if addr == "" {
		addr = "127.0.0.1:8080"
	}

	handler := mcp.NewStreamableHTTPHandler(
		func(r *http.Request) *mcp.Server {
			return s.mcpServer
		},
		&mcp.StreamableHTTPOptions{},
	)

	httpServer := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// done ends the shutdown goroutine when ListenAndServe returns on its own
	// (the port is in use, say), so it never waits on ctx forever.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
		case <-done:
			return
		}
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			fmt.Fprintf(os.Stderr, "error shutting down HTTP server: %v\n", err)
		}
	}()

	fmt.Fprintf(os.Stderr, "creality-slicer-mcp listening on %s (Streamable HTTP)\n", addr)
	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve http: %w", err)
	}
	return nil
}
