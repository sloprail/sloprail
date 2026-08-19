#!/usr/bin/env bash
# For every scanner the sibling context logged (sr-session state, owned by
# scanner-declared — this gate reads it via --owner), confirm ONE gh call
# somewhere in this run's trajectories covered ALL of that scanner's
# declared keywords together (his shape: "ensuring that gh call has all
# keywords in 1 call"). Checked strictly against the DECLARED list — a
# search this gate cannot match to a declared scanner does not count for
# anything, and a declared scanner with no matching search is the
# violation this gate exists to catch. Direction matters here (his
# correction, 2026-08-19): keywords come from the files first, search
# coverage is checked against them second — never the other way around.
set -uo pipefail

input="$(cat)"
transcript_path="$(printf '%s' "$input" | jq -r '.transcriptPath')"
transcript_dir="$(dirname "$transcript_path")"

declared="$(sr-session state list --owner scanner-declared 2>/dev/null | jq -c '[.[] | select(.key | startswith("scanner:"))]')"
declared_count="$(printf '%s' "${declared:-[]}" | jq 'length' 2>/dev/null || echo 0)"

if [ "$declared_count" -eq 0 ]; then
  # Backstop only — gate.yaml's own match: context["scanner-declared"].active
  # already skips this script declaratively when nothing was ever declared;
  # this covers the edge where the context ran but left nothing usable
  # (e.g. every scanner.yaml failed to parse). (Whether a project REQUIRES
  # at least one scanner per research run is a separate concern, not this
  # gate's — install a companion "at least one scanner declared" check if
  # that is wanted.)
  exit 0
fi

# Every gh invocation across every trajectory this run touched, with the
# search terms/query text each one actually carried.
#
# TODO(sr-session query): enumerating this run's sibling trajectories is not
# a real selector yet (research-rigor's own depth-check flags the same open
# gap in its check 4 — decision 20260818_no-slop-primitives Thread 1).
# Globbing the session directory for sibling .jsonl files is a stand-in.
trajectory_files="$(find "$transcript_dir" -maxdepth 1 -name '*.jsonl' 2>/dev/null)"
[ -z "$trajectory_files" ] && trajectory_files="$transcript_path"

all_gh_calls="[]"
while IFS= read -r traj_path; do
  [ -f "$traj_path" ] || continue
  calls="$(sr-session query \
    --transcript "$traj_path" \
    --select tool_use \
    --whole-session \
    --where 'any(invocations, .bin == "gh")' \
    | jq -c '[.[].invocations[]? | select(.bin == "gh")]' 2>/dev/null)"
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
