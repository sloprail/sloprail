#!/usr/bin/env bash
# Shared by the scanner-keywords-hold gate and its file-guard: one library, two thin entries.
# The gate entry reads the pending write (lib_init, then a Pre kind); the file-guard
# entry reads the Changeset and, per scanner, sets `path`, `old` and `new`, calls
# lib_owed, and then lib_check or lib_check_delete. Neither reads an event.
#
# lib_check and lib_check_delete return 1 when the change drops nothing (the citation
# is waived), and exit 0 with a hint when it drops a keyword.

lib_setup() {
set -uo pipefail

# Only lib_waive's DECIDED "drops nothing" may exit 1: any other exit 1 (a tool
# that failed, an incidental status) would waive the citation, so it becomes 0.
waived=""
trap 'rc=$?; if [ "$rc" = 1 ] && [ "$waived" != 1 ]; then exit 0; fi' EXIT

# Undecidable without jq: apply the requirement (exit 0, fail-closed).
# DELIBERATE, and fail-closed: in a `when` predicate exit 0 APPLIES the requirement (only exit 1 waives it), so a missing tool or helper applies it rather than permitting.
command -v jq >/dev/null 2>&1 || exit 0

# The keywords are read by the SAME parser scanner-declared logs them with
# (scanner-lib.sh), found from this rule's own folder. Undecidable without it:
# apply the requirement.
[ -n "${SR_GUARDRAIL_DIR:-}" ] || exit 0
lib="$SR_GUARDRAIL_DIR/../../context/scanner-declared/scanner-lib.sh"
# DELIBERATE, and fail-closed: in a `when` predicate exit 0 APPLIES the requirement (only exit 1 waives it), so a missing tool or helper applies it rather than permitting.
[ -f "$lib" ] || exit 0
# A helper stopped early runs only partly (whether the `.` then fails depends
# on the bash version); only its last-line sentinel proves it loaded whole.
# Not loaded whole is undecidable: apply (exit 0), never waive.
unset scanner_lib_loaded
# shellcheck source=../../context/scanner-declared/scanner-lib.sh
. "$lib" 2>/dev/null || exit 0
[ "${scanner_lib_loaded:-}" = 1 ] || exit 0

# keywords_of CONTENT — the declared keywords, one per line, as a set.
keywords_of() {
  scanner_keywords "$1" | sort -u
}
}

# lib_owed sets `owed` from the registry, for the scanner at `path`. What a change
# drops is measured against everything the scanner is known to declare: the file
# before the change AND what the registry holds owed for it. The file alone is not
# enough — a write the engine cannot parse (`python3 -c
# "open(…).write('active: true\n')"`) empties the file behind every rule's back,
# after which a delete, or a create of a scanner declared this session, compared
# only against the file dropped nothing, needed no citation, and the scanner was
# retired or narrowed with the user never asked.
lib_owed() {
[ -n "$path" ] || exit 0
owed_json="$(registry_keywords "$(scanner_dir "$path")" 2>/dev/null)" || exit 0
owed="$(printf '%s' "$owed_json" | jq -r '.[]?' 2>/dev/null)" || exit 0
}

# lib_init is the gate's: the pending write's own bytes.
lib_init() {
lib_setup

payload="$(cat)"
field() { printf '%s' "$payload" | jq -r "$1" 2>/dev/null; }

path="$(field '.event.path // ""')"
lib_owed

kind="$(field '.event.kind // ""')"
}

# lib_waive: the change was decided to drop nothing: the only exit 1.
lib_waive() {
  waived=1
  exit 1
}

lib_check() {

dropped="$(comm -23 <( { keywords_of "$old"; printf '%s\n' "$owed"; } | sed '/^$/d' | sort -u) <(keywords_of "$new") | paste -sd ',' -)"
[ -n "$dropped" ] || return 1

# It applies. The hint the refusal carries: meet the declaration, don't weaken it.
jq -n --arg dropped "$dropped" '{hint: (
  "This change drops the declared keyword(s) " + $dropped + ". A scanner'\''s keywords are what the search must cover, so cover them all in one gh search rather than weakening the scanner to fit a search already run — that one search counts even if GitHub returns nothing for it; narrower searches besides it can find the results. " +
  "Drop a keyword only if the user asked for it, citing their words (in a commit, a Sloprail-Cites-User: trailer).")}'
exit 0
}

# lib_check_delete: deleting a scanner drops EVERY keyword it declared — measured on a
# real run: refused by the coverage gate, a sub-agent ran `rm -rf scanners/<name>`
# instead of searching. Nothing remains, so the new side is empty.
lib_check_delete() {
dropped="$( { keywords_of "$old"; printf '%s\n' "$owed"; } | sed '/^$/d' | sort -u | paste -sd ',' -)"
# A scanner that declares no keyword, and owes none, drops none.
[ -n "$dropped" ] || return 1
jq -n --arg dropped "$dropped" '{hint: (
  "Deleting this scanner drops every keyword it declared (" + $dropped + "). A scanner declared this session stays owed a search covering all its keywords even once its file is gone (verify-scanner-coverage reads what was logged, not the file), so cover them in one gh search instead. " +
  "Delete a scanner only if the user asked for it, citing their words (in a commit, a Sloprail-Cites-User: trailer).")}'
exit 0
}

drops_keywords_lib_loaded=1
