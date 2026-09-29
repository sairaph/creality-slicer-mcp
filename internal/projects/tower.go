package projects

import (
	"math"
	"strconv"
	"strings"

	"github.com/sairaph/creality-slicer-mcp/internal/catalog"
	"github.com/sairaph/creality-slicer-mcp/internal/render"
	"github.com/sairaph/creality-slicer-mcp/internal/threemf"
)

// wipe_tower_x and wipe_tower_y hold one value per plate, in scene
// coordinates like the build item transforms: the wipe tower of plate 2 sits
// at the plate's origin plus its place on the plate (verified with the slicer:
// a plate 2 tower given as plain plate coordinates lands a whole plate to the
// left). The tools show and accept plate relative positions and convert here.

// bedOf is the printable area of a project's settings as a rectangle.
func bedOf(cfg *threemf.Config) render.Rect {
	bed := render.DefaultBed
	if cfg == nil {
		return bed
	}
	pts := cfg.List("printable_area")
	if len(pts) < 3 {
		return bed
	}
	minX, minY, maxX, maxY := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for _, pt := range pts {
		xs, ys, found := strings.Cut(pt, "x")
		x, err1 := strconv.ParseFloat(xs, 64)
		y, err2 := strconv.ParseFloat(ys, 64)
		if !found || err1 != nil || err2 != nil {
			return bed
		}
		minX, maxX, minY, maxY = math.Min(minX, x), math.Max(maxX, x), math.Min(minY, y), math.Max(maxY, y)
	}
	return render.Rect{X0: minX, Y0: minY, X1: maxX, Y1: maxY}
}

// plateWH is the plate size in whole millimetres as the application lays the
// plates out with.
func plateWH(cfg *threemf.Config) (w, d float64) {
	b := bedOf(cfg)
	return math.Round(b.X1 - b.X0), math.Round(b.Y1 - b.Y0)
}

// towerDefault is the catalog default position of a wipe tower on its plate.
func towerDefault(cat *catalog.Catalog) [2]float64 {
	def := [2]float64{15, 220}
	for i, key := range []string{"wipe_tower_x", "wipe_tower_y"} {
		if o, ok := cat.Get(key); ok {
			if e := valElems(defaultValue(o)); len(e) > 0 {
				if f, err := strconv.ParseFloat(e[0], 64); err == nil {
					def[i] = f
				}
			}
		}
	}
	return def
}

// towerRelative reads the wipe tower positions of every plate as plate
// relative positions; a plate without an entry gets def.
func towerRelative(cfg *threemf.Config, plates int, def [2]float64) [][2]float64 {
	xs, ys := cfg.List("wipe_tower_x"), cfg.List("wipe_tower_y")
	w, d := plateWH(cfg)
	out := make([][2]float64, plates)
	for i := range out {
		o := threemf.PlateOrigin(i+1, plates, w, d)
		out[i] = def
		if i < len(xs) {
			if f, err := strconv.ParseFloat(xs[i], 64); err == nil {
				out[i][0] = f - o[0]
			}
		}
		if i < len(ys) {
			if f, err := strconv.ParseFloat(ys[i], 64); err == nil {
				out[i][1] = f - o[1]
			}
		}
	}
	return out
}

// setTowerScene writes plate relative wipe tower positions as the scene
// coordinates of a project with the given number of plates.
func setTowerScene(cfg *threemf.Config, plates int, rel [][2]float64) {
	w, d := plateWH(cfg)
	xs, ys := make([]string, len(rel)), make([]string, len(rel))
	for i, r := range rel {
		o := threemf.PlateOrigin(i+1, plates, w, d)
		xs[i], ys[i] = formatNumber(round6(r[0]+o[0])), formatNumber(round6(r[1]+o[1]))
	}
	cfg.SetList("wipe_tower_x", xs...)
	cfg.SetList("wipe_tower_y", ys...)
}

// plateVectorRelative turns the value of wipe_tower_x (axis 0) or wipe_tower_y
// (axis 1) into plate relative strings.
func plateVectorRelative(cfg *threemf.Config, key string, plates int) []string {
	axis := 0
	if key == "wipe_tower_y" {
		axis = 1
	}
	rel := towerRelative(cfg, plates, [2]float64{})
	out := make([]string, len(rel))
	for i, r := range rel {
		out[i] = formatNumber(round6(r[axis]))
	}
	return out
}

// towerSceneValues converts plate relative values of wipe_tower_x or
// wipe_tower_y to the scene coordinates the file holds.
func towerSceneValues(cfg *threemf.Config, key string, plates int, rel []string) []string {
	axis := 0
	if key == "wipe_tower_y" {
		axis = 1
	}
	w, d := plateWH(cfg)
	out := make([]string, len(rel))
	for i, s := range rel {
		f, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
		out[i] = formatNumber(round6(f + threemf.PlateOrigin(i+1, plates, w, d)[axis]))
	}
	return out
}
