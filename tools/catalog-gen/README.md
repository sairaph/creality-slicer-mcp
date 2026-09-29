# catalog-gen

Builds the settings catalog of Creality Print from its C++ source. The main
product embeds the result (`internal/catalog/data/catalog-<version>.json`), so
this tool is only run when a new Creality Print version has to be supported.

It is a separate Go module (own `go.mod`, standard library only, same Go version as the main module), so
`go test ./...` in the repository root does not build or run it.

## What it does

The generator reads source files with `git show <ref>:<path>` from a Creality
Print git repository. It never checks anything out, never modifies the
repository, never runs Creality Print and never uses the network. It does not
build the application either: it interprets only the code shapes that define
settings.

| Source (under `src/`) | Extracted |
|---|---|
| `libslic3r/PrintConfig.cpp` | every option definition (type, label, sidetext, category, UI level, limits, enum values and labels, default, CLI fields); the `machine_max_*` axis loop, the `filament_*` override loop and the `init_extruder_enum` lambda are expanded |
| `libslic3r/PrintConfig.hpp` | the static config classes (which options an object, part or layer override can hold) |
| `libslic3r/Preset.cpp`, `PresetBundle.cpp` | the preset key lists and the project key list |
| `slic3r/GUI/Tab.cpp` | tab, page and group layout; per-object, per-part, per-layer and plate key sets; enable and visibility rules |
| `slic3r/GUI/ConfigManipulation.cpp` | enable and visibility rules, forced value changes |
| `resources/profiles/CrealityUserMode.json`, `resources/images/process/ProcessConfig.json` | per-key vendor policy, documentation paths |

## Usage

The repository path and the ref are always flags; there are no defaults.

```
# from tools/catalog-gen
go run . -repo <path to a CrealityPrint clone> -ref v7.2.1 \
    -slim ../../internal/catalog/data/catalog-7.2.1.json

# the full catalog (every option class, tooltips included) for analysis only, never committed
go run . -repo <path> -ref v7.2.1 -out settings-catalog-7.2.1.json

# compare two full catalogs, print the GUI tree or the dependency drivers, draw a hand-check sample
go run . -diff -diff-out diff.json settings-catalog-7.2.1.json settings-catalog-7.3.0.json
go run . -tree settings-catalog-7.2.1.json
go run . -deps settings-catalog-7.2.1.json
go run . -sample settings-catalog-7.2.1.json -n 15 -seed 1
```

A shallow clone is enough: `git clone --depth 1 --no-checkout <url> cp && git -C cp fetch --depth 1 origin tag v7.2.1`.

Every generator run prints its validation results. Read them:

* `expected definitions` equals `parsed definitions` (the count comes from an
  independent line scanner);
* `parser warnings: 0` and no unexplained non-literal definition sites;
* no preset key without a definition, no GUI key without a definition.

If one of these fails, the source uses a shape the parser does not know yet
(a new loop or lambda that defines options, a new statement in a Tab build
function, a renamed list). The messages carry `file:line`; extend `defs.go`,
`gui.go` or `cond.go` and add a case to `gen_test.go`.

## The slim catalog

`-slim` writes what the product embeds: only the settings (the print
definition class, not command-line options or G-code placeholders), with

`key, owner, preset_types, value_type, is_vector, nullable, default, min, max,
enum{values, labels}, label, full_label, sidetext, category, ui_level,
gui{tab, page, group}, scopes, gated_by, forced_by, gui_enum_restrictions,
creality_vendor_policy, cli_flags, nocli, tooltip_hash`.

* `min` and `max` are the effective limits: the application stores them as
  `int`, so fractional source values are truncated.
* Options are ordered as the GUI builds them (tabs, pages, groups, lines), then
  the settings the GUI does not show.
* `gated_by` and `forced_by` carry the GUI's enable rules as small condition
  trees (`cond.go`); the product turns them into sentences. The slim file
  holds no fragments of the application's C++: a condition part the parser
  cannot express becomes an `unparsed` node, and a forced-value rule whose value
  is computed in code is left out.
* SLA settings (owners `sla_*`) are not included, and the `sla_*` preset types
  are removed from settings shared with FDM presets.
* **No tooltip text is written.** Tooltips are Creality's AGPL text.
  `tooltip_hash` is the FNV-1a 64-bit hash (16 lowercase hex digits) of the
  exact message id: the C++ string literal after unescaping and joining
  adjacent literals, which is the string gettext looks up. The product finds the
  wording in the installed application's own `.mo` files by the same hash
  (`internal/motext`).
* Labels, full labels, units, enum labels and GUI tab, page and group names are
  short phrases and are kept as text.

## Embedded versions

`internal/catalog/data/` holds one slim file per supported major.minor:
`catalog-7.2.1.json` (622 options, source tag `v7.2.1`) and
`catalog-7.3.0.json` (684 options, `v7.3.0`). Regenerate a file with the `-slim`
command above and the matching `-ref`. To support a new minor version, generate
its file, add it to `TestVersionsAndLoadByMajorMinor` in `internal/catalog`, and
run the tests; they check every embedded file for tooltip text, SLA options and
code fragments. Whether a setting is a single value or a per-nozzle list is read
from the file of the version in use (43 settings changed between 7.2 and 7.3).

## Tests

`go test -timeout 5m -p 2 -count=1 ./...` in this directory. The tests use
invented C++ snippets only. The module cannot import the main module's
`testhome`, so `TestMain` isolates `HOME` itself.
