#!/bin/sh
# Installs creality-slicer-mcp from GitHub releases and runs its setup wizard.
# Cancelling the wizard undoes everything this script changed.
#
#   curl -fsSL https://github.com/sairaph/creality-slicer-mcp/releases/latest/download/install.sh | sh
#
# Environment:
#   VERSION=v0.1.0           install a specific release instead of the latest
#   CONFIGURE_ARGS="--yes"   extra flags for the setup wizard
OWNER="sairaph"
REPO="creality-slicer-mcp"
BIN="creality-slicer-mcp"
VERSION="${VERSION:-latest}"
CONFIGURE_ARGS="${CONFIGURE_ARGS:-}"
# `creality-slicer-mcp configure` exits with 3 when the wizard is left before it
# changed anything.
EXIT_CANCELLED=3
MARKER="# added by ${BIN} installer"

# --- shell profile edits, undone exactly on cancel --------------------------
# RC_STATE is a private directory holding, for each profile this run
# changed, a copy of the original and the text that was appended.

rc_append() { # rc_append <profile> <line>; returns non-zero when a step failed
  rc="$1"; line="$2"
  key=$(printf '%s' "$rc" | cksum | awk '{print $1}') || return 1
  cp -p "$rc" "$RC_STATE/$key.orig" || return 1
  # Empty files are written with printf, a regular builtin: a failed
  # redirection on the special builtin `:` would end the whole script.
  printf '' > "$RC_STATE/$key.added" || return 1
  # End an unterminated last line first, so the block starts on its own line.
  if [ -s "$rc" ] && [ -n "$(tail -c 1 "$rc")" ]; then
    printf '\n' >> "$RC_STATE/$key.added" || return 1
  fi
  printf '\n%s\n%s\n' "$MARKER" "$line" >> "$RC_STATE/$key.added" || return 1
  # .path once .orig and .added are complete: rc_undo acts on a profile once
  # its .path exists, and an interrupt can come between any two steps.
  printf '%s\n' "$rc" > "$RC_STATE/$key.path" || return 1
  cat "$RC_STATE/$key.added" >> "$rc"
}

rc_create() { # rc_create <profile>: creates it, and rc_undo deletes it again
  rc="$1"
  key=$(printf '%s' "$rc" | cksum | awk '{print $1}') || return 1
  # Recorded as an empty profile before it exists, so rc_undo removes it
  # even when setup is interrupted before rc_append runs.
  printf '' > "$RC_STATE/$key.created" || return 1
  printf '' > "$RC_STATE/$key.orig" || return 1
  printf '' > "$RC_STATE/$key.added" || return 1
  printf '%s\n' "$rc" > "$RC_STATE/$key.path" || return 1
  printf '' > "$rc"
}

