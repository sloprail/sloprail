#!/usr/bin/env bash
# Bumps every plugin.json + marketplace.json to VERSION, lockstep with the
# repo's own release: sloprail, sloprail-tasks, sloprail-content, and the
# marketplace's own per-plugin version entries all carry the SAME version as
# the git tag that will be cut for this release.
#
# Run BEFORE tagging, not after — the tag has to point at a commit where the
# files already say the right version, or the two drift by one commit. The
# intended flow:
#
#   scripts/bump-version.sh 0.2.0
#   git add -A && git commit -m "chore(release): bump plugins to v0.2.0"
#   git push origin main
#   git tag v0.2.0 && git push origin v0.2.0
#
# VERSION is bare semver (0.2.0), no leading "v" — plugin.json/marketplace.json
# never carry the "v" prefix the git tag does.
set -euo pipefail

if [ $# -ne 1 ]; then
  echo "usage: $0 <version>  (e.g. 0.2.0, no leading v)" >&2
  exit 1
fi
version="$1"

case "$version" in
v*) echo "bump-version.sh: pass bare semver, no leading v (got '$version')" >&2; exit 1 ;;
esac

root="$(cd "$(dirname "$0")/.." && pwd)"

plugin_jsons=$(find "$root/marketplace/plugins" -maxdepth 3 -name plugin.json -path '*/.claude-plugin/*')
marketplace_json="$root/.claude-plugin/marketplace.json"

for f in $plugin_jsons; do
  tmp="$(mktemp)"
  jq --arg v "$version" '.version = $v' "$f" > "$tmp"
  mv "$tmp" "$f"
  echo "bumped $f -> $version"
done

tmp="$(mktemp)"
jq --arg v "$version" '.plugins |= map(.version = $v)' "$marketplace_json" > "$tmp"
mv "$tmp" "$marketplace_json"
echo "bumped $marketplace_json -> $version (all plugins)"
