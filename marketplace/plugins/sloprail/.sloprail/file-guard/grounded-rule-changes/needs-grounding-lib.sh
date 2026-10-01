#!/usr/bin/env bash
# Shared by needs-grounding.sh (the `when`), own-refusal-is-no-ground.sh and
# prepare.sh: which files of a changeset change what already stood in `.sloprail/`,
# so must be grounded. Sourced, never run.
#
#   - a file ADDED in the range needs no grounding (a new rule weakens nothing),
#   - except `.sloprail/config.yaml`, which can carry `disabled:` even when new.
# Everything else (modified, renamed, deleted) does.
needs_grounding_lib_loaded=1

# jq program: the paths of the files that need grounding, one per line. Input is the
# whole payload; a changeset that cannot be read is an error, so the caller fails
# closed.
NEEDS_GROUNDING_JQ='
  .changeset.files
  | if type == "array" then . else error("no files") end
  | .[]
  | select(.status != "A" or .path == ".sloprail/config.yaml")
  | .path'

# needing_paths PAYLOAD: print the paths needing grounding. Nonzero when unreadable.
needing_paths() {
  printf '%s' "$1" | jq -r "$NEEDS_GROUNDING_JQ" 2>/dev/null
}
