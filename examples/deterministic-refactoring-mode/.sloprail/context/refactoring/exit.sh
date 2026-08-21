#!/usr/bin/env bash
# exit: PURE LIFECYCLE. Consulted on a Stop while the context is active. A
# context's exit CANNOT block a Stop — the engine reads its verdict only to flip
# this context's own `active` (services/sr-session/nature_context.go: exit "does
# NOT block the Stop ... only flips this context's own active"). So this does the
# ONE thing it can: decide whether the refactor is finished and the context
# should close.
#
# The blocking — "you declared a refactor but never landed it" — is NOT here. It
# is the paired Stop gate `gate/refactor-complete`, which runs BEFORE exits in
# the Stop cycle, reads this context's declared_markers, and refuses the turn if
# a declared move is missing. This exit just reads that gate's settled verdict.
#
# Receives ContextExitPayload on stdin — the Stop event, the transcriptPath,
# `currentContext` (this context's own {active, payload}), and `gates` (each
# gate's most recent {status}). Symmetric to research-rigor's and
# completeness-artifact's thin exits, which read their paired gate the same way.
#
# Done (exit 0)  -> the refactor is complete, deactivate the context.
# Not done (1)   -> stay active, so next cycle's Stop re-runs the gate. This is
#                   what carries a MULTI-CYCLE refactor: while a declared move is
#                   still outstanding the gate keeps refusing and the context
#                   stays open until every move has landed.
set -uo pipefail

input="$(cat)"

# If nothing was declared, there is nothing to keep the context open for — close
# it rather than trapping the agent in a context with no exit. (In practice a
# context with no declared_markers should not have activated, but be defensive.)
declared="$(printf '%s' "$input" | jq -r '.currentContext.payload.declared_markers[]?' 2>/dev/null)"
if [ -z "$declared" ]; then
  exit 0
fi

# The refactor is finished exactly when the completeness gate PASSED this Stop.
# Read its verdict from `gates`, not by re-deriving completeness here (that would
# duplicate the gate). Default "fail" when the gate has no recorded verdict, so
# an unknown state keeps the context OPEN (the more-guarding direction).
status="$(printf '%s' "$input" | jq -r '.gates["refactor-complete"].status // "fail"' 2>/dev/null)"

if [ "$status" = "pass" ]; then
  exit 0
fi

exit 1
