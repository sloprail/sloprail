#!/usr/bin/env bash
# prepare: ask the judge only about a write that changes a pinned spec line — the
# same decision changes-pinned-lines.sh makes for the citation requirement. Any
# other write is `{"skip": true}`: no model call. Exit 0 from the predicate (a
# pinned line changed, or undecidable) proceeds to the judge.
set -uo pipefail

if "${SR_GUARDRAIL_DIR:-.}/changes-pinned-lines.sh" >/dev/null; then
  printf '{"additionalContext": {}}\n'
else
  printf '{"skip": true}\n'
fi
