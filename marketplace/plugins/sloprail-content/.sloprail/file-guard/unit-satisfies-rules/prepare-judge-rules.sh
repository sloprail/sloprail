#!/usr/bin/env bash
# prepare for unit-satisfies-rules's single check: for every unit the changeset
# touches, collect the rules that apply to it (rules-lib.sh, by the unit's tags)
# and hand them to the judge template as that unit's rubric.
#
# EVERY rule is a judge rule (see file-guard.yaml's header for why): a rule that
# needs a measurement, like a character limit, is a PROMPT telling the judge to
# run `wc -c`/similar itself via the Bash tool granted through allowed_tools. This
# prepare's job is to assemble each unit's applicable rule text, the unit's file
# path (so a Bash-run measurement targets the real committed file), and the size
# gate this repo's other judge-fed guards carry.
#
# A UNIT IS A FOLDER. The changeset lists files (UNIT.md and/or 02_draft.md of one
# or more units); the unit's tags always come from its UNIT.md and its text is its
# UNIT.md plus its 02_draft.md, both read from SR_TREE (the committed head), so a
# change to only the draft is judged against the tags its UNIT.md carries.
#
# EXIT 0 with additionalContext on stdout: the judge runs. EXIT 1: a REFUSAL (a
# prepare failure fails closed), carrying this script's own reason.
#
# "NO RULE APPLIES": a judge check's prepare has only two outcomes (refuse or
# proceed-to-judge), so a unit no rule selects still proceeds, with the sentinel
# "NONE" as its rules, and the template passes it trivially.
set -uo pipefail

payload="$(cat)"

refuse() {
  jq -n --arg r "$1" '{reason: $r}'
  exit 1
}

kind="$(printf '%s' "$payload" | jq -r '.event.kind // ""' 2>/dev/null)"
[ "$kind" = "Changeset" ] || refuse "unit-satisfies-rules: expected a Changeset event, got '${kind:-nothing}', so nothing could be judged"

tree="${SR_TREE:-}"
[ -n "$tree" ] || refuse "unit-satisfies-rules: SR_TREE is not set, so the committed units could not be read"

paths="$(printf '%s' "$payload" | jq -r '.changeset.files[].path')" \
  || refuse "unit-satisfies-rules: the changeset could not be read, so nothing was judged"

lib="${SR_GUARDRAIL_DIR:-.}/rules-lib.sh"
[ -f "$lib" ] || refuse "unit-satisfies-rules: rules-lib.sh not found at $lib, so the rule set could not be collected"
# A helper stopped by a syntax error runs only up to it: collect_applicable_rules
# can be defined with rule_applies missing, and then no rule applies. Only the
# last-line sentinel proves it loaded whole.
unset rules_lib_loaded
# shellcheck source=rules-lib.sh
. "$lib"
[ "${rules_lib_loaded:-}" = 1 ] \
  || refuse "unit-satisfies-rules: rules-lib.sh did not load whole (its last-line sentinel rules_lib_loaded is unset), so the rule set could not be collected"

rule_schema="${SR_GUARDRAIL_DIR:-.}/../../schemas/rule.cue"

# The distinct unit folders the changeset touches, in order.
unit_dirs="$(printf '%s\n' "$paths" | while IFS= read -r p; do [ -n "$p" ] && dirname "$p"; done | awk '!seen[$0]++')"
[ -n "$unit_dirs" ] || refuse "unit-satisfies-rules: the changeset holds no unit file, so there is nothing to judge"

units='[]'
total_bytes=0
while IFS= read -r dir; do
  [ -n "$dir" ] || continue

  unit_md="$tree/$dir/UNIT.md"
  [ -f "$unit_md" ] || refuse "unit-satisfies-rules: $dir has no committed UNIT.md, so its tags, and the rules they select, could not be read"
  unit_bytes="$(cat "$unit_md")" || refuse "unit-satisfies-rules: could not read $dir/UNIT.md"

  # WHAT IS JUDGED. A UNIT.md holds the unit's frontmatter, not its text, so the
  # judge is also handed the unit's draft, and a measurement runs against the
  # draft: frontmatter alone would pass every writing rule vacuously, and a rule
  # added since the draft was last written would never meet it.
  unit_text="$unit_bytes"
  unit_file="$unit_md"
  draft="$tree/$dir/02_draft.md"
  if [ -f "$draft" ]; then
    draft_text="$(cat "$draft")" || refuse "unit-satisfies-rules: could not read $dir/02_draft.md, the text this unit's rules judge"
    unit_text="${unit_bytes}

--- 02_draft.md ---
${draft_text}"
    unit_file="$draft"
  fi

  rule_files="$(collect_applicable_rules "$dir/UNIT.md" "$tree" "$unit_bytes")" \
    || refuse "unit-satisfies-rules: $dir/UNIT.md failed validation, so the rules that apply to it could not be selected"

  judge_rules=""
  count=0
  while IFS= read -r rf; do
    [ -n "$rf" ] || continue
    # collect_applicable_rules already validated this file once to decide it
    # applies; a second failure here is surfaced, not silently skipped.
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
  [ "$count" -gt 0 ] || judge_rules="NONE"

  bytes="$(printf '%s' "$unit_text" | wc -c | tr -d ' ')"
  total_bytes=$((total_bytes + bytes))
  units="$(printf '%s' "$units" | jq -c --arg dir "$dir" --arg rules "$judge_rules" --arg file "$unit_file" --arg text "$unit_text" \
    '. + [{unit: $dir, judge_rules: $rules, unit_path: $file, unit_text: $text}]')" \
    || refuse "unit-satisfies-rules: could not assemble the judge's input for $dir"
done <<EOF
$unit_dirs
EOF

# THE SIZE GATE: every unit goes into the judge's prompt, and past roughly 600000
# bytes the request cannot be assembled or exceeds the model's context. Refused
# rather than permitted: units this large cannot be checked against their writing
# rules at all.
max_bytes=600000
if [ "$total_bytes" -gt "$max_bytes" ]; then
  refuse "WRITING RULE CHECK: the units in this changeset total ${total_bytes} bytes, too large for this rule to judge (they all go into the judge's prompt, and past roughly ${max_bytes} bytes the prompt cannot be assembled or exceeds the model's context, and no verdict comes back).

This is refused rather than permitted because units this size cannot be checked against their writing rules at all. Split them into the units they are actually made of (or commit fewer at a time), and each will be judged normally."
fi

jq -n --argjson units "$units" '{additionalContext: {units: $units}}'
