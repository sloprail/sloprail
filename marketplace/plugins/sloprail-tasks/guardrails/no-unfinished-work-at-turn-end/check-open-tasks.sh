#!/usr/bin/env bash
# Refuses the end of a turn while any memories/tasks/*/*/TASK.md is `to_do` or
# `in_progress`.
#
# Bound to TurnEnd, which carries NO fields — so unlike a file rule this hook is
# handed no subject and goes and finds one: it walks the task tree under
# $SR_WORKSPACE and reads each task's status.
#
# TWO FAILURE DIRECTIONS, deliberately opposite. This kind fires on EVERY cycle,
# so a bug here wedges the whole session rather than one file.
#
#   LOGIC  -> FAIL CLOSED. An open task is a refusal. There is no path below
#             where a to_do or in_progress task is permitted.
#   PLUMBING -> FAIL OPEN, exit 0, and say on stderr which path was taken.
#             No SR_WORKSPACE, no tasks directory, no sr-file, no jq. None of
#             those is evidence the work is finished, and refusing on them gives
#             the agent a session it cannot un-wedge by touching its tasks — it
#             would be told to close tasks the rule cannot see, every turn,
#             forever. A missing tool is an environment defect to surface, not a
#             verdict about the tree.
#
# set -uo pipefail, never -e: under errexit an ordinary non-zero from a grep or a
# validate aborts mid-decision, and the status that follows is read as a refusal
# this script never decided to make.
set -uo pipefail

# Read stdin EXACTLY ONCE. The first reader consumes it. TurnEnd's fields are
# empty so nothing is extracted from the payload, but it is drained anyway: a
# hook that leaves its stdin unread can hand the writer a broken pipe.
payload="$(cat)"
: "${payload:=}"   # referenced, so shellcheck and -u are both satisfied

say() { printf '%s\n' "$*" >&2; }

# ---------------------------------------------------------------- plumbing ---
# Everything in this block exits 0. See the header: none of it is evidence.

root="${SR_WORKSPACE:-}"
if [ -z "$root" ]; then
  say "no-unfinished-work-at-turn-end: PLUMBING FAIL-OPEN — SR_WORKSPACE is unset, so there is no tree to look in. Permitting."
  exit 0
fi

tasks_dir="$root/memories/tasks"
if [ ! -d "$tasks_dir" ]; then
  say "no-unfinished-work-at-turn-end: PLUMBING FAIL-OPEN — no tasks directory at $tasks_dir. Permitting."
  exit 0
fi

# The schema ships with this rule. SR_PLUGIN_ROOT is set only for an installed
# copy and unset for a project's own, so the same line reads the schema that
# shipped beside this rule when installed and the project's when developed in
# place. Written as $root/.sloprail unconditionally — as this was while it lived
# in one repo — the rule would find nothing on any consumer's machine and
# fail-open on every turn, which for this rule means never refusing at all.
schema="${SR_PLUGIN_ROOT:-$root/.sloprail}/schemas/task.cue"
if [ ! -f "$schema" ]; then
  say "no-unfinished-work-at-turn-end: PLUMBING FAIL-OPEN — schema missing at $schema, so no status can be read. Permitting."
  exit 0
fi

command -v sr-file >/dev/null 2>&1 || {
  say "no-unfinished-work-at-turn-end: PLUMBING FAIL-OPEN — sr-file not on PATH; this rule reads frontmatter with nothing else. Permitting."
  exit 0
}
command -v jq >/dev/null 2>&1 || {
  say "no-unfinished-work-at-turn-end: PLUMBING FAIL-OPEN — jq not on PATH. Permitting."
  exit 0
}

# ------------------------------------------------------------------- logic ---
# From here everything fails CLOSED.

open_list=""
open_count=0
invalid_count=0
seen_any=0

