#!/usr/bin/env bash
# For every scanner scanner-declared logged, confirm ONE gh call somewhere in
# this run's trajectories covered ALL of its declared keywords together. A
# declared scanner with no matching search is the violation this gate catches.
set -uo pipefail

input="$(cat)"
transcript_path="$(printf '%s' "$input" | jq -r '.transcriptPath')"

# Read scanner-declared's registry; `require` guarantees it ran first, so entries
# are current. `state list` emits JSON-LINES, so slurp with `jq -s`.
declared="$(sr-session state list --owner scanner-declared 2>/dev/null | jq -s -c '[.[] | select(.key | startswith("scanner:"))]')"
declared_count="$(printf '%s' "${declared:-[]}" | jq 'length' 2>/dev/null || echo 0)"

if [ "$declared_count" -eq 0 ]; then
  # Backstop only — gate.yaml's match already skips this script when nothing was
  # declared; this covers the edge where the context ran but left nothing usable
  # (e.g. every scanner.yaml failed to parse).
  exit 0
fi

# Every trajectory this run touched: the current one plus the sub-agent paths
# `describe` reports.
trajectory_files="$(
  { printf '%s\n' "$transcript_path"
    sr-session trajectory describe --path "$transcript_path" 2>/dev/null \
      | jq -r '.subagentPaths[]?'
  } | sort -u)"

all_gh_calls="[]"
while IFS= read -r traj_path; do
  [ -f "$traj_path" ] || continue
  # Flatten every PreCommandInvoke event's invocations[] down to the gh ones.
  # Invocations sit under `.fields.invocations` (event wire form is {kind, fields}).
  calls="$(sr-session trajectory normalize \
    --path "$traj_path" \
    --events PreCommandInvoke \
    --whole-session \
    | jq -c '[ .[] | .events[]? | select(.kind == "PreCommandInvoke")
               | .fields.invocations[]? | select(.bin == "gh") ]' 2>/dev/null)"
  [ -z "${calls:-}" ] && continue
  all_gh_calls="$(printf '%s' "$all_gh_calls" | jq -c --argjson c "$calls" '. + $c')"
done <<< "$trajectory_files"

# Flatten each gh call's queryable text: positional args after `search`,
# plus any `-f`/`--field q=<...>` value.
searchable_text="$(printf '%s' "$all_gh_calls" | jq -r '
  .[] | (
    ((.argv // []) | join(" "))
    + " "
    + ((.flags.field // .flags.f // "") )
  )
')"

missing_scanners=""
while IFS= read -r entry; do
  [ -z "$entry" ] && continue
  scanner_name="$(printf '%s' "$entry" | jq -r '.key | ltrimstr("scanner:")')"
  keywords="$(printf '%s' "$entry" | jq -r '.value | fromjson[]' 2>/dev/null)"

  if [ -z "$keywords" ]; then
    missing_scanners="$missing_scanners $scanner_name(no keywords parsed)"
    continue
  fi

  # Does ANY single gh call's searchable text contain EVERY keyword this
  # scanner declared? Checked call-by-call, not keyword-by-keyword across
  # calls — a keyword found in one call and another keyword found in a
  # DIFFERENT call does not satisfy "all keywords in 1 call".
  covered="false"
  while IFS= read -r call_text; do
    [ -z "$call_text" ] && continue
    all_present="true"
    while IFS= read -r kw; do
      [ -z "$kw" ] && continue
      case "$call_text" in
        *"$kw"*) ;;
        *) all_present="false" ;;
      esac
    done <<< "$keywords"
    if [ "$all_present" = "true" ]; then
      covered="true"
      break
    fi
  done <<< "$searchable_text"

  if [ "$covered" != "true" ]; then
    missing_scanners="$missing_scanners $scanner_name"
  fi
done < <(printf '%s' "$declared" | jq -c '.[]')

if [ -n "$missing_scanners" ]; then
  echo "These declared scanners have no single gh call covering all their keywords:$missing_scanners" >&2
  exit 1
fi

exit 0
