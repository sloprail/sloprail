#!/usr/bin/env bash
# Refuses a TASK.md whose BODY is not derived from the user's own messages.
#
# TWO STAGES, cheap first:
#
#   1. DETERMINISTIC — the body must carry at least one
#      `<abs-path>.jsonl:<ranges>` citation, and every cited line must be a
#      genuine user message (`type == "user"` with STRING content). A tool
#      result is also `type: "user"` but carries an ARRAY; citing one is the
#      exact substitution this rule exists to prevent. No model runs in this
#      stage. Fails CLOSED — it reads the event and the filesystem, neither of
#      which can flake for a reason unrelated to the task.
#
#   2. JUDGE — only reached if stage 1 passed. Asks sr-agent whether the body
#      CORRESPONDS to those messages and contains that AND NOTHING ELSE.
#
# Never spend a model call to learn something a string comparison already
# settled: a body with no citation, or one citing a tool result, is refused by
# stage 1 and the judge is never invoked.
#
# FAIL-OPEN on this rule's own machinery (no sr-agent binary, timeout,
# unparseable verdict) — a model call flakes for reasons that are not evidence
# about the file, and under fail-closed one flake wedges a session that no edit
# to the task can un-wedge. FAIL-CLOSED on the verdict itself. Every fail-open
# branch below says so in its message, so an unjudged task is visible rather
# than merely absent.
#
# Exit 0 permits, non-zero refuses.

set -uo pipefail

event="$(cat)"   # stdin, read ONCE — it is consumed by the first reader.

path="$(printf '%s' "$event" | jq -r '.event.fields.path // empty' 2>/dev/null)"
if [ -z "$path" ]; then
  # The matcher should have kept such an event away, so arriving here means the
  # declaration and this script disagree about what was bound. That is a defect
  # in this rule, not a model failure, so it refuses.
  echo "task-body-is-human-authored: the event named no path, so there is nothing to judge" >&2
  exit 1
fi

root="${SR_WORKSPACE:-.}"

guardrail_dir="$(printf '%s' "$event" | jq -r '.guardrailDir // empty' 2>/dev/null)"
# Read from the payload rather than relying on the cwd. The engine does run a
# hook with the guardrail's folder as its working directory, so `.` would work
# today — but nothing in this script would then SAY it depended on the cwd, and
# the day that changed the failure would be silent. The sibling judge carried
# exactly that latent bug.
gdir="${guardrail_dir:-.}"

# citations.sh is SOURCED from the sibling rule, not reimplemented.
#
# The body rule and the evidence rule must agree exactly about what a citation
# means; two copies that drifted would produce a task satisfying one rule and
# refused by the other, with no edit that satisfies both. The sibling owns the
# parsing (and the two measured bugs already fixed in it: the trailing newline
# in cite_lines, and BSD seq counting DOWN on a reversed range).
lib="$gdir/../task-evidence-resolves/citations.sh"
if [ ! -f "$lib" ]; then
  # Deterministic machinery, not model machinery — refuses. Without the citation
  # parser this rule cannot ask its cheap question at all, and permitting past a
  # missing parser is how a rule silently stops enforcing.
  echo "task-body-is-human-authored: citations.sh not found at $lib — the deterministic half cannot run" >&2
  exit 1
fi
# shellcheck source=../task-evidence-resolves/citations.sh
. "$lib"

# WHERE THE BYTES COME FROM depends on the kind, and the two are opposites.
#
# A Pre event carries the pending bytes because the write has not happened yet —
# $path either does not exist or still holds the OLD body, judging which would
# permit a replacement that is about to violate the rule.
#
# A Post event carries only a path: the write HAS happened, so the file on disk
# is the answer, and the better one — what actually landed rather than what was
# predicted.
#
# Field names confirmed against this build with a deliberately-wrong matcher
# probe rather than assumed:
#   PreFileCreate   content (string), markers (list), path (string)
#   PreFileUpdate   markers (list), path (string), result (string), resultKnown (bool)
#   PostFileCreate  path (string)
#   PostFileUpdate  path (string)
kind="$(printf '%s' "$event" | jq -r '.event.kind // ""' 2>/dev/null)"
case "$kind" in
  Post*)
    abs="$root/$path"
    # Written and then removed within the same cycle: nothing to judge, nothing
    # wrong.
    [ -f "$abs" ] || exit 0
    content="$(cat "$abs" 2>/dev/null)" || exit 0
    ;;
  PreFileUpdate)
    # GUARD ON THE BOOLEAN, THEN READ THE VALUE. A declared field the event
    # omits is filled with its type's zero value, so an uncomputable result and
    # a genuinely emptied file are the same observation — `sed -i` whose outcome
    # the engine cannot derive would otherwise look like a command that emptied
    # the file, and be refused for having no citation. That is a verdict on
    # bytes nobody has seen.
    #
    # Deferred rather than refused: the Post binding judges the same file a
    # moment later against what actually landed. This is why both are bound.
    if [ "$(printf '%s' "$event" | jq -r '.event.fields.resultKnown // false' 2>/dev/null)" != "true" ]; then
      exit 0
    fi
    content="$(printf '%s' "$event" | jq -r '.event.fields.result // ""' 2>/dev/null)"
    ;;
  *)
    # PreFileCreate. ABSENT content is not empty content: a create whose bytes
    # the engine could not derive OMITS the field, while a `touch` produces it
    # as the empty string. Read with `// ""` both arrive as "" and the
    # underivable one would be refused for having no body. Same deferral as
    # above — the Post binding sees what landed.
    if [ "$(printf '%s' "$event" | jq -r '(.event.fields | has("content")) // false' 2>/dev/null)" != "true" ]; then
      exit 0
    fi
    content="$(printf '%s' "$event" | jq -r '.event.fields.content' 2>/dev/null)"
    ;;
