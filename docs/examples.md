# Examples

[Back to README](../README.md) · [Installation](installation.md) · [Configuration](configuration.md) · [Tools](tools.md)

Seven worked flows, each as the tool calls an assistant makes, with short JSON arguments and what the replies contain. Every reply starts with a front matter block of named fields and continues with a text body that ends with the next call to make; the [tools reference](tools.md) lists every field. Paths are examples: use absolute paths on your computer. Project names become ids (`bracket` becomes something like `bracket-3fa9c1`); pass the id, or the exact name when it is unique.

Filament slots are numbered from 1 in the project tools (slot 1 is tool T0 in the G-code). The slice reply numbers the tools from 0, because that is what the printer server takes.

## 1. A first print in one colour

You ask: "Slice C:\Models\bracket.stl for my K2 in PLA, strong enough for a shelf."

1. `get_slicer_status` with `{}`. The reply says whether Creality Print was found (7.3 preferred, 7.2 works), the version, the projects folder and how many settings have a description. Anything not supported is said here, before any work.
2. `get_guide` with `{"topic": "start"}` when the assistant wants the workflow with the calls.
3. `create_project` with

   ```json
   {"name": "bracket", "filaments": [{"preset": "Hyper PLA @Creality K2 0.4 nozzle", "colour": "#FFFFFF"}]}
   ```

   The printer defaults to `Creality K2 0.4 nozzle` and the process to the printer's default. The reply carries the project id, the presets, the filament table and `plates: 1`, and says to call `add_model` next.
4. `add_model` with `{"project": "bracket", "path": "C:\\Models\\bracket.stl", "copies": 2}`. The reply lists each copy with its id, size in mm and position (placed automatically, measured from the corner of the plate). It warns when the size looks like the wrong unit and fails with the free area when a copy does not fit.
5. `get_view` with `{"project": "bracket", "view_name": "Top"}` draws the plate from above with each object's id and name; `Isometric` is the default, and `{"view_name": "Front", "focus": ["bracket"]}` frames one object from the front to check its height. `get_project` is text only: the objects table with sizes, positions and filaments. (`add_model` already attached a small screenshot of the plate to its reply; `include_screenshot` false leaves it out.)
6. "Strong enough" means walls, not infill. `describe_setting` with `{"key": "wall_loops", "project": "bracket"}` explains the setting, its range and dependencies, and shows the value in this project and where it comes from (the preset, until you change it).
7. `update_settings` with `{"project": "bracket", "values": {"wall_loops": 4, "sparse_infill_density": "25%"}}`. Every key and value is checked against the settings catalog first; the reply lists each change as old to new, plus any warning. A wrong key is an `invalid_input` error naming the key and the valid values, and nothing changes.
8. `slice_project` with `{"project": "bracket", "preview": "small"}`. The reply has a table with time, grams, layers, upload name and G-code path per plate, the tools table, the exclusion label of every object (for a part named `cube` it is `cube_id_0_copy_0`), the warnings and, last and most important, the numbered hand-off steps for the printer server, plus two small pictures: an isometric drawing of the sliced toolpaths in the filament colours, and the first layer of all objects. The call waits up to `wait` seconds (default 60). A slice that takes longer returns `state: running` and a `job_id`, and carries on in the background: call `get_slice_status` with `{"job_id": "<id>", "wait": 60}` until it says finished; the finished status reply is the same as the slice reply. `timeout` (default 1800) only stops a hung slicer, and a second `slice_project` on the same project is refused while its job runs.
9. `get_slice_report` with `{"project": "bracket", "section": "layer", "layer": 40, "color_by": "speed"}` returns the extrusion by feature for that layer and a picture coloured by speed.

## 2. Two colours on the CFS, and the hand-off to the printer

You ask: "Make it white with a black logo, and check my spools."

1. `list_presets` with `{"type": "filament", "filament_type": "PLA"}` shows the filament presets that fit the K2 0.4 nozzle. Filament type matters: the printer maps tools to CFS slots by material and colour.
2. `create_project` with

   ```json
   {"name": "logo", "filaments": [
     {"preset": "Hyper PLA @Creality K2 0.4 nozzle", "colour": "#FFFFFF"},
     {"preset": "Hyper PLA @Creality K2 0.4 nozzle", "colour": "#1E1E1E"}]}
   ```

