package threemf

import "testing"

func TestPlateColsAndOrigins(t *testing.T) {
	for count, want := range map[int]int{0: 1, 1: 1, 2: 2, 3: 2, 4: 2, 5: 3, 9: 3, 10: 4, 16: 4, 17: 5, 36: 6} {
		if got := PlateCols(count); got != want {
			t.Errorf("PlateCols(%d) = %d, want %d", count, got, want)
		}
	}
	// A 260 x 260 bed: stride 312. Three plates lie in two columns.
	want := map[int][2]float64{1: {0, 0}, 2: {312, 0}, 3: {0, -312}}
	for plate, o := range want {
		if got := PlateOrigin(plate, 3, 260, 260); got != o {
			t.Errorf("plate %d of 3: %v, want %v", plate, got, o)
		}
	}
	// Adding a fifth plate moves the layout to three columns.
	if got := PlateOrigin(4, 4, 260, 260); got != [2]float64{312, -312} {
		t.Errorf("plate 4 of 4: %v", got)
	}
	if got := PlateOrigin(4, 5, 260, 260); got != [2]float64{0, -312} {
		t.Errorf("plate 4 of 5: %v", got)
	}
	// Non square bed.
	if got := PlateOrigin(2, 2, 300, 200); got != [2]float64{360, 0} {
		t.Errorf("%v", got)
	}
	if got := PlateOrigin(3, 4, 300, 200); got != [2]float64{0, -240} {
		t.Errorf("%v", got)
	}
}
