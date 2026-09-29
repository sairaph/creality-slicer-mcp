# creality-slicer-mcp

MCP server by sairaph.

## Install

macOS / Linux:

```sh
curl -fsSL https://github.com/sairaph/creality-slicer-mcp/releases/latest/download/install.sh | sh
```

Windows (PowerShell):

```powershell
irm https://github.com/sairaph/creality-slicer-mcp/releases/latest/download/install.ps1 | iex
```

The installer downloads the binary, adds it to PATH, and runs `creality-slicer-mcp configure`
to register the server with the AI clients on your machine.

## Usage

```sh
creality-slicer-mcp            # interactive app (in a terminal) or MCP server (stdio)
creality-slicer-mcp mcp        # MCP server over stdio
creality-slicer-mcp install    # register with AI clients (--yes for unattended)
creality-slicer-mcp add        # register in the current project's client configs
creality-slicer-mcp login      # sign in
creality-slicer-mcp doctor     # diagnose the installation
creality-slicer-mcp update     # self-update from GitHub releases
```

Set `TRANSPORT=http` and `ADDR=host:port` to serve over Streamable HTTP instead of stdio.

## Develop

```sh
go vet ./... && go test ./...
go run . mcp
```

Releases are built by GoReleaser when a `v*` tag is pushed.

## License

MIT
