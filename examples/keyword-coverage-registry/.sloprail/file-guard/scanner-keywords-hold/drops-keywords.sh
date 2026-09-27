#!/usr/bin/env bash
# `when` for the user citation a scanner's keywords need: does this write DROP a
# keyword the scanner already declared? Exit 0 — it does, so the write must cite
# the user's words asking for it. Exit 1 — it does not (a new scanner, or one that
# only adds keywords), so no citation is required.
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

payload="$(cat)"
field() { printf '%s' "$payload" | jq -r "$1" 2>/dev/null; }

# keywords_of CONTENT — the entries under `keywords:`, one per line, trimmed.
keywords_of() {
  printf '%s\n' "$1" | awk '
    /^keywords:/ { inlist = 1; next }
    inlist && /^[[:space:]]*-[[:space:]]*/ { sub(/^[[:space:]]*-[[:space:]]*/, ""); print; next }
    inlist && /^[^[:space:]]/ { inlist = 0 }
  ' | sed 's/[[:space:]]*$//' | sort -u
}

kind="$(field '.event.kind // ""')"
case "$kind" in
  PreFileCreate | PostFileCreate)
    # A new scanner has nothing declared before it to drop.
    exit 1
    ;;
  PreFileUpdate)
    # A result the engine could not compute is undecidable: apply (exit 0).
    [ "$(field '.event.resultKnown // false')" = "true" ] || exit 0
    ;;
  PostFileUpdate)
    ;;
  *)
    # A deletion is not this guard's business (deletions default to skip).
    exit 1
    ;;
esac

dropped="$(comm -23 <(keywords_of "$(field '.event.oldContent // ""')") <(keywords_of "$(field '.event.newContent // ""')") | paste -sd ',' -)"
[ -n "$dropped" ] || exit 1

# It applies. The hint the refusal carries: meet the declaration, don't weaken it.
jq -n --arg dropped "$dropped" '{hint: (
  "This change drops the declared keyword(s) " + $dropped + ". A scanner'\''s keywords are what the search must cover, so cover them all in one gh search rather than weakening the scanner to fit a search already run. " +
  "Drop a keyword only if the user asked for it, citing their words.")}'
exit 0
