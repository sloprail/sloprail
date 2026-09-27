#!/usr/bin/env bash
# enter: parse the written scanner.yaml and, if active, log its FULL keyword set
# as one entry (not one per keyword) — the sibling gate checks "did ONE gh call
# cover ALL of these".
#
# The entry is keyed by the scanner's folder, workspace-relative —
# `scanner:scanners/mine` — so two scanners that share a last folder name
# (scanners/mine and zz/scanners/mine) are two obligations, not one the later
# write overwrites.
#
# Two moments log differently:
#   - Pre (the write is about to land): the entry becomes the UNION of what was
#     logged, what the file on disk declares now (`oldContent`), and what this
#     write declares. The write may still be refused after this runs (contexts
#     enter before the preventive scanner-keywords-hold guard), so a Pre write
#     may add to the obligation but never shrink it — including for a committed
#     scanner this session never logged, whose refused narrowing write is
#     followed by no Post event (the file never changed).
#   - Post (the settled file, at Stop): the entry becomes exactly the file's
#     keywords. Anything that dropped one got past scanner-keywords-hold, which
#     asks for the user's words first.
# Every logged declaration also renews `stamp:<folder>`, a token no earlier
# declaration had: an admitted delete retires the scanner at the stamp it saw,
# so declaring it again makes it owed again (see scanner-lib.sh, registry_owed).
#
# An entry is never removed: a declared scanner stays owed a search even if its
# file is later switched off or deleted by a route no rule saw — that is what
# verify-scanner-coverage reads, so losing the file cannot make the obligation
# disappear. Only a delete the user asked for retires it.
set -uo pipefail

# Without the shared parser nothing can be logged correctly: decline. With no
# scanner logged, search-needs-declared-scanner refuses every search — closed.
# shellcheck source=scanner-lib.sh
. "${SR_GUARDRAIL_DIR:-.}/scanner-lib.sh" 2>/dev/null || exit 1

input="$(cat)"
scanner_path="$(printf '%s' "$input" | jq -r '.event.path // ""')"

# Content by event kind. On a Pre write with resultKnown false (a shell-derived
# write) the content is not derivable yet: decline (non-zero: this occurrence
# does not enter) and let the Post kind log the settled file at Stop.
kind="$(printf '%s' "$input" | jq -r '.event.kind // empty')"
old=""
case "$kind" in
  PostFileCreate|PostFileUpdate)
    phase="post"
    content="$(printf '%s' "$input" | jq -r '.event.newContent // ""')"
    ;;
  PreFileCreate|PreFileUpdate)
    phase="pre"
    known="$(printf '%s' "$input" | jq -r '.event.resultKnown // false')"
    if [ "$known" != "true" ]; then
      # Result not derivable ahead of the write: defer to the Post kind.
      exit 1
    fi
    content="$(printf '%s' "$input" | jq -r '.event.newContent // ""')"
    # The file as it stands before this write (an update has it; a create
    # has none).
    old="$(printf '%s' "$input" | jq -r '.event.oldContent // ""')"
    ;;
  *)
    # No kind, or one this context is not about: nothing to activate on.
    exit 0
    ;;
esac

if [ -z "$content" ] || [ -z "$scanner_path" ]; then
  [ "$phase" = "pre" ] && exit 1
  exit 0
fi

scanner="$(scanner_dir "$scanner_path")"

if [ "$(scanner_active "$content")" != "true" ]; then
  # Declared but not active — a scanner can be authored and left off. Before
  # the write there is nothing to log yet.
  [ "$phase" = "pre" ] && exit 1
  jq -n --arg name "$scanner" '{scanner: $name, active: false}'
  exit 0
fi

keywords="$(scanner_keywords "$content")"

if [ -z "$keywords" ]; then
  [ "$phase" = "pre" ] && exit 1
  jq -n --arg name "$scanner" '{scanner: $name, active: true, error: "keywords: is empty or missing"}'
  exit 0
fi

keywords_json="$(printf '%s' "$keywords" | jq -R -s 'split("\n") | map(select(length > 0))')"

if [ "$phase" = "pre" ]; then
  # Never shrink at Pre: union with what was logged and with the file as it
  # stands. A logged entry that cannot be read is treated as empty — the file's
  # own keywords still hold the line.
  old_json="$(scanner_keywords "$old" | jq -R -s 'split("\n") | map(select(length > 0))')"
  logged="$(sr-session state get "scanner:${scanner}" 2>/dev/null)"
  printf '%s' "$logged" | jq -e 'type == "array"' >/dev/null 2>&1 || logged="[]"
  keywords_json="$(jq -n -c --argjson a "$logged" --argjson b "$old_json" --argjson c "$keywords_json" 'reduce ($a + $b + $c)[] as $k ([]; if any(.[]; . == $k) then . else . + [$k] end)')"
fi

sr-session state set "scanner:${scanner}" "$keywords_json"
sr-session state set "stamp:${scanner}" "$(date +%s)-$$-${RANDOM}${RANDOM}"

jq -n --arg name "$scanner" --argjson kw "$keywords_json" \
  '{scanner: $name, active: true, keywords: $kw}'
