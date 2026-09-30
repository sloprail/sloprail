#!/usr/bin/env bash
# The gate decides from bytes it has not seen land, so it cannot admit a write whose
# result the engine could not work out ahead (`sed -i`, `>`, `cp`, `tee`, an sr-file
# line it could not resolve): `resultKnown` is false and `newContent` is "", which
# reads the same as a write that empties the file. Refuse it, unless the predicate
# has DECIDED the write changes nothing a pin names (its `{"waived": …}` sentinel:
# nothing pins the path, or the file is not in a git work tree) — the same line
# only-when-pinned.sh draws for skipping the judge.
set -uo pipefail

payload="$(cat)"
# Only a create or an update has a result to work out; a delete leaves nothing.
kind="$(printf '%s' "$payload" | jq -r '.event.kind // ""' 2>/dev/null)"
case "$kind" in PreFileCreate | PreFileUpdate) ;; *) exit 0 ;; esac
known="$(printf '%s' "$payload" | jq -r '.event.resultKnown // false' 2>/dev/null)"
[ "$known" = "true" ] && exit 0

out="$(printf '%s' "$payload" | "${SR_GUARDRAIL_DIR:-.}/changes-pinned-lines.sh")"
rc=$?
if [ "$rc" -eq 1 ] && printf '%s' "$out" | jq -e 'has("waived")' >/dev/null 2>&1; then
  exit 0
fi
path="$(printf '%s' "$payload" | jq -r '.event.path // ""' 2>/dev/null)"
jq -n --arg p "$path" '{reason: ("What this write leaves in " + $p + " cannot be worked out before it runs (a shell command that edits it, or an sr-file call whose dry run failed), so it cannot be checked against the rules code pins. Write the file with Edit or Write, or with sr-file on its own in the command, so the result can be checked; if sr-file said why its dry run failed, fix that first.")}'
exit 1
