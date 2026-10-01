#!/usr/bin/env bash
# A file carrying an sr:moved-from marker must be byte-identical to its origin at
# the pinned commit, minus imports and whitespace. The marker's fqn carries
# <path>@<sha>:<start>-<end>.
#
# This is the PRE-WRITE copy (the gate): it reads the pending bytes off a
# PreFileCreate/PreFileUpdate. The file-guard of the same name keeps the Stop-time
# copy, which reads the settled bytes.
lib_dir="$(cd "$(dirname "$0")/../../file-guard/moved-content-reconciles" && pwd)"
unset reconciles_against_origin_lib_loaded
. "$lib_dir/reconciles-against-origin-lib.sh" || exit 2
[ "${reconciles_against_origin_lib_loaded:-}" = 1 ] || exit 2
lib_init
# resultKnown, not has("newContent") — newContent is ALWAYS a present key on
# PreFileCreate/PreFileUpdate, so has("newContent") is always true and an absent
# value reads as "", indistinguishable from a write that genuinely empties the
# file. The "engine could not predict the result" signal is resultKnown. A gate
# does not fail closed on it by itself, and a reconcile against bytes nobody saw
# would be no check at all, so refuse: a write whose result cannot be computed
# (sed -i, an unresolvable sr-file line) is not admitted.
known="$(printf '%s' "$input" | jq -r '.event.resultKnown // false')"
if [ "$known" != "true" ]; then
  jq -n '{reason: "This write carries an sr:moved-from marker but the bytes it would leave in the file cannot be worked out from the command, so it cannot be reconciled against its origin. Write the moved content directly (the whole file) instead of editing it in place. If you use sr-file, run it alone in its own Bash call, with nothing before or after it on the line: another command beside it (sr-file ...; cat X) is what makes the bytes unknowable, and the edit itself is allowed."}'
  exit 1
fi

new="$(printf '%s' "$input" | jq -r '.event.newContent')"
lib_check
