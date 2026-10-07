#!/usr/bin/env bash
# prepare: ask the judge only about a write that drops a declared keyword — the
# same decision drops-keywords.sh makes for the citation requirement. A changeset that
# drops nothing (a new scanner, added keywords) is `{"skip": true}`: no model call.
#
# Only drops-keywords.sh's DECIDED "drops nothing" — exit 1 — skips the judge.
# Exit 0 (a drop, or undecidable) and every other code go to the judge: a
# predicate that could not run at all (not executable: 126, missing: 127, a
# crash) decided nothing, and treating it as "drops nothing" skipped the judge,
# so any resolvable quote of the user's then admitted the drop. This is the
# engine's own `when` contract: anything but exit 1 applies.
set -uo pipefail

# The predicate reads the same Changeset payload: it inherits this check's stdin.
"${SR_GUARDRAIL_DIR:-.}/drops-keywords.sh" >/dev/null
status=$?

if [ "$status" -eq 1 ]; then
  printf '{"skip": true}\n'
else
  printf '{"additionalContext": {}}\n'
fi
