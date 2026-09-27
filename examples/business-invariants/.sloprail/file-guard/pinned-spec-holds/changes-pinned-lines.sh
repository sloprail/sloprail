#!/usr/bin/env bash
# `when` for the user citation a pinned spec line needs: does this write change a
# line some sr:invariant marker in the project pins? Exit 0 — it does, so the write
# must cite the user's words asking for the rule to change. Exit 1 — it does not
# (no marker pins this file, or the change leaves every pinned line as it was).
#
# THIS IS A `when` PREDICATE, NOT A CHECK: exit 0 does not permit anything — it
# APPLIES the requirement. So every path this script cannot decide exits 0, the
# fail-closed direction; only exit 1 waives the citation, and only on a decided
# "touches no pinned line".
#
# The failure it exists for, measured on two real Haiku runs: asked for a feature
# that breaks a pinned invariant, the agent rewrote the spec's rule to allow it and
# re-pinned its code to the new wording, so the code-upholds-invariant judge found
# code and pin agreeing — the rule weakened to match the work.
set -uo pipefail

command -v jq >/dev/null 2>&1 || exit 0
command -v git >/dev/null 2>&1 || exit 0

payload="$(cat)"
field() { printf '%s' "$payload" | jq -r "$1" 2>/dev/null; }

path="$(field '.event.path // ""')"
[ -n "$path" ] || exit 0
workspace="${SR_WORKSPACE:-.}"

kind="$(field '.event.kind // ""')"
case "$kind" in
  PreFileCreate | PostFileCreate)
    # A new file: nothing in it was pinned before this write.
    exit 1
    ;;
  PreFileUpdate)
    [ "$(field '.event.resultKnown // false')" = "true" ] || exit 0
    ;;
  PreFileDelete | PostFileDelete | PostFileUpdate)
    ;;
  *)
    exit 0
    ;;
esac

# Every marker in the project, tracked or not. A marker's fqn is
# <repo>@<sha>:<path>#L<start>-<end>; only its path and range matter here.
markers="$(git -C "$workspace" grep --untracked -h -o 'sr:invariant "[^"]*"' 2>/dev/null)"
[ -n "$markers" ] || exit 1

old="$(field '.event.oldContent // ""')"
new="$(field '.event.newContent // ""')"
case "$kind" in
  PreFileDelete | PostFileDelete) new="" ;;
esac

changed=""
while IFS= read -r m; do
  fqn="${m#sr:invariant \"}"
  fqn="${fqn%\"}"
  rest="${fqn#*@}"
  rest="${rest#*:}"
  pinned_path="${rest%%#*}"
  range="${rest#*#L}"
  start="${range%-*}"
  end="${range#*-}"
  [ "$pinned_path" = "$path" ] || continue
  case "$start$end" in
    '' | *[!0-9]*) exit 0 ;;
  esac
  before="$(printf '%s\n' "$old" | sed -n "${start},${end}p")"
  after="$(printf '%s\n' "$new" | sed -n "${start},${end}p")"
  if [ "$before" != "$after" ]; then
    changed="${changed:+$changed, }L${start}-${end}"
  fi
done <<EOF
$markers
EOF

[ -n "$changed" ] || exit 1

# It applies. The hint the refusal carries: a pinned rule is the user's decision.
jq -n --arg path "$path" --arg lines "$changed" '{hint: (
  "This change rewrites " + $path + " " + $lines + ", which code in this project pins as a business rule. " +
  "Change a pinned rule only when the user asked for the rule itself to change, citing their words. " +
  "If what you were asked for conflicts with the rule, keep the rule and tell the user about the conflict instead.")}'
exit 0