rc_undo() { # restores every profile rc_append or rc_create changed in this run
  for pathfile in "$RC_STATE"/*.path; do
    [ -f "$pathfile" ] || continue
    key=$(basename "$pathfile" .path)
    rc=$(cat "$pathfile")
    [ -e "$rc" ] || continue
    if cmp -s "$rc" "$RC_STATE/$key.orig"; then
      # Nothing was appended yet: only a profile this run created goes.
      if [ -f "$RC_STATE/$key.created" ]; then rm -f "$rc"; fi
      continue
    fi
    cat "$RC_STATE/$key.orig" "$RC_STATE/$key.added" > "$RC_STATE/$key.expected"
    if cmp -s "$rc" "$RC_STATE/$key.expected"; then
      # Unchanged since: put the original back byte for byte, or remove
      # the file if this run created it.
      if [ -f "$RC_STATE/$key.created" ]; then
        rm -f "$rc"
      else
        cat "$RC_STATE/$key.orig" > "$rc"
      fi
    else
      # Edited meanwhile: remove only the block this run added, the marker
      # and export lines and the blank line written before them. A blank
      # line is held back until the next line shows whether the block
      # starts there.
      awk -v marker="$MARKER" -v line="$(tail -n 1 "$RC_STATE/$key.added")" '
        held == 1 && $0 == line { held = 0; blank = 0; next }
        held == 1 { if (blank) print ""; print marker; held = 0; blank = 0 }
        $0 == marker { held = 1; next }
        $0 == "" { if (blank) print ""; blank = 1; next }
        { if (blank) print ""; blank = 0; print }
        END { if (blank) print ""; if (held) print marker }' "$rc" > "$RC_STATE/$key.new" && cat "$RC_STATE/$key.new" > "$rc"
    fi
  done
}

# Tests source this file with CREALITY_SLICER_MCP_INSTALL_LIB=1 to use the functions.
if [ "${CREALITY_SLICER_MCP_INSTALL_LIB:-}" = "1" ]; then
  return 0 2>/dev/null || exit 0
fi

set -e

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
if [ "$VERSION" = "latest" ]; then
  BASE="https://github.com/${OWNER}/${REPO}/releases/latest/download"
else
  BASE="https://github.com/${OWNER}/${REPO}/releases/download/${VERSION}"
fi
URL="${BASE}/${ASSET}"
INSTALL_ROOT="$HOME/.${REPO}"
INSTALL_DIR="$INSTALL_ROOT/bin"
TARGET="$INSTALL_DIR/$BIN"
TEMP="${TARGET}.new"
RC_STATE=""
BACKUP=""

# Record what exists now, so a cancelled setup can put it back.
created_root=0; [ -d "$INSTALL_ROOT" ] || created_root=1
created_dir=0; [ -d "$INSTALL_DIR" ] || created_dir=1

undo_directories() {
  if [ "$created_dir" -eq 1 ]; then rmdir "$INSTALL_DIR" 2>/dev/null || true; fi
  if [ "$created_root" -eq 1 ]; then rmdir "$INSTALL_ROOT" 2>/dev/null || true; fi
}
restore_backup() {
  if [ -z "$BACKUP" ]; then
    rm -f "$TARGET"
  elif [ -f "$BACKUP" ]; then
    mv -f "$BACKUP" "$TARGET"
  fi
  # Otherwise the previous binary was not moved aside yet and is in place.
}
rolled_back_message() {
  if [ -n "$BACKUP" ]; then
    printf '  The previously installed %s was kept.\n' "$BIN"
  else
    printf '  %s was not installed.\n' "$BIN"
  fi
}

cleanup() {
  rm -f "$TEMP" "$TEMP.err"
  if [ -n "$RC_STATE" ]; then rm -rf "$RC_STATE"; fi
}

# stage is what an interrupt (HUP, INT or TERM), or a failure that ends the
# script, has to undo, as a cancel does:
#   download   the new binary is fetched and checked; only it goes
#   replace    it replaces the installed one, which is kept as $BACKUP
#   path       shell profiles are edited
#   configure  the setup wizard runs. It gets the interrupt too, and its exit
#              status decides what is kept, as without one; the script then
#              exits with the interrupt's status.
#   done       setup finished, or its outcome was handled; nothing is undone
stage=download
signal_status=""

# undo_stage undoes what this run changed up to $stage, running every step
# even if one fails, and says so ($1 names the cause).
undo_stage() {
  set +e
  trap '' HUP INT TERM # a second interrupt must not cut the undo short
  rm -f "$TEMP" "$TEMP.err"
  if [ "$stage" = download ]; then
    printf '\n  Setup %s; nothing was installed.\n' "$1" >&2
  else
    restore_backup
    if [ -n "$RC_STATE" ]; then rc_undo; fi
    printf '\n  Setup %s; the changes were undone.\n' "$1" >&2
    rolled_back_message >&2
  fi
  undo_directories
  stage=done
}
on_signal() { # on_signal <exit status>
  if [ "$stage" = configure ]; then
    # The script carries on with its own error handling.
    signal_status=$1
    return 0
  fi
  if [ "$stage" != done ]; then undo_stage interrupted; fi
  exit "$1" # runs on_exit through the EXIT trap
}
on_exit() { # on_exit <exit status>
  # A command that failed under set -e while the new binary or the profile
  # edits were in place ends the script here; put things back first.
  if [ "$1" -ne 0 ] && { [ "$stage" = replace ] || [ "$stage" = path ]; }; then
    undo_stage failed
  fi
  cleanup
}
trap 'on_exit "$?"' EXIT
trap 'on_signal 129' HUP
trap 'on_signal 130' INT
trap 'on_signal 143' TERM

mkdir -p "$INSTALL_DIR"
RC_STATE=$(mktemp -d)

printf '\n  %s installer\n\n  Downloading %s (%s)...\n' "$BIN" "$ASSET" "$VERSION"

download_failed() {
  printf '\n  Download failed. Please check your connection and try again.\n' >&2
  printf '  URL: %s\n' "$URL" >&2
  if [ -n "$1" ]; then
    printf '  Reason: %s\n' "$1" >&2
  fi
  rm -f "$TEMP" "$TEMP.err"
  undo_directories
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
  CHECKSUM_URL="${BASE}/SHA256SUMS.txt"
  SUMS=""
  if command -v curl >/dev/null 2>&1; then
    SUMS=$(curl -fsSL "$CHECKSUM_URL" 2>/dev/null) || SUMS=""
  elif command -v wget >/dev/null 2>&1; then
    SUMS=$(wget -q -O - "$CHECKSUM_URL" 2>/dev/null) || SUMS=""
  fi
  if [ -z "$SUMS" ]; then
    printf '\n  Could not fetch %s to verify the download; nothing was installed.\n' "$CHECKSUM_URL" >&2
    printf '  Please check your connection and try again.\n' >&2
    rm -f "$TEMP"
    undo_directories
    exit 1
  fi
  EXPECTED=$(printf '%s\n' "$SUMS" | grep " \*\{0,1\}$ASSET\$" | awk '{print $1}')
  if [ -z "$EXPECTED" ]; then
    printf '\n  %s lists no checksum for %s; nothing was installed.\n' "$CHECKSUM_URL" "$ASSET" >&2
    rm -f "$TEMP"
    undo_directories
    exit 1
  fi
  ACTUAL=$($SHA256_CMD "$TEMP" | awk '{print $1}')
  if [ "$EXPECTED" != "$ACTUAL" ]; then
    printf '\n  SHA256 mismatch; nothing was installed.\n' >&2
    rm -f "$TEMP"
    undo_directories
    exit 1
  fi
else
  printf '\n  Neither sha256sum nor shasum is available to verify the download;\n  nothing was installed.\n' >&2
  rm -f "$TEMP"
  undo_directories
  exit 1
fi

if [ ! -s "$TEMP" ]; then
  printf '  Download did not complete; nothing was installed.\n' >&2
  undo_directories
  exit 1
fi
chmod +x "$TEMP"

# Keep a previous version until setup finishes. Its name must not match
# "<bin>.old-*": the program deletes those files when it starts. From here
# an interrupt or a failure puts it back (restore_backup).
if [ ! -f "$TARGET" ] && [ -f "${TARGET}.bak" ]; then
  # A run that was killed after moving the installed binary aside left it
  # only as the backup: that is the version installed, so it goes back.
  mv -f "${TARGET}.bak" "$TARGET"
fi
if [ -f "$TARGET" ]; then
  # Any backup left beside the installed binary is stale.
  rm -f "${TARGET}.bak"
  BACKUP="${TARGET}.bak"
fi
stage=replace
if [ -n "$BACKUP" ]; then
  mv -f "$TARGET" "$BACKUP"
fi
if ! mv -f "$TEMP" "$TARGET"; then
  printf '\n  Failed to install binary to %s\n' "$TARGET" >&2
  exit 1 # on_exit restores the previous binary
fi

# Put the install directory on PATH through the shell profiles.
stage=path
case ":$PATH:" in
  *":$INSTALL_DIR:"*) on_path=1 ;;
  *) on_path=0 ;;
esac
if [ "$on_path" -eq 0 ]; then
  # zsh, the default shell on macOS, reads none of the other files, so a zsh
  # user without a .zshrc gets one, where zsh looks for it.
  # A profile or directory this user cannot write is left alone, and so is a
  # .zshrc that is a symlink (often into a read-only, managed store, and
  # possibly dangling); a profile that cannot be edited is reported, and
  # setup goes on with the others.
  ZSHRC="${ZDOTDIR:-$HOME}/.zshrc"
  zshrc_dir=$(dirname "$ZSHRC")
  case "${SHELL:-}" in
    */zsh)
      if [ ! -e "$ZSHRC" ] && [ ! -L "$ZSHRC" ] && [ -d "$zshrc_dir" ] && [ -w "$zshrc_dir" ]; then
        if ! rc_create "$ZSHRC"; then
          printf '  Could not create %s; add %s to your PATH yourself.\n' "$ZSHRC" "$INSTALL_DIR" >&2
        fi
      fi ;;
  esac
  for rc in "$ZSHRC" "$HOME/.bashrc" "$HOME/.profile" "$HOME/.bash_profile"; do
    if [ "$rc" = "$ZSHRC" ] && [ -L "$rc" ]; then
      # Checked first: a dangling symlink is not a regular file either.
      if [ -f "$rc" ] && grep -qF "$INSTALL_DIR" "$rc" 2>/dev/null; then
        on_path=2
      else
        printf '  Skipped %s: it is a symlink.\n' "$rc" >&2
      fi
      continue
    fi
    [ -f "$rc" ] || continue
    if grep -qF "$INSTALL_DIR" "$rc" 2>/dev/null; then
      on_path=2
    elif [ ! -w "$rc" ]; then
      printf '  Skipped %s: it is not writable.\n' "$rc" >&2
    elif rc_append "$rc" "export PATH=\"$INSTALL_DIR:\$PATH\""; then
      on_path=2
    else
      printf '  Could not add %s to %s.\n' "$INSTALL_DIR" "$rc" >&2
    fi
  done
