#!/usr/bin/env bash
# Shared by the preserves-unasked-content gate and its file-guard: one library, two thin entries.
# The gate entry reads the pending write (lib_init, then a Pre kind, then lib_check);
# the file-guard entry reads the Changeset, calls lib_count once per file with `old`
# and `new` set, and lib_apply with the total. Neither reads an event here.
#
# lib_check returns 1 when the change only adds (the citation is waived), and exits 0
# with a hint when it removes something.

lib_setup() {
set -uo pipefail

# Undecidable without jq: apply the requirement (exit 0, fail-closed).
# DELIBERATE, and fail-closed: in a `when` predicate exit 0 APPLIES the requirement (only exit 1 waives it), so a missing tool or helper applies it rather than permitting.
command -v jq >/dev/null 2>&1 || exit 0
}

# lib_init is the gate's: the pending write's own bytes.
lib_init() {
lib_setup

input="$(cat)"
kind="$(printf '%s' "$input" | jq -r '.event.kind // empty')"
[ -n "$kind" ] || { echo "preserves-unasked-content: could not read the event's kind, so it could not be checked" >&2; exit 2; }
}

# lib_count sets `removed` to the number of lines present in old but absent in new.
# (Order/whitespace refinements are elided in this sample.)
lib_count() {
removed="$(comm -23 <(printf '%s' "$old" | sort -u) <(printf '%s' "$new" | sort -u) | grep -c . || true)"
}

# lib_apply N: the requirement applies. The hint the refusal carries: append instead,
# or cite the ask.
lib_apply() {
jq -n --arg n "$1" '{hint: (
  "This change removes " + $n + " line(s). If nothing should go, append instead of rewriting; if the user asked for the removal, cite their words asking for it (in a commit, a Sloprail-Cites-User: trailer).")}'
exit 0
}

# The entry sets $old and $new (after deciding they can be trusted).
lib_check() {
lib_count
[ "${removed:-0}" -eq 0 ] && return 1
lib_apply "$removed"
}

removes_content_lib_loaded=1
