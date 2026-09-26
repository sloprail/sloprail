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
# Bash-run measurement targets the real file on disk), pre-computed
# deterministic facts about the draft (so the judge does not have to spend
# its own tool calls measuring what this script can measure for free — see
# "PRE-COMPUTED FACTS" below), and the size gate this repo's other
# judge-fed guards already carry.
#
# EXIT 0 with additionalContext on stdout: the judge runs. EXIT 1: a REFUSAL
# (a prepare failure fails closed), carrying this script's own reason.
#
# THE "NO RULE APPLIES" CASE, same shape as unit-satisfies-constraints's
# "topic has no constraints": a judge: check's prepare has only two outcomes
# (refuse or proceed-to-judge), so when NO rule applies this still proceeds
# with the sentinel "NONE", and the judge template passes trivially on it —
# the same behavioural note that guard's file-guard.yaml documents.
#
# TAGS COME FROM THE UNIT'S UNIT.md, ALWAYS — NOT FROM WHICHEVER FILE WAS
# WRITTEN. A unit's copy lives in 02_draft.md, which carries no frontmatter
# of its own (it is prose, not a unit document) — reading tags off the file
# being written meant a 02_draft.md write got rule set NONE every time (no
# frontmatter to parse => no tags => only global rules, which is itself a
# silent under-application even before considering NONE), while a UNIT.md
# write (just the pitch, no shipped copy yet) got the full tag-selected set
# applied to a paragraph that will never ship as written. Fixed here: this
# prepare always resolves the unit's directory from the event path and reads
# tags from that unit's OWN UNIT.md — on disk, unless the event IS UNIT.md
# itself, in which case the settled event content (which may not be on disk
# yet, e.g. a PostFileCreate) is used instead of a stale or absent on-disk
# copy.
#
# WHAT GETS JUDGED IS THE UNIT'S SHIPPED COPY, NOT JUST THE PITCH. A writing
# rule is about what will ship (the plugin README's own words) — that is
# 02_draft.md, not UNIT.md. So regardless of which file the write event
# named, if 02_draft.md exists on disk this prepare judges ITS content
# (read fresh off disk, or the settled event content when the event IS the
# draft write, to avoid judging a stale on-disk copy mid-write). A UNIT.md
# write with no draft yet has nothing shippable to judge against writing
# rules — see "NO DRAFT YET" below for the chosen behaviour.
#
# NO DRAFT YET: the simplest correct behaviour. A UNIT.md-only write, before
# any 02_draft.md exists, proceeds with the NONE sentinel and passes
# trivially — not because metadata is exempt from all rules by design, but
# because there is no shipped text yet for a writing rule (tone, length,
# banned phrases, structure) to be judged against; judging the pitch
# paragraph instead would be judging prose that is not what ships, exactly
# the bug this fix removes in the other direction. Once a draft exists, the
# very next write (to either file) judges it for real.
set -uo pipefail

payload="$(cat)"

refuse() {
  jq -n --arg r "$1" '{reason: $r}'
  exit 1
}

