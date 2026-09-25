# The rule-collection library, sourced by both of unit-satisfies-rules's checks
# (the script-rule dispatcher and the judge prepare) so the two agree exactly on
# which rules are IN SCOPE for a given unit — one place understands the
# taxonomy, not two copies that can drift.
#
# A RULE LIVES IN ONE OF TWO PLACES (see the plugin README's "where rules live"):
#
#   1. PROJECT-WIDE   $SR_WORKSPACE/.sloprail/content-rules/<NN_name>/RULE.md
#      Selected by applies_to: (channels/type/tags) against the unit's own
#      frontmatter. No applies_to at all = global, every unit.
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
# before ever handing bytes here) are the third argument, exactly as
# cite-links.sh takes its subject as an argument rather than reading .event
# itself. Never call this with content derived from a Pre kind you have not
# first confirmed resultKnown for.
#
# collect_applicable_rules "<unit-path>" "<root>" "<unit-bytes>"  ->  one rule
# FILE PATH per line, for every rule (project-wide or topic-scoped) whose
# applies_to matches the unit. Reads the unit's OWN frontmatter via `sr-file
# validate --emit` against unit.cue to get its channels/type/tags — the same
# schema unit-publish-approved reads, so "what taxonomy does this unit carry" is
# answered identically everywhere.
collect_applicable_rules() {
  unit_path="$1"
  root="$2"
  unit_bytes="$3"

  unit_schema="$root/.sloprail/schemas/unit.cue"
  rule_schema="$root/.sloprail/schemas/rule.cue"

  unit_doc="$(printf '%s' "$unit_bytes" | sr-file validate - --as .md --schema "$unit_schema" --emit 2>/dev/null)" || return 1
  u_channels="$(printf '%s' "$unit_doc" | jq -c '.channels // []' 2>/dev/null)"
  u_type="$(printf '%s' "$unit_doc" | jq -r '.type // ""' 2>/dev/null)"
  u_tags="$(printf '%s' "$unit_doc" | jq -c '.tags // []' 2>/dev/null)"

  # PROJECT-WIDE rules: every .sloprail/content-rules/*/RULE.md, filtered by
  # applies_to against the unit's taxonomy.
  rules_dir="$root/.sloprail/content-rules"
  if [ -d "$rules_dir" ]; then
    for rf in "$rules_dir"/*/RULE.md; do
      [ -f "$rf" ] || continue
      rdoc="$(sr-file validate "$rf" --schema "$rule_schema" --emit 2>/dev/null)" || continue
      if rule_applies "$rdoc" "$u_channels" "$u_type" "$u_tags"; then
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
        if rule_applies "$cdoc" "$u_channels" "$u_type" "$u_tags"; then
          printf '%s\n' "$cf"
        fi
      done
    fi
  fi
}

# rule_applies "<rule-json>" "<unit-channels-json>" "<unit-type>" "<unit-tags-json>"
# -> 0 if the rule's applies_to (absent = global) matches, 1 otherwise.
#
# Each axis PRESENT in applies_to is a SUBSET/intersection test against the
# unit's own value; an axis ABSENT from applies_to imposes no constraint. No
# applies_to at all (jq's `// {}` reads it as empty) matches everything —
# EVERY axis absent, so every per-axis test below is vacuously true.
rule_applies() {
  rule_json="$1"
  u_channels="$2"
  u_type="$3"
  u_tags="$4"

  jq -en --argjson rule "$rule_json" \
        --argjson uch "$u_channels" \
        --arg utype "$u_type" \
        --argjson utags "$u_tags" \
    '
    ($rule.applies_to // {}) as $sel
    | (
        # channels: absent -> true; present -> the rule list and the unit
        # list intersect.
        ($sel.channels == null) or
        (($sel.channels // []) as $rc | ($uch // []) as $uc |
          ($rc - ($rc - $uc)) | length > 0)
      )
      and (
        # type: absent -> true; present -> the unit type is one of the listed.
        ($sel.type == null) or
        (($sel.type // []) | index($utype) != null)
      )
      and (
        # tags: absent -> true; present -> intersect.
        ($sel.tags == null) or
        (($sel.tags // []) as $rt | ($utags // []) as $ut |
          ($rt - ($rt - $ut)) | length > 0)
      )
    ' >/dev/null 2>&1
  # jq -en: no stdin needed (every fact arrives via --arg/--argjson); its EXIT
  # CODE is the verdict (the top-level expression is truthy -> 0, falsy -> 1),
  # which this function returns as-is.
}
