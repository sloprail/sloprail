#!/usr/bin/env bash
# For every scanner scanner-declared logged, confirm ONE gh call somewhere in
# this run's trajectories covered ALL of its declared keywords together. A
# declared scanner with no matching search is the violation this gate catches.
set -uo pipefail

input="$(cat)"
transcript_path="$(printf '%s' "$input" | jq -r '.transcriptPath')"

# A registry this script cannot read decides nothing, so it refuses: reading the
# silence as "nothing declared" passed every Stop whenever sr-session failed
# (`jq -s` of empty input is `[]`, exit 0).
plumbing() {
  echo "verify-scanner-coverage could not check this turn: $1. The Stop was refused rather than passed unchecked; if this keeps happening the sloprail install is broken — say so rather than working around it." >&2
  exit 1
}

# shellcheck source=../../context/scanner-declared/scanner-lib.sh
. "${SR_GUARDRAIL_DIR:-.}/../../context/scanner-declared/scanner-lib.sh" 2>/dev/null \
  || plumbing "the shared scanner-lib.sh beside scanner-declared could not be loaded"

# Read scanner-declared's registry — the scanners still owed a search; `require`
# guarantees the context ran first, so entries are current.
declared="$(registry_owed)" || plumbing "scanner-declared's registry could not be read (sr-session state list failed or returned something that is not its JSON lines)"
declared_count="$(printf '%s' "$declared" | jq 'length' 2>/dev/null)"
case "$declared_count" in
  '' | *[!0-9]*) plumbing "scanner-declared's registry did not parse" ;;
esac

if [ "$declared_count" -eq 0 ]; then
  # Nothing owed: gate.yaml's match already skips this script when nothing was
  # declared, and a scanner the user had deleted (an admitted, cited delete) is
  # retired rather than owed.
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
  # Events are flat: `.invocations` sits beside `.kind`, as on a live check.
  calls="$(sr-session trajectory normalize \
    --path "$traj_path" \
    --events PreCommandInvoke \
    --whole-session \
    | jq -c '[ .[] | .events[]? | select(.kind == "PreCommandInvoke")
               | .invocations[]? | select(.bin == "gh") ]' 2>/dev/null)"
  [ -z "${calls:-}" ] && continue
  all_gh_calls="$(printf '%s' "$all_gh_calls" | jq -c --argjson c "$calls" '. + $c')"
done <<< "$trajectory_files"

# Flatten each gh call's queryable text: positional args after `search`,
# plus any `-f`/`--field q=<...>` value.
searchable_text="$(printf '%s' "$all_gh_calls" | jq -r '
  .[] | (
    ((.argv // []) | join(" "))
    + " "
    + (((.flags.field // []) + (.flags.f // [])) | join(" "))
  )
')"

missing_scanners=""
missing_detail=""
while IFS= read -r entry; do
  [ -z "$entry" ] && continue
  # The scanner's folder, workspace-relative (scanners/mine) — the registry's
  # key, and what the remedy names.
  scanner_name="$(printf '%s' "$entry" | jq -r '.dir')"
  keywords="$(printf '%s' "$entry" | jq -r '.keywords[]?' 2>/dev/null)"

  if [ -z "$keywords" ]; then
    missing_scanners="$missing_scanners $scanner_name(no keywords parsed)"
    continue
  fi

  # Does ANY single gh call's searchable text contain EVERY keyword this
  # scanner declared? Checked call-by-call, not keyword-by-keyword across
  # calls — a keyword found in one call and another keyword found in a
  # DIFFERENT call does not satisfy "all keywords in 1 call".
  #
  # WORD-BOUNDARY, case-insensitive match — not a bare substring. A bare
  # `case "$call_text" in *"$kw"*)` (the earlier version of this script) is
  # defeated in both directions: it credits "art" merely appearing inside
  # "Kubernetes-agnostic-architecture", and it is case-sensitive so
  # "Guardrail" in a call fails a declared "guardrail" keyword. grep -iw
  # (case-insensitive, whole-word) closes both without a model call — this
  # is still a structural fact ("does this literal word appear"), not a
  # meaning question ("does this call address the topic"); crediting a
  # synonym or a rephrasing genuinely needs a judge and is out of scope for
  # what a registry-style coverage check promises: the scanner names its
  # OWN keywords, so requiring a search for those words, not a paraphrase of
  # them, is the check's actual contract.
  covered="false"
  while IFS= read -r call_text; do
    [ -z "$call_text" ] && continue
    all_present="true"
    while IFS= read -r kw; do
      [ -z "$kw" ] && continue
      kw_escaped="$(printf '%s' "$kw" | sed 's/[.[\*^$/]/\\&/g')"
      if ! printf '%s' "$call_text" | grep -qiwE "$kw_escaped"; then
        all_present="false"
      fi
    done <<< "$keywords"
    if [ "$all_present" = "true" ]; then
      covered="true"
      break
    fi
  done <<< "$searchable_text"

  if [ "$covered" != "true" ]; then
    missing_scanners="$missing_scanners $scanner_name"
    # Name the keywords, so the remedy is one command away rather than a
    # re-read of a file (which may be gone — the registry still holds it).
    missing_detail="$missing_detail
  $scanner_name: $(printf '%s' "$keywords" | sed 's/.*/"&"/' | paste -sd ' ' -)"
  fi
done < <(printf '%s' "$declared" | jq -c '.[]')

# The remedy, spelled out: measured on a real run, an agent whose covering search
# came back empty took the refusal to mean it needed RESULTS from that search,
# tried to drop keywords (refused), and thrashed through twenty narrower
# searches — although its one covering call had already satisfied this gate.
if [ -n "$missing_scanners" ]; then
  echo "These declared scanners have no single gh call covering all their keywords:$missing_scanners.
Run ONE gh search whose query contains every keyword of the scanner, e.g.:$missing_detail
That one call is what counts — it satisfies this check even if GitHub returns nothing for so specific a query. Run narrower searches besides it for actual results; they do not have to carry every keyword." >&2
  exit 1
fi

exit 0
