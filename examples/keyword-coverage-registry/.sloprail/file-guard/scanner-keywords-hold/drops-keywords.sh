#!/usr/bin/env bash
# `when` for the user citation a scanner's keywords need: does this write DROP a
# keyword the scanner already declared? Exit 0 — it does, so the write must cite
# the user's words asking for it. Exit 1 — it does not (a new scanner, or one that
# only adds keywords), so no citation is required. Deleting the scanner drops
# every keyword it declared, so a delete of one that declared any applies too.
# A create can drop keywords too: the settled file of a scanner declared this
# session reaches Stop as a PostFileCreate, and it drops whatever the registry
# owes that the file no longer declares.
#
# THIS IS A `when` PREDICATE, NOT A CHECK: exit 0 does not permit anything — it
# APPLIES the requirement. So every path this script cannot decide exits 0, the
# fail-closed direction; only exit 1 waives the citation, and only on a decided
# "drops nothing".
#
# The failure it exists for, measured on a real Haiku run: refused by the
# coverage gate for a search that missed keywords, the agent rewrote the
# scanner's keywords to fit the search it had already run — the declaration
# weakened to match the work, instead of the work meeting the declaration.
set -uo pipefail

# Undecidable without jq: apply the requirement (exit 0, fail-closed).
command -v jq >/dev/null 2>&1 || exit 0

# The keywords are read by the SAME parser scanner-declared logs them with
# (scanner-lib.sh), found from this rule's own folder. Undecidable without it:
# apply the requirement.
[ -n "${SR_GUARDRAIL_DIR:-}" ] || exit 0
lib="$SR_GUARDRAIL_DIR/../../context/scanner-declared/scanner-lib.sh"
[ -f "$lib" ] || exit 0
# A helper stopped early runs only partly (whether the `.` then fails depends
# on the bash version); only its last-line sentinel proves it loaded whole.
# Not loaded whole is undecidable: apply (exit 0), never waive.
unset scanner_lib_loaded
# shellcheck source=../../context/scanner-declared/scanner-lib.sh
. "$lib" 2>/dev/null || exit 0
[ "${scanner_lib_loaded:-}" = 1 ] || exit 0

payload="$(cat)"
field() { printf '%s' "$payload" | jq -r "$1" 2>/dev/null; }

# keywords_of CONTENT — the declared keywords, one per line, as a set.
keywords_of() {
  scanner_keywords "$1" | sort -u
}

# What a change drops is measured against everything the scanner is known to
# declare: the file before the change AND what the registry holds owed for it.
# The file alone is not enough — a write the engine cannot parse (`python3 -c
# "open(…).write('active: true\n')"`) empties the file behind every rule's back,
# after which a delete, or a PostFileCreate of a scanner declared this session,
# compared only against the file dropped nothing, needed no citation, and the
# scanner was retired or narrowed with the user never asked.
path="$(field '.event.path // ""')"
[ -n "$path" ] || exit 0
owed_json="$(registry_keywords "$(scanner_dir "$path")" 2>/dev/null)" || exit 0
owed="$(printf '%s' "$owed_json" | jq -r '.[]?' 2>/dev/null)" || exit 0

kind="$(field '.event.kind // ""')"
case "$kind" in
  PreFileCreate)
    # A result the engine could not compute is undecidable: apply (exit 0).
    [ "$(field '.event.resultKnown // false')" = "true" ] || exit 0
    old=""
    new="$(field '.event.newContent // ""')"
    ;;
  PostFileCreate | PostFileUpdate)
    # Settled bytes the engine could not read (newContentKnown false):
    # undecidable, apply.
    [ "$(field 'if (.event | has("newContentKnown")) then .event.newContentKnown else true end')" = "true" ] || exit 0
    old="$(field '.event.oldContent // ""')"
    new="$(field '.event.newContent // ""')"
    ;;
  PreFileUpdate)
    [ "$(field '.event.resultKnown // false')" = "true" ] || exit 0
    old="$(field '.event.oldContent // ""')"
    new="$(field '.event.newContent // ""')"
    ;;
  PreFileDelete | PostFileDelete)
    # Deleting a scanner drops EVERY keyword it declared — measured on a real
    # run: refused by the coverage gate, a sub-agent ran `rm -rf scanners/<name>`
    # instead of searching. Nothing remains, so the new side is empty.
    old="$(field '.event.oldContent // ""')"
    # Bytes the engine did not read (a file past a removal's byte budget, or not
    # a regular file) and nothing owed to fall back on: undecidable, apply.
    if [ "$(field 'if .event | has("oldContentKnown") then .event.oldContentKnown else true end')" != "true" ] && [ -z "$owed" ]; then
      exit 0
    fi
    dropped="$( { keywords_of "$old"; printf '%s\n' "$owed"; } | sed '/^$/d' | sort -u | paste -sd ',' -)"
    # A scanner that declares no keyword, and owes none, drops none.
    [ -n "$dropped" ] || exit 1
    jq -n --arg dropped "$dropped" '{hint: (
      "Deleting this scanner drops every keyword it declared (" + $dropped + "). A scanner declared this session stays owed a search covering all its keywords even once its file is gone (verify-scanner-coverage reads what was logged, not the file), so cover them in one gh search instead. " +
      "Delete a scanner only if the user asked for it, citing their words.")}'
    exit 0
    ;;
  *)
    # A kind this script does not know: undecidable, so apply (fail-closed).
    exit 0
    ;;
esac

dropped="$(comm -23 <( { keywords_of "$old"; printf '%s\n' "$owed"; } | sed '/^$/d' | sort -u) <(keywords_of "$new") | paste -sd ',' -)"
[ -n "$dropped" ] || exit 1

# It applies. The hint the refusal carries: meet the declaration, don't weaken it.
jq -n --arg dropped "$dropped" '{hint: (
  "This change drops the declared keyword(s) " + $dropped + ". A scanner'\''s keywords are what the search must cover, so cover them all in one gh search rather than weakening the scanner to fit a search already run — that one search counts even if GitHub returns nothing for it; narrower searches besides it can find the results. " +
  "Drop a keyword only if the user asked for it, citing their words.")}'
exit 0
