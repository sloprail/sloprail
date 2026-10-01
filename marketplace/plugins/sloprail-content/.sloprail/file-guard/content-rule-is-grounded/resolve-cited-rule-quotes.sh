#!/usr/bin/env bash
# prepare for stage 2 of content-rule-is-grounded: hand the judge the rule's
# bodies as they will stand, for context. What the judge rules on — the change —
# and the cited words need no preparing: judge-rule-body.md.j2 reads `change` and
# `changeset.citations` (the quotes the range's commits cite) straight off its input.
#
# THE PREPARE CONTRACT: exit 0 with additionalContext proceeds to the judge;
# non-zero fails the check closed. Every read of the payload goes through
# field(), which fails the prepare (and so the check) on an unreadable payload
# rather than handing the judge an empty body as if the rule had none.
set -uo pipefail

fail() {
  echo "content-rule-is-grounded: $1" >&2
  exit 1
}

event="$(cat)"
printf '%s' "$event" | jq -e '.event.kind == "Changeset"' >/dev/null 2>&1 \
  || fail "the check payload is not a readable Changeset, so the cited words could not be assembled"

# body_of CONTENT — the prose after the frontmatter: check-rule-lib.sh's, the one
# extraction every guard in this plugin uses. Sourcing it only defines functions.
lib_dir="$(cd "$(dirname "$0")" && pwd)"
unset check_rule_lib_loaded
. "$lib_dir/check-rule-lib.sh" || fail "check-rule-lib.sh could not be loaded, so the rule bodies could not be read"
[ "${check_rule_lib_loaded:-}" = 1 ] || fail "check-rule-lib.sh did not load whole, so the rule bodies could not be read"

n="$(printf '%s' "$event" | jq -r '.changeset.files | length')" \
  || fail "could not read the changeset's files"

bodies='[]'
i=0
while [ "$i" -lt "$n" ]; do
  path="$(printf '%s' "$event" | jq -r --argjson i "$i" '.changeset.files[$i].path')" \
    || fail "could not read file $i of the changeset"
  content="$(printf '%s' "$event" | jq -r --argjson i "$i" '.changeset.files[$i].newContent')" \
    || fail "could not read $path from the changeset"
  i=$((i + 1))
  bodies="$(printf '%s' "$bodies" | jq -c --arg path "$path" --arg body "$(body_of "$content")" '. + [{path: $path, body: $body}]')" \
    || fail "could not assemble the body of $path"
done

jq -n --argjson bodies "$bodies" '{additionalContext: {bodies: $bodies}}'