3. `add_model` twice, for the base and for the logo: `{"project": "logo", "path": "C:\\Models\\base.stl", "filament": 1}` and `{"project": "logo", "path": "C:\\Models\\logo.stl", "filament": 2, "position": [130, 130]}`. `position` is `[x, y]` in mm from the corner of the object's plate.
4. Optional: `set_presets` with `{"project": "logo", "flush_multiplier": 0.8}` lowers the purge between colours. The reply shows the flush matrix (`auto` or `manual`) and its volumes. Passing `flush_matrix` sets it by hand and keeps it; `auto_flush` true goes back to the automatic one.
5. `slice_project` with `{"project": "logo"}`. The `handoff` in the reply has, per plate, `gcode_path`, `upload_name` (for example `logo_plate1.gcode`), the `tools` table (`T0` is filament 0 with its preset, type, colour and `filament_id`) and `exclude_names`, the labels the printer's exclusion takes; the body shows the same once, with each object next to its label.
6. Now the creality-k2-mcp server, as the reply's body spells out:
   - `get_filaments` with `{}` lists the loaded spools with their slots (`T1A` to `T4D`). Match each tool to a slot of the same material type and a close colour.
   - `upload_gcode_file` with `{"path": "<gcode_path>", "filename": "logo_plate1.gcode"}`.
   - `start_print` with

     ```json
     {"filename": "logo_plate1.gcode", "source": "cfs",
      "slot_map": [{"filament": 0, "slot": "T1A"}, {"filament": 1, "slot": "T1C"}]}
     ```

     This is call 1, without `confirm_token`: it sends nothing and returns a mapping proposal with warnings. Show the person the proposal and every warning, get their word that the spools are in place and the bed is clear, then call again with the same arguments plus the returned `confirm_token`.
   - During the print, `exclude_object` with `{"object_name": "base_id_0_copy_0"}` (a label from the reply; an object that came from the app may be named like the file, for example `cube.stl_id_0_copy_0`) skips a failed object.

## 3. A functional part: stronger only where it matters

You ask: "Reinforce this hinge around the pin, and pause at 12 mm to drop in a magnet."

1. `create_project` and `add_model` as in the first flow, with the object named `hinge`: `{"project": "kit", "path": "C:\\Models\\hinge.stl", "name": "hinge"}`.
2. Walls for the whole object: `update_settings` with

   ```json
   {"project": "kit", "scope": "object", "target": "hinge", "values": {"wall_loops": 6, "sparse_infill_density": "40%"}}
   ```

3. Denser infill around the pin only: `add_modifier` with

   ```json
   {"project": "kit", "object": "hinge", "kind": "modifier", "shape": "cylinder",
    "size": [14, 14, 20], "position": [0, 0, 0],
    "values": {"sparse_infill_density": "80%", "wall_loops": 6}}
   ```

   `position` is relative to the object's centre. The reply carries a screenshot with the cylinder as a translucent yellow volume; `get_view` with `{"project": "kit", "view_name": "Front", "focus": ["hinge"]}` shows how far up it reaches, and `"view_name": "Right"` the same from the side. The values are checked at part scope. A `support_enforcer` or `support_blocker` kind takes no values (giving some is an error; an enforcer needs `enable_support` on), and a `negative_part` cuts the object.
4. Finer layers in a band: `set_height_ranges` with `{"project": "kit", "object": "hinge", "ranges": [{"from_z": 0, "to_z": 10, "values": {"layer_height": 0.12}}]}`. The list replaces the object's current ranges; an empty list clears them. Ranges must not overlap. `get_view` with `{"project": "kit", "view_name": "Front", "focus": ["hinge"], "show_ranges": true}` draws each range as a tinted band on the object.
5. The pause: `set_layer_actions` with `{"project": "kit", "plate": 1, "actions": [{"z": 12.4, "type": "pause"}]}`. Give `z` in mm or `layer` from 1; a `color_change` or `tool_change` also needs `filament` (on the K2 a colour change is stored as a tool change: the CFS switches spools), a `custom` action needs `gcode`. The list replaces the plate's actions.
6. `get_project` shows the object with its override count and lists its parts with their kind, size and centre. `slice_project` with `{"project": "kit"}`, then `get_slice_report` with `{"project": "kit", "section": "settings"}` lists the effective settings that differ from the process preset, to confirm the changes reached the slicer.

## 4. Open a project from the app, tweak it, slice it

You ask: "Open my saved lamp.3mf, use 4 walls, and slice it."

1. `open_project` with `{"path": "C:\\Models\\lamp.3mf", "name": "lamp", "preview": "small"}`. The file is copied into the store and never changed. The reply reports printer, process, filaments, plates, objects, objects with painted data (kept as they are; only the app edits them) and warnings: another printer than the K2 family, a file from a newer app version, presets missing on this machine. `preview` `small` attaches the picture the app stored in the file (there is no large one). A file that is not a Creality Print or Bambu-family project is refused with a hint to use `add_model`.
2. `update_settings` with `{"project": "lamp", "values": {"wall_loops": 4}}`.
3. `slice_project` with `{"project": "lamp", "preview": "small"}`.
4. To look at it or paint supports in the app: `export_project` with `{"project": "lamp", "path": "C:\\Models\\lamp-tuned.3mf"}` (add `"overwrite": true` to replace an existing file). The exported file is not linked to the store: after saving changes in the app, call `open_project` on it again.

