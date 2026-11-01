#!/usr/bin/env bash
# Release notes: the version's Новое/Исправлено lists from CHANGELOG, followed by download buttons from body.md.
# The download block stays under a separate heading so clients 1.1.1+ omit it in the update window.
# Platform tags become words for older clients that display the entire body.
# Environment: VERSION.
set -euo pipefail
out=$1

changes=$(awk -v v="$VERSION" '
  $0 == "## " v { on = 1; next }
  /^## / { on = 0 }
  on && /^### / { keep = ($0 == "### Новое" || $0 == "### Исправлено") }
  on && keep { print }
' CHANGELOG.md | sed -e 's/\[umbrel\] */Только для Docker и Umbrel: /' -e 's/\[windows\] */Только для Windows: /' -e 's/\[linux\] */Только для Linux: /' | cat -s)
[ -n "$(echo "$changes" | tr -d '[:space:]')" ] || { echo "CHANGELOG.md has no lists for $VERSION"; exit 1; }

tmp=$(mktemp)
printf '%s\n' "$changes" > "$tmp"
sed -e "/^{{CHANGES}}\$/{r $tmp" -e 'd}' .github/release/body.md > "$out"
