#!/usr/bin/env bash
# exit: reads the recording-verify gate's verdict from `gates` and reflects
# pass/fail into active/inactive. Does NOT re-run the completeness check or
# refuse the Stop itself.
#
# The original version of this script ran the trajectory scan and tried to
# refuse the Stop directly, via a non-zero exit carrying a {"decision":"block",…}
# body — the OLD context contract. Under the current engine (nature_context.go's
# runContextExits: "exit is pure lifecycle... nothing here contributes to a turn
# block"), a context's exit verdict only ever flips `active`; the engine does not
# read its stdout as a refusal at all. So that refusal was silently discarded —
# an undocumented eval run was NEVER actually blocking a Stop, contradicting the
# README's "must be documented... before the turn can end." The fix mirrors
# goal-tracking/exit.sh: the real check moves to a paired gate
# (gate/recording-verify/run-verify.sh, bound to Stop, which DOES have a refusal
# channel), and this script becomes a thin read of that gate's own verdict.
set -uo pipefail

input="$(cat)"
status="$(printf '%s' "$input" | jq -r '.gates["recording-verify"].status // "fail"' 2>/dev/null)"

if [ "$status" = "pass" ]; then
  # Every run so far is documented — this context deactivates.
  exit 0
fi

# Not documented (or the gate hasn't run yet this cycle) — stay active.
exit 1
