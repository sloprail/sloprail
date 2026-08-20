#!/usr/bin/env bash
# exit: nothing to keep open. The skips this context declared live in its own
# state (read by the intake gate via --owner), which persists across cycles on
# its own, so there is no reason to hold the context active between turns.
# Clean exit (zero) => done, deactivate. If a later cycle declares another
# #skip, enter simply activates it again and appends to the same registry.
cat >/dev/null
exit 0
