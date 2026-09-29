# Configuration

[Back to README](../README.md) · [Installation](installation.md) · [Tools](tools.md)

## Environment variables

The MCP server reads its settings from environment variables, which you set in
the `env` block of the `creality-slicer-mcp` entry in your AI client's
configuration:

| Variable | Default | Purpose |
| --- | --- | --- |
| `CREALITY_SLICER_MCP_CMD` | auto-detected | Absolute path of `CrealityPrint.exe`, when Creality Print is installed somewhere the server does not find it. It must be an absolute path to an existing file. |
| `TRANSPORT` | `stdio` | `http` serves MCP over Streamable HTTP instead. |
| `ADDR` | `127.0.0.1:8080` | Listen address when `TRANSPORT=http`. |
| `CREALITY_SLICER_MCP_ONLY_TEXT_FEEDBACK` | unset (screenshots on) | `1` (or `true`, `yes`, `on`) leaves the automatic screenshots out of the replies of the tools that change a project; `0` (or `false`, `no`, `off`) keeps them. It wins over the tools' `include_screenshot` argument. `get_view` still draws: asking for a picture is not feedback. |

Slicing needs Creality Print on Windows; on macOS and Linux the server
installs and answers, but reports that it cannot slice, and
`CREALITY_SLICER_MCP_CMD` does not change that.

Use `CREALITY_SLICER_MCP_ONLY_TEXT_FEEDBACK=1` for a client that cannot show images or when replies must stay small. Any other value than the ones listed stops the server at startup like the other variables.

An invalid value of `CREALITY_SLICER_MCP_CMD` stops the server at startup (exit
status 2) with a message naming the variable, and `creality-slicer-mcp doctor`
reports it as a failure. `TRANSPORT` must be `stdio` or `http` (case-insensitive); any other
value stops the server the same way, and with `http` so does an `ADDR` that is
not `host:port`. For example, a client entry that names the slicer explicitly:

```json
{
  "mcpServers": {
    "creality-slicer-mcp": {
      "command": "creality-slicer-mcp",
      "args": ["mcp"],
      "env": {
        "TRANSPORT": "stdio",
        "CREALITY_SLICER_MCP_CMD": "C:\\Program Files\\Creality\\Creality Print 7.2\\CrealityPrint.exe"
      }
    }
  }
}
```

Restart your AI client after changing its configuration. Running
`creality-slicer-mcp install` again leaves an entry you edited as it is (the
wizard shows it deselected), unless you select that client, name it with
`--clients`, or pass `--all`; `creality-slicer-mcp uninstall` removes it like
any other.

## Per-user folders

Everything the server keeps for you lives under `.creality-slicer-mcp` in your
home folder:

| Folder | Holds |
| --- | --- |
| `.creality-slicer-mcp/cache` | Scratch files of a run, removed when the run ends. `uninstall --all` removes the folder. |
| `.creality-slicer-mcp/projects` | Your projects, one folder each. `uninstall --all` keeps them. |

On macOS and Linux the installer also puts the program in
`.creality-slicer-mcp/bin`; on Windows it goes to
`%LOCALAPPDATA%\creality-slicer-mcp\bin`. `creality-slicer-mcp doctor` checks
that the data folder can be written.