# NUL-delimited find rather than a glob or `ls`, because a task folder name may
# contain a space and word-splitting would tear it in half. -maxdepth/-mindepth 3
# pins the shape the schema rule's matcher pins:
# ^memories/tasks/[^/]+/[^/]+/TASK\.md$ — group/task-name, exactly two folder
# levels.
while IFS= read -r -d '' f; do
  seen_any=1

  # THE FRONTMATTER IS READ BY THE PRODUCT'S OWN COMMAND, never parsed here. A
  # hand-rolled awk/sed frontmatter reader would be a second opinion about where
  # frontmatter ends, and on the day the two disagree this rule silently reads a
  # status out of a `---` in the prose body. --emit prints the validated
  # frontmatter as JSON on success and NOTHING on failure, which is what makes
  # the invalid case detectable without parsing an error message.
  #
  # stderr is discarded: a schema failure's detail belongs to
  # task-evidence-resolves, which already reported it at write time.
  doc="$(sr-file validate "$f" --schema "$schema" --emit 2>/dev/null)"
  rc=$?

  if [ $rc -ne 0 ] || [ -z "$doc" ]; then
    # SCHEMA-INVALID: SKIPPED ON PURPOSE. --emit prints nothing for a document
    # that did not validate, so this rule cannot learn the status even in
    # principle. task-evidence-resolves owns schema violations and refuses them
    # at the moment of writing with a message that names the field; refusing
    # again here would be two rules for one mistake, and this one's message
    # would be the worse of the two because it knows only that nothing printed.
    # Counted and reported to stderr so it is not INVISIBLE — just not a
    # refusal. See GUARDRAIL.md, "A task that fails the schema is skipped".
    invalid_count=$((invalid_count + 1))
    continue
  fi

  status="$(printf '%s' "$doc" | jq -r '.status // empty' 2>/dev/null)"

  # An empty status after a SUCCESSFUL validate should be impossible — the
  # schema marks status required and --concrete defaults on. Treated as invalid
  # rather than as "not open", so a surprise here cannot quietly become a permit.
  if [ -z "$status" ]; then
    invalid_count=$((invalid_count + 1))
    continue
  fi

  # THE REFUSAL SET IS EXACTLY THESE TWO. backlog / blocked / in_review are
  # allowed to rest; see the table in GUARDRAIL.md for why each one is.
  # Written as an explicit case rather than as "not one of the resting three":
  # a negative list would silently refuse on any sixth status the schema might
  # grow, and refusing on an unknown status is not this rule's call to make.
  case "$status" in
    to_do|in_progress)
      # Reported relative to the workspace, which is how the agent refers to
      # them and how the schema rule's matcher spells them.
      rel="${f#"$root"/}"
      open_count=$((open_count + 1))
      open_list="${open_list}  ${rel} — status: ${status}
"
      ;;
  esac
done < <(find "$tasks_dir" -mindepth 3 -maxdepth 3 -name 'TASK.md' -type f -print0 2>/dev/null | sort -z)

if [ "$invalid_count" -gt 0 ]; then
  say "no-unfinished-work-at-turn-end: skipped $invalid_count task file(s) whose frontmatter does not satisfy the schema — task-evidence-resolves owns those."
fi

if [ "$seen_any" -eq 0 ]; then
  # LOGIC, not plumbing: the directory exists and holds no tasks. No tasks means
  # no open tasks, and that is a permit on the merits.
  exit 0
fi

if [ "$open_count" -eq 0 ]; then
  exit 0
fi

# THE REFUSAL. Written to stderr, which the engine reads as the reason.
#
# It names EVERY open task and its status rather than a count, because a refusal
# at TurnEnd does not advance the read mark: the agent sees this message again
# next cycle and every cycle until it acts, so the message has to be a work
# queue rather than an alarm. "You have 3 open tasks" is not actionable.
#
# All four exits are named because three of them close a turn honestly WITHOUT
# doing the work, and an agent that does not know they exist will either grind
# on work it should not, or invent a status the schema refuses.
if [ "$open_count" -eq 1 ]; then
  headline="A turn cannot end with open work: 1 task is still unfinished."
else
  headline="A turn cannot end with open work: $open_count tasks are still unfinished."
fi

cat >&2 <<EOF
$headline

$open_list
Each of these must reach a status that is allowed to rest before the turn ends.
There are four ways to do that, and three of them do not require finishing the
work:

  1. FINISH IT — do the work, then set status: in_review and attach BOTH
     observations (citations into the session record proving it happened) and
     artifacts (citations into the files it produced). The review guardrail
     judges the claim; approved tasks are deleted folder and all.

  2. status: blocked — you cannot proceed. The body must NAME the blocker:
     what you are waiting on and who or what unblocks it. "Blocked" with no
     named blocker is just to_do with a different spelling.

  3. status: backlog — real, but not now. This is where an ask goes when it is
     genuinely deferred rather than abandoned, and it is the honest answer for
     anything you have no intention of touching this session.

  4. status: in_review — you are already done and just had not said so. Same
     evidence requirement as (1).

Do NOT invent a status to get past this. The schema has exactly five
(backlog, to_do, in_progress, in_review, blocked) and there is no \`done\`:
a task the reviewer approves is deleted, so marking your own work complete is
the one move the schema exists to refuse.
EOF
exit 1
