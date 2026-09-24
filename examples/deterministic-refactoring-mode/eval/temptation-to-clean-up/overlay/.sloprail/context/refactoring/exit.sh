#!/usr/bin/env bash
# Pure lifecycle: consulted on a Stop while the context is active, this only
# decides whether to close the context — it cannot block (that's the gate's job).
# The refactor is finished exactly when the completeness gate passed this Stop,
# so read that verdict rather than re-deriving it. Staying open across a failing
# Stop is what carries a multi-cycle refactor.
set -uo pipefail

input="$(cat)"

# Nothing declared -> nothing to stay open for.
declared="$(printf '%s' "$input" | jq -r '.currentContext.payload.declared_markers[]?' 2>/dev/null)"
if [ -z "$declared" ]; then
  exit 0
fi

# Default "fail" (context stays open) when the gate has no recorded verdict —
# the more-guarding direction.
status="$(printf '%s' "$input" | jq -r '.gates["refactor-complete"].status // "fail"' 2>/dev/null)"

if [ "$status" = "pass" ]; then
  exit 0
fi

exit 1
