#!/usr/bin/env bash
# Reaching this script at all already means `match` confirmed no tag was
# declared this cycle — nothing left to check, just refuse with the
# remedy.
echo "This turn declared no tag (#update, #decision, or #skip) — declare one before the turn can end." >&2
exit 1
