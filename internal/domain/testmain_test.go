package domain

import (
	"os"
	"testing"

	"github.com/sairaph/creality-slicer-mcp/internal/userhome/testhome"
)

// TestMain isolates HOME/USERPROFILE for the whole package run
// (internal/userhome/testhome), so no test can reach the developer's real
// per-user directory even if it forgets t.Setenv.
func TestMain(m *testing.M) {
	os.Exit(testhome.Run(m))
}
