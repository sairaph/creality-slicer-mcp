#!/bin/sh
# Tests install.sh's shell-profile edit and undo on real files.
#   sh tests/install_sh_test.sh
set -e
here=$(cd "$(dirname "$0")" && pwd)
CREALITY_SLICER_MCP_INSTALL_LIB=1
. "$here/../install.sh"
set -e

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
LINE='export PATH="/home/me/.creality-slicer-mcp/bin:$PATH"'
failures=0

check_same() { # check_same <name> <file> <expected-file>
  if cmp -s "$2" "$3"; then
    printf 'ok   %s\n' "$1"
  else
    printf 'FAIL %s\n  got:\n' "$1"; od -c "$2" | sed 's/^/    /'
    printf '  want:\n'; od -c "$3" | sed 's/^/    /'
    failures=$((failures + 1))
  fi
}

run_case() { # run_case <name> <original content printf format>
  RC_STATE=$(mktemp -d "$work/state.XXXXXX")
  rc="$work/profile with space"
  printf "$2" > "$rc"
  cp "$rc" "$work/original"
  rc_append "$rc" "$LINE"
  if ! grep -qF "$LINE" "$rc"; then
    printf 'FAIL %s: nothing appended\n' "$1"; failures=$((failures + 1)); return
  fi
  if [ "$(grep -c "^$MARKER\$" "$rc")" -ne 1 ]; then
    printf 'FAIL %s: marker not on its own line\n' "$1"; failures=$((failures + 1))
  fi
  rc_undo
  check_same "$1" "$rc" "$work/original"
}

run_case "profile ending with a newline" 'alias ll="ls -l"\nexport EDITOR=vim\n'
run_case "profile without a final newline" 'alias ll="ls -l"\nexport EDITOR=vim'
run_case "empty profile" ''

# Edited after the installer appended: only the installer's lines go.
RC_STATE=$(mktemp -d "$work/state.XXXXXX")
rc="$work/edited"
printf 'first\n' > "$rc"
rc_append "$rc" "$LINE"
printf 'added later\n' >> "$rc"
rc_undo
printf 'first\nadded later\n' > "$work/want-edited"
check_same "profile edited after the append" "$rc" "$work/want-edited"

# A profile the installer created (zsh without a .zshrc) is removed again.
RC_STATE=$(mktemp -d "$work/state.XXXXXX")
rc="$work/created"
rc_create "$rc"
rc_append "$rc" "$LINE"
rc_undo
if [ -e "$rc" ]; then
  printf 'FAIL created profile: still exists after undo\n'; failures=$((failures + 1))
else
  printf 'ok   created profile\n'
fi

# ...unless the user wrote to it meanwhile: then only our lines go.
RC_STATE=$(mktemp -d "$work/state.XXXXXX")
rc="$work/created-then-edited"
rc_create "$rc"
rc_append "$rc" "$LINE"
printf 'alias g=git\n' >> "$rc"
rc_undo
printf 'alias g=git\n' > "$work/want-created-edited"
check_same "created profile edited after the append" "$rc" "$work/want-created-edited"

if [ "$failures" -ne 0 ]; then
  printf '%s failure(s)\n' "$failures"
  exit 1
fi
printf 'all install.sh profile tests passed\n'
