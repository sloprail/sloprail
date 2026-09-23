#!/usr/bin/env bash
# The deterministic half. Grounds "asked" in the user's actual words, not a
# deletion-keyword grep:
#   - newContent absent      -> BLOCK (fail-closed): result unknowable, no-loss
#     cannot be established.
#   - no removed lines       -> PASS: pure additions.
#   - removed, no sr:asked    -> BLOCK: an unasked rewrite.
#   - removed, quote unresolved -> BLOCK: a fabricated ask.
#   - removed, quote resolves -> PASS to the judge.
set -uo pipefail

input="$(cat)"
path="$(printf '%s' "$input" | jq -r '.event.path')"
old="$(printf '%s' "$input" | jq -r '.event.oldContent // ""')"
# The trajectory to ground the quote against. cite MUST be given --path
# explicitly: with none it fails closed and would refuse every removal.
transcript_path="$(printf '%s' "$input" | jq -r '.transcriptPath')"

# Absent newContent (not empty — absent) means the result is unknowable.
if ! printf '%s' "$input" | jq -e '.event | has("newContent")' >/dev/null 2>&1; then
  echo "Refusing the write to $path: its result cannot be computed (an in-place or environment-dependent command), so it cannot be shown NOT to drop content. Write the file directly." >&2
  exit 1
fi
new="$(printf '%s' "$input" | jq -r '.event.newContent')"

# Any line present in old but absent in new. (Order/whitespace refinements are
# elided in this sample.)
removed="$(comm -23 <(printf '%s' "$old" | sort -u) <(printf '%s' "$new" | sort -u) | grep -c . || true)"

if [ "${removed:-0}" -eq 0 ]; then
  exit 0   # pure additions — always fine
fi

# Something was removed. The authorizing sr:asked marker arrives already
# extracted on the event's newMarkers — no file parsing here.
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
