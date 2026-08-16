#!/usr/bin/env bash
# Reviews ONE task that claims to be finished.
#
# Fires on a task written into `in_review`. Reads the task's stated outcome and
# the bytes its citations actually name, and asks a judge whether that evidence
# substantiates the claim. See GUARDRAIL.md for the whole argument; the parts
# that decide what this script does:
#
#   - The standard is RUBRIC.md beside this file, not a heredoc here.
#   - REJECTED refuses, naming which evidence failed.
#   - APPROVED ALSO refuses, with an instruction to delete the task folder and
#     commit solely that deletion. This hook runs no git. A validation hook that
#     `git rm`s the file it was handed, on the strength of one model verdict, has
#     no fail-open direction — see GUARDRAIL.md, "The approve path".
#   - So there is no verdict that permits. The permits are: not in_review, and
#     the fail-open branches.
#
# FAIL-OPEN on this rule's own machinery (no sr-agent, no jq, missing library,
# timeout, unparseable verdict). FAIL-CLOSED on the verdict. Every fail-open
# branch is marked FAIL-OPEN and says so on stderr.
#
# `set -uo pipefail`, never `set -e`: under errexit an ordinary non-zero from a
# grep or a lookup aborts mid-decision and the exit status that follows is read
# as a refusal this rule never decided to make.
set -uo pipefail

# stdin is read EXACTLY ONCE. It is consumed by the first reader, so it is
# captured here and every later extraction reads the variable.
event="$(cat)"

if ! command -v jq >/dev/null 2>&1; then
  # FAIL-OPEN: without jq nothing about the event can be read, so this rule has
  # no opinion. Unlike the deterministic sibling — which refuses here, because
  # its whole job is local and a missing jq is a defect — this one is a judged
  # rule that can wedge a session, so it declines instead.
  echo "task-review: jq is not on PATH, so nothing was reviewed. PERMITTING (fail-open — see GUARDRAIL.md)." >&2
  exit 0
fi

path="$(printf '%s' "$event" | jq -r '.event.fields.path // empty' 2>/dev/null)"
if [ -z "$path" ]; then
  # Not a fail-open: the matcher binds only kinds that carry a path, so arriving
  # here means the declaration and this script disagree about what was bound.
  # That is a defect in this guardrail, and surfacing it is right.
  echo "task-review: the event named no path, so there is nothing to review" >&2
  exit 1
fi

root="${SR_WORKSPACE:-.}"
abs="$root/$path"

# Post kinds only, so the bytes on disk ARE the answer — what actually landed
# rather than what some tool call predicted. Written and then removed inside one
# cycle: nothing to review, nothing wrong.
[ -f "$abs" ] || exit 0
[ -s "$abs" ] || exit 0

guardrail_dir="$(printf '%s' "$event" | jq -r '.guardrailDir // empty' 2>/dev/null)"
# The engine runs a hook with the guardrail folder as cwd, so `.` would work —
# and that is exactly why it is not used. A dependency that holds by accident is
# one nothing will flag the day it stops holding, and the failure here is silent
# and permissive: the library is not found, the branch below fails open, and
# every task is permitted unreviewed while the rule still looks installed.
gdir="${guardrail_dir:-.}"

# ---------------------------------------------------------------- the gate ---
#
# Only `in_review` is reviewed, and this is asked BEFORE anything expensive:
# most task writes are to_do or in_progress and must cost nothing.
#
# The status is read with a small awk over the frontmatter rather than with
# `sr-file validate --emit`. That command is the sibling rule's instrument and
# the right one for validating; here the schema has ALREADY been enforced by
# `task-evidence-resolves` on this same path in this same cycle, and re-running
# it would mean a second process on every task write to learn a value one line
# of awk can read. If the frontmatter is malformed, the status read comes back
# empty and this rule stays out of the way — which is correct, because the
# deterministic rule is already refusing that write with a better message.
frontmatter="$(awk 'NR==1 && $0=="---"{inside=1; next} inside && $0=="---"{exit} inside{print}' "$abs" 2>/dev/null)"
status="$(printf '%s\n' "$frontmatter" | awk -F': *' '$1=="status"{gsub(/^[ \t"'"'"']+|[ \t"'"'"']+$/,"",$2); print $2; exit}')"

