package threemf

import "math"

// PlateGap is the gap between the plates of the scene, as a fraction of the
// plate size (PartPlate.cpp LOGICAL_PART_PLATE_GAP = 1/5).
const PlateGap = 1.0 / 5.0

// PlateCols is the number of columns the application lays count plates out in:
// the square root of the count, rounded up (PartPlate.hpp compute_colum_count).
func PlateCols(count int) int {
	if count < 1 {
		return 1
	}
	v := math.Sqrt(float64(count))
	r := math.Round(v)
	if v > r {
		return int(r) + 1
	}
	return int(r)
}

// PlateOrigin is the position of a plate in the scene of a project. The build
// item transforms of a Creality Print / Bambu Studio project are all in one
// scene in which plate i (0 based) sits at column i mod cols, row i div cols,
// cols = PlateCols(count): x = col * width * 1.2 and y = -row * depth * 1.2
// (PartPlate.cpp compute_origin, compute_shape_position). The slicer subtracts
// the origin, so G-code and everything the tools show is plate relative. plate
// is 1 based like Plate.Index; width and depth are the printable area size in
// mm, rounded to whole millimetres as the application does. The result is x, y.
func PlateOrigin(plate, count int, width, depth float64) [2]float64 {
	if plate < 1 {
		plate = 1
	}
	cols := PlateCols(count)
	i := plate - 1
	row, col := i/cols, i%cols
	return [2]float64{float64(col) * width * (1 + PlateGap), -float64(row) * depth * (1 + PlateGap)}
}
