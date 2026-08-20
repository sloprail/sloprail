#!/usr/bin/env bash
# The deterministic half of unit 17, reworked (his 2026-08-20) to ground "asked"
# in the user's actual words rather than grep for deletion keywords:
#   - newContent absent  -> BLOCK (fail-closed): the write's result is unknowable
#     (a `sed -i`, an env-dependent command), so no-loss cannot be established.
#   - no removed lines   -> PASS: pure additions is "append, not rewrite".
#   - removed lines, but NO sr:asked quote-marker on the file
#                        -> BLOCK: a removal with no declared authorizing quote
#     is exactly the unasked rewrite this rule catches (the incident: 27 lines
#     gone, nothing recorded as the ask).
#   - removed lines WITH an sr:asked marker whose quote does NOT resolve in the
#     trajectory -> BLOCK: a fabricated ask. `sr-session trajectory cite` is the
#     authority — the quote must be the user's own words (a message, or an
#     AskUserQuestion answer), not a paraphrase the agent invented.
#   - removed lines WITH a marker whose quote RESOLVES -> PASS to the judge:
#     the ask is real; whether the change is clean and only covers it is the
#     model's call, not this script's.
set -uo pipefail

input="$(cat)"
path="$(printf '%s' "$input" | jq -r '.event.path')"
old="$(printf '%s' "$input" | jq -r '.event.oldContent // ""')"
# The trajectory to ground the quote against — the CheckPayload carries it, the
# same field task-management's resolve-referenced-message.sh reads. cite MUST be
# given it explicitly: with no --path cite fails closed (it cannot rule out a
# sub-agent context from a tool call's environment), so a bare `cite "$quote"`
# would refuse EVERY removal, grounded or not. Pass the payload's path.
transcript_path="$(printf '%s' "$input" | jq -r '.transcriptPath')"

# Absent newContent (not empty — absent) means the result is unknowable.
if ! printf '%s' "$input" | jq -e '.event | has("newContent")' >/dev/null 2>&1; then
  echo "Refusing the write to $path: its result cannot be computed (an in-place or environment-dependent command), so it cannot be shown NOT to drop content. Write the file directly." >&2
  exit 1
fi
new="$(printf '%s' "$input" | jq -r '.event.newContent')"

# Any line present in old but absent in new. (Order/whitespace refinements are
# the exception-rule layer unit 12 shares; elided in this sample.)
removed="$(comm -23 <(printf '%s' "$old" | sort -u) <(printf '%s' "$new" | sort -u) | grep -c . || true)"

if [ "${removed:-0}" -eq 0 ]; then
  exit 0   # pure additions — always fine
fi

# Something was removed. The authorizing quote is an sr:asked marker the agent
# put in the file's frontmatter (a YAML comment the extractor reads) — it
# arrives already extracted on the event, so no file parsing here. Read it off
# newMarkers (the result's markers).
quote="$(printf '%s' "$input" \
  | jq -r '(.event.newMarkers // [])[] | select(.kind == "asked") | .fqn' \
  | head -1)"

if [ -z "$quote" ]; then
  echo "Refusing the write to $path: it removes content, and the file declares no sr:asked marker naming what the user asked for. Add '# sr:asked \"<the user's words>\"' to the frontmatter, or append rather than rewrite." >&2
  exit 1
fi

# Ground the quote: it must be the user's own words in this trajectory. cite
# exits 0 for a single resolving match, 2 for several, 1 for none — each a
# DIFFERENT fix for the agent, so the codes are branched, not merged.
sr-session trajectory cite --path "$transcript_path" "$quote" >/dev/null 2>&1
cite_rc=$?

case "$cite_rc" in
  0) : ;;  # exactly one match — the ask is grounded, on to the judge.
  1)
    echo "Refusing the write to $path: its sr:asked quote resolves to nothing the user actually said or chose in this trajectory — a paraphrase or a fabricated ask. Quote the user's own words verbatim (a message, or an answer you selected)." >&2
    exit 1
    ;;
  2)
    echo "Refusing the write to $path: its sr:asked quote matches SEVERAL user messages — it is ambiguous about which ask authorized this change. Extend the quote until it lands on exactly one." >&2
    exit 1
    ;;
  *)
    echo "Refusing the write to $path: could not verify the sr:asked quote against the trajectory (cite exited $cite_rc)." >&2
    exit 1
    ;;
esac

# The ask is real and unambiguous. Whether the change cleanly and only covers
# it is the judge's call.
exit 0
