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
markers="$(printf '%s' "$input" | jq -c '.event.newMarkers // []')"
lib_reconcile
}

# Drop imports and normalize whitespace on both sides, so a legitimate import
# rewrite is not read as a rewrite of the moved code. Blank lines are dropped too
# (a PEP8 file has them around every def); every non-blank line must still match.
normalize() { grep -vE '^\s*(import|from)\b' | sed 's/[[:space:]][[:space:]]*/ /g;s/^ //;s/ $//' | grep -v '^$'; }

# lib_reconcile checks EVERY moved-from marker against its own origin range. A
# file may carry several markers (one per moved block); each marker owns the lines
# from just after it to just before the next moved-from marker. The first marker
# also owns anything above it, so a single-marker file is compared whole, as it
# always was. Marker lines of every comment style (// # --) are not code and are
# dropped from the comparison.
lib_reconcile() {
# `path` is the file being reconciled (set by the caller, before the loop reuses the name):
# every refusal names it, so an agent never has to guess which file the reason is about.
file="${path:-the moved file}"
count="$(printf '%s' "$markers" | jq -r '[.[] | select(.kind == "moved-from")] | length')"
[ "$count" -gt 0 ] || return 0
total="$(printf '%s\n' "$new" | wc -l | tr -d ' ')"

k=0
while [ "$k" -lt "$count" ]; do
  fqn="$(printf '%s' "$markers" | jq -r --argjson k "$k" '[.[] | select(.kind == "moved-from")][$k].fqn // ""')"
  line="$(printf '%s' "$markers" | jq -r --argjson k "$k" '[.[] | select(.kind == "moved-from")][$k].line // 0')"
  next="$(printf '%s' "$markers" | jq -r --argjson k "$((k + 1))" '[.[] | select(.kind == "moved-from")][$k].line // 0')"
  k=$((k + 1))
  [ -n "$fqn" ] || continue

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

  from=$((line + 1)); [ "$k" -eq 1 ] && from=1
  to="$total"; [ "$next" -gt 0 ] && to=$((next - 1))
  moved_body="$(printf '%s\n' "$new" | sed -n "${from},${to}p" | grep -vE '^\s*(//|#|--)\s*sr:' | normalize)"
  origin_body="$(printf '%s' "$origin" | normalize)"

  if [ "$moved_body" != "$origin_body" ]; then
    # The first normalized line that differs (or is missing on one side), so the agent
    # can see WHAT differs rather than guess.
    o_line="$(diff <(printf '%s\n' "$origin_body") <(printf '%s\n' "$moved_body") | sed -n 's/^< //p' | head -1)"
    n_line="$(diff <(printf '%s\n' "$origin_body") <(printf '%s\n' "$moved_body") | sed -n 's/^> //p' | head -1)"
    jq -n --arg file "$file" --arg fqn "$fqn" --arg o "$o_line" --arg n "$n_line" '{reason: ($file + ": content marked moved-from \u0027" + $fqn + "\u0027 does not reconcile against its origin — after dropping imports, blank lines and whitespace, the bytes differ. First differing line: origin: \u0027" + $o + "\u0027 / new: \u0027" + $n + "\u0027 (empty means that side has no such line). A move must carry the origin\u0027s bytes, not regenerated ones.")}'
    exit 1
  fi
done

return 0
}

reconciles_against_origin_lib_loaded=1
