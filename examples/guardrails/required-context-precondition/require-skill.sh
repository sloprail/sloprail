#!/bin/sh
# Refuses a write under a guarded prefix unless the skill that prefix requires
# was actually loaded earlier in this session.
#
# "Actually loaded" means a Skill tool_use is in the session's own record. Not
# that the agent said it read the skill, and not that the written content looks
# like the skill was followed — the trajectory is the only one of the three that
# cannot be narrated.
#
# Exit 0 permits. Any non-zero exit refuses, and the engine treats a hook that
# could not run at all as a refusal too, so every failure path below lands on
# the safe side without having to arrange it.

set -u

# The event arrives on stdin. Only the path is read from it: the mapping from a
# prefix to the skill it demands is this rule's own and lives in the table
# below.
event="$(cat)"
path="$(printf '%s' "$event" | jq -r '.event.fields.path // empty' 2>/dev/null)"
if [ -z "$path" ]; then
  # Nothing named a file. The matcher should have kept such an event away, so
  # arriving here means the declaration and this script disagree about what was
  # bound — not something to permit through.
  echo "required-context-precondition: the event named no path, so there is nothing to judge" >&2
  exit 1
fi

# ---------------------------------------------------------------------------
# The rule's own table: which prefix demands which skill.
#
# Edit this, and the matcher in GUARDRAIL.md, to point the rule at your folders.
# The two are kept deliberately in step: the matcher decides only WHETHER this
# hook runs, and WHICH skill a given path demands is a second question the
# matcher has no way to answer, so a path admitted by the matcher and absent
# from this table is permitted rather than guessed at.
# ---------------------------------------------------------------------------
required_skill=""
case "$path" in
  memories/topics/*)    required_skill="document-topic" ;;
  memories/decisions/*) required_skill="document-strategy" ;;
  *)
    # Admitted by the matcher, unnamed here. This rule speaks only for the
    # prefixes it names; refusing everything else would make it a rule about all
    # writes wearing this one's name.
    exit 0
    ;;
esac

# ---------------------------------------------------------------------------
# The trajectory.
#
# `sloprail session query` is what reads the session's record. Parsing the JSONL
# here would work today and break the first time the harness moves a field — and
# every rule doing it would reimplement the same traversal slightly differently,
# which is what the subcommand exists to absorb.
#
# It is told which record to read on stdin, in the same payload shape a harness
# sends its own hook points. What this script does not have is the path to put
# there: the engine hands a hook `{event, guardrailDir}` and nothing naming the
# session's record.
#
# SR_TRANSCRIPT is the variable that closes that gap. It does not exist yet on
# any branch — see the note in GUARDRAIL.md. Until it does, this rule refuses
# every write it is shown, loudly and by name, rather than permitting one.
# ---------------------------------------------------------------------------
transcript="${SR_TRANSCRIPT:-}"
if [ -z "$transcript" ]; then
  # Refused rather than permitted. A precondition that could not check its
  # precondition has established nothing, and permitting here would turn a gap
  # in the environment into consent — the one failure mode a guardrail must not
  # have. The message names what is missing, so whoever reads it fixes the
  # wiring rather than the write.
  echo "required-context-precondition: SR_TRANSCRIPT is unset, so this rule cannot read the session's record and cannot establish whether '$required_skill' was loaded. Refusing: a precondition that could not be checked is not a precondition that passed." >&2
  exit 1
fi

# Entries where the agent invoked the Skill tool with this skill's name.
#
# `type` is the field that distinguishes record kinds — not `kind`.
#
# Sub-agent entries are excluded, which is `session query`'s default and is the
# right default here: a skill loaded inside a delegated sub-agent was not loaded
# on the line of work doing the writing.
#
# The --where asks only the coarse question, and the skill's own name is checked
# in the shaping below. An expression reaching for `.input.skill` would also be
# evaluated against every user turn, where `message.content` is a string rather
# than a list; the engine reports an expression that no entry could be evaluated
# against as an error rather than as "nothing matched", so keeping the reach out
# of the expression is what keeps this working on a real session.
#
# `.input.skill` is read outright, with no fallback. It used to be
# `.input.skill // .input.command`, hedging between two field names because
# nothing declared which one a Skill tool_use carries. The spec now declares it
# — see `SkillToolInput` in the claude-code dependency — and the measurement
# behind that declaration settles the hedge: across 664 Skill calls in real
# transcripts, `skill` is present on every one and `command` on none. A fallback
# to a field that never occurs is not caution, it is a second thing that can
# silently start matching the wrong entry.
loaded="$(
  printf '{"transcript_path":%s}' "$(printf '%s' "$transcript" | jq -R .)" |
    sloprail session query --where 'type == "assistant"' 2>/dev/null |
    jq --arg s "$required_skill" '
      [ .[]
        | (.message.content // [])
        | select(type == "array")
        | .[]
        | select(.type == "tool_use" and .name == "Skill")
        | select(.input.skill == $s)
      ] | length
    ' 2>/dev/null
)"

if [ -z "$loaded" ]; then
  # The query or the shaping produced nothing at all — a different failure from
  # "the skill was not loaded", and one this rule must not read as either an
  # answer or a permission. Same reasoning as the unset transcript above.
  echo "required-context-precondition: could not read this session's record, so whether '$required_skill' was loaded is unknown. Refusing: a precondition that could not be checked is not a precondition that passed." >&2
  exit 1
fi

if [ "$loaded" -gt 0 ]; then
  exit 0
fi

# Refused, with the one instruction that clears it.
cat >&2 <<EOF
SKILL REQUIRED: writing under '$path' requires the '$required_skill' skill to have been loaded first, and this session's record holds no Skill tool_use naming it.

Invoke the Skill tool with skill "$required_skill", then retry this write. Stating that you have read it is not what is checked — the session's own record is.
EOF
exit 1
