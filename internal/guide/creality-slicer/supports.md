# Supports

Creality Print 7.2 or 7.3. Supports are material printed under overhangs so the layers above have something to sit on. On the K2 Combo they are off by default.

## Turn them on first

Nothing in the support group has any effect until `enable_support` is true. Forgetting it is the most common reason a model with overhangs comes out drooping. Set it for the whole project:

```json
{"project": "bracket", "values": {"enable_support": true}}
```

Send that to update_settings. To support only one object, use `scope` `object` with a `target`; supports for the whole plate need the project scope.

## Which kind

`support_type` has four values: `normal(auto)`, `tree(auto)`, `normal(manual)`, `tree(manual)`. Auto types decide where supports go from the overhang angle; manual types only support where you place enforcers.

- Normal supports (styles `grid`, `snug`) suit large flat overhangs. They are sturdier and leave a flat underside, but are harder to remove.
- Tree supports (styles `tree_slim`, `tree_strong`, `tree_hybrid`, `organic`) suit small, uneven or organic overhangs. They use less material and touch the model in fewer places. `organic` is the default tree style.

`support_style` only offers the styles that fit the chosen type; a wrong one is rejected with the valid list.

## Settings worth knowing

- `support_threshold_angle`: overhangs steeper than this get support (1 to 90 degrees; lower means more support). Applies to auto types.
- `support_on_build_plate_only`: supports never rest on the model, only on the bed. Cleaner surfaces, but overhangs that start mid-air lose support.
- `raft_layers`: a raft under the whole model instead of, or as well as, supports.
- `support_filament`: which filament prints supports (0 follows the object). In a multi-colour print a different material here needs purge, see `multicolor-cfs`.

Look up anything else with search_settings using `area` "process/Support"; the finer controls (patterns, interface layers, spacing) are advanced level, so pass `level` "advanced" to see them.

## Enforce and block

To force support in one spot, or forbid it in another, add a shape with add_modifier and `kind` `support_enforcer` or `support_blocker`. An enforcer only helps when `enable_support` is on.

```json
{"project": "bracket", "object": "arm", "kind": "support_blocker", "shape": "box", "size": [20, 20, 10], "position": [0, 0, -15]}
```

Painting supports by hand is app-only: see `gui-handoff`.

## After slicing

Read the preview or get_slice_report with `section` `layer` and `color_by` `feature`: support layers have their own colour. If supports look excessive, raise the threshold angle or switch to tree.

## Removal tips

- Print supports from the same material unless you want an interface material.
- A larger gap between support and model makes removal easier and the underside rougher.
- PETG bonds strongly to itself: prefer tree supports and a bigger gap.
