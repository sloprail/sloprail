#!/usr/bin/env bash
# prepare: hand the judge the paths that need grounding, or skip it. A range that
# only adds rules asks for nothing, so no model call is spent on it. An unreadable
# changeset is never a skip: the judge is asked.
set -uo pipefail
lib_dir="$(cd "$(dirname "$0")" && pwd)"
unset needs_grounding_lib_loaded
. "$lib_dir/needs-grounding-lib.sh" || exit 2

payload="$(cat)"
needing="$(needing_paths "$payload")" || {
  jq -n '{additionalContext: {paths: []}}'
  exit 0
}
if [ -z "$needing" ]; then
  echo '{"skip": true}'
  exit 0
fi
printf '%s\n' "$needing" | jq -R . | jq -s '{additionalContext: {paths: .}}'