From the command line, `creality-slicer-mcp slice C:\Models\lamp.3mf --out C:\Models\gcode` does steps 1 and 3 without changing the file and copies the G-code to the folder.

## 5. Several plates in one project

You ask: "Print three of these on a second plate as well, and slice both."

1. `manage_plates` with `{"project": "kit", "action": "add", "name": "second batch"}` returns the plate table.
2. `add_model` with `{"project": "kit", "path": "C:\\Models\\clip.stl", "plate": 2, "copies": 3}` places the copies on plate 2. Positions are measured from the corner of each object's own plate, so the same numbers mean the same spot on every plate.
3. Plate settings: `manage_plates` with `{"project": "kit", "action": "set", "plate": 2, "values": {"print_sequence": "by object"}}` prints plate 2 one object at a time. `lock` and `unlock` set the plate's lock flag, which the app honours when it arranges (the `arrange` option of `slice_project` ignores it); `rename` and `remove` work as their names say, and removing a plate that still has objects is refused.
4. `slice_project` with `{"project": "kit", "plate": 0, "wait": 300}` slices every plate that has objects and waits up to five minutes for the result. The reply has one table row and one handoff entry per plate, with upload names `kit_plate1.gcode` and `kit_plate2.gcode`. `plate` 1 or 2 slices only that plate.
5. `get_slice_report` with `{"project": "kit", "plate": 2, "section": "filaments"}` gives the grams, millimetres and cubic centimetres per tool for plate 2. If you change the project after slicing, the report says it is older than the project: slice again.

## 6. One object in two colours, from a CAD plate

You ask: "Here is my FreeCAD plate with the screw and its markings. Keep the layout, make the markings white."

1. `add_model` with `{"project": "dial", "path": "C:\\Models\\dial_plate.3mf", "keep_positions": true, "names": ["screw", "top marks", "bottom marks"]}`. `keep_positions` places every object at the position it has in the file instead of arranging it; `names` names the objects in file order (CAD exports often carry no names, and they would become `dial_plate 1`, `dial_plate 2`, ...). Objects outside the bed are added with a warning.
2. `group_objects` with `{"project": "dial", "objects": ["screw", "top marks", "bottom marks"], "name": "dial screw"}`. The first object is the base; the others become its parts, each keeping its place and the filament it had. The reply lists the parts with their filaments and attaches a picture.
3. `update_settings` with `{"project": "dial", "scope": "part", "targets": ["dial screw/top marks", "dial screw/bottom marks"], "values": {"extruder": 2}}` prints both mark parts with filament 2. `extruder` also works per height range (`set_height_ranges`, a range's `values`); 0 means the object's own filament.
4. `slice_project`, then `get_slice_report` with `{"project": "dial", "section": "layer", "layer": 1, "color_by": "filament"}` shows which filament prints where, with grams per tool for that layer.

A colour change in height instead (stripes): `set_layer_actions` with `{"project": "dial", "actions": [{"layer": 20, "type": "color_change", "filament": 2}]}`. On the K2 with the CFS it is stored as a tool change that the printer performs by itself. Creality Print applies layer filament changes only when every object on the plate prints with one filament, and writes no layer actions at all on a plate printed by object with several objects; the replies warn in both cases, and the slice reply reports each action as found in the G-code or ignored.

## 7. Check thin features before printing

You ask: "Will these thin tines print? Fix what would fail."

1. `get_guide` with `{"topic": "thin-features"}`: field results for thin features on the K2 (classic against variable-width walls, tip dots, lifting tips, the recipe below).
2. `slice_project` with `{"project": "tines"}`, then `analyze_toolpaths` with `{"project": "tines", "measure": ["first_layers", "unsupported_starts", "short_runs"]}`. It reads the sliced G-code per object: the first and last layer of each feature, extrusion that starts in mid-air (a feature first printed above nothing, which fails), and short runs that leave dots on tips. Findings come first; long answers are paged, and every page gives each measure that still has rows a share.
3. If a thin feature starts in mid-air, switch those objects to variable-width walls in one call: `update_settings` with `{"project": "tines", "scope": "object", "targets": ["tine A", "tine B"], "values": {"wall_generator": "arachne", "min_bead_width": "50%", "initial_layer_min_bead_width": "50%", "min_feature_size": "10%"}}`.
4. Slice again and repeat step 2 until `unsupported_starts` has no findings. Other measures: `bounds` (outline per layer), `flow`, `radius` around a `center`, `support_contacts` (support patches and the object printed on each), `wall_order`.
