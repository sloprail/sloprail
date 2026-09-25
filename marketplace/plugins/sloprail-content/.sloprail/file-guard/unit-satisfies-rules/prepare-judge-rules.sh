#!/usr/bin/env bash
# prepare for stage 2 of unit-satisfies-rules: collect every JUDGE rule that
# applies to this unit (rules-lib.sh; script rules are stage 1's business and
# are excluded here) and hand them to the judge template as the rubric —
# exactly the shape unit-satisfies-constraints's prepare.sh hands its
# constraints, generalized from "this topic's constraints" to "every rule
# (project-wide + this unit's topic) this unit's taxonomy selects".
#
# Reached only once stage 1 (check-script-rules.sh) passed, so every script
# rule already holds; this prepare's ONE job is to assemble the judge rules'
# text as ground truth for the model, plus the size gate this repo's other
# judge-fed guards already carry.
#
# EXIT 0 with additionalContext on stdout: the judge runs. EXIT 1: a REFUSAL
# (a prepare failure fails closed), carrying this script's own reason.
#
# THE "NO JUDGE RULES APPLY" CASE, same shape as unit-satisfies-constraints's
# "topic has no constraints": a judge: check's prepare has only two outcomes
# (refuse or proceed-to-judge), so when NO judge rule applies this still
# proceeds with the sentinel "NONE", and the judge template passes trivially on
# it — the same behavioural note that guard's file-guard.yaml documents.
set -uo pipefail

payload="$(cat)"

refuse() {
  jq -n --arg r "$1" '{reason: $r}'
  exit 1
}

proceed() {
  jq -n --arg r "$1" '{additionalContext: {judge_rules: $r}}'
  exit 0
}

path="$(printf '%s' "$payload" | jq -r '.event.path // empty' 2>/dev/null)"
if [ -z "$path" ]; then
  refuse "unit-satisfies-rules: the event named no path, so there is nothing to judge"
fi

root="${SR_WORKSPACE:-.}"
gdir="${SR_GUARDRAIL_DIR:-.}"

lib="$gdir/rules-lib.sh"
if [ ! -f "$lib" ]; then
  refuse "unit-satisfies-rules: rules-lib.sh not found at $lib, so the rule set could not be collected"
fi
# shellcheck source=rules-lib.sh
. "$lib"

kind="$(printf '%s' "$payload" | jq -r '.event.kind // ""' 2>/dev/null)"
case "$kind" in
  PostFileCreate|PostFileUpdate)
    abs="$root/$path"
    content="$(cat "$abs" 2>/dev/null || true)"
    ;;
  PreFileCreate|PreFileUpdate)
    known="$(printf '%s' "$payload" | jq -r '.event.resultKnown // false' 2>/dev/null)"
    if [ "$known" != "true" ]; then
      content=""
    else
      content="$(printf '%s' "$payload" | jq -r '.event.newContent // ""' 2>/dev/null)"
    fi
    ;;
  *)
    content=""
    ;;
esac

rule_files="$(collect_applicable_rules "$path" "$root" "$content")"
rule_schema="$root/.sloprail/schemas/rule.cue"

judge_rules=""
count=0
while IFS= read -r rf; do
  [ -n "$rf" ] || continue
  rdoc="$(sr-file validate "$rf" --schema "$rule_schema" --emit 2>/dev/null)" || continue
  sname="$(printf '%s' "$rdoc" | jq -r '.script.name // empty' 2>/dev/null)"
  [ -z "$sname" ] || continue   # a SCRIPT rule — stage 1 already ran it.

  rule_name="$(basename "$(dirname "$rf")")"
  level="$(printf '%s' "$rdoc" | jq -r '.level // "must"' 2>/dev/null)"
  body="$(cat "$rf" 2>/dev/null)" || continue
  [ -z "$body" ] && continue
  judge_rules="${judge_rules}### ${rule_name} [${level}]
${body}

"
  count=$((count + 1))
done <<EOF
$rule_files
EOF

if [ "$count" -eq 0 ]; then
  proceed "NONE"
fi

# THE SIZE GATE — same threshold and reasoning as unit-satisfies-constraints's
# prepare.sh: the whole unit goes into the judge's prompt, and past roughly
# 600000 bytes the request cannot be assembled or exceeds the model's context.
max_bytes=600000
body_bytes="$(printf '%s' "$content" | wc -c | tr -d ' ')"
if [ -n "$body_bytes" ] && [ "$body_bytes" -gt "$max_bytes" ] 2>/dev/null; then
  refuse "WRITING RULE CHECK: $path is ${body_bytes} bytes, too large for this rule to judge (the whole unit goes into the judge's prompt, and past roughly ${max_bytes} bytes the prompt cannot be assembled or exceeds the model's context, and no verdict comes back).

This is refused rather than permitted because a unit this size cannot be checked against its writing rules at all. Split it into the units it is actually made of, and each will be judged normally."
fi

proceed "$judge_rules"
