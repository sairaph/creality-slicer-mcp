# Modifiers, per-object settings and height ranges

Creality Print 7.2 or 7.3. Project settings apply to everything. Four narrower tools change a smaller region.

## Settings scopes

update_settings takes `scope`:

- `project`: the whole project (default).
- `object`: one object; `target` is its id or name. About 260 process settings can be set per object.
- `part`: one part or modifier of an object; `target` as `object/part`. Region settings only (walls, shells, infill, ironing, feature speeds and the like); the seam position is an object setting.
- `layer_range`: settings for a height band, set through set_height_ranges.
- `plate`: per-plate settings, set through manage_plates.

Some settings never work per object: print sequence (plate), the initial-layer speeds, `travel_speed`, prime tower keys, skirt keys, `timelapse_type`, and everything in the printer and filament presets. describe_setting lists a key's `scopes`; an update at a scope it does not support is rejected.

## Object settings

```json
{"project": "kit", "scope": "object", "target": "hinge", "values": {"wall_loops": 6, "sparse_infill_density": "40%"}}
```

Send that to update_settings. A value of `null` removes an override and the object falls back to the project.

## Modifiers and other parts

add_modifier adds a helper shape to an object:

- `kind` `modifier`: a region with different settings (`values`). Dense infill around a screw boss, slower walls on a detail.
- `kind` `negative_part`: subtracts the shape when slicing; the model file is unchanged.
- `kind` `support_enforcer` and `support_blocker`: force or forbid supports there.

`shape` is `box`, `cylinder` or `sphere`; `size` is [x, y, z] in mm; `position` is relative to the object's centre. Example, a stronger zone:

```json
{"project": "kit", "object": "hinge", "kind": "modifier", "shape": "cylinder", "size": [14, 14, 20], "position": [0, 0, 0], "values": {"sparse_infill_density": "80%", "wall_loops": 6}}
```

Remove a wrong modifier, negative part, enforcer or blocker with remove_part (project, object, part), which needs no more than the part's id or name from get_project; remove_object would delete the whole object. Only settings valid at part scope are accepted. Check the result: add_modifier returns a screenshot with the modifier drawn as a translucent volume, and get_project lists the parts with their size and centre. To look closer, call get_view with `focus` set to the object and the `Front` or `Right` view (they show heights, so you can see how far up the modifier reaches), or `Top` for the footprint. A modifier inside a model is drawn with its outline and a faint tint; where it sticks out it is drawn solid.

## Height ranges

set_height_ranges replaces the list of ranges on an object; an empty list clears it:

```json
{"project": "kit", "object": "vase", "ranges": [{"from_z": 0, "to_z": 10, "values": {"layer_height": 0.12}}, {"from_z": 10, "to_z": 60, "values": {"layer_height": 0.24}}]}
```

Ranges must not overlap. A range can change `layer_height` and the region settings. Use it for fine detail low down and fast layers above. Every range carries `layer_height`: the tools add it (the object's current layer height) when you leave it out, because a range without it crashes the slicer. get_view with `show_ranges` true draws the bands and lists each one's heights and values.

## Layer actions

set_layer_actions changes what happens at a height, per plate: `pause`, `color_change` or `tool_change` (both with `filament`, the slot to switch to) or `custom` (with `gcode`). The K2 has no colour change G-code, so a `color_change` is stored as a `tool_change`: the CFS switches spools by itself, which needs two or more filaments. The app applies such changes only when every object on the plate uses one filament; with mixed filaments they do nothing (the reply warns): give those objects a plate of their own. Give `z` or a `layer` number; layers are converted using the layer heights. A pause lets you insert a magnet or change material by hand; the printer waits until resumed. The slice reply lists the pauses, colour changes and custom G-code it found in the G-code, by height, so you can confirm they were written.

```json
{"project": "kit", "plate": 1, "actions": [{"z": 12.4, "type": "pause"}]}
```

## Filament per object and per feature

add_model and update_object take `filament` per object. Deeper control (a different filament for walls or infill) uses `wall_filament`, `sparse_infill_filament`, `solid_infill_filament` and `support_filament`; 0 means follow the object. Every extra filament adds purge cost: see `multicolor-cfs`.

## Limits

Painting exact regions, cut planes and boolean operations are app-only: `gui-handoff`. An imported .3mf keeps its own modifiers and painting; the tools show them but do not edit painted data.
