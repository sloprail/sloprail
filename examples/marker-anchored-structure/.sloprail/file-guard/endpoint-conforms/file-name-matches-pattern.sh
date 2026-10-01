#!/usr/bin/env bash
# Settle before the judge: (1) every endpoint file in the changeset follows the
# endpoint file-name pattern, and (2) log each declared marker into the registry,
# so a later Stop gate (not built here) can total what showed up this session.
set -uo pipefail

input="$(cat)"

[ "$(printf '%s' "$input" | jq -r '.event.kind // ""')" = "Changeset" ] || {
  echo "endpoint-conforms: expected a Changeset event, so the endpoint files could not be checked" >&2
  exit 1
}
n="$(printf '%s' "$input" | jq -r '.changeset.files | length')" || n=""
case "$n" in '' | *[!0-9]*)
  echo "endpoint-conforms: the changeset's files could not be read, so nothing was checked" >&2
  exit 1
  ;;
esac

# Every file is checked before any is registered, so a refusal leaves the registry
# untouched.
paths=()
fqns=()
i=0
while [ "$i" -lt "$n" ]; do
  path="$(printf '%s' "$input" | jq -r --argjson i "$i" '.changeset.files[$i].path')" || exit 1
  fqn="$(printf '%s' "$input" | jq -r --argjson i "$i" '(.changeset.files[$i].newMarkers // []) | map(select(.kind == "endpoint")) | (.[0].fqn // "")')" || exit 1
  i=$((i + 1))
  name="$(basename "$path")"

  # <verb>-<resource>.<ext> — e.g. get-users.ts, create-order.py.
  if ! [[ "$name" =~ ^[a-z]+-[a-z0-9-]+\.[a-z]+$ ]]; then
    echo "Endpoint file '$name' does not follow the <verb>-<resource>.<ext> naming pattern (e.g. get-users.ts)." >&2
    exit 1
  fi
  paths+=("$path")
  fqns+=("$fqn")
done

k=0
while [ "$k" -lt "${#paths[@]}" ]; do
  sr-session state set "endpoint:${fqns[$k]:-${paths[$k]}}" "${paths[$k]}" >/dev/null 2>&1 || true
  k=$((k + 1))
done

exit 0
