#!/usr/bin/env bash
# exit: nothing to keep open. The skips live in state that persists across
# cycles on its own, so deactivate (exit 0); a later #skip re-enters and appends.
cat >/dev/null
exit 0
