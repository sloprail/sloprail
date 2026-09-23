#!/usr/bin/env bash
# match already confirmed no tag was declared — just refuse with the remedy.
echo "This turn declared no tag (#update, #decision, or #skip) — declare one before the turn can end." >&2
exit 1
