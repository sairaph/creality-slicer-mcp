---
name: creality-slicer
description: Workflows and rules for the creality-slicer MCP server, which slices 3D models with the Creality Print installed on this computer and explains its settings. Use when the user wants to slice or prepare a print for a Creality K2 or K2 Combo, choose or explain printer, process and filament settings, set up multi-colour prints with the CFS, fix a failed slice, look at sliced G-code, or hand a slice over to the creality-k2-mcp server.
metadata:
  generator: creality-slicer-mcp
  version: "0.1.0"
---

# Creality slicer guide

Written for Creality Print 7.2 or 7.3 and the K2 family. Call the tools by their bare names, such as create_project; your client may add a prefix. The same topics are served by get_guide, so any client can read them.

## Start here

1. Call get_slicer_status. If the install is missing or unsupported, settings and guides still work, but slicing does not.
2. Call get_guide with topic `start` for the whole flow with example calls.
3. Call create_project (new) or open_project (an existing .3mf), then add_model for each mesh.
4. Check presets with list_presets and set_presets; change settings with update_settings after describe_setting.
5. Call slice_project, read the result, then hand off: see topic `multicolor-cfs`.

## Rules

- Paths are absolute paths on this computer. Source files are never changed: projects live in the MCP's own store, and export_project writes a copy.
- Never quote a setting from memory. Call search_settings to find the key and describe_setting to see its range, meaning and dependencies. Keys are Creality's own.
- Some things exist only in the app: painting, visual checks, cut, boolean, AI features. Say so, and follow topic `gui-handoff` (export_project, edit, save, open_project).
- Before any multi-colour print, read the loaded spools with the creality-k2-mcp server (get_filaments) and match each project filament to a spool by exact type. Topic `multicolor-cfs`.
- Do not start a print without the user's go-ahead.
- A failed slice is reported with an exit code name and a hint: topic `troubleshooting`.

## Topics

| Topic | What it covers |
| --- | --- |
| `start` | The beginner flow from nothing to G-code, with calls |
| `k2-combo` | K2 facts, the default presets, filament pitfalls |
| `multicolor-cfs` | CFS slots, spool matching, colours, flush, prime tower, handoff |
| `supports` | When and how to enable supports |
| `strength` | Walls, infill and where to spend material |
| `surface-quality` | Seams, layer height, ironing, fuzzy skin |
| `thin-features` | Tines, teeth, tips: wall choice, mid-air starts, tip dots, field results |
| `speed-vs-quality` | Trading time for quality |
| `modifiers-and-ranges` | Per-object, per-part and per-height settings |
| `multi-plate` | Several plates in one project |
| `calibration` | What the app and the printer can calibrate |
| `troubleshooting` | Exit codes, crashes, common mistakes |
| `gui-handoff` | Work that needs the Creality Print window |
| `glossary` | Terms, one line each; ask get_guide for a term |