esac

# THE BODY IS THE PROSE AFTER THE FRONTMATTER.
#
# The frontmatter is the sibling rule's subject, checked there against
# task.cue; re-reading it here would be two rules drifting apart. awk rather
# than sed because the state machine ("skip until the second ---, print the
# rest") is what this is, and expressing it as a line-address range makes the
# no-frontmatter case behave differently from the with-frontmatter one.
#
# A file with no frontmatter at all yields the whole file as the body, which is
# the honest reading: everything in it is prose the agent wrote.
body="$(printf '%s\n' "$content" | awk '
  BEGIN { seen = 0; started = 0 }
  NR == 1 && $0 == "---" { seen = 1; next }
  seen == 1 && $0 == "---" { seen = 2; started = 1; next }
  seen == 1 { next }
  { print }
')"

# Trim leading/limit trailing whitespace so a body of only blank lines reads as
# empty rather than as content.
body_trimmed="$(printf '%s' "$body" | tr -d '[:space:]')"
if [ -z "$body_trimmed" ]; then
  cat >&2 <<EOF
TASK BODY IS EMPTY: $path has no body to attribute to anyone.

A task's body states the human's ask and cites where they made it:

    The user asked for X.

    Cited from /abs/path/to/session.jsonl:120-140

Write the ask in the user's own terms and attach the citation.
EOF
  exit 1
fi

# ---------------------------------------------------------------------------
# STAGE 1 — DETERMINISTIC. No model runs below this line until it has passed.
# ---------------------------------------------------------------------------

# EVERY CITATION IN THE BODY, extracted by shape.
#
# `<absolute-path>.jsonl:<ranges>` — a leading /, then anything that is not a
# colon or whitespace, then `.jsonl:`, then ranges of N or N-M comma-separated.
# The same grammar task.cue pins for frontmatter citations, restricted to .jsonl
# because a user message only exists in a transcript.
#
# grep -o with -E: one citation per line, so a body naming several is a list.
citations="$(printf '%s' "$body" | grep -oE '/[^:[:space:]]+\.jsonl:[0-9]+(-[0-9]+)?(,[0-9]+(-[0-9]+)?)*' 2>/dev/null)"

if [ -z "$citations" ]; then
  cat >&2 <<EOF
TASK BODY IS NOT ATTRIBUTED: $path carries no citation of a user message.

A task body must be derived from what the user actually said, and must name
where they said it:

    /absolute/path/to/session.jsonl:120-140

Ranges are N or N-M, several of them comma-separated (:12-30,44). The cited
lines must be real user messages in the transcript.

This is the one rule protecting the specification itself. An ask nobody can
trace to a human is indistinguishable from an ask an agent invented, and once
the task is softened every later check passes against the softened version.
Cite the message and state the ask in the user's own terms.
EOF
  exit 1
fi

# EACH CITED LINE MUST BE A GENUINE USER MESSAGE.
#
# cite_check_user_message is the sibling's, and it is the whole reason this half
# CAN be deterministic: a real user message is `type == "user"` with STRING
# content, while a tool result is also `type: "user"` but carries an ARRAY.
# Citing a tool result is citing the agent's own output as the human's ask.
problems=""
n_ok=0
for citation in $citations; do
  if reason="$(cite_check_user_message "$citation" "body citation")"; then
    n_ok=$((n_ok + 1))
  else
    problems="${problems}  ${reason}
"
  fi
done

if [ -n "$problems" ]; then
  cat >&2 <<EOF
TASK BODY CITES SOMETHING THAT IS NOT A USER MESSAGE: $path

$problems
A body citation must name lines that are real user messages in a .jsonl
transcript — entries whose type is "user" and whose content is a string.

A tool-result entry is ALSO type "user", but its content is an array. Citing
one attributes the agent's own output to the human, which is the substitution
this rule exists to prevent. Cite the line where the user actually spoke.
EOF
  exit 1
