# Multi-colour and the CFS

For the K2 Combo with Creality Print 7.2 or 7.3. The CFS holds four spools; the slicer decides which project filament prints where, and the printer decides which slot supplies it.

## How it fits together

- Each project filament is a slot in the project: index 1 is `T0`, index 2 is `T1`, and so on. The G-code says `T0`, `T1`; the printer maps those to CFS slots.
- `multicolor_method` is 0 for the K2: the printer's own firmware performs the swaps. The slicer does not write the swap commands of the multi-material protocol.
- The printer matches by filament type (exact string) and by colour. A tool with no matching spool cannot print.

## Before you slice: read the spools

1. Call get_filaments on the creality-k2-mcp server. Its `slots` list what is loaded in each CFS slot (T1A to T4D): material and colour.
2. For each colour you want, find a spool of the same type. Types must be identical: PLA and PLA-CF differ, PETG and PETG-GF differ.
3. Give the spools as they are: pass the slots of get_filaments to create_project (or set_presets) as `spools`, each `{slot, catalog_id, material, colour, status, name}` as reported, in project filament order (the first is filament 1, tool T0); not together with `filaments`. Each spool gets the system preset with its `catalog_id` (an exact match), else the Generic preset of its material (a generic match, called out in the reply: check temperatures and flow), else the call is refused with the presets of that material. A slot that is `undefined` or `unknown` is refused. The colour is the spool's, and the slice reply then carries a ready `slot_map` for start_print. To choose presets and colours yourself, build the list to match, with the real colours:

```json
{"project": "logo", "filaments": [
  {"preset": "Hyper PLA @Creality K2 0.4 nozzle", "colour": "#FFFFFF"},
  {"preset": "Hyper PLA @Creality K2 0.4 nozzle", "colour": "#1E1E1E"}
]}
```

Send that to set_presets, or pass the same list to create_project (then you build the `slot_map` yourself, below). Colours are not taken from the preset: always give one `#RRGGBB` per filament, close to the spool's actual colour, because the purge calculation and the previews use it.

Mixing materials in one print needs their temperatures to be compatible. A big temperature gap between filaments makes the slicer refuse (exit code -62, see `troubleshooting`).

## Assigning colours to objects

add_model takes `filament` (1-based) for a whole object, and update_object changes it later. Colour inside one object (painting, colour OBJ import, gradients) is done in the app: see `gui-handoff`. `set_layer_actions` `color_change` (stored as a tool change on the K2) with a `filament` switches colour at a height.

## Flush and the prime tower

- Every colour change pushes old material out of the nozzle (flush). The amount is a matrix: from filament A to filament B. Dark to light needs much more than light to dark.
- The slicer computes the matrix from the colours you gave. `set_presets` accepts `flush_matrix` (an N by N list, row by row, from-filament in rows) to override it, and `flush_multiplier` to scale everything.
- The prime tower catches the purge and primes the nozzle. It is on in the K2 process presets (`enable_prime_tower`, width `prime_tower_width`, `prime_volume`), but the slicer builds it only when the print uses two or more filaments and is printed by layer (or by object with a single object): a plate with several objects printed by object, or one that uses one filament, gets no tower, and the slice report's settings section then shows `enable_prime_tower: 1 -> 0` as a rule of Creality Print, not something you changed. Its position is chosen by the app; turning it off with several filaments makes prints messy.
- `flush_into_support`, `flush_into_infill` and `flush_into_objects` reuse the purge inside the model to save filament. Only one style at a time; the app's skeleton flush is experimental and app-only.
- A multi-filament by-layer plate gets `Purge waste X g of Y g (Z%)` right after the `Sliced` line (see `troubleshooting`).
- The K2 firmware may hard-code part of the purge, so a smaller matrix does not always mean less waste on the machine.

## Slice, then hand off

slice_project returns a `handoff` block: `gcode_path`, `upload_name`, `tools` (each with `filament`, its 0-based index where T0 is 0, plus type and colour) and `exclude_names`. This server never talks to the printer, so you build the slot mapping yourself from get_filaments. Then:

1. get_filaments again if time has passed: spools change.
2. upload_gcode_file on creality-k2-mcp with `path` set to the `gcode_path` and `filename` set to `upload_name`.
3. For each tool, pick the slot whose material is the same type (and a close colour). Show the user the tool to slot table and get their go-ahead.
4. start_print on creality-k2-mcp with `filename` (the uploaded name), `source` `cfs` and a `slot_map`; when the project was made from `spools`, the slice reply already gives the `slot_map`, so use it as it is. `filament` is the file's 0-based index, `slot` is T1A to T4D:

```json
{"filename": "logo_plate1.gcode", "source": "cfs", "slot_map": [{"filament": 0, "slot": "T1A"}, {"filament": 1, "slot": "T1C"}]}
```

   `self_test` optionally asks the printer to run its self test first. That server has its own confirmation steps; follow them.
5. During the print, exclude_object with `object_name` set to a label from `exclude_names` skips a failed object; it needs that server's confirmation flow too. Labels look like `part_id_0_copy_0` (the object's name, its index and the copy). The slice reply lists each object with its label, and get_project shows the label of every object once the project was sliced; use those, never guess one.

## Things that go wrong

- Two or more filaments on one plate printed by layer crash the 7.2 slicer (7.3 slices them). Watch for the warning from create_project, set_presets and get_project; on 7.2 print one filament per plate, or upgrade to 7.3, or print by object with enough clearance (see multi-plate).
- Colours swap at print time: the mapping on the printer differs from what you intended. Check the mapping on the screen or re-run get_filaments.
- "No slot for this filament": type mismatch. Fix the preset type or load the right spool.
- Only one colour prints: the printer was fed from the external spool holder instead of the CFS, which treats multi-colour files as single colour.
- A spool ran out: the CFS can switch to an identical spool if automatic refill is on.
