#!/usr/bin/env bash
# Shared by the content-rule-is-grounded gate and its file-guard: one library, two thin entries.
# Each entry reads the event kind and its own bytes; nothing that follows branches
# on it.

lib_init() {
set -uo pipefail

refuse() {
  jq -n --arg reason "$1" '{reason: $reason}'
  exit 1
}

event="$(cat)"

path="$(printf '%s' "$event" | jq -r '.event.path // empty' 2>/dev/null)"
if [ -z "$path" ]; then
  refuse "content-rule-is-grounded: the event named no path, so there is nothing to check"
fi

root="${SR_WORKSPACE:-.}"

# The schema is the PLUGIN's, read from its own tree — this guard's folder is two
# levels under the plugin's .sloprail/ — never a consumer-side copy.
schema="${SR_GUARDRAIL_DIR:-.}/../../schemas/rule.cue"
if [ ! -f "$schema" ]; then
  refuse "content-rule-is-grounded: schema not found at $schema — the plugin's own rule.cue is missing, so no rule can be checked."
fi

kind="$(printf '%s' "$event" | jq -r '.event.kind // ""' 2>/dev/null)"
[ -n "$kind" ] || { echo "content-rule-is-grounded: could not read the event's kind, so it could not be checked" >&2; exit 2; }
}

lib_check() {

if ! doc="$(printf '%s' "$content" | sr-file validate - --as .md --schema "$schema" --emit 2>&1)"; then
  detail="$(printf '%s' "$doc" | sed "s|^-:|$path:|g")"
  refuse "RULE FRONTMATTER INVALID: $path does not satisfy .sloprail/schemas/rule.cue.

$detail"
fi

# THE BODY IS THE PROSE AFTER THE FRONTMATTER — same extraction every guard
# in this plugin uses.
body="$(printf '%s\n' "$content" | awk '
  BEGIN { seen = 0 }
  NR == 1 && $0 == "---" { seen = 1; next }
  seen == 1 && $0 == "---" { seen = 2; next }
  seen == 1 { next }
  { print }
')"

body_trimmed="$(printf '%s' "$body" | tr -d '[:space:]')"
if [ -z "$body_trimmed" ]; then
  refuse "RULE BODY IS EMPTY: $path has no body stating the rule. Write the rule itself after the frontmatter, in words derived from what the user said; the citation of their words goes on the sr-file command (--cite:user '<exact quote>'), not in the file."
fi

exit 0
}

check_rule_lib_loaded=1