# proceed rules facts_json unit_path judged_path judged_content
# facts_json is a JSON object (already assembled by compute-draft-facts.sh,
# or "{}" when there is nothing to judge). Its fields are spread as their OWN
# top-level additionalContext keys (facts_title_line, facts_title_chars, …)
# rather than nested under one additionalContext.facts object — the engine's
# template renderer (gonja) has no `tojson` filter registered, so `{{
# additionalContext.facts }}` would render Go's own map-stringification, not
# JSON, if facts were kept nested; flat scalar keys render exactly as typed,
# the same way judge_rules/unit_path already do. segment_char_counts (an
# array) is the one exception, kept as an array so judge-rules.md.j2 can
# `{% for %}` over it the same way authoring-slop's judge.md.j2 already
# iterates additionalContext.rules.
#
# judged_content is handed to the template explicitly (rather than the
# template reading event.newContent itself) because the content actually
# being judged is not always the event's own bytes — see the file header: it
# may be 02_draft.md's on-disk content when the write that triggered this
# check was to UNIT.md instead.
proceed() {
  rules="$1"
  facts="$2"
  up="$3"
  jp="$4"
  jc="$5"
  jq -n \
    --arg r "$rules" \
    --arg p "$up" \
    --arg jp "$jp" \
    --arg jc "$jc" \
    --argjson facts "$facts" '
    {additionalContext: ({
      judge_rules: $r,
      unit_path: $p,
      judged_path: $jp,
      judged_content: $jc,
      facts_title_line: ($facts.title_line // ""),
      facts_title_chars: ($facts.title_chars // 0),
      facts_body_word_count: ($facts.body_word_count // 0),
      facts_em_dash_count: ($facts.em_dash_count // 0),
      facts_emoji_count: ($facts.emoji_count // 0),
      facts_segment_char_counts: ($facts.segment_char_counts // [])
    })}'
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
    # A Post kind carries the SETTLED bytes directly on the flat event.
    written_content="$(printf '%s' "$payload" | jq -r '.event.newContent // ""' 2>/dev/null)"
    ;;
  PreFileCreate|PreFileUpdate)
    known="$(printf '%s' "$payload" | jq -r '.event.resultKnown // false' 2>/dev/null)"
    if [ "$known" != "true" ]; then
      written_content=""
    else
      written_content="$(printf '%s' "$payload" | jq -r '.event.newContent // ""' 2>/dev/null)"
    fi
    ;;
  *)
    written_content=""
    ;;
esac

# unit_dir: the memories/topics/<topic>/units/<unit>/ this write is under —
# both UNIT.md and 02_draft.md live directly inside it. The match regex in
# file-guard.yaml already anchors on this shape, so a non-empty path here
# always has one.
unit_dir="$(printf '%s' "$path" | sed -n 's|^\(memories/topics/[^/]*/units/[^/]*/\).*|\1|p')"
if [ -z "$unit_dir" ]; then
  refuse "unit-satisfies-rules: $path did not resolve to a memories/topics/<topic>/units/<unit>/ directory, so the unit's UNIT.md could not be located for tags"
fi

unit_md_path="${unit_dir}UNIT.md"
draft_path="${unit_dir}02_draft.md"
is_unit_md_write=0
[ "$path" = "$unit_md_path" ] && is_unit_md_write=1

# TAGS: read from UNIT.md, always — the settled event content when the event
# IS UNIT.md itself (its on-disk copy may be stale, absent, or mid-write),
# otherwise from UNIT.md on disk.
if [ "$is_unit_md_write" -eq 1 ]; then
  unit_md_content="$written_content"
else
  if [ -f "$root/$unit_md_path" ]; then
    unit_md_content="$(cat "$root/$unit_md_path" 2>/dev/null)"
  else
    unit_md_content=""
  fi
fi

if [ -z "$unit_md_content" ]; then
  refuse "unit-satisfies-rules: $unit_md_path could not be read, so this unit's tags could not be resolved. A unit's writing rules are selected by its own UNIT.md tags — create or fix UNIT.md before its draft is judged."
fi

# JUDGED CONTENT: the unit's shipped copy, 02_draft.md, when one exists —
# read fresh off disk, or the settled event content when THIS write is the
# draft write (so a stale on-disk read never shadows the bytes just
# written). A UNIT.md-only write with no draft yet has nothing to judge
# (see the file header's "NO DRAFT YET").
draft_exists=0
if [ "$path" = "$draft_path" ]; then
  draft_exists=1
  judged_content="$written_content"
  judged_path="$draft_path"
elif [ -f "$root/$draft_path" ]; then
  draft_exists=1
  judged_content="$(cat "$root/$draft_path" 2>/dev/null)"
  judged_path="$draft_path"
else
  judged_content=""
  judged_path="$unit_md_path"
fi

if [ "$draft_exists" -eq 0 ]; then
  # No shipped copy yet — nothing for a writing rule to be judged against.
  proceed "NONE" "{}" "$root/$unit_md_path" "$root/$unit_md_path" "$unit_md_content"
fi

rule_files="$(collect_applicable_rules "$unit_md_path" "$root" "$unit_md_content")"
rule_schema="$root/.sloprail/schemas/rule.cue"

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
  proceed "NONE" "{}" "$root/$unit_md_path" "$root/$judged_path" "$judged_content"
fi

# THE SIZE GATE — same threshold and reasoning as unit-satisfies-constraints's
# prepare.sh: the whole unit goes into the judge's prompt, and past roughly
# 600000 bytes the request cannot be assembled or exceeds the model's context.
max_bytes=600000
body_bytes="$(printf '%s' "$judged_content" | wc -c | tr -d ' ')"
if [ -n "$body_bytes" ] && [ "$body_bytes" -gt "$max_bytes" ] 2>/dev/null; then
  refuse "WRITING RULE CHECK: $judged_path is ${body_bytes} bytes, too large for this rule to judge (the whole unit goes into the judge's prompt, and past roughly ${max_bytes} bytes the prompt cannot be assembled or exceeds the model's context, and no verdict comes back).

This is refused rather than permitted because a unit this size cannot be checked against its writing rules at all. Split it into the units it is actually made of, and each will be judged normally."
fi

# PRE-COMPUTED FACTS. A real run showed the judge burning most of its turns
# on measurements this script can do once, deterministically, cheaper than
# a model's own one-tool-call-per-turn Bash loop: title-line character
# count, body word count (frontmatter and title excluded), em-dash count,
# emoji count, and — for a thread split on literal `---` lines — each
# segment's own character count. judge-rules.md.j2 tells the judge to use
# these numbers and reach for its own Bash measurement only when a rule
# needs something these facts do not cover.
facts_json="$("$gdir/compute-draft-facts.sh" "$judged_content")" || {
  refuse "unit-satisfies-rules: compute-draft-facts.sh failed, so the judge could not be given pre-computed measurements"
}

proceed "$judge_rules" "$facts_json" "$root/$unit_md_path" "$root/$judged_path" "$judged_content"
