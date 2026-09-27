# The rule-collection library, sourced by unit-satisfies-rules's judge
# prepare (the only check left — see file-guard.yaml's header for why the
# script-rule stage was removed) so a rule's applicability is answered in one
# place.
#
# A RULE LIVES IN ONE OF TWO PLACES (see the plugin README's "where rules live"):
#
#   1. PROJECT-WIDE   $SR_WORKSPACE/.sloprail/content-rules/<NN_name>/RULE.md
#      Selected by applies_to: (a list of tags) against the unit's own tags.
#      No applies_to at all = global, every unit.
#   2. TOPIC-SCOPED    memories/topics/<topic>/constraints/<NN_name>/CONSTRAINT.md
#      The pre-existing shape (unit-satisfies-constraints, migrated in place —
#      see the README). Scope is implicit: only a unit UNDER that topic sees it.
#      If it ALSO carries an applies_to, that is honoured as an additional
#      filter within the topic (rule.cue allows it; most topic rules omit it).
#
# Every rule (either location) validates against .sloprail/schemas/rule.cue.
#
# THIS LIBRARY READS NO EVENT OF ITS OWN — it walks the rule tree and the
# filesystem only. The unit's bytes (whichever kind's content is safe to read,
# a decision each CALLER makes for itself by checking resultKnown on a Pre kind
# before ever handing bytes here) are the third argument, so the library
# never reads .event itself. Never call this with content derived from a Pre
# kind you have not first confirmed resultKnown for.
#
# collect_applicable_rules "<unit-path>" "<root>" "<unit-bytes>"  ->  one rule
# FILE PATH per line, for every rule (project-wide or topic-scoped) whose
# applies_to matches the unit's tags. Reads the unit's OWN frontmatter via
# `sr-file validate --emit` against unit.cue to get its tags.
collect_applicable_rules() {
  unit_path="$1"
  root="$2"
  unit_bytes="$3"

  # The schemas are the PLUGIN's, read from its own tree — this guard's folder
  # is two levels under the plugin's .sloprail/ — never a consumer-side copy.
  unit_schema="${SR_GUARDRAIL_DIR:-.}/../../schemas/unit.cue"
  rule_schema="${SR_GUARDRAIL_DIR:-.}/../../schemas/rule.cue"

  unit_doc="$(printf '%s' "$unit_bytes" | sr-file validate - --as .md --schema "$unit_schema" --emit 2>/dev/null)" || return 1
  u_tags="$(printf '%s' "$unit_doc" | jq -c '.tags // []' 2>/dev/null)"

  # PROJECT-WIDE rules: every .sloprail/content-rules/*/RULE.md, filtered by
  # applies_to against the unit's tags.
  rules_dir="$root/.sloprail/content-rules"
  if [ -d "$rules_dir" ]; then
    for rf in "$rules_dir"/*/RULE.md; do
      [ -f "$rf" ] || continue
      rdoc="$(sr-file validate "$rf" --schema "$rule_schema" --emit 2>/dev/null)" || continue
      if rule_applies "$rdoc" "$u_tags"; then
        printf '%s\n' "$rf"
      fi
    done
  fi

  # TOPIC-SCOPED rules: memories/topics/<topic>/constraints/*/CONSTRAINT.md,
  # for the topic THIS unit is under. Scope is the folder itself; applies_to
  # (if present) is an ADDITIONAL filter within it.
  topic="$(printf '%s' "$unit_path" | sed -n 's|^memories/topics/\([^/]*\)/units/.*|\1|p')"
  if [ -n "$topic" ]; then
    constraints_dir="$root/memories/topics/$topic/constraints"
    if [ -d "$constraints_dir" ]; then
      for cf in "$constraints_dir"/*/CONSTRAINT.md; do
        [ -f "$cf" ] || continue
        cdoc="$(sr-file validate "$cf" --schema "$rule_schema" --emit 2>/dev/null)" || continue
        if rule_applies "$cdoc" "$u_tags"; then
          printf '%s\n' "$cf"
        fi
      done
    fi
  fi
}

# rule_applies "<rule-json>" "<unit-tags-json>"  -> 0 if the rule's applies_to
# (absent = global) matches, 1 otherwise.
#
# applies_to PRESENT is a SUBSET/intersection test against the unit's own
# tags; applies_to ABSENT (global) matches everything.
rule_applies() {
  rule_json="$1"
  u_tags="$2"

  jq -en --argjson rule "$rule_json" --argjson utags "$u_tags" '
    ($rule.applies_to == null) or
    (($rule.applies_to // []) as $rt | ($utags // []) as $ut |
      ($rt - ($rt - $ut)) | length > 0)
    ' >/dev/null 2>&1
  # jq -en: no stdin needed (every fact arrives via --argjson); its EXIT CODE
  # is the verdict (the top-level expression is truthy -> 0, falsy -> 1),
  # which this function returns as-is.
}
