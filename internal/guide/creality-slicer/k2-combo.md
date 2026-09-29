# K2 Combo: printer facts, presets, pitfalls

Facts for Creality Print 7.2 or 7.3 and the K2 family. Other models (K2 Plus, K2 Pro, K2 SE) have their own presets; never mix them into a K2 project.

## The machine

- The K2 Combo is a K2 with one CFS, the multi-spool filament box with 4 slots. The build volume is a 260 mm cube. Nozzle up to 300 C, bed up to 100 C, no heated chamber.
- The printer is a Klipper machine. It reads its G-code as `T0`, `T1`, ... for filament changes and maps those to CFS slots itself.
- Good materials: PLA family, PETG, PET, PLA-CF, and ABS with care (no chamber heater, so warping is likely). Materials that need a hot chamber are not a fit.

## Default presets

Printer presets, one per nozzle: `Creality K2 0.4 nozzle` (the default), `Creality K2 0.2 nozzle`, `Creality K2 0.6 nozzle`, `Creality K2 0.8 nozzle`.

Process presets for the 0.4 nozzle, named `<height>mm Standard @Creality K2 0.4 nozzle`: 0.08, 0.12, 0.16, 0.20 (default), 0.24, 0.28, plus a 0.08 HueForge one. The other nozzles have one Standard process each (0.10 for 0.2, 0.30 for 0.6, 0.40 for 0.8).

Filament presets end in `@Creality K2 0.4 nozzle`. The default is `Hyper PLA`. Others include the CR-PLA and CR-PETG lines and Generic PLA, PETG, ABS, TPU and more. list_presets with `type` `filament` and `filament_type` lists them, each with its type, vendor and nozzle temperature. get_preset shows every value, or compares two presets with `compare_to`.

What the default `0.20mm Standard` process sets, worth knowing before you change anything: layer height 0.2, prime tower on in the preset (width 40, volume 45; the slicer only builds it when two or more filaments are used and the plate prints by layer, see multicolor-cfs), flush into supports on, print sequence by layer, object exclusion on, supports off, brim automatic.

## Filament type is an exact string

The printer matches project filaments to CFS slots by their type, and the comparison is exact. A preset whose type is `PETG-GF` will not match a spool the printer knows as `PETG`. Read `filament_type` in get_preset before you rely on a preset, and compare it with the spool (see `multicolor-cfs`).

## Pitfalls to remember

- Slower than expected: a low `filament_max_volumetric_speed` in the filament preset is the usual reason. Read it with get_preset and compare the speeds of the sliced result (get_slice_report).
- Custom filaments need a slot: an unlisted filament type is a common reason a print cannot map to the CFS. Use a Generic preset of the right type as the base.
- Flush volumes: the printer's own firmware may purge more than the slicer asks for, so lowering flush settings can have little effect. Treat flush tuning as unverified on the K2.
- Bed levelling can run on the first print after power-up whatever the send options say.
- Supports are off by default. A model with overhangs needs `enable_support`.
- If the installed Creality Print is newer or older than the printer's firmware expects, estimates and purge behaviour can drift. Keep both current.

## Settings the K2 cares about

`layer_height`, `wall_loops`, `sparse_infill_density`, `enable_support`, `enable_prime_tower`, `print_sequence`, `filament_type`, `filament_max_volumetric_speed`. Look each up with describe_setting before changing it; ranges are enforced.

## PETG and the bed

Creality's CR-PETG preset supports the Cool Plate at 70 C, which is the default bed type, and the Textured PEI plate. Many users prefer the textured PEI plate for PETG, because PETG can stick too well to a smooth plate. Replies show the plate type and the first-layer bed temperature for each filament. Set `bed_type` on create_project, or `curr_bed_type` with manage_plates `set`, to change it.

## Nozzle sizes

A 0.6 or 0.8 nozzle trades detail for speed and strength. Pick the printer preset that matches the nozzle actually fitted: process and filament presets are tied to it, and set_presets refuses an incompatible combination and lists what fits.
