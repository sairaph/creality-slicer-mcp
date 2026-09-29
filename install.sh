#!/bin/sh
set -e
OWNER="sairaph"
REPO="creality-slicer-mcp"
BIN="creality-slicer-mcp"
# Extra flags for the configure step, e.g. CONFIGURE_ARGS="--yes --all" sh install.sh
CONFIGURE_ARGS="${CONFIGURE_ARGS:-}"

# --- detect OS / arch ------------------------------------------------------
OS="$(uname -s)"
ARCH="$(uname -m)"
case "$OS" in
  Linux*)  os=linux ;;
  Darwin*) os=darwin ;;
  *) printf '\n  Unsupported OS: %s\n' "$OS" >&2; exit 1 ;;
esac
case "$ARCH" in
  x86_64|amd64)  arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) printf '\n  Unsupported architecture: %s\n' "$ARCH" >&2; exit 1 ;;
esac

ASSET="${BIN}-${os}-${arch}"
URL="https://github.com/${OWNER}/${REPO}/releases/latest/download/${ASSET}"
INSTALL_DIR="$HOME/.${REPO}/bin"
TARGET="$INSTALL_DIR/$BIN"
mkdir -p "$INSTALL_DIR"

printf '\n  %s installer\n\n  Downloading %s...\n' "$BIN" "$ASSET"

TEMP="${TARGET}.new"
trap 'rm -f "$TEMP" "$TEMP.err"' EXIT HUP INT TERM

download_failed() {
  printf '\n  Download failed. Please check your connection and try again.\n' >&2
  printf '  URL: %s\n' "$URL" >&2
  if [ -n "$1" ]; then
    printf '  Reason: %s\n' "$1" >&2
  fi
  exit 1
}

if command -v curl >/dev/null 2>&1; then
  curl -fSL "$URL" -o "$TEMP" 2>/dev/null || download_failed
elif command -v wget >/dev/null 2>&1; then
  if ! wget -q --show-progress -O "$TEMP" "$URL" 2>"$TEMP.err"; then
    if ! wget -q -O "$TEMP" "$URL" 2>"$TEMP.err"; then
      download_failed "$(cat "$TEMP.err" 2>/dev/null | tr '\n' ' ')"
    fi
  fi
else
  download_failed "neither curl nor wget is available"
fi
rm -f "$TEMP.err"

if command -v sha256sum >/dev/null 2>&1; then
    SHA256_CMD="sha256sum"
elif command -v shasum >/dev/null 2>&1; then
    SHA256_CMD="shasum -a 256"
else
    SHA256_CMD=""
fi

if [ -n "$SHA256_CMD" ]; then
    CHECKSUM_URL="${URL%/*}/SHA256SUMS.txt"
    EXPECTED=""
    if command -v curl >/dev/null 2>&1; then
        EXPECTED=$(curl -fsSL "$CHECKSUM_URL" 2>/dev/null | grep " $ASSET\$" | awk '{print $1}')
    elif command -v wget >/dev/null 2>&1; then
        EXPECTED=$(wget -q -O - "$CHECKSUM_URL" 2>/dev/null | grep " $ASSET\$" | awk '{print $1}')
    fi
    if [ -n "$EXPECTED" ]; then
        ACTUAL=$($SHA256_CMD "$TEMP" | awk '{print $1}')
        if [ "$EXPECTED" != "$ACTUAL" ]; then
            printf '\n  SHA256 mismatch.\n' >&2
            exit 1
        fi
    else
        printf '  Warning: could not fetch SHA256SUMS.txt; the download was not verified.\n' >&2
    fi
else
    printf '  Warning: no sha256 tool found; the download was not verified.\n' >&2
fi

if [ ! -s "$TEMP" ]; then
  printf '  Download did not complete; nothing was installed.\n' >&2
  exit 1
fi
chmod +x "$TEMP"
if ! mv -f "$TEMP" "$TARGET"; then
	printf '\n  Failed to install binary to %s\n' "$TARGET" >&2
	exit 1
fi
trap - EXIT HUP INT TERM

case ":$PATH:" in
  *":$INSTALL_DIR:"*) on_path=1 ;;
  *) on_path=0 ;;
esac

if [ "$on_path" -eq 0 ]; then
  line="export PATH=\"$INSTALL_DIR:\$PATH\""
  for rc in "$HOME/.zshrc" "$HOME/.bashrc" "$HOME/.profile" "$HOME/.bash_profile"; do
    [ -f "$rc" ] || continue
    if ! grep -qF "$INSTALL_DIR" "$rc" 2>/dev/null; then
      printf '\n# added by %s installer\n%s\n' "$BIN" "$line" >> "$rc"
    fi
    on_path=2
  done
fi

PATH="$INSTALL_DIR:$PATH"
export PATH

if ( : </dev/tty ) 2>/dev/null; then
  "$TARGET" configure $CONFIGURE_ARGS </dev/tty || {
    printf '  configure did not complete.\n'
    printf '  Re-run `%s configure` later to finish setup.\n' "$BIN"
  }
else
  printf '\n  Not running on a terminal. Finish setup with:\n    %s configure\n' "$BIN"
fi

if [ "$on_path" -eq 0 ]; then
  printf '\n  Add this to your shell profile:\n    export PATH="%s:$PATH"\n' "$INSTALL_DIR"
elif [ "$on_path" -eq 2 ]; then
  printf '\n  Open a new terminal so `%s` is on your PATH.\n' "$BIN"
fi
