package projects

import (
	"strings"
	"testing"

	"github.com/sairaph/creality-slicer-mcp/internal/render"
)

func TestViewLegendNamesThePrimeTower(t *testing.T) {
	with := strings.Join(viewLegend(ViewRequest{}, &render.Scene{WipeTower: &render.Rect{X0: 1, Y0: 1, X1: 20, Y1: 20}}), "\n")
	if !strings.Contains(with, "PRIME TOWER") || !strings.Contains(with, "only with two or more filaments on a plate printed by layer") {
		t.Errorf("legend:\n%s", with)
	}
	without := strings.Join(viewLegend(ViewRequest{}, &render.Scene{}), "\n")
	if strings.Contains(without, "PRIME TOWER") {
		t.Errorf("a tower in the legend without one:\n%s", without)
	}
}