[ "$status" = "in_review" ] || exit 0

# ------------------------------------------------------- citation resolving ---
#
# SOURCED from the sibling rule, not reimplemented. The body rule, this review
# rule and the stop rule must agree exactly about what `40-60` means; two copies
# would drift and a task would satisfy one rule while another refused it with no
# spelling that satisfies both. That file also carries two measured bug fixes
# (the missing trailing newline that made `read` discard its only input, and BSD
# `seq` counting DOWN on a reversed range) which a fresh copy would reintroduce.
lib="$gdir/../task-evidence-resolves/citations.sh"
if [ ! -f "$lib" ]; then
  # FAIL-OPEN: without the library no citation can be expanded, so there is
  # nothing to put in front of a judge. A judged rule declines rather than
  # wedging the session over its own missing dependency.
  echo "task-review: citations.sh not found at $lib, so '$path' was NOT reviewed. PERMITTING (fail-open — see GUARDRAIL.md)." >&2
  exit 0
fi
# shellcheck source=../task-evidence-resolves/citations.sh
. "$lib"

rubric_file="$gdir/RUBRIC.md"
if [ ! -f "$rubric_file" ]; then
  # FAIL-OPEN: no standard to judge against.
  echo "task-review: RUBRIC.md not found beside this hook, so '$path' was NOT reviewed. PERMITTING (fail-open — see GUARDRAIL.md)." >&2
  exit 0
fi
# Comment lines are stripped so the rubric can explain that it is a prompt
# without that explanation becoming part of the prompt.
template="$(grep -v '^#' "$rubric_file")"
if [ -z "$template" ]; then
  # FAIL-OPEN: an empty standard judges nothing.
  echo "task-review: RUBRIC.md is empty, so '$path' was NOT reviewed. PERMITTING (fail-open — see GUARDRAIL.md)." >&2
  exit 0
fi

# The lists, as JSON arrays, read out of the frontmatter.
#
# yq is not assumed present. The lists are flow-style JSON arrays under
# task.cue's spelling (`observations: ["/a.jsonl:1-2"]`), so the value after the
# key is already valid JSON and jq parses it directly. A block-style list would
# not be read here — and that is visible rather than silent: the count comes back
# 0, the pre-flight below refuses naming the empty list, and the agent is told to
# spell it inline. Guessing at block YAML with a hand-rolled parser is how a rule
# ends up quietly reviewing half the evidence.
read_list() {
  printf '%s\n' "$frontmatter" \
    | awk -v k="$1" -F': *' '$1==k{sub(/^[^:]*: */,""); print; exit}' \
    | jq -c '. // []' 2>/dev/null
}
obs_json="$(read_list observations)"
art_json="$(read_list artifacts)"
[ -z "$obs_json" ] && obs_json='[]'
[ -z "$art_json" ] && art_json='[]'

n_obs="$(printf '%s' "$obs_json" | jq -r 'length' 2>/dev/null)"
n_art="$(printf '%s' "$art_json" | jq -r 'length' 2>/dev/null)"

# ------------------------------------------------------ the pre-flight gate ---
#
# Deterministic, and it runs BEFORE any model call. Cheap gates expensive: never
# spend a judge to learn something `sed -n` already settled.
#
# `task-evidence-resolves` normally refuses these first. This is not a duplicate
# check for its own sake — a citation that does not resolve leaves this rule with
# NOTHING TO SHOW A JUDGE, so it has to be handled here whether or not the
# sibling rule fired. If the sibling is uninstalled or was bypassed, this is the
# refusal the task gets, and it names the citation the same way.
problems=""
i=0
while IFS= read -r citation; do
  [ -n "$citation" ] || continue
  if reason="$(cite_check "$citation" "observations[$i]")"; then :; else
    problems="${problems}  ${reason}
