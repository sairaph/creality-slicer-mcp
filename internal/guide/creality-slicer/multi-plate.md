# Several plates in one project

Creality Print 7.2 or 7.3. A project can hold many plates, each a separate print of up to a bed's worth of objects. The K2 bed is a 260 mm square. Positions in every tool are relative to the plate the object is on: x and y run from the plate's own corner, the same on every plate (moving an object to another plate keeps its position); the prime tower position is plate-relative too.

## Add and arrange

manage_plates does all plate work:

```json
{"project": "kit", "action": "add", "name": "second batch"}
```

`action` is `add`, `remove`, `rename`, `lock`, `unlock` or `set`. Removing a plate that still holds objects is refused: move or remove the objects first.

Put an object on a plate with add_model (`plate`) or update_object (`plate`). If an object does not fit, add_model says so and suggests another plate or a smaller scale.

## Per-plate settings

Only a few keys are per plate, set with `action` `set` and `values`: `curr_bed_type` (the bed surface: `Cool Plate`, `Textured PEI Plate` and so on), `print_sequence` (`by layer` or `by object`), `first_layer_print_sequence`, `other_layers_print_sequence`, `spiral_mode`.

```json
{"project": "kit", "action": "set", "plate": 2, "values": {"print_sequence": "by object"}}
```

`by object` prints one object completely, then the next. It needs enough head clearance between objects, and the app checks for collisions; `by layer` is the default and needs no clearance.

Pick the bed surface that the filament allows: a filament not permitted on a plate's bed type fails the slice (exit code -61).

## Slicing

slice_project with `plate` 0 slices every plate; `plate` N slices one. The result lists one G-code file per plate, named `plate_N.gcode` in the project folder, with the summary of each. Send them to the printer one at a time: each has its own `upload_name` in the handoff.

`arrange` true makes the tools re-pack the objects of the plate and save the new positions in the project. The plate lock is ignored by it: `lock` only marks the plate for the app, which skips locked plates when it arranges. Keep a finished layout by not asking for `arrange`.

```json
{"project": "kit", "action": "lock", "plate": 1}
```

## Reporting

get_slice_report has a `plate` parameter; the default is plate 1. Totals across plates are yours to add: sum `total_g` and `time_s` from each plate's summary.

## Things to know

- Each plate is a separate print job for the printer: a multi-colour project with several plates needs its own spool check per plate.
- Changing anything on a plate discards its earlier slice result; slice again.
- Plate numbers start at 1.
