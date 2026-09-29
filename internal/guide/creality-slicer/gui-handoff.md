# Work that needs the Creality Print window

Creality Print 7.2 or 7.3. Some jobs exist only in the app. Do not promise them through this server; hand the project to the user and bring it back.

## What only the app does

- Painting: colour painting (triangle, circle, fill, height range, gap fill, sphere brushes), support painting, seam painting, fuzzy skin painting, brim ears.
- Colour OBJ import with colour-to-filament matching, gradients and colour mixing (newer builds).
- Cut, boolean operations, text and emboss, measuring.
- Variable layer height with the adaptive slider and smoothing.
- Auto orientation beyond a simple lay-flat (update_object with `lay_flat` covers the simple case).
- Visual checks: rotating the 3D preview, inspecting travel moves.
- Sending to the printer through the app, and its AI features (they need an account and internet).
- Calibration test prints (see `calibration`).

Painting already in a project is kept: it travels inside the file. The tools list painted objects (get_project shows `painted`) but never parse or edit the painted data.

## The round trip

1. Call export_project with an absolute `path` ending in .3mf (and `overwrite` true only if replacing your own earlier export):

```json
{"project": "bracket", "path": "C:/work/bracket-for-app.3mf"}
```

2. Tell the user exactly what to do in the app: open the file, do the painting or check, and save with Ctrl+S (keep the same file name and place).
3. When they say it is done, call open_project on that file:

```json
{"path": "C:/work/bracket-for-app.3mf", "name": "bracket v2"}
```

The file is copied into the store as a new project; your earlier project stays as it was. Check the reply for warnings (a newer app version, missing presets on this machine, painted objects).
4. Continue with add_model, update_settings or slice_project on the new project.

The app does not need to be closed for slicing here; the server runs its own separate slicing process and never changes the app's own profiles or settings.

## Steps to give the user

Colour painting: in the Prepare stage, select the object, open the colour paint tool, pick a filament colour and paint. Fill and height-range brushes are fastest for large areas.

Seam: pick the seam paint tool; paint enforce on the wanted edge and block on the visible faces.

Support painting: choose the support paint tool for enforcers and blockers. Automatic supports still need `enable_support`.

Cut: right-click the object, choose cut, position the plane, apply. Colour, seam and support painting stay on the pieces in current builds.

Variable layer height: use the adaptive layer height tool, adjust the detail slider, smooth, apply.

Preview: after slicing in the app, switch to Preview and drag the layer slider. Colour by speed, feature or flow helps spot problems.

## When the app is the better route

If the server keeps failing a slice with an unclear error, slice in the app: its messages are fuller. Then compare with what get_slice_report shows for the same plate.

## Handing a finished slice to the printer

If the user sliced in the app and saved G-code, upload it through creality-k2-mcp as usual (upload_gcode_file with `path`, then start_print with `filename` and, for a CFS job, `source` `cfs` and a `slot_map`, after the user's go-ahead). Check spools with get_filaments first when the job has several colours; `multicolor-cfs` shows how to build the `slot_map`.
