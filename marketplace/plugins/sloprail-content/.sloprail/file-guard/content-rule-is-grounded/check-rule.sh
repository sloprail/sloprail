#!/usr/bin/env bash
# A writing RULE (`.sloprail/content-rules/<NN>/RULE.md` or a topic's
# `constraints/<NN>/CONSTRAINT.md`) must be traceable to something a human
# actually asked for, and a SCRIPT rule must actually be capable of refusing.
# Two independent checks, both fail-closed, no model — the rule-authoring
# analogue of task-body-is-human-authored's citation half and of
# authoring-slop's "a guardrail that never fires is worse than no guardrail".
#
#   1. GROUNDED PROVENANCE — at least one entry in transcript_paths: must
#      GROUND, via `sr-session trajectory cite --source-types user` (the exact
#      mechanism task bodies and unit-publish-approved's approved: use), to a
#      REAL USER MESSAGE. An agent inventing a writing rule nobody asked for —
#      "always mention the product name" — and citing nothing, or citing its
#      own prior turn, is refused. transcript_paths entries are
#      `<jsonl>:<line>` strings (not markdown links, unlike approved: — this
#      matches the field's EXISTING shape from the pre-existing CONSTRAINT.md
#      convention, so no rule already on disk needs reshaping beyond adding a
#      real citation if it lacks one). The QUOTE grounded is the transcript
#      line's own text — cite is asked to confirm that exact line is a real
#      user message, not to match a separate quote string, which is why this
#      check reads a citation differently from a body link (there is no quote
#      text alongside a transcript_paths entry to compare against; the LINE
#      itself is what must be a user message).
#   2. NOT A TRIVIAL SCRIPT RULE — a rule.script.name's script must contain
#      SOME conditional exit path, not just an unconditional `exit 0` (or no
#      exit at all, which bash reads as the last command's status — usually
#      0). A script rule that can never refuse is exactly the "guardrail that
#      never fires" failure the authoring-guardrails skill calls out, one
#      level down: it would load, validate, and admit everything, forever.
#      Detected the same way authoring-slop's own rules are: a grep-shaped
#      structural check (see check_script_can_refuse below) — cheap,
#      deterministic, and stated as a heuristic rather than a proof, exactly
#      like authoring-slop's own docs say of themselves.
#
# REFUSAL CONTRACT: exit 0 permits; non-zero refuses with `{"reason": "..."}` on
# stdout. `set -uo pipefail`, never `set -e`.
set -uo pipefail

refuse() {
  jq -n --arg reason "$1" '{reason: $reason}'
  exit 1
}

event="$(cat)"

path="$(printf '%s' "$event" | jq -r '.event.path // empty' 2>/dev/null)"
if [ -z "$path" ]; then
  refuse "content-rule-is-grounded: the event named no path, so there is nothing to check"
fi

root="${SR_WORKSPACE:-.}"
gdir="${SR_GUARDRAIL_DIR:-.}"

schema="$root/.sloprail/schemas/rule.cue"
if [ ! -f "$schema" ]; then
  refuse "content-rule-is-grounded: schema not found at $schema — install the plugin's rule.cue under the project's .sloprail/schemas/."
fi

kind="$(printf '%s' "$event" | jq -r '.event.kind // ""' 2>/dev/null)"
case "$kind" in
  PostFileCreate|PostFileUpdate)
    # A Post kind carries the SETTLED bytes directly on the flat event — no
    # disk re-read.
    content="$(printf '%s' "$event" | jq -r '.event.newContent // ""' 2>/dev/null)"
    ;;
  PreFileCreate|PreFileUpdate)
    known="$(printf '%s' "$event" | jq -r '.event.resultKnown // false' 2>/dev/null)"
    if [ "$known" != "true" ]; then
      exit 0
    fi
    content="$(printf '%s' "$event" | jq -r '.event.newContent // ""' 2>/dev/null)"
    ;;
  *)
    exit 0
    ;;
esac

doc="$(printf '%s' "$content" | sr-file validate - --as .md --schema "$schema" --emit 2>&1)"
if [ $? -ne 0 ]; then
  detail="$(printf '%s' "$doc" | sed "s|^-:|$path:|g")"
  refuse "RULE FRONTMATTER INVALID: $path does not satisfy .sloprail/schemas/rule.cue.

