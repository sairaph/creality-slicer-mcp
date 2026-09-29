# Calibration

Creality Print 7.2 or 7.3 and the K2. This server cannot generate calibration models or run calibration prints in this version: they are app and printer features. What it can do is apply the numbers you get.

## What the app offers

The Calibration menu in Creality Print builds test prints for one filament:

| Test | What you read | Result goes into |
| --- | --- | --- |
| Temperature tower | The temperature with the cleanest surface and least stringing | `nozzle_temperature` |
| Flow rate, coarse then fine | The step with the smoothest top surface (gaps mean too little flow, roughness too much) | `filament_flow_ratio` |
| Pressure advance tower | The height where corners look best; value = start + height x step | `pressure_advance` (with `enable_pressure_advance`) |
| Max volumetric speed | The height where extrusion starts to fail; value = start + height x step | `filament_max_volumetric_speed` |
| VFA | The speed band with the least vertical ripple | outer wall speed |

Do them in that order: temperature, flow, pressure advance, flow limit. Save the outcome in the app as a user filament preset.

## Using the results here

If the tuned preset exists on this computer (saved in the app), pick it with set_presets and a `filaments` entry. It shows in list_presets with `source` `user`.

Or set the numbers for one project. These filament keys are per filament slot. At project scope a list gives one value per slot (its length must equal the number of project filaments) and a single value is applied to every slot:

```json
{"project": "bracket", "values": {"filament_flow_ratio": [0.96], "enable_pressure_advance": [true], "pressure_advance": [0.035]}}
```

Send that to update_settings. describe_setting shows the valid range of each key.

## Printer-side calibration

The K2 can run its own tests when a job starts. In Creality Print's send dialog these appear as automatic pressure advance and flow ratio calibration flags. They run on a test disc before the print and store what they find on the printer.

- Use the original textured plate for them, not other surfaces.
- Turn them off again afterwards; routine prints do not need them.
- Bed levelling can run on the first print after power-up regardless of the flags.

This server hands the printer plain G-code; the flags belong to the app's own send path and to the printer's screen.

## New filament checklist

1. Start from a Generic preset of the same type (see `k2-combo`); the type must match the loaded spool.
2. Dry the spool.
3. Run the app's temperature, flow, pressure advance and flow limit tests.
4. Save the preset in the app, then use it here.
5. Slice a small part and inspect one layer with get_slice_report.

## Limits

Calibration models are not created by this server. Do not invent numbers: a flow ratio far from 1.0 or a pressure advance far from the preset's value usually means a mechanical problem.
