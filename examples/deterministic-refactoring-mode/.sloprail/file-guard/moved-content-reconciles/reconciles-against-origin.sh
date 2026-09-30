#!/usr/bin/env bash
# A file carrying an sr:moved-from marker must be byte-identical to its origin at
# the pinned commit, minus imports and whitespace. The marker's fqn carries
# <path>@<sha>:<start>-<end>.
#
# This is the STOP-TIME copy (the file-guard): it reads the settled bytes off a
# PostFileCreate/PostFileUpdate. A file-guard never sees a Pre event, so the
# Pre-only `resultKnown` field does not apply here; the settled-bytes equivalent
# is `newContentKnown`, handled below. The gate of the same name keeps the
# pre-write copy, which reads the pending bytes and consults `resultKnown`.
lib_dir="$(cd "$(dirname "$0")" && pwd)"
unset reconciles_against_origin_lib_loaded
. "$lib_dir/reconciles-against-origin-lib.sh" || exit 2
[ "${reconciles_against_origin_lib_loaded:-}" = 1 ] || exit 2
lib_init
# newContentKnown is false when the settled bytes could not be read (a file that
# is not a regular file, past the byte budget); newContent is then "", the same
# string as an emptied file. A reconcile against bytes nobody read is no check, so
# refuse rather than permit.
known="$(printf '%s' "$input" | jq -r 'if (.event | has("newContentKnown")) then .event.newContentKnown else true end')"
if [ "$known" != "true" ]; then
  jq -n '{reason: "This file carries an sr:moved-from marker but its settled bytes could not be read, so it cannot be reconciled against its origin. Make it a regular file holding the moved content."}'
  exit 1
fi

new="$(printf '%s' "$input" | jq -r '.event.newContent // ""')"
lib_check
