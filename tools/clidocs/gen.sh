#!/usr/bin/env bash
# Generate the CLI reference JSON from the real Cobra command trees.
#
# Each binary has a clidoc_test.go that emits its command tree to $GEN_CLI_DOCS.
# This runs that test for every binary and merges the trees into one file the
# docs render. The CLI reference is therefore generated from the actual
# commands/flags/help — it can't drift from the binaries.
#
#   tools/clidocs/gen.sh
set -euo pipefail

cd "$(dirname "$0")/../.."   # repo root
out="docs/reference/generated"
mkdir -p "$out"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

binaries=(sr sr-session sr-file sr-mark sr-agent sr-checks)
for b in "${binaries[@]}"; do
  GEN_CLI_DOCS="$tmp/$b.json" go test "./services/$b" -run TestGenerateCLIDocs -count=1 >/dev/null
done

# Merge the trees into one array, in binary order.
jq -s '.' "$tmp/sr.json" "$tmp/sr-session.json" "$tmp/sr-file.json" \
  "$tmp/sr-mark.json" "$tmp/sr-agent.json" "$tmp/sr-checks.json" > "$out/cli.json"

echo "[clidocs] wrote $out/cli.json ($(jq 'length' "$out/cli.json") binaries)"
