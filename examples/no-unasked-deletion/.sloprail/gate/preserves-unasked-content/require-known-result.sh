#!/usr/bin/env bash
# The gate decides from the bytes a write is about to leave. On a create or an
# update whose result the engine could not work out ahead (`sed -i`, `>`, `cp`,
# `tee`, an sr-file line it could not resolve) `resultKnown` is false and
# `newContent` is "" — the same as a write that empties the file — so a removal
# cannot be told from an append. Refuse it: a check that could not run has
# established nothing. A delete has no result to work out (its bytes are
# oldContent, and an unread one is judged, never skipped).
set -uo pipefail

input="$(cat)"
kind="$(printf '%s' "$input" | jq -r '.event.kind // ""' 2>/dev/null)"
case "$kind" in PreFileCreate | PreFileUpdate) ;; *) exit 0 ;; esac
[ "$(printf '%s' "$input" | jq -r '.event.resultKnown // false' 2>/dev/null)" = "true" ] && exit 0
echo '{"reason":"What this write leaves in the memory file cannot be worked out before it runs (a shell command that edits it, or an sr-file call whose dry run failed), so it cannot be shown to keep the content nobody asked to remove. Make the edit with Edit or Write, or with sr-file on its own in the command (sr-file edit <path> --old-string ... --new-string ... [--cite:user ...]); if sr-file said why its dry run failed, fix that first."}'
exit 1
