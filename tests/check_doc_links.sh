#!/bin/sh
# Fails when README.md or a docs/*.md file links to a relative file that does
# not exist. Anchors and external links (http, https, mailto) are ignored.
#   sh tests/check_doc_links.sh [root]
root="${1:-$(cd "$(dirname "$0")/.." && pwd)}"
cd "$root" || exit 2
failures=0
for md in README.md docs/*.md; do
  [ -f "$md" ] || continue
  dir=$(dirname "$md")
  # Inline links: ](target) or ](target "title").
  grep -o '\]([^)]*)' "$md" | sed 's/^](//; s/)$//; s/ .*//' | while read -r target; do
    case "$target" in
      ''|'#'*|http://*|https://*|mailto:*) continue ;;
    esac
    path="${target%%#*}"
    [ -e "$dir/$path" ] || printf 'BROKEN %s -> %s\n' "$md" "$target"
  done
done > "${TMPDIR:-/tmp}/doc-links.$$"
if [ -s "${TMPDIR:-/tmp}/doc-links.$$" ]; then
  cat "${TMPDIR:-/tmp}/doc-links.$$"
  failures=1
fi
rm -f "${TMPDIR:-/tmp}/doc-links.$$"
if [ "$failures" -ne 0 ]; then exit 1; fi
echo "all relative documentation links resolve"