fi

# Defensive: every citation resolved but none was counted. Cannot happen given
# the loop above, and it refuses rather than falling through to a judge call
# with an empty message set — which would ask a model to compare the body
# against nothing and would very likely come back PASS.
if [ "$n_ok" -eq 0 ]; then
  echo "task-body-is-human-authored: no citation resolved for $path" >&2
  exit 1
fi

# ---------------------------------------------------------------------------
# STAGE 2 — THE JUDGE. Reached only because stage 1 passed.
# ---------------------------------------------------------------------------

# THE CITED MESSAGES, verbatim, as the judge's ground truth.
#
# Read here rather than handing the judge a path, because a judge that has to
# open a file is a judge that can fail to and then reason about nothing. The
# text of what the user said is the entire basis of the verdict, so it goes in
# the prompt.
cited_messages=""
for citation in $citations; do
  cpath="$(cite_path "$citation")"
  for n in $(cite_lines "$(cite_ranges "$citation")"); do
    # BOTH SHAPES, because stage 1 accepts both.
    #
    # An ordinary user message keeps its text at `.message.content`. A QUEUED
    # message — the user typing while the agent is still working — is a
    # `type: "queue-operation"` record whose text is at `.content`, with no
    # `.message` object at all.
    #
    # Reading only the first shape does not fail loudly: it yields an empty
    # string, the citation is skipped, and if it was the only one the judge is
    # handed NOTHING as its ground truth. It then compares a real body against
    # an empty message set and returns whatever it makes of that — measured, it
    # returned FAIL with reasoning that argued itself to "there is no
    # untraceable material" and failed anyway.
    #
    # So the symptom of this bug was a correct task refused by a judge whose
    # own words said it should pass, which is unfixable from the agent's side.
    msg="$(sed -n "${n}p" "$cpath" 2>/dev/null | jq -r '.message.content // .content // empty' 2>/dev/null)"
    [ -n "$msg" ] || continue
    cited_messages="${cited_messages}--- user message at ${cpath}:${n}
${msg}

"
  done
done

if [ -z "$cited_messages" ]; then
  # FAIL-OPEN. Stage 1 already proved these lines are user messages, so an empty
  # read here is this script's own extraction failing, not evidence about the
  # body. Refusing would blame the agent for a defect it cannot fix by editing
  # the task.
  echo "task-body-is-human-authored: could not read the cited messages for '$path', so the body was NOT judged. PERMITTING (fail-open — see GUARDRAIL.md)." >&2
  exit 0
fi

if ! command -v sr-agent >/dev/null 2>&1; then
  # FAIL-OPEN: no judge available.
  echo "task-body-is-human-authored: 'sr-agent' is not on PATH, so the body of '$path' was NOT judged. PERMITTING (fail-open — see GUARDRAIL.md)." >&2
  exit 0
fi

rubric_file="$gdir/RUBRIC.md"
if [ ! -f "$rubric_file" ]; then
  # FAIL-OPEN: the standard is missing, so there is nothing to judge against.
  echo "task-body-is-human-authored: RUBRIC.md not found beside this hook, so the body of '$path' was NOT judged. PERMITTING (fail-open — see GUARDRAIL.md)." >&2
  exit 0
fi

# Comment lines are stripped so the file can explain that it is a prompt without
# that explanation becoming part of the prompt.
template="$(grep -v '^#' "$rubric_file")"
if [ -z "$template" ]; then
  echo "task-body-is-human-authored: RUBRIC.md is empty, so the body of '$path' was NOT judged. PERMITTING (fail-open — see GUARDRAIL.md)." >&2
  exit 0
fi

# Substituted with awk rather than a shell expansion. The values carry newlines
# and arbitrary punctuation — the body is agent-shaped text and the cited
# messages are user-shaped text, neither of them escaped for anything. sed would
# need every metacharacter handled. envsubst is deliberately not used: it is not
# present everywhere and would expand every $ appearing in the body itself.
# Only the three names this rule defines are substituted, by exact match.
prompt="$(
  MSGS="$cited_messages" BODY="$body" P="$path" \
  awk '
    { line = $0
      gsub(/\$cited_messages/, ENVIRON["MSGS"], line)
      gsub(/\$body/,           ENVIRON["BODY"], line)
      gsub(/\$path/,           ENVIRON["P"],    line)
      print line }
  ' <<RUBRIC_INPUT
$template
RUBRIC_INPUT
)"