$detail"
fi

# ============================================================ CHECK 1 =====
tp_lines="$(printf '%s' "$doc" | jq -r '(.transcript_paths // [])[]' 2>/dev/null)"

if [ -z "$tp_lines" ]; then
  refuse "RULE NOT GROUNDED: $path carries no transcript_paths — a writing rule must be traceable to something a human actually asked for.

Add at least one entry, <absolute-session.jsonl>:<line>, naming the transcript line where a user asked for this rule. A rule with no origin citation is indistinguishable from one an agent invented on its own."
fi

any_grounded=0
problems=""
while IFS= read -r tp; do
  [ -n "$tp" ] || continue
  tpath="${tp%%:*}"
  tline="${tp##*:}"
  case "$tpath" in
    /*) : ;;
    *)  problems="${problems}  transcript_paths entry \"$tp\" is not an absolute .jsonl path
"
        continue ;;
  esac
  if [ ! -f "$tpath" ]; then
    problems="${problems}  transcript_paths entry \"$tp\" names a transcript that is not there
"
    continue
  fi
  # The cited LINE itself must be a real user message — sr-session trajectory
  # cite grounds a QUOTE, so the line's own verbatim text is used as the quote
  # being asked to resolve against itself at that line. A line that is a tool
  # result, the agent's own turn, or a harness-injected message will not
  # ground under --source-types user, by cite's own construction.
  line_text="$(sed -n "${tline}p" "$tpath" 2>/dev/null | jq -r '.message.content // .content // empty' 2>/dev/null)"
  if [ -z "$line_text" ]; then
    # Fall back to asking cite about the RAW line text if the jsonl entry's
    # content is not the simple shape above (e.g. a content-block array) —
    # sr-session trajectory cite --source-types user resolves a quote against
    # the whole trajectory, so a short, distinctive substring of the raw JSON
    # line still grounds if that line is genuinely a user message.
    line_text="$(sed -n "${tline}p" "$tpath" 2>/dev/null | cut -c1-120)"
  fi
  if [ -z "$line_text" ]; then
    problems="${problems}  transcript_paths entry \"$tp\" names line $tline, which is empty or does not exist
"
    continue
  fi
  if sr-session trajectory cite --source-types user --path "$tpath" "$line_text" >/dev/null 2>&1; then
    any_grounded=1
  else
    problems="${problems}  transcript_paths entry \"$tp\" does not ground to a real user message (it is not in the user pool — an agent's own turn, a tool result, or a harness-injected message does not count)
"
  fi
done <<EOF
$tp_lines
EOF

if [ "$any_grounded" -ne 1 ]; then
  refuse "RULE NOT GROUNDED: $path's transcript_paths do not resolve to a real user message —

$problems
At least one entry must cite a line where a user actually asked for this rule. Quote the transcript line honestly; do not cite your own prior turn."
fi

# ============================================================ CHECK 2 =====
sname="$(printf '%s' "$doc" | jq -r '.script.name // empty' 2>/dev/null)"
if [ -n "$sname" ]; then
  spath="$gdir/../../../scripts/$sname.sh"
  [ -f "$spath" ] || spath="$root/.sloprail/content-rules/scripts/$sname.sh"
  if [ -f "$spath" ]; then
    lib="$gdir/can-refuse.sh"
    if [ -f "$lib" ]; then
      # shellcheck source=can-refuse.sh
      . "$lib"
      if ! reason="$(script_can_refuse "$spath")"; then
        refuse "TRIVIAL SCRIPT RULE: $path names script '$sname' ($spath), which cannot be proven capable of refusing —

  $reason

A script rule that can only ever exit 0 is a guardrail that never fires, which is worse than no guardrail: it validates and admits everything, silently, forever. Give the script a real conditional refusal path (a branch that calls the shared refuse() helper, or an explicit non-zero exit under a condition). If this really is a permanent no-op by design, document that decision in the rule's own body or this guard's file-guard.yaml, the same override convention authoring-slop uses."
      fi
    fi
  fi
fi

exit 0
