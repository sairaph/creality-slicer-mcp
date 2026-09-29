# Surface quality

Creality Print 7.2 or 7.3. What decides how a print looks, roughly in order of effect.

## Layer height

`layer_height` is the biggest lever. On a 0.4 nozzle the K2 presets run from 0.08 (fine, slow) through 0.20 (the standard) to 0.28 (fast, visible steps). Pick the preset first with set_presets (the process name carries the height), then fine-tune.

`initial_layer_print_height` is the first layer's height; leave it unless bed adhesion needs a change.

Variable layer height (thin layers on curves, thick on straight walls) is an app feature: see `gui-handoff`. In a project the same idea is a height range: set_height_ranges sets a different `layer_height` over a Z range, see `modifiers-and-ranges`.

## Seams

Every layer starts and ends somewhere, and that spot leaves a seam. `seam_position` chooses: `nearest`, `aligned` (a tidy vertical line), `aligned_back`, `back` (hide it at the rear), `random`. For visible parts, `back` or `aligned` plus turning the part so the seam faces away works well. Painting the seam onto a chosen edge is app-only.

## Top and bottom surfaces

- `ironing_type`: `top` or `topmost` adds a slow smoothing pass over top surfaces; `solid` irons every solid layer. It gives glassy tops at a large time cost. The value `no ironing` turns it off.
- More `top_shell_layers` hide the infill pattern that can show through thin tops.
- `elefant_foot_compensation` shrinks the first layer slightly so the bottom edge does not bulge.

## Fuzzy skin

`fuzzy_skin` roughens the outer wall for grip or a textured look: `none`, `external` (contour), `all`, `allwalls`. Painting fuzzy skin onto chosen areas is app-only (7.1 and later).

## Smooth walls

Slower wall speeds give cleaner surfaces: `outer_wall_speed`, `inner_wall_speed`. Overhangs are slowed automatically when `enable_overhang_speed` is on. See `speed-vs-quality`.

## Cooling and material

Small features need cooling and layer time; the filament preset carries the fan curve. A wet spool gives rough, stringy walls whatever the settings: dry it first. A wrong or optimistic `filament_max_volumetric_speed` gives under-extruded walls at speed.

## A quality recipe

```json
{"project": "figurine", "values": {"outer_wall_speed": 40, "seam_position": "back", "top_shell_layers": 6}}
```

Send that to update_settings, and choose the `0.12mm Standard` process with set_presets. Then check a layer image: get_slice_report with `section` `layer`, `color_by` `feature`.

## Things that look like settings but are not

- Ringing (ripples after corners) is mostly speed and acceleration. Acceleration is clamped in the app, so slow the walls instead.
- Z banding is mechanical or comes from wobbly layer heights.
- Blobs and zits come from the seam and from retraction; try another `seam_position` first.

## Ask, do not guess

For a setting you are unsure of, search_settings with `area` "process/Quality", then describe_setting for the meaning and range.
