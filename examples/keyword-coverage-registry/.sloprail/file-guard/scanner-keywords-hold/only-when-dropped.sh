#!/usr/bin/env bash
# prepare: ask the judge only about a write that drops a declared keyword — the
# same decision drops-keywords.sh makes for the citation requirement. A write that
# drops nothing (a new scanner, added keywords) is `{"skip": true}`: no model call.
# Exit 0 from drops-keywords.sh (a drop, or undecidable) proceeds to the judge.
set -uo pipefail

if "${SR_GUARDRAIL_DIR:-.}/drops-keywords.sh" >/dev/null; then
  printf '{"additionalContext": {}}\n'
else
  printf '{"skip": true}\n'
fi
