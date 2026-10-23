#!/usr/bin/env bash
# Writes the description of a release draft to $1: only the lists "### Новое" and "### Исправлено" of the entry of
# $VERSION in CHANGELOG.md, as in the releases up to 1.0.46 (decision of 2026-10-23: the update window of copies up to
# 1.1.0 shows the description as it is, and the files are in the list of the release's assets anyway). The template,
# .github/release/body.md, holds the placeholder {{CHANGES}} alone. The platform tags of the changelog become words
# ("[umbrel]" -> "Только для Docker и Umbrel:"): those copies would show the tag itself.
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
