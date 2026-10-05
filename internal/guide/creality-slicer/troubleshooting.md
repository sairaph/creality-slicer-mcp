# Troubleshooting

Creality Print 7.2 or 7.3. A failed slice comes back as a `slicer_error` (or `unavailable` when the install is unusable) with the exit code name, its meaning, a hint and the app's own output. Start from that text: a failed slice shows the last lines of the app's log and the path of the full log file, so read the tail before asking for more.

## Exit codes you may meet

| Code | Name | What it means and what to do |
| --- | --- | --- |
| -2 | INVALID_PARAMS | A request the app rejects: bad option, plate out of range. Check the plate number and any `overrides`. |
| -3 | FILE_NOTFOUND | An input or preset file is missing. Check paths; call get_project. |
| -5 | CONFIG_FILE_ERROR | A preset or project config is unreadable or inconsistent, such as a filament count that does not match. Re-apply presets with set_presets. |
| -6 | DATA_FILE_ERROR | A model file failed to load. Try another format or re-export the mesh. |
| -17 | PROCESS_NOT_COMPATIBLE | The process preset is not for this printer. Use list_presets for the printer and set_presets. |
| -18 | INVALID_VALUES_IN_3MF | A setting is out of range. Use describe_setting for the range. |
| -24 | FILE_VERSION_NOT_SUPPORTED | The project was saved by a newer Creality Print than the installed one. Update the app or re-save in the installed version. |
| -50 | NO_SUITABLE_OBJECTS | The plate is empty or nothing lies fully on the bed. Check get_project. |
| -52 | OBJECTS_PARTLY_INSIDE | An object crosses the bed edge. update_object with a new `position`, or a smaller `scale`. |
| -61 | FILAMENT_NOT_MATCH_BED_TYPE | A filament is not allowed on the plate's bed surface. Change `curr_bed_type` with manage_plates or the filament. |
| -62 | FILAMENTS_DIFFERENT_TEMP | The used filaments need very different temperatures. Choose compatible materials. |
| -63, -64 | OBJECT_COLLISION | Objects collide when printed one after another (`by object`). That order needs extruder clearance between objects: the error names the distance needed. Call slice_project with `arrange` true (it packs the plate with that clearance) or move the objects apart with update_object; get_project warns about too-close objects before you slice. `by layer` avoids the check, but on 7.2 it is safe only with one filament on the plate (see multicolor-cfs); on 7.3 any filament count works. |
| -100 | SLICING_ERROR | The slicer failed on this geometry or settings; often empty layers or a bad wall setting. Read the attached output. |
| -101 | GCODE_PATH_CONFLICTS | The toolpaths conflict. Reduce the offending setting or move objects. |

Other codes exist; the message names them. A code the table lacks is reported with its raw number.

## Crashes

An exit code of 3221225477 (or -1073741819) is a native crash of the app, an access violation. It has been seen when the app renders thumbnails and on very heavy models. Retry once. If it repeats, simplify the model (fewer triangles), and check that the app itself opens the file. Some Intel 13th and 14th generation CPUs make the app unstable; Creality ships a script to pin it to efficiency cores.

## Unsupported install

get_slicer_status says `supported` false when the installed app is neither 7.2 nor 7.3. Settings search, presets and guides still work; slicing does not until a supported version is installed.

## Common mistakes

1. Supports forgotten: overhangs droop. Set `enable_support` (see `supports`).
2. Prints slower than expected: check the filament's `filament_max_volumetric_speed` (see `speed-vs-quality`).
3. The CFS does not know the filament: type mismatch or an unlisted custom filament (see `multicolor-cfs`).
4. Colours swap in the print: the printer's mapping differs from the project; check the mapping before starting.
5. Flush changes seem to do nothing: the printer firmware may fix part of the purge.
6. `arrange` re-packs a plate even when it is locked; leave it out to keep a layout (see `multi-plate`).
7. A setting has no effect: it is gated by another one. describe_setting lists the dependency.
8. A mesh looks tiny or huge: wrong units. add_model warns; use `scale`.
9. Slicer and firmware versions drifted apart: time estimates and purge behave oddly; update both. A catalog drift note (the app sets settings the shipped catalog does not know) appears in get_slicer_status and doctor only, never on a project; those settings are not written into new projects, or into an opened project when set_presets rebuilds its settings; projects opened from the app otherwise keep them.

## Purge waste and by object

For a plate with two or more filaments printed by layer the slice reply has `Purge waste X g of Y g (Z%)` right after the `Sliced` line (and first in `warnings`): the grams thrown away in the tower and flush, of all the filament used, with the number of colour changes. A large share means the colours change too often or the flush matrix is heavy. When every object on the plate uses only one filament, the reply also gives the exact `update_settings` call that prints the plate by object, which removes the prime tower and most of the flush (a change between objects that use different filaments still flushes). For one object whose changes are layer actions, by object does not apply: fewer changes or a smaller flush_multiplier or matrix reduce it. Act on it, or ask the user whether to; do not ignore it. Printing by object needs extruder clearance between objects (see `multi-plate` and the -63 row above), and the tools warn when it is missing.

## Reading the output

The attached app output is the last part of the run, kept from the end. The last lines usually name the failing step. A warning inside a successful slice is not a failure: read `warnings` in the result.

## Still stuck

Open the project in the app (export_project, then see `gui-handoff`) and slice there: the app's own messages are more detailed than the command line's.
