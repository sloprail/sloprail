#!/usr/bin/env bash
# Shared by the unit-publish-approved gate and its file-guard: one library, two thin entries.
# Each entry reads the event kind and its own bytes; nothing that follows branches
# on it.

lib_init() {
set -uo pipefail

refuse() {
  jq -n --arg reason "$1" '{reason: $reason}'
  exit 1
}

event="$(cat)"
printf '%s' "$event" | jq -e '.event | type == "object"' >/dev/null 2>&1 \
  || refuse "unit-publish-approved: the check payload is not readable JSON with an .event object, so this write could not be checked"

# field FILTER — one jq read of the payload. It runs inside $(…), so on a jq
# error it says why on STDERR (stdout is being captured) and returns non-zero;
# every caller follows it with `|| exit 1`, which refuses with that reason.
field() {
  local out
  if ! out="$(printf '%s' "$event" | jq -r "$1")"; then
    echo "unit-publish-approved: could not read $1 from the check payload, so this write could not be checked" >&2
    return 1
  fi
  printf '%s' "$out"
}

path="$(field '.event.path // ""')" || exit 1
if [ -z "$path" ]; then
  refuse "unit-publish-approved: the event named no path, so there is nothing to check"
fi

root="${SR_WORKSPACE:-.}"
# The schema is the PLUGIN's, read from its own tree — this guard's folder is two
# levels under the plugin's .sloprail/ — never a consumer-side copy.
schema="${SR_GUARDRAIL_DIR:-.}/../../schemas/unit.cue"
if [ ! -f "$schema" ]; then
  refuse "unit-publish-approved: schema not found at $schema — the plugin's own unit.cue is missing, so no unit can be checked."
fi
# A helper stopped by a syntax error runs only up to it (whether the `.` then
# fails depends on the bash version), so its last-line sentinel is what proves
# it loaded whole.
unset publish_claim_loaded
# shellcheck source=publish-claim.sh
. "$lib_dir/publish-claim.sh" 2>/dev/null \
  || refuse "unit-publish-approved: publish-claim.sh is missing beside this check, so whether $path claims published could not be read"
[ "${publish_claim_loaded:-}" = 1 ] \
  || refuse "unit-publish-approved: publish-claim.sh did not load whole (its last-line sentinel publish_claim_loaded is unset), so whether $path claims published could not be read"
}

lib_check() {

publish_claim "$content"
case "$claim" in
  no)
    # Not published: its shape is not this guard's subject.
    exit 0
    ;;
  undecidable)
    refuse "UNREADABLE FRONTMATTER: $path's frontmatter cannot be read, so whether it claims status: published cannot be told — fix the frontmatter so it reads:
$claim_why"
    ;;
  unsupported)
    refuse "$claim_why"
    ;;
esac

if ! new_doc="$(printf '%s' "$content" | sr-file validate - --as .md --schema "$schema" --emit 2>&1)"; then
  refuse "PUBLISH INVALID: $path claims status: published, but its frontmatter does not satisfy the plugin's unit.cue — fix the frontmatter; a published unit must hold to the schema:
$new_doc"
fi

n_urls="$(printf '%s' "$new_doc" | jq -r '(.published_urls // []) | length')" \
  || refuse "unit-publish-approved: could not read published_urls from $path"
if [ -z "$n_urls" ] || [ "$n_urls" -eq 0 ] 2>/dev/null; then
  refuse "PUBLISH INCOMPLETE: $path claims status: published with no published_urls: in the frontmatter — a unit cannot be published without recording where it went out (a list, e.g. published_urls: [\"https://x.com/you/status/…\"])."
fi

exit 0
}

check_publish_lib_loaded=1
