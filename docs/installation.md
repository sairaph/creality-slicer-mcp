# Installation

[Back to README](../README.md) · [Configuration](configuration.md) · [Tools](tools.md)

## Requirements

- Windows with Creality Print 7.2.x or 7.3.x installed (7.3 preferred; it needs nothing extra from you) on the computer that runs the server. Slicing needs it; on macOS and Linux the server installs and answers, but reports that it cannot slice. Other Creality Print versions are reported as unsupported and slicing is disabled (settings, presets and guides still work).
- An AI client that speaks MCP: Claude Desktop, Claude Code, Cursor, VS Code, Windsurf, Zed, Codex CLI, Gemini CLI and others the wizard detects.
- Nothing else. `creality-slicer-mcp` is a single binary; it needs no Python, uv or pip.

## Install

Windows (PowerShell):

```powershell
irm https://github.com/sairaph/creality-slicer-mcp/releases/latest/download/install.ps1 | iex
```

macOS / Linux:

```sh
curl -fsSL https://github.com/sairaph/creality-slicer-mcp/releases/latest/download/install.sh | sh
```

The script downloads `creality-slicer-mcp-<os>-<arch>` for your machine, checks its SHA256 against `SHA256SUMS.txt` (and installs nothing it cannot verify), puts it on your `PATH`, and runs `creality-slicer-mcp configure`, the setup wizard.

Options for the scripts:

| What | Windows (`install.ps1`) | macOS / Linux (`install.sh`) |
| --- | --- | --- |
| A specific release | `-Version v0.1.0` | `VERSION=v0.1.0` |
| Flags for the wizard | `-ConfigureArgs "--yes"` | `CONFIGURE_ARGS="--yes"` |

With parameters, PowerShell needs the script block form: `& ([scriptblock]::Create((irm <url>))) -Version v0.1.0 -ConfigureArgs "--yes"`. With `CONFIGURE_ARGS`, put the variable on the `sh` side of the pipe: `curl -fsSL <url> | CONFIGURE_ARGS="--yes" sh`.

## The setup wizard

`creality-slicer-mcp install` (or `configure`) in a terminal:

1. Finds the AI clients on your machine and lets you pick the ones to register with. A client whose `creality-slicer-mcp` entry you edited by hand (settings added to its `env`), or that runs another program under that name, starts unticked so the entry is kept; ticking it replaces the entry.
2. Registers the server with the selected clients, then writes the `creality-slicer` guide skill into the skill folder each of them reads. Claude Desktop takes skills only as an upload, so it gets a note instead.

Nothing is written until step 2, so leaving the wizard earlier (`q`, Ctrl+C) changes nothing. It then exits with status 3, which the install scripts use to undo what they placed (the binary and the `PATH` entry), so a cancelled install leaves your machine as it was.

### Unattended

`creality-slicer-mcp install --yes` skips the wizard. It also skips it without a terminal, or with `--all` or `--clients`.

| Flag | Meaning |
| --- | --- |
| `--yes` | No questions; register with every detected client that has no entry yet. |
| `--all` | Every detected client, replacing edited entries too (never another program's). |
| `--clients a,b` | Only these client ids; replaces even another program's entry. An unknown id exits with status 2. |
| `--dry-run` | Show what would change, write nothing. |
| `--name` | Register under another server name. |
| `--scope project --dir <folder>` | Register in that project's client configs instead of yours (the same as `add`). |

`--email` and `--token` are accepted for compatibility and ignored: the server has no login.

Running install again is safe: entries that are already right are left as they are, edited ones are kept unless you ask, and the guide skill is rewritten.

## What is written where

| What | Where |
| --- | --- |
| The program | Windows: `%LOCALAPPDATA%\creality-slicer-mcp\bin`. macOS / Linux: `~/.creality-slicer-mcp/bin`. Added to your user `PATH` (registry on Windows, shell profiles elsewhere). |
| Client registration | One `creality-slicer-mcp` entry in each selected client's config file: the program, `mcp` as its argument, and `TRANSPORT=stdio` in `env`. |
| Guide skill | `~/.claude/skills/creality-slicer` (Claude Code), `~/.agents/skills/creality-slicer` (Codex, Gemini CLI and others), `~/.kiro/skills/creality-slicer`, Continue's own `skills` folder; in a project, the same folders inside the project. Only a folder that carries this program's marker is ever replaced or removed. |
| Your data | `~/.creality-slicer-mcp` with `projects/` (your projects) and `cache/` (scratch files of a run, removed when it ends). |

The server never changes Creality Print's own profiles or settings. See [Configuration](configuration.md) for the environment settings and the client entry.

## Project scope

`creality-slicer-mcp add` (or `install --scope project`) registers the server in the config files of the current folder (or `--dir`), so a team can share it through the repository. It takes the same flags as install.

## Update

```sh
creality-slicer-mcp update
```

Checks GitHub for a newer release, replaces the binary and refreshes the guide skill copies this program wrote. A development build (`dev`) does not update itself. `update --from <file>` swaps in a binary that was already downloaded and verified. Restart your AI client afterwards.

## Uninstall

```sh
creality-slicer-mcp uninstall
```

Removes the server entry from your clients and the guide skill from their skill folders. Entries that run another program are left alone unless you name the client with `--clients`. `--scope project` does the same for one project.

`creality-slicer-mcp uninstall --all` also removes the cache and the program with its `PATH` entry. It refuses to run together with `--scope project` or `--clients`, because it deletes the program the other clients still run. Your projects are your work, not part of the installation: they are kept, and the command prints where they are so you can delete that folder yourself. On Windows the program's folder is removed after the command exits; close AI clients that still run it first. Add `--dry-run` to see what would go.

## Doctor

```sh
creality-slicer-mcp doctor
```

Changes nothing that stays (the data folder check creates and removes one temporary file), and prints one line per check with `[ok]`, `[warn]` or `[fail]`:

| Check | What it tells you |
| --- | --- |
| Executable, PATH | The running program, and whether its folder is on `PATH`. |
| AI clients | Which clients are registered, including edited entries and entries that run another copy. |
| Settings | The environment settings; a bad `CREALITY_SLICER_MCP_CMD`, `TRANSPORT` or `ADDR` is a failure because the server will not start. |
| Data folder | Whether `~/.creality-slicer-mcp` can be written. |
| Creality Print | Version, build and dialect of the install found; a warning "slicing disabled" when it is missing or neither 7.2.x nor 7.3.x. |
| Creality Print data | Whether the app's own data folder can be read. |
| Profiles | How many process and filament presets the installed bundle has for `Creality K2 0.4 nozzle`. |
| Setting descriptions | How many settings have a description from the app's message catalogs. |
| Settings catalog | Settings the installed K2 presets use that the shipped catalog does not know (up to ten are listed); they are passed through untouched. |
| Update | Whether a newer release exists (skipped in development builds). |

A warning does not make doctor fail; the exit status is 1 only when a check fails.

## Running with no client

Started bare in a terminal, `creality-slicer-mcp` opens a small menu app (run doctor, show slicer status, recent projects, quit). Started by an AI client, or with `mcp`, it serves MCP over stdio; `TRANSPORT=http` serves Streamable HTTP on `ADDR`.
