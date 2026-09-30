#!/usr/bin/env bash
# Shared by the preserves-unasked-content gate and its file-guard: one library, two thin entries.
# Each entry reads the event kind and its own bytes; nothing that follows branches
# on it.

lib_init() {
set -uo pipefail

# Undecidable without jq: apply the requirement (exit 0, fail-closed).
command -v jq >/dev/null 2>&1 || exit 0

input="$(cat)"
kind="$(printf '%s' "$input" | jq -r '.event.kind // empty')"
[ -n "$kind" ] || { echo "preserves-unasked-content: could not read the event's kind, so it could not be checked" >&2; exit 2; }
}

lib_check() {

old="$(printf '%s' "$input" | jq -r '.event.oldContent // ""')"
new="$(printf '%s' "$input" | jq -r '.event.newContent // ""')"

# Any line present in old but absent in new. (Order/whitespace refinements are
# elided in this sample.)
removed="$(comm -23 <(printf '%s' "$old" | sort -u) <(printf '%s' "$new" | sort -u) | grep -c . || true)"
[ "${removed:-0}" -eq 0 ] && exit 1

# It applies. The hint the refusal carries: append instead, or cite the ask.
jq -n --arg n "$removed" '{hint: (
  "This change removes " + $n + " line(s). If nothing should go, append instead of rewriting; if the user asked for the removal, cite their words asking for it.")}'
exit 0
}

removes_content_lib_loaded=1
