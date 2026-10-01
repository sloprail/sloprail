#!/usr/bin/env bash
# prepare: hand the judge the paths that need grounding, or skip it. A range that
# only adds rules asks for nothing, so no model call is spent on it. An unreadable
# changeset is never a skip: the judge is asked.
set -uo pipefail
lib_dir="$(cd "$(dirname "$0")" && pwd)"
unset needs_grounding_lib_loaded
. "$lib_dir/needs-grounding-lib.sh" || exit 2
[ "${needs_grounding_lib_loaded:-}" = 1 ] || exit 2

payload="$(cat)"
# paths: the files that need grounding, one per line. diff: their parts of the squashed
# diff, headed by path and status. Both are strings, so the template prints them bare.
needing="$(needing_paths "$payload")" || {
  jq -n '{additionalContext: {paths: "", diff: ""}}'
  exit 0
}
if [ -z "$needing" ]; then
  echo '{"skip": true}'
  exit 0
fi
printf '%s' "$payload" | jq --arg needing "$needing" '
  ($needing | split("\n")) as $paths
  | {additionalContext: {
      paths: $needing,
      diff: ([.changeset.files[] | select(.path as $p | $paths | index($p))
              | "--- " + .path + " (" + .status + ")\n" + (.diff // "")] | join("\n"))}}'
