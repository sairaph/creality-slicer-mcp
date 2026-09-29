# Start: from a model to G-code

The shortest safe path for a Creality K2 or K2 Combo with the installed Creality Print (7.2 or 7.3; 7.3 is preferred and needs nothing extra from you or the user). Every step is one tool call.

## 1. Check the installation

Call get_slicer_status. It reports the app version, whether it is supported, and where projects are stored. Slicing needs a supported install; everything else works without.

## 2. Create or open a project

New project from a mesh:

```json
{"name": "bracket", "filaments": [{"preset": "Hyper PLA @Creality K2 0.4 nozzle", "colour": "#FFFFFF"}]}
```

Send that to create_project. The printer defaults to `Creality K2 0.4 nozzle` and the process to the printer's default (`0.20mm Standard`). At least one filament with a `#RRGGBB` colour is required. Use list_presets (type `filament`, `filament_type` "PLA") to find exact preset names.

Existing project: open_project with the .3mf path. The file is copied; your original is untouched. The reply lists painted objects and whether the file already holds a slice.

## 3. Add models

```json
{"project": "bracket", "path": "C:/models/bracket.stl", "copies": 2}
```

add_model places objects automatically unless you give `position` (x, y in mm from the corner of the object's own plate). Look at the screenshot in the reply (or call get_view: `Top` shows the layout, `Front` and `Right` show heights; `focus` frames one object). Read the reply: it warns about suspicious sizes (a mesh a few millimetres wide is probably in metres or inches) and about objects that do not fit. Use update_object with `lay_flat` true to put the largest flat face down.

## 4. Check presets

get_project shows printer, process and filaments. To change them, call set_presets. Changing a preset keeps this project's own edits unless you pass `keep_changes` false.

## 5. Change settings only when needed

Find the key with search_settings, read it with describe_setting, then set it:

```json
{"project": "bracket", "values": {"wall_loops": 4, "sparse_infill_density": "25%"}}
```

That goes to update_settings (scope `project` by default). Pass `scope` `object` and a `target` to change one object only. Warnings mean a setting has no effect yet, for example a support option while `enable_support` is off.

## 6. Slice

```json
{"project": "bracket", "plate": 0}
```

slice_project with plate 0 slices every plate. The call waits up to 60 seconds (`wait`, at most 600); a longer slice carries on in the background and the reply gives a job_id: call get_slice_status with that job_id and `wait` 60 until it says finished (a second slice_project on the same project is refused while it runs). `background` true returns the job_id at once. Ask for `preview` small or large to see the sliced plate and its first layer.

Read the summary: print time, grams per tool, layer count, warnings. get_slice_report gives more: sections `filaments`, `objects`, `layers`, and `layer` (with a picture, coloured by feature, filament or speed).

## 7. Hand off to the printer

The slice result carries a `handoff` block: the G-code path, a suggested upload name, the tools with their filament type and colour, and the object labels. Continue with the creality-k2-mcp server, as described in `multicolor-cfs`: get_filaments to read the spools, upload_gcode_file (`path`, optional `filename`), then start_print (`filename`, `source` `cfs`, a `slot_map` from the tools table) once the user agrees.

## When something needs the app

Painting, visual inspection and a few edits exist only in Creality Print. Follow `gui-handoff`.

## Mistakes that cost prints

- Forgetting `enable_support` on a model with overhangs (see `supports`).
- A filament type that does not match the spool in the CFS (see `multicolor-cfs`).
- A filament flow limit that is lower than you expect, which slows everything (see `speed-vs-quality`).
- Guessing setting names instead of calling search_settings.
