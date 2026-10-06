package main

// The interactive app opens when the binary is run bare in a terminal. The
// screens are in internal/appui; the data and the actions behind them are
// realBackend (app_backend.go), which runs on the same server object as the
// MCP tools, so the app and the tools never disagree.

import (
	"context"

	"github.com/sairaph/mcp-wizard/app"

	"github.com/sairaph/creality-slicer-mcp/internal/appui"
	"github.com/sairaph/creality-slicer-mcp/internal/domain"
)

func runApp(ctx context.Context) int {
	model := appui.New(appui.Options{
		Backend:      &realBackend{},
		Version:      version,
		ConfigureCmd: configureCmd,
		Unicode:      appui.UnicodeTerminal(),
	})
	return app.Run(ctx, model, app.Options{Title: domain.BinaryName, Version: version})
}