fi

PATH="$INSTALL_DIR:$PATH"
export PATH

# Run the setup wizard, in a terminal only: without one it cannot ask, and
# `configure` would register every detected client unasked. Its input and
# output both go to the terminal, since the script's own may be a pipe
# (curl ... | sh | tee log). With --yes, or --all, --clients or --token,
# which also make configure skip the wizard, setup runs unattended anywhere.
# CONFIGURE_ARGS is split into words below, never expanded as file names.
set -f
unattended=0
for arg in $CONFIGURE_ARGS; do
  # Go's flag package reads -flag and --flag alike.
  case "$arg" in
    --*) flag=${arg#--} ;;
    -*) flag=${arg#-} ;;
    *) continue ;;
  esac
  case "$flag" in
    # configure skips the wizard for --yes, --all, --clients and the
    # credential flags (runsUnattended in wizard.go). These are the spellings
    # Go's flag package reads as true, and the string flags with a value.
    yes|yes=1|yes=t|yes=T|yes=true|yes=TRUE|yes=True|all|all=1|all=t|all=T|all=true|all=TRUE|all=True)
      unattended=1 ;;
    token|token=?*|clients|clients=?*|email|email=?*)
      unattended=1 ;;
  esac
done
if [ "$unattended" -eq 1 ]; then
  set +e
  stage=configure
  # shellcheck disable=SC2086
  "$TARGET" configure $CONFIGURE_ARGS </dev/null
  code=$?
  set -e
