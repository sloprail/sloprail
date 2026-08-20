#!/usr/bin/env bash
# The deterministic half of unit 17 — "worth shipping before the judge half"
# (his implementation note). A line-by-line diff of old vs new:
#   - newContent absent  -> BLOCK (fail-closed): the engine could not compute
#     the result (a `sed -i`, an env-dependent command), so no-loss cannot be
#     established, and a check that could not run has established nothing —
#     the exact fail-open trap the incident came from.
#   - no removed lines   -> PASS: pure additions is "append instead of
#     rewriting", always fine (his rule), whatever was added.
#   - removed lines, and NO deletion intent in this turn's human messages
#                        -> BLOCK: unasked removal, deterministic. This is what
#     would have caught the incident (27 lines removed, nothing ever asked).
#   - removed lines, but a human message this turn DID voice a deletion/rewrite
#                        -> PASS HERE, on to the judge: whether that (often
#     loose) ask actually authorized dropping THESE lines is the one question
#     a model is for, not this script.
set -uo pipefail

input="$(cat)"
transcript_path="$(printf '%s' "$input" | jq -r '.transcriptPath')"
path="$(printf '%s' "$input" | jq -r '.event.path')"
old="$(printf '%s' "$input" | jq -r '.event.oldContent // ""')"

# Absent newContent (not empty — absent) means the write's result is unknowable.
if ! printf '%s' "$input" | jq -e '.event | has("newContent")' >/dev/null 2>&1; then
  echo "Refusing the write to $path: its result cannot be computed (an in-place or environment-dependent command), so it cannot be shown NOT to drop content. Write the file directly so the change can be verified." >&2
  exit 1
fi
new="$(printf '%s' "$input" | jq -r '.event.newContent')"

# Removed lines = present in old, absent in new. `comm -23` on sorted-unique
# sets is enough for "did any line disappear"; order/whitespace refinements are
# the exception-rule layer unit 12 shares and are elided here for the sample.
removed="$(comm -23 <(printf '%s' "$old" | sort -u) <(printf '%s' "$new" | sort -u) | grep -c . || true)"

if [ "${removed:-0}" -eq 0 ]; then
  # Pure additions (or a no-op) — append, not rewrite. Always fine.
  exit 0
fi

# Something was removed. Did a HUMAN ask for a removal this turn? The authority
# is the human's own words in the trajectory (his: "did anyone ask for this
# removal", human messages, not the whole record — same ground-truth move as
# unit 16). Read user messages via the normalized trajectory; scan for any
# deletion/rewrite intent.
human_text="$(sr-session trajectory normalize --path "$transcript_path" \
  | jq -r '.[]
      | select(.type == "user")
      | .message
      | if type == "string" then .
        elif type == "array" then ([.[] | select(.type? == "text") | .text] | join(" "))
        else "" end' 2>/dev/null | tr "[:upper:]" "[:lower:]")"

if printf '%s' "$human_text" | grep -Eq "delete|remove|drop|rewrite|clean up|clean it up|trim|cut |cut the|shorten|replace|overwrite|start over|redo"; then
  # A deletion/rewrite was voiced. Whether it authorized THESE specific removed
  # lines — especially when phrased loosely — is the judge's call. Pass through.
  exit 0
fi

echo "Refusing the write to $path: it removes content, and nothing in this turn's messages asked for any removal or rewrite. Append rather than rewrite, or preserve the ${removed} removed line(s) — if the removal is intended, say so." >&2
exit 1
