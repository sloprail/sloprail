#!/usr/bin/env bash
# prepare for unit-satisfies-rules's single check: collect every rule that
# applies to this unit (rules-lib.sh, by tags) and hand them to the judge
# template as the rubric — exactly the shape unit-satisfies-constraints's
# prepare.sh hands its constraints, generalized from "this topic's
# constraints" to "every rule (project-wide + this unit's topic) this unit's
# tags select".
#
# EVERY rule is a judge rule now — there is no separate deterministic script
# stage (see file-guard.yaml's header for why: a rule that needs a
# measurement, like a character limit, is a PROMPT telling the judge to run
# `wc -c`/similar itself via the Bash tool granted through allowed_tools,
# not a shipped script). This prepare's job is to assemble every applicable
# rule's text as ground truth for the model, the unit's own file path (so a
# Bash-run measurement targets the real file on disk), and the size gate
# this repo's other judge-fed guards already carry.
#
# EXIT 0 with additionalContext on stdout: the judge runs. EXIT 1: a REFUSAL
# (a prepare failure fails closed), carrying this script's own reason.
#
# THE "NO RULE APPLIES" CASE, same shape as unit-satisfies-constraints's
# "topic has no constraints": a judge: check's prepare has only two outcomes
# (refuse or proceed-to-judge), so when NO rule applies this still proceeds
# with the sentinel "NONE", and the judge template passes trivially on it —
# the same behavioural note that guard's file-guard.yaml documents.
set -uo pipefail

payload="$(cat)"

refuse() {
  jq -n --arg r "$1" '{reason: $r}'
  exit 1
}

proceed() {
  jq -n --arg r "$1" --arg p "$2" --arg t "${unit_text:-}" \
    '{additionalContext: {judge_rules: $r, unit_path: $p, unit_text: $t}}'
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
# A helper stopped by a syntax error runs only up to it (whether the `.` then
# fails depends on the bash version): collect_applicable_rules can be defined
# with rule_applies missing, and then no rule applies. Only the last-line
# sentinel proves it loaded whole.
unset rules_lib_loaded
# shellcheck source=rules-lib.sh
. "$lib"
[ "${rules_lib_loaded:-}" = 1 ] \
  || refuse "unit-satisfies-rules: rules-lib.sh did not load whole (its last-line sentinel rules_lib_loaded is unset), so the rule set could not be collected"

kind="$(printf '%s' "$payload" | jq -r '.event.kind // ""' 2>/dev/null)"
case "$kind" in
  PostFileCreate|PostFileUpdate)
    # A Post kind carries the SETTLED bytes directly on the flat event.
    content="$(printf '%s' "$payload" | jq -r '.event.newContent // ""' 2>/dev/null)"
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

# WHAT IS JUDGED. A UNIT.md holds the unit's frontmatter, not its text, so on a
# UNIT.md write (publishing, a tag change) the judge is also handed the unit's
# draft, and a measurement runs against the draft: frontmatter alone would pass
# every writing rule vacuously, and a rule added since the draft was last
# written would never meet it.
unit_text="$content"
unit_file="$root/$path"
draft="$root/$(dirname "$path")/02_draft.md"
if [ "$(basename "$path")" = "UNIT.md" ] && [ -f "$draft" ]; then
  if ! draft_text="$(cat "$draft")"; then
    refuse "unit-satisfies-rules: could not read $(dirname "$path")/02_draft.md, the text this unit's rules judge"
  fi
  unit_text="${content}

--- 02_draft.md ---
${draft_text}"
  unit_file="$draft"
fi

rule_files="$(collect_applicable_rules "$path" "$root" "$content")"
rule_schema="${SR_GUARDRAIL_DIR:-.}/../../schemas/rule.cue"

judge_rules=""
count=0
while IFS= read -r rf; do
  [ -n "$rf" ] || continue
  # collect_applicable_rules already validated this file once to decide it
  # applies; a second failure here (a race, or a future bug upstream) is
  # surfaced rather than silently dropped, not silently skipped.
  if ! rdoc="$(sr-file validate "$rf" --schema "$rule_schema" --emit 2>&1)"; then
    judge_rules="${judge_rules}### $(basename "$(dirname "$rf")") [UNREADABLE]
This rule's frontmatter failed schema validation on re-read and could not be included: ${rdoc}

"
    count=$((count + 1))
    continue
  fi

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
  proceed "NONE" "$unit_file"
fi

# THE SIZE GATE — same threshold and reasoning as unit-satisfies-constraints's
# prepare.sh: the whole unit goes into the judge's prompt, and past roughly
# 600000 bytes the request cannot be assembled or exceeds the model's context.
max_bytes=600000
body_bytes="$(printf '%s' "$unit_text" | wc -c | tr -d ' ')"
if [ -n "$body_bytes" ] && [ "$body_bytes" -gt "$max_bytes" ] 2>/dev/null; then
  refuse "WRITING RULE CHECK: $path is ${body_bytes} bytes, too large for this rule to judge (the whole unit goes into the judge's prompt, and past roughly ${max_bytes} bytes the prompt cannot be assembled or exceeds the model's context, and no verdict comes back).

This is refused rather than permitted because a unit this size cannot be checked against its writing rules at all. Split it into the units it is actually made of, and each will be judged normally."
fi

proceed "$judge_rules" "$unit_file"
