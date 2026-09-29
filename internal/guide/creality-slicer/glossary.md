# Glossary

One line per term, grouped; every line starts with the term in bold. Facts are for Creality Print 7.2 or 7.3.

## This server

- **Project** - a saved plate setup in this server's store: printer, process, filaments, models, edits.
- **Handoff** - the block a slice returns for the printer side: G-code path, upload name, tools, object labels.
- **Revision** - a counter on a project that rises with every change, to spot stale views.
- **Override** - a setting changed for the project, an object, a part or a height range instead of the preset.
- **Scope** - where a setting applies: project, object, part, layer range or plate.

## Presets

- **Preset** - a named bundle of settings of one type: printer, process or filament.
- **System preset** - shipped with the app, read-only.
- **User preset** - your own, based on a system one.
- **Process** - the print preset: layer height, walls, infill, speeds, supports.
- **Filament preset** - material settings: type, temperatures, flow limit, fans.
- **Printer preset** - the machine: bed, nozzle, start and end G-code.
- **Inherits** - a preset takes everything from a parent and lists only its differences.

## Printer and CFS

- **K2 Combo** - the K2 printer bundled with one CFS.
- **CFS** - Creality Filament System, the box with 4 spool slots; up to 4 units.
- **Slot** - one CFS position, named like T1A.
- **Tool** - the filament number in G-code (T0, T1); the printer maps it to a slot.
- **RFID spool** - a Creality spool whose tag the CFS reads for type, colour and amount.
- **External spool holder** - feeding from outside the CFS; multi-colour files then print single colour.
- **Filament type** - the material string such as PLA or PETG; slots are matched by exact type.
- **multicolor_method** - printer flag; 0 on the K2 means the printer's firmware performs the swaps.

## Multi-colour

- **Flush** - filament pushed out at a colour change to clear the old colour; also called purge.
- **Flush matrix** - flush volume from each filament to each other; dark to light needs the most.
- **Flush multiplier** - one factor scaling the whole matrix.
- **Prime tower** - a printed column that catches purge and primes the nozzle; also called wipe tower. Not built for a one-filament print or a by-object plate.
- **Flush into support** - spend the purge inside supports (or infill, or objects) to save filament.
- **Colour change** - a layer action that switches to another filament at a height.
- **Colour painting** - assigning colours to model areas in the app.
- **Beam interlocking** - small bridges between materials for a mechanical bond.

## Model and plate

- **Plate** - one bed layout; a project holds several.
- **Object** - a placed model.
- **Part** - a piece of an object that produces paths.
- **Modifier** - a shape that applies different settings where it overlaps the object.
- **Negative part** - a shape subtracted from the object when slicing.
- **Support enforcer** - a region that forces supports; a blocker forbids them.
- **Height range** - a Z band of an object with its own settings, such as another layer height.
- **Layer action** - pause, colour change or custom G-code at a height.
- **Lay flat** - rotate an object so its largest flat face sits on the bed.
- **Print sequence** - by layer (objects together) or by object (one after another).
- **Bed type** - the plate surface: Cool Plate, Textured PEI Plate and others.
- **Lock** - a plate flag: the app skips a locked plate when it arranges or orients. The arrange and orient options of slice_project do not look at it.
- **Arrange** - automatic packing of objects on a plate.

## Walls, shells and infill

- **Wall loops** - number of perimeters around each layer.
- **Wall generator** - classic (constant width) or arachne (variable width).
- **Shell** - the solid top and bottom layers.
- **Sparse infill** - the internal fill; density is a percent, pattern a shape.
- **Gyroid** - smooth, direction-neutral infill pattern.
- **Cubic** - all-round infill pattern, equally strong in every direction.
- **Lightning** - minimal infill that only holds up top surfaces.
- **Zag patterns** - zig-zag, cross-zag and locked-zag interlocking fills.
- **TPMS** - mathematical-surface infills, mostly decorative.

## Surface and layers

- **Layer height** - thickness of each layer; smaller means finer and slower.
- **Seam** - where each layer starts and ends; positions are nearest, aligned, back or random.
- **Scarf seam** - a ramped seam that hides the join.
- **Ironing** - a slow smoothing pass over top surfaces.
- **Fuzzy skin** - a deliberately rough outer wall.
- **Elephant foot** - a bulge at the first layer; compensation shrinks it.
- **Adaptive layer height** - thinner layers where detail needs them; app tool.
- **Brim** - a flat ring for bed adhesion.
- **Skirt** - a ring printed apart from the part to prime the nozzle.
- **Raft** - a stack of base layers under the model.

## Supports

- **Normal support** - grid or snug columns under overhangs.
- **Tree support** - branching supports with few contact points: slim, strong, hybrid or organic.
- **Threshold angle** - the overhang angle above which supports are added.
- **On build plate only** - supports never rest on the model.
- **Support interface** - the dense layers between support and model.

## Speed and flow

- **Volumetric speed** - plastic melted per second in mm^3/s; the filament preset caps it.
- **Flow ratio** - a multiplier on the extruded amount, tuned per filament.
- **Pressure advance** - compensation for pressure lag at corners and speed changes.
- **Acceleration** - how fast speeds change; mostly locked in the app.
- **Overhang slowdown** - automatic speed reduction on overhanging walls.
- **VFA** - fine vertical ripples that depend on speed.
- **Ringing** - ripples after sharp corners.

## Output

- **G-code** - the file the printer runs; one per plate.
- **Slice** - turning a plate into toolpaths and G-code.
- **Feature type** - the label of a move: outer wall, inner wall, sparse infill and so on.
- **Thumbnail** - a small picture embedded in the G-code for the printer's file list.
- **Exclude object** - skipping one object mid-print; needs the object labels.
- **Object label** - the name written for an object in G-code, like part.stl_id_0_copy_0.
- **Exit code** - the number the slicer process returns; 0 is success.
