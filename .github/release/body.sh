#!/usr/bin/env bash
# Writes the description of a release draft to $1 from .github/release/body.md: the placeholders become the version of
# each platform (latest.json in $OUT) and the lists "Новое"/"Исправлено" of CHANGELOG.md for $VERSION. The platform tags
# of the changelog become words ("[umbrel]" -> "Только для Docker и Umbrel:"): the update window of copies up to 1.1.0
# shows the description as it is.
# Environment: VERSION, OUT.
set -euo pipefail
out=$1

changes=$(awk -v v="$VERSION" '
  $0 == "## " v { on = 1; next }
  /^## / { on = 0 }
  on { print }
' CHANGELOG.md | sed -e 's/\[umbrel\] */Только для Docker и Umbrel: /' -e 's/\[windows\] */Только для Windows: /' -e 's/\[linux\] */Только для Linux: /')
[ -n "$(echo "$changes" | tr -d '[:space:]')" ] || { echo "CHANGELOG.md has no entry for $VERSION"; exit 1; }

versions=""
for pair in "windows:Windows" "linux:Linux" "umbrel:Docker и Umbrel"; do
  key=${pair%%:*}; label=${pair#*:}
  v=$(jq -r --arg k "$key" '.[$k] // empty' "$OUT/latest.json")
  [ -z "$v" ] || versions="${versions:+$versions · }$label $v"
done

tmp=$(mktemp)
printf '%s\n' "$changes" > "$tmp"
sed -e "s/{{VERSION}}/$VERSION/g" -e "s|{{VERSIONS}}|$versions|" -e "/^{{CHANGES}}\$/{r $tmp" -e 'd}' .github/release/body.md > "$out"
