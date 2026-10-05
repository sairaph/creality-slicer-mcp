# Tools

[Back to README](../README.md) · [Installation](installation.md) · [Configuration](configuration.md)

Creality Slicer MCP exposes 30 tools, grouped below by task: the first seven read the settings catalog, the guide and the installed Creality Print's presets and change nothing; the project tools change only this server's own projects store; the slicing tools run the installed Creality Print. Nothing here talks to a printer. Every tool carries a title and behaviour hints (read-only, changing, destructive) that clients use for approval prompts, and the server sends short instructions that name the rules shared by all tools: paths, the store, the printer handoff and never guessing a setting key. The `creality-slicer` guide skill (see [installation](installation.md#what-is-written-where)) holds the same pages `get_guide` serves.

Conventions shared by the tools:

- Tools that change a project (`add_model`, `update_object`, `remove_object`, `add_modifier`, `set_height_ranges`, `manage_plates` with add, remove or set, and `set_presets` with filaments) attach a small screenshot of the affected plate: the whole plate, Isometric, 512 by 384 pixels, parts and labels on, with the objects that changed outlined in magenta (so a move shows). For a change to one object (`update_object`, `add_modifier`, `remove_part`, `set_height_ranges`) the screenshot is framed on that object with its parts and at least 80 mm of context around it, because a modifier is a few millimetres on a 260 mm plate; the object is still outlined, and `set_height_ranges` draws the range bands. `add_model`, `remove_object`, `manage_plates` and `set_presets` show the whole plate. Every tool that changes a project also lists, under Warnings, the project warnings that call introduced (present after, absent before, by warning code). The wipe tower footprint is labelled PRIME TOWER in the picture and in the `get_view` legend: it is printed only with two or more filaments on a plate printed by layer. `include_screenshot` false leaves it out; the environment setting `CREALITY_SLICER_MCP_ONLY_TEXT_FEEDBACK` (see [configuration](configuration.md)) turns it off for every call and wins. A picture that cannot be drawn or does not fit is left out and never fails the call.
- Setting keys are Creality's own (`wall_loops`, `sparse_infill_density`). Find one with `search_settings`, read it with `describe_setting`; a wrong key never silently works.
- Levels: `beginner` shows the app's simple mode (the default), `advanced` adds advanced settings, `all` adds developer settings.
- Long lists are paged by size. The reply carries `page`, `total` and `total_pages` in its front matter and ends with `Next: page=N.` when there is another page; a page past the end is an empty page, not an error.
- Presets are the flattened result: everything a preset inherits is applied, so a value is what the app would use.
- Replies start with a YAML front matter block of named fields and continue with a Markdown body. A failure is an error reply with a `code` (`not_found`, `invalid_input`, `conflict`, `unavailable`, `slicer_error`, `internal_error`), a message and a hint that names the next call. An argument error (an unknown argument, a wrong type) lists the tool's valid argument names in its hint, the required ones marked. Paths in error texts are written as they are, in backticks. A refused change of a key Creality's presets lock says to pass `allow_locked` true to override the lock.
- Paths are absolute paths on this computer.
- Settings and presets come from the installed Creality Print (its bundled profiles and its setting descriptions). Without it the setting names and defaults of the shipped catalog (version 7.2.1) still work, but presets, K2 defaults and descriptions are unavailable, and the tools say so. With Creality Print 7.3 installed the 7.3.0 catalog is used instead.

- [Status and guidance](#status-and-guidance)
- [Settings](#settings)
- [Presets](#presets)
- [Projects](#projects)
- [Slicing](#slicing)
- [Command line](#command-line)

## Status and guidance

### `get_slicer_status`

Report whether Creality Print is installed and usable. Call it first.

- `refresh` (boolean, optional, default false): detect Creality Print again and reload presets and descriptions. Without it the result cached since the server started is used.

The front matter has `installed`, `supported`, `version`, `build`, `dialect`, `exe`, `data_dir`, `profile_version`, `profile_source` (`install` or `data_dir`: which bundle the presets are read from), `gui_running`, `projects_dir`, `catalog_version`, `tooltip_coverage` (for example `561/568` settings have a description from the installed app), `catalog_drift` (how many settings the installed K2 presets set that the shipped catalog does not know; the body lists up to ten of them, and they are not written into new projects, or into an opened project when set_presets rebuilds its settings; projects opened from the app otherwise keep them; legacy keys that the app ignores or converts, such as `adaptive_layer_height`, `bed_type` and `wall_infill_order` on 7.3, are not counted as drift) and, when not usable, `reason`. The body gives a one-line verdict, what works and what does not (with an unsupported version, settings, presets and guides work but slicing does not), and the next call.

### `get_guide`

Read the guide: written workflows for slicing on a K2 or K2 Combo. With no arguments it lists the topics.

- `topic` (string, optional): a topic from the index: `start`, `k2-combo`, `multicolor-cfs`, `supports`, `strength`, `surface-quality`, `thin-features`, `speed-vs-quality`, `modifiers-and-ranges`, `multi-plate`, `calibration`, `troubleshooting`, `gui-handoff`, `glossary`. Case is ignored.
- `term` (string, optional): look a word up in the glossary. Every entry whose term contains it is returned. Use it alone or with topic `glossary`; with another topic it is an `invalid_input` error.

The front matter has `topic` (or `topics` on the index) and `term`; the body is the page. An unknown topic or a term with no match is a `not_found` error listing what exists.

## Settings

### `search_settings`

Find settings by words in their key, label, area, choices or description. Results are ranked, then paged.

- `query` (string, optional): the words to look for, such as `wall loops` or `support angle`. Without it every setting the other filters allow is listed in the app's order.
- `area` (string, optional): a GUI path prefix to search under, such as `process/Quality` or `filament/Filament`. `browse_settings` shows the paths.
- `level` (string, optional, `beginner` | `advanced` | `all`, default `beginner`).
- `scope` (string, optional): `printer`, `process` or `filament` for settings of that preset type; `object`, `part`, `layer_range` or `plate` for settings that can be overridden there. Default: all.
- `page` (integer, optional, from 1, default 1).

The front matter has `query`, `count` (all matches), `catalog_version`, `defaults` and the page fields. `defaults` says where the K2 default column came from: `presets` (the value in the flattened `Creality K2 0.4 nozzle`, `0.20mm Standard @Creality K2 0.4 nozzle` and `CR-PLA @Creality K2 0.4 nozzle` presets), `catalog` (those presets could not be read, so the catalog's own defaults) or `mixed`. A setting those presets do not set shows the catalog default marked `(catalog)`. The body has one line per setting, `key | label | area | unit | K2 default | level`, with the first sentence of the installed app's description under it when there is one, and ends with the next-page hint and the next call, `describe_setting`.

### `describe_setting`

Explain one setting.

- `key` (string, required): the setting key. An unknown key is a `not_found` error with up to five close keys.
- `preset` (string, optional): a preset to read the current value from, as `type:name`, for example `process:0.20mm Standard @Creality K2 0.4 nozzle`.
- `project` (string, optional): a project whose current project level value to show, with where it comes from (changed in the project, or from its presets).

The front matter has `key`, `label`, `type` (`bool`, `int`, `float`, `percent`, `enum`, `string`, ...), `vector` (true for per-filament settings), `unit`, `default`, `min`, `max`, `enum` (`value: label` per choice), `level`, `area`, `scopes` (`project` and any of `object`, `part`, `layer_range`, `plate`), `locked` (`read_only` or `hidden` when Creality's own presets lock the setting; unlocking needs `allow_locked` in `update_settings`) and, with `preset` or `project`, `current` and `origin` (the preset in its inheritance chain that set the value, or where the project's value comes from; with both, the project's value). The body has the app's description (or "No description in this Creality Print build"), the dependencies in plain words (when the app shows or enables the setting, what it forces), related settings of the same group, and how to change it with `update_settings`, with an example.

### `browse_settings`

Walk the settings as the app lays them out: tabs (`process`, `filament`, `printer`, `plate`), pages, then groups.

- `path` (string, optional): the area to open, such as `process`, `process/Strength` or `process/Strength/Infill`. Case and surrounding slashes are ignored. Default: the top.
- `level` (string, optional, `beginner` | `advanced` | `all`, default `beginner`).

The front matter has `path`, `kind` (`root`, `tab`, `page` or `group`), `children` and `settings` (counts at this level). At the top, tab and page level the body lists the children with their setting counts; a group (and a page's ungrouped settings) lists `label | key | unit | default`. An unknown path is a `not_found` error.

## Presets

Preset names are exact: `list_presets` shows them.

### `list_presets`

List presets of one type with their key facts. Results are paged.

- `type` (string, required): `printer`, `process` or `filament`.
- `printer` (string, optional, default `Creality K2 0.4 nozzle`): the printer preset the process and filament presets must fit. `any` lists every Creality preset, `all` the whole K2 family. For type `printer` the default lists the K2 family (every nozzle).
- `filament_type` (string, optional): only filament presets of this material, such as `PLA` or `PETG`, case ignored.
- `source` (string, optional, `system` | `user` | `all`, default `all`): shipped with the app, yours, or both.
- `page` (integer, optional, from 1, default 1).

The front matter has `type`, `printer`, `count`, `profile_version` and the page fields. The body is one line per preset: process `name | source | layer height | walls | infill`, filament `name | source | type | vendor | filament_id | nozzle temp`, printer `name | source | nozzle | bed`. A printer preset that does not exist is `not_found`; without a detected Creality Print the tool is `unavailable` and points at `get_slicer_status`.

### `get_preset`

Show one preset's values, grouped by where the app shows them (in the app's order), with the inheritance chain.

- `type` (string, required): `printer`, `process` or `filament`.
- `name` (string, required): the exact preset name.
- `compare_to` (string, optional): another preset of the same type, or `parent` for the one this preset inherits from. Only the differing keys are shown, as `this -> other`.
- `keys` (array of strings, optional): only these setting keys. A key the preset does not set (or, when comparing, that does not differ) is listed as not set.
- `level` (string, optional, `beginner` | `advanced` | `all`, default `beginner`): which keys are shown when `keys` is empty. Keys the catalog does not know appear only at level `all`.

The front matter has `type`, `name`, `source`, `inherits_chain` (the preset first, then its parents), `compatible_printers`, `values_shown`, and in compare mode `compare_to` and `differences`. The body groups values by area, `key (label) = value` (vectors show one entry per filament slot). A very long body is cut at a line with a note; pass `keys` or a lower level.

## Projects

A project is one Creality Print project (printer, process, filament slots, plates, objects, settings) kept in this server's own store, `projects_dir` in `get_slicer_status`. Every project has an id (its name plus a short suffix); a `project` argument takes the id or an exact name that is unique. Every edit is a new revision; the reply front matter carries `project`, `name` and `revision`. Source files are copied and never changed. Filament slots are numbered from 1 in these tools (slot 1 is tool T0 in the G-code); the slice handoff numbers them from 0, as the printer server does. Positions (objects, modifiers, the prime tower) are in mm relative to the corner of the plate they are on, the same on every plate.

### `create_project`

Create a project with a printer, a process and at least one filament.

- `name` (string, required).
- `printer` (string, optional, default `Creality K2 0.4 nozzle`), `process` (string, optional, default the printer's default process), `bed_type` (string, optional, default the bed Creality Print last used for this printer, read from `Creality.conf` in the app's data folder (`orca_presets[].machine` equal to the printer preset, its `curr_bed_type`; nothing else in that file is read), else the printer's default). The reply says when the bed came from the app. A missing or unreadable file, an unknown printer or a value that is not a plate type falls back silently.
- `filaments` (list, optional when `spools` is given, otherwise at least one): each `{preset, colour}`, colour `#RRGGBB`. One entry per colour of a multi-colour print.
- `spools` (list, optional, instead of `filaments`): CFS spools exactly as `creality-k2-mcp` `get_filaments` reports them, `{slot (T1A to T4D), catalog_id, material, colour, status, name}`; the order is the filament order. Each spool gets the preset compatible with the printer whose `filament_id` equals `catalog_id` (match `exact`), else the Generic preset of the material (match `generic`, called out in the reply), else `invalid_input` listing the compatible presets of that material. A spool with status `undefined` or `unknown`, or without a material, is refused. The colour is normalised to `#RRGGBB` (white with a note when missing). The slot, catalog id and match are kept per filament (front matter `spool_slot`, `catalog_id`, `match`, and a Spools block in the reply); `slice_project` then gives each handoff a ready `slot_map` for `start_print` when every used tool has a spool slot. `set_presets` takes the same `spools` list.

The front matter has the project fields, `printer`, `process`, `filaments` (`index`, `preset`, `type`, `colour`) and `plates` (each with `first_layer_bed_c`: the first-layer bed temperature of every filament on that plate's bed type, one entry per filament; 0 means the filament does not support that plate type). The body lists the parts that have a filament of their own (`filament` column) and every height range with its filament, and has a bed block: the bed type of each plate and, per filament, the first-layer bed temperature; `bed_type` sets the bed type of the first plate. An unknown or incompatible preset is an `invalid_input` error with the compatible names. The body says what to do next (`add_model`) and reminds you to match filament types to the loaded spools.

### `open_project`

Import a Creality Print or Bambu-family 3MF as a new project. The file is copied.

- `path` (string, required): a `.3mf` project file. A file with no project settings is an `invalid_input` error (use `create_project` and `add_model`).
- `name` (string, optional, default the file name).
- `into` (string, optional): the id of an existing project whose content this file replaces (a project saved in the app after `open_in_app` mode project): id, name and folder stay, the revision goes up by one, the last slice is void, spool links stay while the filament keeps its preset. `name` is ignored then.
- `preview` (string, optional, `none` | `small`, default `none`): `small` attaches the thumbnail the app stored in the file, when it has one.

The front matter has the project fields, `source_path`, `app_version`, `printer`, `process`, `filaments`, `plates`, `objects`, `painted` (objects with painted data: kept, but only the app can edit them) and `sliced_in_file`. The body summarises the project and warns about another printer than the K2 family, a file from a newer app version and presets missing on this machine.

### `list_projects`

List the projects, newest first, with printer, objects, plates and last slice.

- `page` (integer, optional, from 1).

The front matter has `count` and the page fields.

### `get_project`

Show one project as text: no pictures (for those see `get_view`).

- `project` (string, required).

The front matter has the project fields, `printer`, `process`, `filaments`, `plates` (`index`, `name`, `objects`, `bed_type`, `first_layer_bed_c` (the first-layer bed temperature of every filament on the plate's bed type, one entry per filament; 0 means the filament does not support that plate type), `print_sequence`, `locked`), `overrides` (settings changed from the presets) and `last_slice`. The body lists filaments, plates, objects (name, id, plate, size in mm, position, rotation, filament, override count, parts, and the exclusion label once the project was sliced), the parts of each object (modifier, negative part, support enforcer or blocker) with kind, name, size and centre on the plate, the changed settings and warnings: an object outside the bed, `sequence_clearance` (a plate printed by object whose objects are too close or too tall for the extruder clearance, with the distance needed), `v72_by_layer_crash` (Creality Print 7.2 with two or more filaments on a plate printed by layer) and `range_no_layer_height` (a height range without `layer_height`). It ends with a `Next:` line that suggests `get_view`.

### `get_view`

Draw a plate from a named camera, to check placement, heights and modifier coverage. It reads the project and changes nothing.

- `project` (string, required), `plate` (integer, optional, from 1, default 1).
- `view_name` (string, optional, default `Isometric`): `Isometric` (azimuth 45 degrees from the front left, elevation 35.26), `Front` (camera at the front looking along +Y), `Top`, `Right` (camera at +X), `Back`, `Left` (camera at -X), `Bottom`, `Dimetric` (azimuth 45, elevation 20.7) or `Trimetric` (azimuth 60, elevation 30). Z is up and the projection is orthographic.
- `focus` (list of object ids or names, optional): the picture is framed on these objects (with their parts) instead of the whole bed.
- `hide` (list, optional): objects left out. `isolate` (list, optional): only these objects are drawn.
- `show_parts` (boolean, optional, default true): modifiers (translucent yellow), negative parts (red), support enforcers (green) and support blockers (blue-grey), each with a solid outline. A part is drawn at full strength where it is in front of the model and only as a faint tint where the model hides it; its outline is always drawn.
- `show_labels` (boolean, optional, default true): each object's id and short name at its top.
- `show_ranges` (boolean, optional, default false): height ranges as tinted bands on their objects.
- `width`, `height` (integers, optional, 1 to 2048): the image size. Without both, the frame is 1024 by 768 (4:3); one given keeps the 4:3 shape. The framing is fitted inside the frame.

The picture shows the bed (260 mm square on the K2, printable area outline, 10 mm grid), the plate axes at the plate origin (X red, Y green), the wipe tower footprint, and the objects lit in their filament colours (tinted red outside the printable area). The front matter has the project fields, `plate`, `view`, `focus` (the names framed), `objects` and `parts` (counts drawn), `ranges` (bands drawn, only with `show_ranges`; 0 means no object in the picture has height ranges), `width` and `height`. The body says what is shown, gives a legend (each height range band by colour, object, heights and settings), lists labels that were left out because they would have covered another, and ends with a `Next:` line; the image follows the text, scaled down when it would not fit in a reply (a picture that cannot be made to fit is an `invalid_input` error with a hint to ask for a smaller size).

### `add_model`

Add a model (`.stl`, `.obj` or `.3mf`) to a plate.

- `project`, `path` (string, required).
- `plate` (integer, optional, default 1), `position` (`[x, y]` or `[x, y, z]` mm from the corner of the object's own plate, optional: automatic placement without it), `rotation` (`[x, y, z]` degrees), `scale` (a number or `[x, y, z]`, above 0), `filament` (slot, default 1), `name`, `copies` (integer, default 1), `objects` (list of names: for a `.3mf`, take only these objects; default all).
- `names` (list of strings): one name per object taken from the file, in file order (after `objects`); the count must match, else `invalid_input` lists the objects. They win over the names in the file. An object the file does not name (FreeCAD exports carry none) is called `<file name> <n>`, n from 1 in file order.
- `keep_positions` (boolean, default false): each object keeps the XY of the file: the centre of its bounding box in file coordinates is its centre measured from the plate corner. No automatic placement; Z drops to the bed. For a Creality or Bambu slicer project the plate origins are subtracted, so an object on plate 2 lands at its plate relative XY on the target plate (such a project is usually better opened with `open_project`). Objects outside the printable area are added with a warning that names them. Not with `position` or `copies` above 1.
- `include_screenshot` (boolean, optional, default true): attach the screenshot described in the conventions above.

The first object on an empty plate goes to the middle of the bed; on a plate printed by object the objects are spaced by the clearance the printer needs.

The front matter has `added` (id, name, plate, size, position, rotation, filament). The body says where each copy went and warns when the size looks like the wrong unit. A model that does not fit is a `conflict` error with the free area and what to try. A `.3mf` adds all its objects, or only the ones named in `objects` (an unknown name is an `invalid_input` error that lists the names in the file).

### `update_object`

Change one object; only the given fields change.

- `project`, `object` (string, required): an id or a unique name.
- `position` (mm from the corner of the object's own plate; moving an object to another plate keeps it), `rotation`, `scale` (as in `add_model`), `filament` (slot), `plate` (move it), `name`, `lay_flat` (boolean: largest flat face down).
- `include_screenshot` (boolean, optional, default true): attach the screenshot described in the conventions above.

The front matter has `object` (its new state).

### `group_objects`

Merge objects into one object with several parts, for different filaments or settings per part.

- `project` (string, required), `objects` (list, required, at least 2): ids or names; the first is the base and keeps its id, settings, height ranges and place. `name` (string, optional): new name of the grouped object.
- `include_screenshot` (boolean, optional, default true): attach the screenshot described in the conventions above.

Every other object becomes parts of the base: each part keeps its world position and mesh, is named after its old object (plus the part name when it had several) and gets `extruder` = the filament that object printed with. Settings of a merged object that are valid on a part move to its parts; the others, and its height ranges, are dropped, and painted data is not carried; each is a warning. Refused with `invalid_input`: fewer than 2 objects, an object twice, an object with several instances, objects on different plates. One change, one revision. The front matter has `object`, `parts` (`name`, `kind`, `filament`) and `warnings`.

### `remove_object`

Remove an object with its parts, modifiers and height ranges.

- `project`, `object` (string, required).
- `include_screenshot` (boolean, optional, default true): attach the screenshot described in the conventions above.

The front matter has `removed`.

### `remove_part`

Remove one part of an object: a modifier, negative part, support blocker or enforcer, or an extra model part.

- `project`, `object`, `part` (string, required): the part is an id or a name, as `get_project` lists under the object.
- `include_screenshot` (boolean, optional, default true): attach the screenshot described in the conventions above.

The front matter has `object`, `removed` (the part name), `kind` and `parts` (how many are left). The last model part of an object cannot be removed (`invalid_input`, use `remove_object`); an unknown object or part is `not_found`.

### `update_settings`

Change settings at one scope.

- `project` (string, required).
- `scope` (string, optional, `project` | `object` | `part` | `layer_range` | `plate`, default `project`).
- `target` (string): an object id or name; a part as `object/part`; a height range as `object/N`; a plate number. Not used for `project` scope.
- `targets` (list of strings, instead of `target`): the same change for several objects, parts, height ranges or plates of one scope. All are checked first and nothing changes unless all pass; one revision. The reply groups the changes by target and drops repeated warnings. A bad target names itself in the error, and the front matter `changed` lines name their target. A vector value whose entries are all equal reads as one value (`60 -> 40`, not `[60] -> [40]`).
- `values` (object, required): setting key to value. Values are strings, numbers, booleans or lists (vector keys: a list is one value per filament slot, a single value applies to every slot); `null` removes the override.
- `allow_locked` (boolean, optional, default false): allow keys Creality's system presets lock.

`extruder` (the filament slot) is a setting of object scope (1 to the number of filaments), part scope and height range scope (0 to the number of filaments, 0 = the object's own filament); `set_height_ranges` takes it in a range's `settings` too. Every key and value is checked against the settings catalog first (unknown key, type, choice, range, scope, vendor lock); nothing changes unless all pass, and each failing key is named with its reason. The front matter has `scope`, `changed` (`key: old -> new`) and `warnings`. The body explains each change, warns about settings that do nothing until another is on, and lists the side effects the app would also apply (they are applied).

### `set_presets`

Change the printer, process or filament presets, or the flush matrix.

- `project` (string, required).
- `printer`, `process` (string, optional).
- `filaments` (list, optional): the full list in slot order, `{preset, colour}`. Shrinking is refused while objects use the removed slots.
- `spools` (list, optional, instead of `filaments`): CFS spools as in `create_project`; they replace the filament list. Filaments given by hand drop the spool links.
- `keep_changes` (boolean, optional, default true): keep this project's changed settings when re-basing on a new preset.
- `flush_matrix` (list of integers, optional): N*N flush volumes in mm3, row by row, from filament to filament; omit for the automatic matrix. `flush_multiplier` (number, optional).
- `include_screenshot` (boolean, optional, default true): attach the screenshot described in the conventions above.

At least one of printer, process, filaments, flush_matrix or flush_multiplier is required. The front matter has `printer`, `process`, `filaments`, `flush_matrix` (`auto` or `manual`) and `flush_volumes`; the body lists what changed and which changed settings were dropped.

### `add_modifier`

Add a modifier, negative part, support enforcer or support blocker to an object.

- `project`, `object`, `kind` (`modifier` | `negative_part` | `support_enforcer` | `support_blocker`), `shape` (`box` | `cylinder` | `sphere`), `size` (`[x, y, z]` mm): required.
- `position` (`[x, y, z]` mm relative to the object's centre, default `[0, 0, 0]`), `rotation` (`[x, y, z]` degrees that turn the shape about its centre, applied about X, then Y, then Z of the bed, default none), `values` (settings for kind `modifier`, checked at part scope), `name`.
- `include_screenshot` (boolean, optional, default true): attach the screenshot described in the conventions above.

The front matter has `part` and `kind`. A support enforcer only works with `enable_support` on; the body says so.

### `set_height_ranges`

Replace the height ranges of an object: bands of layers with their own settings.

- `project`, `object` (string, required).
- `ranges` (list, required): each `{from_z, to_z, values}` in mm above the bed. The list replaces the current one; an empty list clears it. Ranges must not overlap; values are checked at layer range scope. Every range also carries `layer_height` (the object's own value unless the range sets one): the slicer crashes on a range without it, so the tools add it and refuse to remove it.
- `include_screenshot` (boolean, optional, default true): attach the screenshot described in the conventions above.

The front matter has `object` and `ranges` (the count). The body notes each range that got a `layer_height` added, and why.

### `set_layer_actions`

Replace the actions of a plate: a pause, a colour change or tool change to a filament slot, or custom G-code at a height.

- `project` (string, required), `plate` (integer, optional, default 1).
- `actions` (list, required): each `{z or layer, type, filament, gcode}` with `type` `pause` | `color_change` | `tool_change` | `custom`. A layer number (from 1) is converted to a height with the project's layer heights. `filament` is the slot to change to (required for `tool_change`, 1 to the number of filaments); `gcode` is for `custom`. A printer without colour change G-code (the K2 with the CFS) cannot write M600, so a `color_change` there is stored as a `tool_change` to that filament (with the filament's colour, the plate set to the multi extruder mode) and the reply says so; with fewer than two filaments it is refused. Where the printer preset has colour change G-code, `color_change` stays a colour change. Creality Print applies layer filament changes only when every object on the plate prints with one filament (ToolOrdering: one object extruder in total); on a plate whose objects use more, nothing is refused but the reply and `get_project` carry the warning `layer_tool_change_ignored`: put the objects that change filament on their own plate, or give them all the same filament. The list replaces the current one.

The front matter has `plate` and `actions` (the count).

### `manage_plates`

Add, remove, rename, lock or unlock a plate, or set plate settings.

- `project`, `action` (`add` | `remove` | `rename` | `lock` | `unlock` | `set`): required.
- `plate` (integer, from 1; not used for `add`), `name` (for `rename` or `add`), `values` (for `set`: `curr_bed_type`, `print_sequence`, `first_layer_print_sequence`, `other_layers_print_sequence`, `spiral_mode`).
- `include_screenshot` (boolean, optional, default true): attach the screenshot described in the conventions above.

Removing a plate with objects is refused. Locking sets the plate's lock flag, which the app honours when it arranges; the `arrange` option of `slice_project` ignores it. The front matter has `action` and `plates`.

### `open_in_app`

Open a project in Creality Print, in a new window. It starts the installed app on one file and nothing else: no option is passed, no running window is contacted, and no window is ever closed, signalled or replaced. The app takes a while to start; the user closes the new window when done, and windows that were already open are untouched. It needs a supported install (7.2 or 7.3) and is refused with `unavailable` otherwise.

- `project` (string, required).
- `plate` (integer, optional, from 1, default 1).
- `mode` (string, optional, `preview` | `project`, default `preview`): `preview` copies the G-code of that plate's slice to `view/plate<N>_r<revision>.gcode` in the project's folder and opens it in the app's Preview tab (the slice must be from the current revision, else a `conflict` that says to call `slice_project` first). `project` writes the current project to `view/<name>_r<revision>.3mf` and opens it in the 3D editor. Both open a copy, so a running app never locks the slice output or the project; older view files of the project that are not in use are removed when a new one is written.

The front matter has the project fields, `mode`, `plate`, `file`, `pid` (the new process) and `app_version`. The body says what opened and that other windows are untouched. A file in the view folder that the user changed after the server wrote it (saved in the app) is never overwritten or deleted: it is listed in `saved_files` on every call until it is removed, with a line for each, "You saved changes in <file>: bring them back with open_project with {path, into}", and the app is opened on a new file, not on that one. In `project` mode it adds how to bring changes back: save in the app (File > Save Project), then call `open_project` with `path` set to the file and `into` set to the project id, which replaces that project's content and keeps its id, name and folder.

### `export_project`

Write the project as a Creality Print 3MF, for painting or checks in the app.

- `project`, `path` (string, required): an absolute `.3mf` path; missing folders are created.
- `overwrite` (boolean, optional, default false).

The front matter has `file` and `bytes`. The file is not linked to the store: after saving changes in the app, call `open_project` on it. When the project was made from CFS spools, the copy carries the spool links in an extra member (`Metadata/creality_slicer_mcp.json`; an older one is always dropped), and the project remembers the exported path in its metadata (no new revision). Opening that file again restores the slots from the member, or, when the app dropped the member by saving, from the project that exported the path; only the links from the first filament on whose preset is unchanged are kept. The reply says where they came from (`CFS slots restored from ...`), and the slice handoff then has its `slot_map`.

### `delete_project`

Delete a project's folder from the store, with the G-code of its slices. Exported files and source files are not touched.

- `project` (string, required), `confirm` (string, required): the project id again.

## Slicing

Slicing runs the installed Creality Print (7.2 or 7.3, 7.3 preferred and needing nothing extra from the user; other versions give an `unavailable` error) and writes G-code into the project folder. The tools never print: the reply carries what the `creality-k2-mcp` server needs.

### `slice_project`

Slice a project.

- `project` (string, required).
- `plate` (integer, optional, default 0 = every plate with objects), `arrange`, `orient` (boolean, optional, default false: the tools place objects first and write it into the project).
- `overrides` (object, optional): settings for this slice only, checked like `update_settings` at project scope; the project is not changed.
- `wait` (number, optional, seconds, 1 to 600, default 60): the call waits this long for the result. A slice that finishes in time returns the full result; a longer one returns `state: running` and its `job_id` with polling advice, and carries on in the background (`get_slice_status`).
- `background` (boolean, optional, default false): true returns the `job_id` at once without waiting.
- `thumbnails` (boolean, optional, default true): put the plate pictures the printer preset asks for into the G-code.
- `timeout` (number, optional, seconds, default 1800, at most 1800): the anti-hang limit for the slicer run, not the wait.
- `preview` (string, optional, `none` | `small` | `large`, default `none`): attach two pictures of the sliced result: an isometric drawing of the toolpaths, layer upon layer in the colour of each filament (512 or 1024 pixels wide), and the first layer of all objects (384 or 1024 pixels square; a plate printed by object has one first layer per object).

The front matter has the project fields, `state` (`finished` or `running`), `elapsed_s` (how long the slicer ran), `plates` (`index`, `bytes`, `time_s`, `time_text`, `filament_g` per tool, `total_g`, `layers`, `objects`, `multicolour`, `changes` (filament changes), and only for plates that print several filaments: `prime_tower_g`, `prime_tower_s`, `flush_g`, `flush_changes` and `flush_estimated`), `warnings`, `stale` and `handoff`. Per plate the handoff has `gcode_path`, `upload_name` (`<project name>_plate<N>.gcode`, sanitised), `tools` (`tool` T0 and so on, `filament` the 0-based index, `preset`, `type`, `colour`, `filament_id`) and `exclude_names` (the object labels `exclude_object` takes; `get_project` and the slice reply also show the label of each object). The body has one table with a row per plate (time, grams, layers, upload name, G-code path), the tools table, a block `Purge material besides the objects (plate | cost)` (the prime tower in grams and at least this much printing time, and the flush of the filament changes in grams over N changes, from the G-code footer, or marked as an estimate from the flush matrix when the footer has no usage lines; `get_slice_report` shows the same line under the plate summary), the exclusion label of every object, the warnings and the printer steps: `get_filaments`, then `upload_gcode_file` with `path` = `gcode_path` and `filename` = `upload_name`, then `start_print` with `source` `cfs` and `slot_map` (`filament` = the 0-based index, `slot` = the CFS slot with the same material), then `exclude_object` during the print. For a plate with two or more filaments printed by layer the line right after `Sliced ...` is `Purge waste X g of Y g (Z%)` (the grams thrown away in the tower and flush, of all the filament used, and the colour changes); it is also the first entry of the front matter `warnings`. When every object on the plate uses one filament it also gives the exact `update_settings` call that prints the plate by object, which removes the prime tower and most of the flush (a change between objects that use different filaments still flushes), to act on or to put to the user. For one object whose filament changes are layer actions, the note says the waste comes from N layer filament changes (fewer changes, or a smaller `flush_multiplier` or flush matrix, reduce it) and that printing by object does not apply. The body then starts with the time the slicer ran (`Sliced 1 plate(s) from revision 4 in 21m 31s.`; under a minute it reads `in 0.9 s`, and `in under 0.1 s` for a slice that took less than 0.05 s (the time is always stated)). When the plate has layer actions (pauses, colour changes, custom G-code), a table `Layer actions (plate | action | result in the G-code)` follows the tools table: each action is listed as found in the G-code at its height, as `not found in the G-code: it did nothing`, or, for a tool change on a plate whose objects use several filaments, as `ignored by Creality Print: the plate's objects use several filaments` (the warning `layer_tool_change_ignored` is listed in the reply too). The tools table is sorted by tool index.

With `arrange` or `orient` the reply lists the objects that were moved (old and new position on the plate, front matter `moved`). A failed slice is a `slicer_error` with the exit code name, its meaning, a hint, the last 15 meaningful lines of the slicer's own log and the path of the full log (`log_file` in the fields; the log of the last slice is `slice.log` in the project's output folder); the application's whole output is not repeated. An unsupported install is `unavailable`. Numbers in the front matter are rounded: millimetres and grams to 0.01, degrees and seconds to 0.1.

### `get_slice_status`

Check a background slice.

- `job_id` (string, optional): without it the running and recent jobs are listed.
- `wait` (number, optional, seconds, 0 to 600, default 0): hold the call while the job runs, until it ends or the seconds are up, instead of polling.
- `cancel` (boolean, optional, default false): stop the job (needs `job_id`).

The job list shows how each job really ended: `running`, `finished`, `failed` (with the exit name, for example OBJECT_COLLISION_IN_SEQ_PRINT or a crash name) or `cancelled`. A running job gives its state and elapsed time. A finished job gives the same reply as `slice_project`, including the handoff. A failed job gives the same error. An id nobody knows (or one that is not a job id, such as a project id) is a `not_found` error "no slice job with id ..."; jobs are forgotten after a day, and `get_project` still shows the last slice of a project.

### `get_slice_report`

Read the last slice of a plate.

- `project` (string, required), `plate` (integer, optional, default 1).
- `section` (string, optional, `summary` | `filaments` | `objects` | `layers` | `layer` | `settings`, default `summary`).
- `layer` (integer, from 1) or `z` (number, mm) for section `layer`; `color_by` (`feature` | `filament` | `speed`, default `feature`); `preview` (`none` | `small` | `large`; default `small` for section `layer`).

`summary` is the plate line, the generator and the bounds; `filaments` gives grams, millimetres and cubic centimetres per tool; `objects` the labels with centre and box; `layers` the count, first layer and height range; `layer` the extrusion by feature and travel, and the filament pushed per tool in that layer (mm and grams), with a picture (on a plate printed by object the layers are counted across all objects, so the report says so and shows every object at that layer's height; pick a height with `z`); `settings` the effective settings that differ from the process preset, grouped: changed in this project, switched by a rule of Creality Print (for example the prime tower when printing by object, with the condition), and differences nothing in the project explains. A report older than the project says so (`stale`). Time per layer is not reported. The `summary` also says what a multi-colour plate spends besides its objects: the prime tower grams and the flush of the filament changes (from the G-code footer minus the G-code's own extrusion, or an estimate from the flush matrix when the footer has no usage lines). After those, a block lists what is set below the project level on the plate, as it was when the plate was sliced (recorded in the project metadata at slice time): the plate settings, then each object on the plate with its overrides, its parts and modifiers (with their filament) and its height ranges (with their filament).

### `analyze_toolpaths`

Measure what the slicer really printed, from the plate G-code of the last slice. Reads only; a slice older than the project is answered and marked `stale`.

- `project` (string, required), `plate` (integer, optional, default 1).
- `objects` (list of names as `get_project` shows them; matched with the label before `_id_` of the G-code's `EXCLUDE_OBJECT_START`, ignoring case and punctuation; default every object), `layers` (`[from, to]`, from 1) or `z` (`[from, to]` mm), not both; `features` (`;TYPE:` names such as `Outer wall`, `Overhang wall`, `Support interface`, ignoring case; default all).
- `measure` (list, default `first_layers` and `bounds`):
  - `first_layers`: per object and feature the first and last layer with extrusion, and the layers with no extrusion inside an object's range.
  - `bounds`: XY min, max and span of the extruded paths per object, feature and layer; arcs (G2, G3 with I, J and the P full turns) are sampled with chords of at most 0.05 mm.
  - `flow`: path length, E (mm of filament), E per mm and the flow ratio per object and feature, as median, p01, p99 and max. Ratio = E x filament cross-section / (length x `;WIDTH:` x `;HEIGHT:`); 1 is the flow the width and height ask for. The filament diameter is read from the G-code header.
  - `radius` (needs `center` `[x, y]`): minimum and maximum distance of the paths from the centre.
  - `short_runs` (`min_run` mm, default 1): runs of extrusion of one feature without a travel, retract, prime or wipe between, counted per object and layer, with the end positions of the first 20 short ones.
  - `unsupported_starts`: islands of extrusion (paths on a 0.1 mm grid, dilated by half the line width, 8-connected) on layer n above 1 with under 10 % overlap with the extrusion of layer n-1, bridges excluded: object, layer, z, features, XY box, area, supported %. It catches a feature that first prints in mid air. Only the requested objects are read, so support of another object is not counted.
  - `support_contacts`: per layer the patches of `Support` and `Support interface` with their XY box and area, and the objects that print on them on the next layer.
  - `wall_order`: per object the layers where the outer wall printed before the inner wall and where it printed after.
- `detail` (`summary` default | `per_layer`), `page` (integer, default 1). With several measures every page gives each measure that still has rows an equal share, so page 1 shows all of them; each section says `rows a-b of N`. Sections are ordered findings first (`unsupported_starts`, `support_contacts`, `short_runs`, then `first_layers`, `bounds`, `flow`, `radius`, `wall_order`); empty ones are left out and named in one line `no findings: ...`. On a plate printed by object, layers are counted per object from 1.

Retract and prime moves without XY, and the moves between `;WIPE_START` and `;WIPE_END`, are never path; extrusion outside an object block (the prime tower) belongs to no object. The front matter has the plate, `stale`, the layer count, the measures, the objects with their first and last layer, and the counts of the finding measures. The body is Markdown tables, paged; it ends with how to narrow the question. The file is read once, with memory bounded by the requested measures.

## Command line

Three commands have tool equivalents. They call the same functions as the tools, so the answers agree; the output is the tool's reply as text (for `status`, its fields then its body; for `presets` and `slice`, its body).

- `creality-slicer-mcp status [--refresh]`: the same as `get_slicer_status`, including the catalog drift line.
- `creality-slicer-mcp presets <printer|process|filament> [--printer NAME|all|any] [--filament-type TYPE] [--source system|user|all]`: the same as `list_presets` with the same defaults, but every preset on one page instead of paged output.
- `creality-slicer-mcp slice <project.3mf> [--plate N] [--out DIR]`: copy the project file into the projects store (the file itself is never changed), slice it (every plate, or plate N) and print the summary `slice_project` gives, including the G-code path. With `--out` the G-code files are copied to that folder under their upload names and the temporary project is deleted; without it the project stays in the store and the printed paths point into it.

All exit 0 on success, 2 for a usage error or invalid input (a bad flag, an unknown type, a file that is not a project, a bad environment setting) and 1 for any other failure, with the message and hint on stderr. The interactive app (run the program with no arguments in a terminal) has the same status as its "Show slicer status" menu entry, and a "Recent projects" entry that lists the projects in the store, newest first.
