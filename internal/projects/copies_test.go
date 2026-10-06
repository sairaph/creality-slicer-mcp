package projects

import (
	"runtime"
	"testing"
	"time"
)

// Many copies are placed in about linear time and never overlap or touch
// closer than the placement gap. The time is logged, not asserted.
func TestManyCopiesArePlacedQuickly(t *testing.T) {
	for _, tc := range []struct {
		n    int
		size float64
	}{{150, 10}, {400, 4}} {
		e := newEnv(t)
		info := e.newProject(t, "Many")
		start := time.Now()
		res, err := e.st.AddModel(info.ID, AddModelRequest{Path: writeSTL(t, "tiny", tc.size, tc.size, tc.size), Copies: tc.n})
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%d copies of %g mm: %v", tc.n, tc.size, time.Since(start))
		objs := res.Info.Objects
		if len(objs) != tc.n {
			t.Fatalf("%d objects, want %d", len(objs), tc.n)
		}
		for i := range objs {
			for j := i + 1; j < len(objs); j++ {
				dx := abs(objs[i].Position[0]-objs[j].Position[0]) - (objs[i].Size[0]+objs[j].Size[0])/2
				dy := abs(objs[i].Position[1]-objs[j].Position[1]) - (objs[i].Size[1]+objs[j].Size[1])/2
				if dx < PlacementGap-1e-3 && dy < PlacementGap-1e-3 {
					t.Fatalf("copies %d and %d are closer than the gap: dx %.2f dy %.2f", i, j, dx, dy)
				}
			}
		}
	}
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

// A bad bounding box (millions of mm wide) must not fill millions of grid cells.
func TestRectGridClipsHugeObstacles(t *testing.T) {
	usable := rect{10, 10, 250, 250}
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	start := time.Now()
	g := newRectGrid([]rect{{-1e6, -1e6, 1e6, 1e6}, {1e12, 1e12, 2e12, 2e12}, {100, 100, 120, 120}}, 5, usable)
	elapsed := time.Since(start)
	runtime.ReadMemStats(&after)
	if len(g.cells) > 12*12 {
		t.Fatalf("%d cells for a 240 mm bed", len(g.cells))
	}
	if grown := after.TotalAlloc - before.TotalAlloc; grown > 8<<20 || elapsed > time.Second {
		t.Fatalf("a huge obstacle took %v and %d bytes", elapsed, grown)
	}
	t.Logf("huge obstacle: %v, %d KiB allocated, %d cells", elapsed, (after.TotalAlloc-before.TotalAlloc)>>10, len(g.cells))
	if !g.hits(rect{50, 50, 60, 60}) {
		t.Error("the huge obstacle does not block the bed")
	}
}

// Two edges 0.4 micron apart stay two candidates (rounding to a micron merged
// them and could keep the one that does not fit); equal edges still merge.
func TestSortedEdgesKeepsNearlyEqualValues(t *testing.T) {
	got := sortedEdges([]float64{50, 50.0000004, 50, 20}, 10, 100)
	if len(got) != 3 {
		t.Fatalf("%d edges, want 3: %+v", len(got), got)
	}
	if got[0].v != 50.0000004 || got[1].v != 50 || got[2].v != 20 {
		t.Errorf("order %+v", got)
	}
}
