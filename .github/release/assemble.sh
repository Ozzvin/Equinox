#!/usr/bin/env bash
# Puts together the files of a release in $OUT (the artifacts of this run are already there) and writes latest.json and
# SHA256SUMS.txt beside them.
#
#   mode=release: the platforms this run did not build are carried over from the previous published release, each file
#                 checked against that release's SHA256SUMS.txt; latest.json tells the version each platform really is.
#   mode=beta:    only what this run built (no carrying over, no latest.json).
#
# Environment: MODE, VERSION, OUT, BUILT_WINDOWS, BUILT_LINUX ("true"/"false"), UMBREL ("true" = the Docker image of
# this version is published), GH_TOKEN, GITHUB_REPOSITORY, FILES (the list next to this script).
set -euo pipefail

cd "$OUT"

if [ "$MODE" = release ]; then
  prev=$(gh release list --exclude-drafts --exclude-pre-releases --limit 1 --json tagName --jq '.[0].tagName // empty')
  [ -n "$prev" ] || { echo "no published release to take the unchanged files from"; exit 1; }
  [ "$prev" != "v$VERSION" ] || { echo "v$VERSION is already published"; exit 1; }
  echo "previous release: $prev"
  mkdir -p "$RUNNER_TEMP/prev"
  gh release download "$prev" -D "$RUNNER_TEMP/prev" --clobber

  # the version of each platform in the previous release: its latest.json, or (before it existed) its tag for all
  prevver=${prev#v}
  if [ -f "$RUNNER_TEMP/prev/latest.json" ]; then
    manifest=$(cat "$RUNNER_TEMP/prev/latest.json")
  else
    manifest=$(jq -n --arg v "$prevver" '{windows: $v, umbrel: $v}')
  fi

  carry() { # platform
    local platform=$1 name sum
    grep -E "^$platform " "$FILES" | while read -r _ name; do
      [ -f "$name" ] && continue
      src="$RUNNER_TEMP/prev/$name"
      if [ ! -f "$src" ]; then
        echo "not in $prev, left out: $name"
        continue
      fi
      sum=$(grep -E "  \*?$name\$" "$RUNNER_TEMP/prev/SHA256SUMS.txt" | cut -d' ' -f1)
      [ -n "$sum" ] || { echo "no checksum of $name in $prev"; exit 1; }
      [ "$(sha256sum "$src" | cut -d' ' -f1)" = "$sum" ] || { echo "checksum mismatch: $name"; exit 1; }
      cp "$src" "$name"
      echo "carried over from $prev: $name"
    done
  }

  versions=$manifest
  if [ "$BUILT_WINDOWS" = true ]; then versions=$(jq --arg v "$VERSION" '.windows = $v' <<<"$versions"); else carry windows; fi
  if [ "$BUILT_LINUX" = true ]; then versions=$(jq --arg v "$VERSION" '.linux = $v' <<<"$versions"); else carry linux; fi
  if [ "$UMBREL" = true ]; then versions=$(jq --arg v "$VERSION" '.umbrel = $v' <<<"$versions"); fi
  # a platform that never had a version has no key; the client then offers it nothing
  jq -S . <<<"$versions" > latest.json
  cat latest.json
fi

ls | grep -v -e '^SHA256SUMS.txt$' | xargs sha256sum > SHA256SUMS.txt
cat SHA256SUMS.txt
