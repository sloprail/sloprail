#!/usr/bin/env bash
# A file carrying an sr:moved-from marker must be byte-identical to its origin at
# the pinned commit, minus imports and whitespace. The marker's fqn carries
# <path>@<sha>:<start>-<end>.
#
# This is the FILE-GUARD copy: it reads each moved file's committed bytes from the
# Changeset, always known (committed blobs have no "could not be read" case), and
# reconciles every file in it. The gate of the same name keeps the pre-write copy,
# which reads the pending bytes and consults `resultKnown`.
lib_dir="$(cd "$(dirname "$0")" && pwd)"
unset reconciles_against_origin_lib_loaded
. "$lib_dir/reconciles-against-origin-lib.sh" || exit 2
[ "${reconciles_against_origin_lib_loaded:-}" = 1 ] || exit 2
lib_init

[ "$(printf '%s' "$input" | jq -r '.event.kind // ""')" = "Changeset" ] || {
  jq -n '{reason: "reconciles-against-origin: expected a Changeset event, so the moved files could not be reconciled."}'
  exit 1
}
n="$(printf '%s' "$input" | jq -r '.changeset.files | length')" || n=""
case "$n" in '' | *[!0-9]*)
  jq -n '{reason: "reconciles-against-origin: the changeset'"'"'s files could not be read, so nothing was reconciled."}'
  exit 1
  ;;
esac

i=0
while [ "$i" -lt "$n" ]; do
  path="$(printf '%s' "$input" | jq -r --argjson i "$i" '.changeset.files[$i].path // ""')" || exit 1
  # A file whose committed content or markers are missing from the payload was not
  # reconciled: refuse, naming it, rather than read the gap as "no moved-from marker".
  new="$(printf '%s' "$input" | jq -r --argjson i "$i" '.changeset.files[$i].newContent | if type == "string" then . else error("missing") end' 2>/dev/null)" &&
  markers="$(printf '%s' "$input" | jq -c --argjson i "$i" '.changeset.files[$i].newMarkers | if type == "array" then . else error("missing") end' 2>/dev/null)" || {
    jq -n --arg path "$path" '{reason: ($path + ": its committed content or markers are missing from the changeset, so it could not be reconciled against its origin.")}'
    exit 1
  }
  i=$((i + 1))
  lib_reconcile
done
exit 0