"
  fi
  i=$((i + 1))
done <<EOF
$(printf '%s' "$obs_json" | jq -r '.[]' 2>/dev/null)
EOF

i=0
while IFS= read -r citation; do
  [ -n "$citation" ] || continue
  if reason="$(cite_check "$citation" "artifacts[$i]")"; then :; else
    problems="${problems}  ${reason}
"
  fi
  i=$((i + 1))
done <<EOF
$(printf '%s' "$art_json" | jq -r '.[]' 2>/dev/null)
EOF

if [ "${n_obs:-0}" -eq 0 ]; then
  problems="${problems}  observations — the list is empty or not readable as an inline JSON array
"
fi
if [ "${n_art:-0}" -eq 0 ]; then
  problems="${problems}  artifacts — the list is empty or not readable as an inline JSON array
"
fi

if [ -n "$problems" ]; then
  # FAIL-CLOSED, and deliberately without a model call.
  cat >&2 <<EOF
REVIEW CANNOT RUN: $path is in_review but its evidence does not resolve.

$problems
There is nothing to review until every citation points at bytes a reviewer can
open. A citation is <absolute-path>:<ranges>, ranges being N or N-M, several of
them comma-separated, written as an inline list:

    observations: ["/abs/session.jsonl:120-140"]
    artifacts:    ["/abs/src/foo.go:10-60"]

Fix the paths or the ranges. The status stays in_review; correct the evidence
and write the task again.
EOF
  exit 1
fi

# --------------------------------------------------- expanding the evidence ---
#
# The judge is given the CITED LINES, not the files. A judge handed a whole
# 4000-line source file and asked whether 10-60 substantiates a claim reads the
# file and answers about the file. Slicing to what was cited is also what makes
# the "shows the command but not its result" verdict possible at all: if the
# range genuinely stops before the output, the judge sees a range that stops
# before the output.
#
# Bounded, because one task citing 1-10000 would otherwise build an unbounded
# prompt. Truncation is ANNOUNCED inline so a judge looking at part of a slice
# knows it and can say the evidence was not legible, rather than inventing a
# verdict about bytes it was never shown. The rubric's criterion 5 is what reads
# that marker.
MAX_LINES_PER_CITATION=120
MAX_CHARS_PER_LINE=600
MAX_TOTAL_CHARS=60000

