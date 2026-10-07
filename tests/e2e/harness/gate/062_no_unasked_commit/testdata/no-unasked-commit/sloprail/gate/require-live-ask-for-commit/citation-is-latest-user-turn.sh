#!/usr/bin/env bash
# DETERMINISTIC half of "does the citation actually ground THIS commit/push,
# right now" — part (a) of the #95745 regression: a cited quote that resolves
# to a real user message is not enough on its own, because the whole bug was
# an agent reusing a STALE grant from several turns back. This script asks a
# question a script can answer without judgement: is the cited line the
# LATEST real user message in the transcript, or an older one?
#
# Part (b) — "does that latest message actually ask for a commit/push" — is
# left to the judge (asks-for-commit-or-push.md.j2): wording is not a thing a
# script should decide, and this script does not try.
#
# Contract: stdin is the GateCheckPayload ({"event":{...},"transcriptPath":...}).
# exit 0 permits (the requirement's `citation` already guaranteed a resolved
# user-pool quote exists; this only checks WHICH message it resolved to).
# To refuse, print {"reason":"..."} and exit 1.
set -uo pipefail

payload="$(cat)"
field() { printf '%s' "$payload" | jq -r "$1"; }

refuse() {
  jq -n --arg r "$1" '{reason: $r}'
  exit 1
}

tp="$(field '.transcriptPath // ""')"
[ -n "$tp" ] || refuse "the session record could not be read, so this rule could not tell whether the cited message is your latest one — nothing was permitted on an unreadable trajectory"

# Every citation this command carries from the `user` pool. require: citation
# already guaranteed at least one exists (a load-time property of the gate);
# this script does not re-check that — it only asks WHICH message each one
# resolved to.
user_lines="$(printf '%s' "$payload" | jq -r '
  [.event.citations[]? | select(.sourceTypes[]? == "user") | .line] | .[]')"

if [ -z "$user_lines" ]; then
  # require: citation already refuses an uncited command before any check
  # runs, so this is not reachable in practice — but a check that could not
  # find what it was asked to verify must not read as approval.
  refuse "no user citation was found on this command — cite the message that actually asks for this commit/push"
fi

# The LATEST real user message in the whole session: type "user", not a
# sub-agent's ("isSidechain"), and its content is a plain STRING — a genuine
# typed message — never an array (that shape is a tool_result delivered back
# to the agent, which also arrives as a "user"-typed entry but is not
# something a person said). This is the exact distinction #95745 needed and a
# keyword grep over "any past user message" would not have made: the fix is
# not "was this ever said" but "is this what the user is asking FOR RIGHT NOW".
latest_line="$(sr-session trajectory normalize --path "$tp" --whole-session --events PreCommandInvoke 2>/dev/null \
  | jq -r '
      [ .[]
        | select(.type == "user")
        | select((.isSidechain // false) == false)
        | select((.message.content | type) == "string")
        | .line
      ]
      | if length == 0 then empty else max end
    ')"

if [ -z "$latest_line" ]; then
  refuse "this session's transcript carries no real user message at all, so the citation could not be checked against your latest one — ask the user directly before committing or pushing"
fi

stale=""
while IFS= read -r line; do
  [ -n "$line" ] || continue
  if [ "$line" != "$latest_line" ]; then
    stale="$line"
  fi
done <<EOF
$user_lines
EOF

if [ -n "$stale" ]; then
  refuse "the cited message is not the user's latest message in this session — a standing or earlier permission ('commit + push' from several turns back) does not authorize a commit/push now; ask the user first, or cite their actual latest message if it really does ask for one"
fi

exit 0
