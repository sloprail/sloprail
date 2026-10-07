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
# The entry only ever GROWS here, at both moments it is logged:
#   - Pre (the write is about to land): the union of what is owed, what the
#     file on disk declares now (`oldContent`), and what this write declares.
#     The write may still be refused after this runs (contexts enter before the
#     scanner-keywords-hold gate), and a refused narrowing of a
#     committed scanner this session never logged is followed by no Post event
#     at all (the file never changed).
#   - Post (the settled file, at Stop): the union of what is owed and the
#     file's keywords. It used to become exactly the file's keywords, trusting
#     that anything which dropped one had got past scanner-keywords-hold — but a
#     write the engine cannot parse (`python3 -c "open(…).write(…)"`) reaches
#     Stop as a PostFileCreate for a scanner declared this session, and a
#     create drops nothing by definition, so the narrowing landed unasked.
# The one way the owed set shrinks is a drop the USER asked for:
# scanner-keywords-hold's last check records it as `narrowed:<folder>` once the
# user's words were cited and judged to ask for it, and "what is owed" below is
# that narrowing while it matches the current stamp (scanner-lib.sh).
#
# Every logged declaration also renews `stamp:<folder>`, a token no earlier
# declaration had: an admitted delete retires, and an admitted drop narrows,
# the scanner only at the stamp it saw, so declaring it again makes it owed in
# full again.
#
# An entry is never removed: a declared scanner stays owed a search even if its
# file is later switched off or deleted by a route no rule saw — that is what
# verify-scanner-coverage reads, so losing the file cannot make the obligation
# disappear. Only a delete the user asked for retires it.
set -uo pipefail

# Without the shared parser nothing can be logged correctly: decline. With no
# scanner logged, search-needs-declared-scanner refuses every search — closed.
[ -n "${SR_GUARDRAIL_DIR:-}" ] || exit 1
# A helper stopped early runs only partly (whether the `.` then fails depends
# on the bash version); only its last-line sentinel proves it loaded whole.
unset scanner_lib_loaded
# shellcheck source=scanner-lib.sh
. "$SR_GUARDRAIL_DIR/scanner-lib.sh" 2>/dev/null || exit 1
[ "${scanner_lib_loaded:-}" = 1 ] || exit 1

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
    # Settled bytes the engine could not READ (newContentKnown false: a link to
    # a FIFO or a device, or past the read cap) declare nothing this context can
    # log. Enter without touching the registry: what is owed stays owed.
    known="$(printf '%s' "$input" | jq -r 'if (.event | has("newContentKnown")) then .event.newContentKnown else true end')"
    if [ "$known" != "true" ]; then
      jq -n --arg path "$scanner_path" '{scanner: $path, error: "the settled scanner file could not be read"}'
      exit 0
    fi
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
    # The file as it stands before this write: only an update carries it — a
    # create has none, and `old` stays "".
    if [ "$kind" = "PreFileUpdate" ]; then
      old="$(printf '%s' "$input" | jq -r '.event.oldContent // ""')"
    fi
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

as_json() { jq -R -s -c 'split("\n") | map(select(length > 0))'; }
keywords_json="$(printf '%s' "$keywords" | as_json)"
old_json="$(scanner_keywords "$old" | as_json)"

# What is owed so far (an admitted narrowing applied). A registry that cannot be
# read decides nothing to take away: the file's own keywords still hold the line.
owed="$(registry_keywords "$scanner" 2>/dev/null)"
printf '%s' "$owed" | jq -e 'type == "array"' >/dev/null 2>&1 || owed="[]"

# Never shrink, at either moment. Order kept: owed first, then what is new.
keywords_json="$(jq -n -c --argjson a "$owed" --argjson b "$old_json" --argjson c "$keywords_json" \
  'reduce ($a + $b + $c)[] as $k ([]; if any(.[]; . == $k) then . else . + [$k] end)')"

# A declaration that could not be recorded has not been declared: fail this
# occurrence rather than activate on a registry that does not hold it.
sr-session state set "scanner:${scanner}" "$keywords_json" || exit 1
sr-session state set "stamp:${scanner}" "$(date +%s)-$$-${RANDOM}${RANDOM}" || exit 1

jq -n --arg name "$scanner" --argjson kw "$keywords_json" \
  '{scanner: $name, active: true, keywords: $kw}'
