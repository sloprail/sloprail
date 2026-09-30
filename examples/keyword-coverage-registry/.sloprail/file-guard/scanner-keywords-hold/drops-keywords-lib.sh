#!/usr/bin/env bash
# Shared by the scanner-keywords-hold gate and its file-guard: one library, two thin entries.
# Each entry reads the event kind and its own bytes; nothing that follows branches
# on it.

lib_init() {
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
}

lib_check() {

dropped="$(comm -23 <( { keywords_of "$old"; printf '%s\n' "$owed"; } | sed '/^$/d' | sort -u) <(keywords_of "$new") | paste -sd ',' -)"
[ -n "$dropped" ] || exit 1

# It applies. The hint the refusal carries: meet the declaration, don't weaken it.
jq -n --arg dropped "$dropped" '{hint: (
  "This change drops the declared keyword(s) " + $dropped + ". A scanner'\''s keywords are what the search must cover, so cover them all in one gh search rather than weakening the scanner to fit a search already run — that one search counts even if GitHub returns nothing for it; narrower searches besides it can find the results. " +
  "Drop a keyword only if the user asked for it, citing their words.")}'
exit 0
}

drops_keywords_lib_loaded=1
