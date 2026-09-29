// Package mcpserver provides the MCP server setup and tool registration.
package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sairaph/mcp-wizard/render"
)

// Server wraps the MCP server and domain logic.
type Server struct {
	mcpServer *mcp.Server
	config    Config
}

// New creates a new MCP server with the given config and registers its tools.
func New(config Config) *Server {
	srv := &Server{
		config: config,
		mcpServer: mcp.NewServer(
			&mcp.Implementation{
				Name:    "creality-slicer-mcp",
				Version: config.Version,
			},
			&mcp.ServerOptions{
				Capabilities: &mcp.ServerCapabilities{},
			},
		),
	}

	srv.registerGreet()
	// Register your tools here, one register* method per tool.

	return srv
}

// Run starts the server and blocks until ctx is cancelled.
func (s *Server) Run(ctx context.Context) error {
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
		Addr:    addr,
		Handler: handler,
	}

	go func() {
		<-ctx.Done()
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

// --- Example tool: greet ---
//
// Every tool follows this shape: a typed input struct whose jsonschema tags
// become the tool's input schema, a typed frontmatter struct rendered as
// YAML, and a Markdown body with guidance. Errors are returned as structured
// results via render.ErrorResult so the model gets a code and a hint rather
// than a bare failure. Delete this tool once you have your own.

type greetInput struct {
	Name string `json:"name" jsonschema:"the name to greet"`
}

type greetFront struct {
	Greeting string `yaml:"greeting"`
	Name     string `yaml:"name"`
}

func (s *Server) registerGreet() {
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "greet",
		Description: "Return a greeting for the given name. Use it to confirm the creality-slicer-mcp " +
			"server is reachable and responding. The name must not be empty; the result carries the " +
			"greeting in its frontmatter and a short message in the body.",
	}, s.handleGreet)
}

func (s *Server) handleGreet(ctx context.Context, req *mcp.CallToolRequest, in greetInput) (*mcp.CallToolResult, any, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return render.ErrorResult(render.Error{
			Code:    render.CodeInvalidInput,
			Message: "name must not be empty",
			Hint:    "Call greet with {\"name\": \"world\"}.",
		}), nil, nil
	}
	front := greetFront{Greeting: "Hello, " + name + "!", Name: name}
	body := fmt.Sprintf("Greeted %s from creality-slicer-mcp %s.", name, s.config.Version)
	return render.SuccessResult(front, body), nil, nil
}