expand_citations() {
  label="$1"
  list_json="$2"
  idx=0
  total=0
  while IFS= read -r citation; do
    [ -n "$citation" ] || continue
    cpath="$(cite_path "$citation")"
    cranges="$(cite_ranges "$citation")"
    printf '### %s[%s] %s\n' "$label" "$idx" "$citation"
    shown=0
    for n in $(cite_lines "$cranges"); do
      if [ "$shown" -ge "$MAX_LINES_PER_CITATION" ]; then
        printf '[TRUNCATED: this citation names more lines than were shown]\n'
        break
      fi
      if [ "$total" -ge "$MAX_TOTAL_CHARS" ]; then
        printf '[TRUNCATED: the evidence exceeded the size this reviewer can show]\n'
        break
      fi
      line="$(sed -n "${n}p" "$cpath" 2>/dev/null | cut -c "1-$MAX_CHARS_PER_LINE")"
      printf '%s: %s\n' "$n" "$line"
      shown=$((shown + 1))
      total=$((total + ${#line} + 8))
    done
    printf '\n'
    idx=$((idx + 1))
  done <<INNER
$(printf '%s' "$list_json" | jq -r '.[]' 2>/dev/null)
INNER
}

observations="$(expand_citations observations "$obs_json")"
artifacts="$(expand_citations artifacts "$art_json")"
task_body="$(cat "$abs" 2>/dev/null)"

# ------------------------------------------------------------- the judge ------
agent_bin="${SR_AGENT_BIN:-sr-agent}"
if ! command -v "$agent_bin" >/dev/null 2>&1; then
  # FAIL-OPEN: no judge available.
  echo "task-review: '$agent_bin' is not on PATH, so '$path' was NOT reviewed. PERMITTING (fail-open — see GUARDRAIL.md)." >&2
  exit 0
fi

# A plain, absolute, unique .json path — NOT mktemp. A random-looking path is one
# a model quietly declines to write to, or "corrects" to something tidier, and
# the verdict then lands somewhere this script never looks. Same reasoning as the
# reference judge in the sibling project.
verdict="/tmp/task-review-verdict-$$-$(date +%s).json"
rm -f "$verdict"
trap 'rm -f "$verdict"' EXIT INT TERM

# Substituted with awk rather than a shell expansion or envsubst. The values
# carry newlines and arbitrary punctuation — this is the agent's own text and the
# cited file contents, which is attacker-shaped — and sed would need every one of
# them escaped. envsubst would additionally expand every `$` appearing in a
# task's own body, which is exactly the wrong thing to do to untrusted text. Only
# the five names this rule defines are substituted, by exact match.
prompt="$(
  TB="$task_body" OBS="$observations" ART="$artifacts" P="$path" V="$verdict" \
  awk '
    { line = $0
      gsub(/\$task_body/,    ENVIRON["TB"],  line)
      gsub(/\$observations/, ENVIRON["OBS"], line)
      gsub(/\$artifacts/,    ENVIRON["ART"], line)
      gsub(/\$verdict/,      ENVIRON["V"],   line)
      gsub(/\$path/,         ENVIRON["P"],   line)
      print line }
  ' <<RUBRIC_INPUT
$template
RUBRIC_INPUT
)"

# 20s, and it MUST stay under the engine's own hookTimeout of 30s.
#
# The engine kills a hook that outruns that deadline and reads the kill as a
# refusal. A judge given a longer budget can never reach its own timeout: the
# engine fires first, the fail-open branch below never runs, and a model that was
# merely slow refuses correct work. Two judges in the sibling project carried 60s
# and 90s and both had a fail-open that could not fire.
#
# Run from /tmp so the project's CLAUDE.md, skills and .sloprail/ are not loaded
# into the judge, and with hooks/MCP/plugins emptied so the judge cannot recurse
# into the very hook point that dispatched it.
#
# The prompt goes in `--prompt`, NOT down a pipe. A first draft wrote
# `printf '%s' "$prompt" | sr-agent ... --prompt "$(cat)"`, copying the reference
# judge's `--print` shape. That is broken twice over: sr-agent takes its prompt
# as an argument and reads nothing from stdin, and `$(cat)` inside the pipeline
# is a SECOND reader of a stream this script's contract says is read exactly
# once. Measured with --dry-run: the piped form printed
# "no prompt: pass it positionally ... or with --prompt" and exited 2, which
# lands in the no-verdict fail-open — a judge that never runs, permitting every
# task, with the rule looking installed. Caught only because the permit case was
# exercised.
#
# --claude-args takes SCALARS only; a JSON array value is refused with
# `only strings, numbers and booleans can be flag values`. So the tool allowlist
# is the string "Write" and the isolation overlay is a JSON string, both of which
# --dry-run confirmed reach claude as `--allowed-tools Write --settings {...}`.
( cd /tmp && timeout 20 "$agent_bin" \
    --model size-md \
    --claude-args '{"allowed-tools":"Write","settings":"{\"hooks\":{},\"mcpServers\":{},\"enabledPlugins\":{}}"}' \
    --prompt "$prompt" >/dev/null 2>&1 )

if [ ! -s "$verdict" ]; then
  # FAIL-OPEN: timed out, errored, or wrote nowhere readable.
  echo "task-review: the judge produced no verdict for '$path' (timeout or error), so it was NOT reviewed. PERMITTING (fail-open — see GUARDRAIL.md)." >&2
  exit 0
fi

# The span stays GREEDY. This verdict NESTS — `failures` is an array — so a
# non-greedy `{[^{}]*}` would take an inner object and the reads below would find
# nothing, which permits. The greedy span is safe here because a merged span
# makes jq emit more than one value, and both reads below are then multi-line,
# which matches no known decision and lands in the unparseable fail-open rather
# than in a silent approval.
raw="$(tr -d '\r' < "$verdict" 2>/dev/null | sed 's/```json//g; s/```//g')"
json="$(printf '%s' "$raw" | tr '\n' ' ' | grep -o '{.*}' | head -1)"
if [ -z "$json" ]; then
  # FAIL-OPEN: unparseable.
  echo "task-review: the judge's verdict for '$path' could not be parsed, so it was NOT reviewed. PERMITTING (fail-open — see GUARDRAIL.md)." >&2
  exit 0
fi

decision="$(printf '%s' "$json" | jq -r '.decision // empty' 2>/dev/null | tr '[:lower:]' '[:upper:]')"
reasoning="$(printf '%s' "$json" | jq -r '.reasoning // ""' 2>/dev/null)"
failures="$(printf '%s' "$json" | jq -r '(.failures // []) | map("  - " + .) | join("\n")' 2>/dev/null)"

task_dir="$(dirname "$path")"

case "$decision" in
  REJECTED)
    # THE VERDICT FAILS CLOSED.
    #
    # The status is NOT rewritten by this hook. A guardrail that edits the file
    # it guards collides with task-body-is-human-authored, and the user's choice
    # was explicit: refuse the turn, the agent fixes the evidence. So the task
    # stays in_review and comes back round — a Post refusal does not advance the
    # read mark, so it is judged again next cycle rather than scrolling away.
    cat >&2 <<EOF
REVIEW REJECTED: $path claims to be finished, but its evidence does not
substantiate the claim.

$reasoning

${failures:-  (the judge named no specific citation)}

The task STAYS in_review. Do not change its status and do not delete it. Fix the
evidence — cite the lines that actually show the result, and the artifacts that
actually hold it — then write the task again to be reviewed.
EOF
    exit 1
    ;;
  APPROVED)
    # APPROVED ALSO REFUSES, and this is the designed behaviour rather than a
    # missing branch. Two things it is doing at once:
    #
    #   1. It does NOT delete or commit anything itself. See GUARDRAIL.md, "The
    #      approve path" — a hook that `git rm`s a human-authored task folder on
    #      one model verdict has no fail-open direction, and a hook committing
    #      mid-cycle cannot know what else is staged. The agent can; it just did
    #      the work.
    #   2. It does NOT permit. A permitted approval would leave the task sitting
    #      in in_review forever — re-judged every cycle, or never again once
    #      revalidation has seen those bytes — which is the rule being a no-op on
    #      its success path.
    #
    # Refusing closes both. The read mark does not advance, so this same task is
    # reviewed again next cycle until the folder is gone. The deletion is what
    # clears it, which is the lifecycle the user specified.
    #
    # It opens with APPROVED so the agent is not told it did something wrong.
    cat >&2 <<EOF
TASK APPROVED: the evidence in $path substantiates the claim. The work is
accepted. Nothing about the task needs fixing.

$reasoning

There is no \`done\` status. An approved task is DELETED, and that deletion is
committed on its own. Do this now, and commit NOTHING else in the same commit:

    git rm -r "$task_dir"
    git commit -m "$(basename "$task_dir"): approved in review, task removed"

This review will be reported again on every turn until that folder is gone —
that is what closes an approved task, and this hook deliberately does not do it
for you.
EOF
    exit 1
    ;;
  *)
    # FAIL-OPEN: a verdict word this rule does not know. Not treated as a
    # rejection — an unrecognised decision is the judge's machinery misbehaving,
    # not evidence about the task, and the whole point of the fail-open split is
    # that machinery does not get to refuse.
    echo "task-review: the judge returned decision '${decision:-<none>}' for '$path', which this rule does not recognise, so it was NOT reviewed. PERMITTING (fail-open — see GUARDRAIL.md)." >&2
    exit 0
    ;;
esac
