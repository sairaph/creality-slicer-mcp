# Creality Slicer MCP

Slice for your Creality K2 or K2 Combo from Claude Desktop and other MCP
clients, with the Creality Print you already have installed (7.3 preferred,
7.2 works). The assistant builds real Creality Print projects with the right
printer, process and filament presets, places your models, explains every
setting in plain words before changing it, slices to G-code, and shows previews
of the plate and of single layers. It handles multi-colour work on the CFS
(per object, per part, per height and per layer) and measures what the slicer
really printed, object by object, so problems such as a thin feature starting
in mid-air are found before printing. The projects are ordinary 3MF files you can
open in Creality Print to paint or check, and the sliced result comes with
everything the [creality-k2-mcp](https://github.com/sairaph/creality-k2-mcp)
server needs to upload and print it. This server never talks to the printer.
It is a single self-contained binary.

## What you can ask

- "Slice C:\Models\bracket.stl for my K2 in PLA, strong enough to hold a shelf."
- "What does `sparse_infill_density` do, and what is it set to in my project?"
- "Make this a two colour print, white and black, and check my CFS spools first."
- "Open my saved project `lamp.3mf`, use 4 walls, and slice it."
- "Reinforce this hinge with denser infill around the pin hole only."
- "Print the same part three times on a second plate and slice both plates."
- "Show me the front view of the hinge with the modifier, then layer 40 coloured by speed."
- "Make the screw's markings white: join the marking parts to the screw and give them filament 2."
- "Check the sliced plate: does any thin tine start printing in mid-air?"
- "Slice it, then upload it to the printer and start the print."

## Quick start

You need [Creality Print](https://www.creality.com/pages/download-creality-print)
on the computer that runs this server. Creality Slicer MCP is a single
self-contained binary: it needs no Python, uv or pip on your machine.

Windows (PowerShell):

```powershell
irm https://github.com/sairaph/creality-slicer-mcp/releases/latest/download/install.ps1 | iex
```

macOS / Linux:

```sh
curl -fsSL https://github.com/sairaph/creality-slicer-mcp/releases/latest/download/install.sh | sh
```

The installer downloads `creality-slicer-mcp`, verifies its SHA256 checksum,
puts it on your `PATH` and starts the setup wizard, which:

1. finds the AI clients on your machine (Claude Desktop, Claude Code, Cursor,
   VS Code, Windsurf, Zed and more) and lets you pick the ones to register with;
2. registers the `creality-slicer-mcp` server with the selected clients, then
   writes the `creality-slicer` guide skill into the skill folder each
   selected client reads, such as `~/.claude/skills` for Claude Code or
   `~/.agents/skills` for Codex and Gemini CLI. Claude Desktop takes skills
   only as an upload, so it gets a note instead.

Nothing is written until step 2, so cancelling earlier leaves your machine as
it was. An entry you edited by hand, or one that runs another program, is kept
unless you select that client, name it with `--clients` or pass `--all`.

Restart your AI client afterwards. Run `creality-slicer-mcp` on its own to open
the app: your projects (open one in Creality Print, export or delete it), the
slicer status, a health check and "Configure AI clients" to run the setup again.
Run `creality-slicer-mcp doctor` at any time
to check the installation, and `creality-slicer-mcp update` to update to the
latest release. `creality-slicer-mcp uninstall --all` removes the client
entries, the guide skill, the cache and the program; your projects are kept.

## Documentation

| Guide | Contents |
| --- | --- |
| [Installation](docs/installation.md) | Installer, commands, unattended installs, troubleshooting |
| [Configuration](docs/configuration.md) | Environment variables, per-user folders |
| [Tools](docs/tools.md) | Every tool, its arguments and what its replies contain |
| [Examples](docs/examples.md) | Seven worked flows: a first print, two colours on the CFS, local reinforcement, an app project, several plates, a two-colour part from a CAD plate, thin-feature checks |

## Requirements

- Creality Print 7.3 or 7.2 on the computer that runs the server. Nothing else
  is needed from you: the server drives the app's own command line.
- Windows for slicing. On macOS and Linux the server installs and answers, but
  reports that it cannot slice.
- An AI client that speaks MCP. The setup wizard finds the common ones.
- For printing, the creality-k2-mcp server; this one only prepares the G-code.

## Third-party data

The settings catalog embedded in the program (setting names, types, ranges, short labels and the rules that enable or force a setting) is derived from the Creality Print source code (AGPL-3.0, [github.com/CrealityOfficial/CrealityPrint](https://github.com/CrealityOfficial/CrealityPrint)) as facts about the settings. It contains no Creality descriptive text: the descriptions the tools show are read at run time from your own Creality Print installation. See [NOTICE](NOTICE).

## License

MIT
