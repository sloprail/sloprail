#!/usr/bin/env bash
# The deterministic half. "Asked" means the change carries a CITATION of the
# user's own words — resolved by the engine against the session's record before
# this script runs, and delivered on `.event.citations`. The quote rides on the
# command that made the change (`sr-file ... --cite:user '<quote>'`), never in
# the file, so the file keeps only its own content.
#   - result not derivable      -> BLOCK (fail-closed): no-loss cannot be shown.
#   - no removed lines          -> PASS: pure additions need no ask.
#   - removed, no user citation  -> BLOCK: an unasked removal.
#   - removed, user citation     -> PASS to the judge, which rules whether the
#                                   removal is what the cited words asked for.
# A deletion is the largest removal there is: it needs a citation too.
set -uo pipefail

input="$(cat)"
path="$(printf '%s' "$input" | jq -r '.event.path')"
old="$(printf '%s' "$input" | jq -r '.event.oldContent // ""')"
cited="$(printf '%s' "$input" \
  | jq '[(.event.citations // [])[] | select((.sourceTypes // []) | index("user"))] | length')"

how="Make the change with sr-file, citing the user's exact words, and run it ON ITS OWN in the command (nothing else in the line but sr-file calls, && and echo) so its result can be checked before it runs:
  sr-file edit $path --old-string '<old>' --new-string '<new>' [--replace-all] --cite:user '<the user's exact words>'
  sr-file delete $path --cite:user '<the user's exact words>'
The quote must match exactly one message of this conversation; check it with \`sr-session trajectory cite '<quote>'\`."

# Content by event kind. This is a PREVENTIVE guard, so both Pre and Post kinds
# reach it: Pre before the write lands, and Post at Stop re-checking the settled
# file. resultKnown is declared ONLY on the Pre kinds — it is ABSENT on a Post
# event, whose bytes are settled — so it is read only on a Pre kind; reading it
# unconditionally turns every Post recheck into a permanent refusal.
kind="$(printf '%s' "$input" | jq -r '.event.kind // empty')"
case "$kind" in
  PreFileCreate|PreFileUpdate)
    # resultKnown, not merely whether newContent is present: an underivable write
    # (a sed -i, sr-file mixed into a longer command line) still carries
    # newContent="" — present but not derived — which reads identically to a
    # genuinely-empty file if only presence is checked.
    known="$(printf '%s' "$input" | jq -r '.event.resultKnown // false')"
    if [ "$known" != "true" ]; then
      echo "Refusing the write to $path: its result cannot be computed ahead of time, so it cannot be shown NOT to drop content. Either the change shares its command line with other programs, or one of its --cite: quotes does not resolve to exactly one message of this conversation. $how" >&2
      exit 1
    fi
    ;;
  PostFileCreate|PostFileUpdate)
    ;;
  PreFileDelete|PostFileDelete)
    if [ "${cited:-0}" -gt 0 ]; then
      exit 0
    fi
    echo "Refusing to delete $path: deleting it removes all of its content, and the deletion cites nothing the user said asking for it. $how" >&2
    exit 1
    ;;
  *)
    exit 0
    ;;
esac
new="$(printf '%s' "$input" | jq -r '.event.newContent // ""')"

# Any line present in old but absent in new. (Order/whitespace refinements are
# elided in this sample.)
removed="$(comm -23 <(printf '%s' "$old" | sort -u) <(printf '%s' "$new" | sort -u) | grep -c . || true)"

if [ "${removed:-0}" -eq 0 ]; then
  exit 0   # pure additions — always fine
fi

if [ "${cited:-0}" -eq 0 ]; then
  echo "Refusing the write to $path: it removes content, and the change cites nothing the user said asking for that removal. Append rather than rewrite, or: $how" >&2
  exit 1
fi

# The removal cites the user's words. Whether it cleanly and only covers them is
# the judge's call.
exit 0
