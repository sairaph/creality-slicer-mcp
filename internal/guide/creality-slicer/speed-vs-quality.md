# Speed versus quality

Creality Print 7.2 or 7.3 on the K2 family. You trade time against detail and strength; the presets already pick sensible points.

## The quick way: pick another process preset

The K2 0.4 nozzle has processes for 0.08, 0.12, 0.16, 0.20, 0.24 and 0.28 mm layers. A taller layer is the fastest way to save time: 0.28 takes roughly two thirds of the time of 0.20 on most parts, at a visible cost in surface. Switch with set_presets (`process`), keeping your project changes with `keep_changes` true.

Compare two presets before you commit: get_preset with `compare_to` lists only the differences.

## Where the time goes

Read the slice result: `time_text` for the total, and get_slice_report `section` `layers` for the layer count. Time is roughly: number of layers, times perimeter length, times number of walls, plus infill, plus travel, plus colour changes.

Colour changes are expensive on the K2: each swap costs purge and travel to the prime tower. A multi-colour job with many swaps can take longer than the printing itself. Group colours by height where you can, and keep the number of changes down.

## Knobs that save time

- Fewer walls (`wall_loops`) and lower `sparse_infill_density` where strength allows.
- Taller `layer_height`, or a height range with thick layers in plain regions (set_height_ranges).
- Sparse infill patterns that print fast (`grid`, `lightning`).
- Prime tower size (`prime_tower_width`, `prime_volume`) for multi-colour jobs: a smaller tower saves filament and time, but too small a tower primes poorly.

## Knobs that cost quality when raised

Feed speeds: `outer_wall_speed`, `inner_wall_speed`, `sparse_infill_speed`, `travel_speed`. Raising the wall speeds gives more ringing and rougher surfaces. Infill speed can go up cheaply until the filament's flow limit is reached.

## The flow limit

`filament_max_volumetric_speed` (in the filament preset, mm^3/s) caps how fast plastic can melt. If a speed setting asks for more flow than the limit, the slicer slows the move. Raising a speed above what the limit allows changes nothing; raising the limit beyond what the hot end can melt gives under-extrusion. PLA on the K2 is comfortable at high values; PETG and ABS take lower ones.

## Acceleration and jerk

In 7.2 these are mostly locked or clamped to what the machine supports. You cannot buy speed there; do not try to override them.

## Slow for no visible reason

A low `filament_max_volumetric_speed` in the filament preset is the usual reason a print is slower than expected. Read it with get_preset and compare it with the speeds in the sliced result (get_slice_report).

## Recipes

Draft: the `0.28mm Standard` process with

```json
{"project": "prototype", "values": {"wall_loops": 2, "sparse_infill_density": "10%", "sparse_infill_pattern": "lightning"}}
```

Quality: the `0.12mm Standard` process and `outer_wall_speed` 40.

## Check

After slicing, compare `time_s` and `total_g`. If time did not fall as expected, look at the layer count and the number of tool changes first.
