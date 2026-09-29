package main

import (
	"os"
	"testing"

	"github.com/sairaph/creality-slicer-mcp/internal/userhome/testhome"
)

// TestMain isolates the home directory: install and uninstall resolve the
// guide skill folders through it.
func TestMain(m *testing.M) { os.Exit(testhome.Run(m)) }
