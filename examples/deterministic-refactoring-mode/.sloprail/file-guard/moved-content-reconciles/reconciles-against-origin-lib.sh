#!/usr/bin/env bash
# Shared by the moved-content-reconciles gate and its file-guard: one library, two thin entries.
# The gate entry reads the pending write (lib_init, lib_check); the file-guard entry
# reads the Changeset and calls lib_reconcile once per file, with `markers` (the
# file's markers as JSON) and `new` (its bytes) set. lib_reconcile reads no event.

lib_init() {
set -uo pipefail

input="$(cat)"
}

# lib_check is the gate's: the pending write's markers, off its event.
lib_check() {
path="$(printf '%s' "$input" | jq -r '.event.path // ""')"
markers="$(printf '%s' "$input" | jq -c '.event.newMarkers // []')"
lib_reconcile
}

# `path` is the file being reconciled (set by the caller): every refusal names it, so an
# agent never has to guess which of its commits or files the reason is about.
lib_reconcile() {
file="${path:-the moved file}"
fqn="$(printf '%s' "$markers" | jq -r '[.[] | select(.kind == "moved-from")][0].fqn // ""')"
if [ -z "$fqn" ]; then
  return 0
fi

# <path>@<sha>:<start>-<end>
path="${fqn%@*}"; rest="${fqn#*@}"
sha="${rest%%:*}"; range="${rest#*:}"
start="${range%-*}"; end="${range#*-}"

origin="$(git show "$sha:$path" 2>/dev/null | sed -n "${start},${end}p")" || {
  cat <<EOF
{"reason":"$file: moved-from origin '$fqn' names a commit or path this checkout does not have — a move cannot be verified against bytes that are not here."}
EOF
  exit 1
}

# Drop imports and normalize whitespace on both sides, so a legitimate import
# rewrite is not read as a rewrite of the moved code.
normalize() { grep -vE '^\s*(import|from)\b' | sed 's/[[:space:]]\+/ /g;s/^ //;s/ $//'; }
moved_body="$(printf '%s' "$new" | grep -vE '^\s*//\s*sr:' | normalize)"
origin_body="$(printf '%s' "$origin" | normalize)"

if [ "$moved_body" != "$origin_body" ]; then
  cat <<EOF
{"reason":"$file: content marked moved-from '$fqn' does not reconcile against its origin — after dropping imports and whitespace, the bytes differ. A move must carry the origin's bytes, not regenerated ones."}
EOF
  exit 1
fi

return 0
}

reconciles_against_origin_lib_loaded=1
