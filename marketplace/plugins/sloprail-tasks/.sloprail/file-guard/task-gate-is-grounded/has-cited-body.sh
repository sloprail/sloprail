#!/usr/bin/env bash
# Stage 1 of task-gate-is-grounded: the DETERMINISTIC half, no model.
#
# A gate is a start condition an agent is about to make binding — task-
# gates-hold refuses the move to to_do/in_progress until it holds. Nothing
# re-checks WHY a gate exists once it is there, so a gate that is trivial
# (`exit 0`), fabricated (a condition the user never asked for), or a plain
# `.sh` that tests nothing meaningful is a silent way to either wave through
# a real ready_when-style protection with a rubber stamp, OR invent a hurdle
# the user did not set. Both are the same failure task-body-is-human-authored
# guards for the ask itself, relocated to gate files: a gate must be
# TRACEABLE to the task's own cited ask, must not CONTRADICT it, and must not
# INVENT a condition the ask does not support.
#
# STAGE 1 asks the cheap, deterministic question ONLY: does the task's own
# TASK.md (the sibling of this gates/ directory) carry at least one grounded
# `[quote](jsonl)` body citation to begin with? Without one there is no ask to
# trace the gate against at all — refused here, before the judge (which needs
# that citation as its ground truth) is ever paid for. This does NOT itself
# judge the gate's CONTENT against the ask (contradiction, invention,
# triviality) — that is stage 2's model call, over the actual gate text.
#
# THE REFUSAL CONTRACT: exit 0 permits; non-zero refuses with
# `{"reason": "..."}` on stdout. Fails CLOSED: no sibling TASK.md, or no
# grounded citation in it, is a refusal — a gate cannot be grounded against an
# ask nothing traces to a human.
set -uo pipefail

refuse() {
  jq -n --arg reason "$1" '{reason: $reason}'
  exit 1
}

event="$(cat)"

path="$(printf '%s' "$event" | jq -r '.event.path // empty' 2>/dev/null)"
if [ -z "$path" ]; then
  refuse "task-gate-is-grounded: the event named no path, so there is nothing to check"
fi

root="${SR_WORKSPACE:-.}"
gdir="${SR_GUARDRAIL_DIR:-.}"

# path is memories/tasks/<group>/<name>/gates/<gate-name>.(sh|md). The
# sibling TASK.md is two levels up from gates/<file>.
gates_dir="$(dirname "$path")"
task_dir="$(dirname "$gates_dir")"
task_md="$root/$task_dir/TASK.md"

if [ ! -f "$task_md" ]; then
  refuse "GATE HAS NO TASK: $path names a gate, but there is no sibling TASK.md at $task_dir/TASK.md to ground it against. A gate cannot exist without the task whose ask it must trace to."
fi

lib="$gdir/../task-evidence-resolves/cite-links.sh"
if [ ! -f "$lib" ]; then
  refuse "task-gate-is-grounded: cite-links.sh not found at $lib, so no citation could be resolved"
fi
# shellcheck source=../task-evidence-resolves/cite-links.sh
. "$lib"

task_content="$(cat "$task_md" 2>/dev/null || true)"

# THE BODY IS THE PROSE AFTER THE FRONTMATTER — same extraction
# task-body-is-human-authored uses.
body="$(printf '%s\n' "$task_content" | awk '
  BEGIN { seen = 0 }
  NR == 1 && $0 == "---" { seen = 1; next }
  seen == 1 && $0 == "---" { seen = 2; next }
  seen == 1 { next }
  { print }
')"

n_ok=0
while IFS="$(printf '\t')" read -r href quote; do
  [ -n "$href" ] || continue
  cpath="$(cite_link_href_path "$href")"
  case "$cpath" in
    /*) : ;;
    *)  cpath="$root/$cpath" ;;
  esac
  if cite_ground user "$cpath" "$quote" >/dev/null 2>&1; then
    n_ok=$((n_ok + 1))
  fi
done <<EOF
$(cite_links_extract "$body")
EOF

if [ "$n_ok" -eq 0 ]; then
  refuse "GATE'S TASK HAS NO GROUNDED ASK: $task_md carries no citation of the user's own words that resolves — a gate must trace to a real, cited ask, and this task's body does not have one yet. Cite the user's ask in the task body first (task-body-is-human-authored's own subject), then add the gate."
fi

exit 0