elif ( : </dev/tty >/dev/tty ) 2>/dev/null; then
  set +e
  stage=configure
  # shellcheck disable=SC2086
  "$TARGET" configure $CONFIGURE_ARGS </dev/tty >/dev/tty
  code=$?
  set -e
else
  stage=done
  printf '\n  Installed %s to %s.\n  Not running in a terminal. Finish setup in one with:\n    %s configure\n' "$BIN" "$TARGET" "$BIN"
  code=0
fi
set +f

if [ "$code" -eq "$EXIT_CANCELLED" ]; then
  # Put everything back as it was.
  restore_backup
  rc_undo
  undo_directories
  # The wizard already said "Setup cancelled"; say what was restored.
  rolled_back_message
  stage=done
  exit "${signal_status:-0}"
fi

rm -f "$BACKUP"
stage=done
if [ "$code" -ne 0 ]; then
  printf '  Setup did not finish (exit code %s). %s is installed at %s.\n' "$code" "$BIN" "$TARGET"
  printf '  Run `%s configure` to finish, or `%s uninstall --all` to remove it.\n' "$BIN" "$BIN"
fi

if [ "$on_path" -eq 0 ]; then
  printf '\n  Add this to your shell profile:\n    export PATH="%s:$PATH"\n' "$INSTALL_DIR"
elif [ "$on_path" -eq 2 ]; then
  printf '\n  Open a new terminal so `%s` is on your PATH.\n' "$BIN"
fi
# An interrupt while the wizard ran ends the script with its status.
if [ -n "$signal_status" ]; then exit "$signal_status"; fi
