#!/usr/bin/env bash
# The judge rules on the bytes the write is about to leave (`event.newContent`),
# and on a write whose result the engine could not work out ahead (a `>` redirect, a
# `cp`, a `tee`, an sr-file line it could not resolve) `resultKnown` is false and
# `newContent` is "" — the same as a write that empties the file. Judging that would
# read no claims and admit whatever landed, so refuse it: the file's content has to
# be visible before it is written.
set -uo pipefail

known="$(jq -r '.event.resultKnown // false' 2>/dev/null)"
[ "$known" = "true" ] && exit 0
echo '{"reason":"What this write leaves in the markdown file cannot be worked out before it runs (a shell command that writes it, or an sr-file call whose dry run failed), so its claims cannot be checked against the cited output. Write the file content directly with sr-file write ... --cite:tool_result on its own in the command, so the result can be checked; if sr-file said why its dry run failed, fix that first."}'
exit 1
