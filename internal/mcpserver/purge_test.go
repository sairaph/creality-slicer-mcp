package mcpserver

import (
	"strings"
	"testing"

	"github.com/sairaph/creality-slicer-mcp/internal/projects"
)

func TestPurgeLine(t *testing.T) {
	if got := purgeLine(projects.PlateResult{}); got != "" {
		t.Fatalf("single colour: %q", got)
	}
	got := purgeLine(projects.PlateResult{PrimeTowerG: 2.2, PrimeTowerS: 90, FlushG: 22.4, FlushChanges: 30, FlushEstimated: true})
	for _, want := range []string{"prime tower 2.2 g", "flush about 22.4 g over 30 changes", "estimate"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q lacks %q", got, want)
		}
	}
}
