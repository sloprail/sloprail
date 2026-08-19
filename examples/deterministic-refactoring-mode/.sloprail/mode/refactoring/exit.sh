#!/usr/bin/env bash
# exit: is the declared refactor done? Consulted on a Stop while the mode is
# active. Done (exit 0) lets the mode deactivate and the Stop proceed; not
# done (exit 1) keeps the mode active and refuses the Stop, so the agent
# cannot end a turn having declared a refactor it never completed.
#
# Receives ModeExitPayload on stdin — the Stop event, the transcriptPath, and
# `currentContext`: this mode's own context, the object enter() produced.
set -uo pipefail

input="$(cat)"
declared="$(printf '%s' "$input" | jq -r '.currentContext.declared_markers[]?' 2>/dev/null)"

if [ -z "$declared" ]; then
  # Nothing was declared to reconcile against — treat as done rather than
  # trapping the agent in a mode with no exit.
  exit 0
fi

# Every declared marker must actually be present in the tree now, and the
# content it marks must reconcile against its origin (byte-identical minus the
# import/whitespace exceptions — the same reconciliation unit 12 names). The
# deterministic-refactoring file-guard already owns the per-file byte check;
# this exit only confirms the SET is complete and each one landed.
missing=""
for marker in $declared; do
  if ! grep -rq "sr:$marker" . 2>/dev/null; then
    missing="$missing $marker"
  fi
done

if [ -n "$missing" ]; then
  cat <<EOF
{"decision":"block","reason":"Refactor declared but not complete — these declared markers were never written:$missing. Finish the moves you declared, or the turn cannot end."}
EOF
  exit 1
fi

# All declared markers present and (via the file-guard) reconciled — done.
exit 0
