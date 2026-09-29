# Strength: walls, infill and where to spend material

Creality Print 7.2 or 7.3. Strength comes mostly from walls and shells, much less from infill. Spend material where the load goes.

## Order of effect

1. Walls (`wall_loops`): each extra wall adds strength around the whole part. Going from 2 to 4 walls usually beats doubling infill.
2. Top and bottom shells (`top_shell_layers`, `bottom_shell_layers`): the solid skins. Raise them for parts that are pressed on flat faces or must be watertight.
3. Infill density (`sparse_infill_density`, a percent from 0 to 100) and pattern (`sparse_infill_pattern`).
4. Material and orientation: layers are weakest between each other. Orient so the load does not pull layers apart. update_object with `rotation` does that.

## Choosing an infill pattern

`sparse_infill_pattern` has over thirty values. Practical picks:

- `grid` or `cubic`: solid all-round choices; cubic is equally strong in all directions.
- `gyroid`: smooth, isotropic, good for flexible or impact loads.
- `lightning`: minimum material for top surfaces only, no strength; use for decoration.
- `zig-zag` and the zag family (7.2 adds `cross-zag` and others): fast, with interlocking options for regions with different densities.
- The TPMS patterns (`tpmsd`, `tpmsfk`, gradients): decorative or specialised.

Patterns are gated: some settings only appear for some patterns. describe_setting on a key lists what it depends on.

## Typical recipes

Functional bracket that takes load:

```json
{"project": "bracket", "values": {"wall_loops": 4, "top_shell_layers": 5, "bottom_shell_layers": 4, "sparse_infill_density": "25%", "sparse_infill_pattern": "cubic"}}
```

Send to update_settings. Light display piece:

```json
{"project": "vase", "values": {"wall_loops": 2, "sparse_infill_density": "10%", "sparse_infill_pattern": "lightning"}}
```

## Strong only where needed

A whole part at high density is slow and heavy. Instead, keep the project settings modest and reinforce a region: add_modifier with `kind` `modifier` around the stressed area and `values` such as a higher `sparse_infill_density` or `wall_loops`. See `modifiers-and-ranges`. You can also give one object stronger settings than its neighbours with update_settings at `scope` `object`.

## Walls: classic or arachne

`wall_generator` is `classic` (constant line width, best for sharp corners and speed) or `arachne` (variable width, better for thin features and fine text). Some thin-wall options exist for one generator only; the dependency notes from describe_setting say which.

## Material matters more than settings

PLA is stiff and brittle, PETG tougher and slightly flexible, ABS strong but prone to warp without a chamber. Choose the filament preset for the job before tuning infill (see `k2-combo`). Carbon-fibre types stiffen but still split between layers.

## Check the result

get_slice_report with `section` `filaments` shows grams per tool. If a part came out much heavier than expected, look at density and wall count before anything else.
