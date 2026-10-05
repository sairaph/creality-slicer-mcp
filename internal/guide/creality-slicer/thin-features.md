# Thin features

Creality Print 7.3, K2, 0.4 nozzle, PETG, 0.20 mm layers. Field results from printed test plates: tines, comb teeth and narrow tips from 0.4 to 2.0 mm wide. P = confirmed by a print, G = seen in the sliced G-code.

## Rules

- NEVER print a feature whose first layer is not on the bed or on printed material. Check every thin plate with analyze_toolpaths, measure `first_layers` and `unsupported_starts`, before handing it to the printer.
- Features under about 2 line widths (0.8 mm): use variable-width walls (the recipe below), not classic walls.
- Judge a technique on the sliced G-code first (analyze_toolpaths), then with a small test plate. Do not promise a result the G-code does not show.

## Walls: classic or variable width

- Classic walls (`wall_generator` `classic`, the K2 default) skip a feature narrower than one line width. A 0.4 mm feature does not print at all (P).
- Worse, classic walls skip a sub-line-width feature while it is joined to a larger area, then print it once it becomes its own island. On rising tines the 0.4 mm tip started at layer 6 in mid-air, and all 4 such tines failed (P). analyze_toolpaths `unsupported_starts` finds this.
- Variable width (`wall_generator` `arachne`) with `min_bead_width` and `initial_layer_min_bead_width` at 50 % and `min_feature_size` at 10 % prints 0.4 mm features from layer 1. Each comb tooth becomes one pass on layer 1, and passes ending at a tip drop from 1005 to 145 (G).
- At the default 85 % bead width, variable width draws a narrowing tooth as a main pass plus a separate 2 mm tip piece on layer 1. Narrower first-layer lines do not fix that (G).
- Classic walls still give crisper square tips on flat bars and the cleanest round rods at 0.8 mm and up (P). There is no single best technique: pick per shape.

## Gap fill and tip dots

- Gap fill puts about 0.2 mm dots on narrow tips (G). `gap_fill_target` `nowhere` removes them with variable-width walls; classic walls still fill thin gaps with walls (G).
- Variable width at 50 % still leaves tip dots on constant-width 1.2 mm flat tines; tapered teeth had none (G). analyze_toolpaths `short_runs` counts runs under `min_run` mm.

## Lifting tips

- Thin PETG tines (1.2 to 1.6 mm) lift at the tip on layer 2 at default speeds (P).
- This helped without fully stopping it, per object: `outer_wall_speed` 60, `inner_wall_speed` 80, `outer_wall_acceleration` and `inner_wall_acceleration` 2000, `elefant_foot_compensation` 0.05. For the whole project (update_settings without scope): `initial_layer_speed` 40 and `slow_down_layers` 3 (P).
- The slowed variant gives more even tine width than the same recipe without the slow-down (P).
- `elefant_foot_compensation` 0.05 instead of 0.15 brings layer 1 0.1 to 0.2 mm closer to the tip (G).

## Seam

- An aligned seam can land on a tooth tip. `seam_position` `back`, with the teeth pointing to the front, keeps it on the spine (G).

## Tip flare

- Thin tine ends print 0.1 to 0.15 mm wider than the body; the last 2 mm of a 0.4 mm tine flare most (P).
- It is not in the G-code: plastic per mm in the last 2 mm stays within 3 % of the body for every speed, wall order and flow tried (G). Only a model change helps: narrow the last 2 mm (13 to 15 % less on 0.4 mm). Tell the user it is a model fix, not a slicer setting.

## K2 multi-colour

- `flush_into_infill` does nothing on the K2: the firmware purges every CFS swap into the chute (G). Do not offer it to save filament.

## Recipe: variable-width thin features

Apply it to the thin objects only. update_settings with `targets` covers many objects in one call:

```json
{"project": "tines", "scope": "object", "targets": ["comb 1", "comb 2"], "values": {"wall_generator": "arachne", "min_bead_width": "50%", "initial_layer_min_bead_width": "50%", "min_feature_size": "10%", "gap_fill_target": "nowhere", "seam_position": "back"}}
```

Then check the slice:

```json
{"project": "tines", "measure": ["first_layers", "unsupported_starts", "short_runs"]}
```