# 25s, and it MUST stay under the engine's own hookTimeout of 30s.
#
# The engine kills a hook that outruns that deadline and reads the kill as a
# refusal. A judge given a longer budget could never reach its own timeout: the
# engine fires first, the fail-open branch below never runs, and a model that
# was merely slow refuses the write instead of permitting it. The fail-open
# would be a comment describing unreachable code.
#
# --prompt rather than a positional: the prompt carries the whole body and every
# cited message, and passing that as an argv element is how a long ask meets the
# argument-length limit.
#
# size-md rather than a named model: sr-agent resolves a size alias under
# whatever harness is running, so this rule does not break when run under a
# harness whose model names differ. cwd is /tmp so the judge cannot be tempted
# to read the repo it is judging.
#
# `< /dev/null` IS LOAD-BEARING, and it was measured rather than added for tidi-
# ness. sr-agent reads a prompt from stdin when one is piped, and waits 3s for it
# before giving up:
#
#   "Warning: no stdin data received in 3s, proceeding without it."
#
# This hook has ALREADY drained its own stdin into $event. What sr-agent inherits
# is a pipe that is empty but not closed, so it blocks for the full grace period
# every single call. Measured on this machine: 5.79s with the inherited pipe
# against 3.24s with /dev/null — 2.5s of dead wait burned out of a 25s budget on
# every judged task, for nothing.
#
# It is not merely slow, it is a correctness margin. The budget has to stay under
# the engine's 30s hookTimeout, and spending a tenth of it waiting on a pipe that
# will never deliver moves a slow-but-fine model call closer to the deadline
# where the engine kills the hook and reads the kill as a refusal.
answer="$( cd /tmp && timeout 25 sr-agent --model size-md --prompt "$prompt" < /dev/null 2>/dev/null )"
rc=$?

if [ "$rc" -ne 0 ] || [ -z "$answer" ]; then
  # FAIL-OPEN: timed out, errored, or said nothing.
  echo "task-body-is-human-authored: the judge produced no verdict for '$path' (exit $rc — timeout or error), so the body was NOT judged. PERMITTING (fail-open — see GUARDRAIL.md)." >&2
  exit 0
fi

# THE VERDICT LINE. Matched anywhere in the answer rather than requiring it to
# be the only output, because a model that prefixes a sentence of preamble has
# still answered. `head -1` so a model that repeats itself decides once.
# THE LAST VERDICT, not the first.
#
# The rubric asks for one line and nothing else. A model does not always obey:
# measured, this judge emitted a FAIL, argued itself out of it inside the same
# sentence ("actually re-checking, the heading is a verbatim excerpt of the
# user's words, not invented material, so no untraceable content exists"), and
# then emitted `VERDICT: PASS` on its own line.
#
# With `head -1` the abandoned first attempt won, and a task whose body was
# NOTHING but a verbatim quote and a citation was refused — by a judge whose own
# final answer was PASS. That refusal is unfixable from the agent's side: there
# is no edit to a correct body that makes it more correct.
#
# `tail -1` takes the conclusion instead of the first draft. Reasoning that
# reaches a verdict and revises it is a model thinking aloud, and the answer is
# where it stopped, not where it started.
verdict="$(printf '%s' "$answer" | tr -d '\r' | grep -oE 'VERDICT:[[:space:]]*(PASS|FAIL)' | tail -1)"

if [ -z "$verdict" ]; then
  # FAIL-OPEN: unparseable. Neither PASS nor FAIL was stated, so no verdict was
  # reached — this is the model failing to answer, not the body failing to
  # comply.
  echo "task-body-is-human-authored: the judge's answer for '$path' named no verdict, so the body was NOT judged. PERMITTING (fail-open — see GUARDRAIL.md)." >&2
  exit 0
fi

case "$verdict" in
  *PASS*) exit 0 ;;
esac

# THE VERDICT FAILS CLOSED.
#
# The reason line is carried through so the refusal says WHICH material was
# untraceable — "the body contains slop" sends the agent rereading the whole
# file, while naming the invented paragraph says what to delete.
# tail -1 for the same reason the verdict uses it: the reason must come from the
# FAIL the model settled on, not from one it wrote and then abandoned.
detail="$(printf '%s' "$answer" | tr -d '\r' | grep -E 'VERDICT:[[:space:]]*FAIL' | tail -1 | sed 's/^.*VERDICT:[[:space:]]*FAIL[[:space:]]*—*[[:space:]]*//')"
[ -n "$detail" ] || detail="the body contains material not traceable to the cited user messages"

cat >&2 <<EOF
TASK BODY IS NOT THE USER'S ASK: $path

$detail

The body of a task must contain what the user said in the cited messages, and
NOTHING ELSE. A valid citation wrapped in agent-authored elaboration — inferred
requirements, a suggested approach, invented rationale or acceptance criteria —
is content slopped around a legitimate reference, and the next reader cannot
tell which sentences are the ask and which are a previous agent's guess.

Delete what the user did not say. Restating their ask in fewer words, or
splitting it into bullets, is fine. Adding to it is not.

You may flush an update onto the RESULT of the work. You may not edit the ask.
EOF
exit 1
