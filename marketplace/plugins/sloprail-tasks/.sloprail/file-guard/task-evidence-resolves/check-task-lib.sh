#!/usr/bin/env bash
# Shared by the task-evidence-resolves gate and its file-guard: one library, two thin entries.
# The gate entry reads the pending write (lib_init, then a Pre kind); the file-guard
# entry reads the Changeset and calls lib_check once per file, with `path`, `content`
# and `root` (the committed head) set. lib_check reads no event.

lib_setup() {
  set -uo pipefail

  # refuse emits the structured `{"reason": ...}` and exits non-zero, in the CURRENT
  # shell (never behind a pipe — `something | refuse` would run refuse in a subshell
  # and its exit would not stop the script, silently PERMITTING). Callers build the
  # reason into a variable first, with a QUOTED heredoc tail joined to a dynamic head.
  refuse() {
    jq -n --arg reason "$1" '{reason: $reason}'
    exit 1
  }

  command -v jq >/dev/null 2>&1 || {
    echo "task-evidence-resolves could not run: it needs jq, which is not on PATH. Refusing, because a check that could not run has not approved the write." >&2
    exit 1
  }

  # $SR_GUARDRAIL_DIR is this guard's own folder, so cite-links.sh resolves beside
  # this script. It is set by the engine (internal/dispatch/exec.go) — never
  # guessed from $PWD.
  gdir="${SR_GUARDRAIL_DIR:-.}"

  # The schema is the PLUGIN's, read from the plugin's own tree — never copied
  # into a consumer's .sloprail/schemas/. $gdir is this guard's own folder, two
  # levels under the plugin's .sloprail/, so ../../schemas/task.cue is the
  # plugin's schemas/.
  schema="$gdir/../../schemas/task.cue"
  if [ ! -f "$schema" ]; then
    refuse "task-evidence-resolves: schema not found at $schema — the rule cannot check anything without it."
  fi

  lib="$lib_dir/cite-links.sh"
  if [ ! -f "$lib" ]; then
    refuse "task-evidence-resolves: cite-links.sh not found beside this hook at $lib, so no artifact could be resolved"
  fi
  # A helper stopped by a syntax error runs only up to it (whether the `.` then
  # fails depends on the bash version); only its last-line sentinel proves it
  # loaded whole.
  unset cite_links_loaded
  # shellcheck source=cite-links.sh
  . "$lib"
  [ "${cite_links_loaded:-}" = 1 ] \
    || refuse "task-evidence-resolves: cite-links.sh did not load whole (its last-line sentinel cite_links_loaded is unset), so no artifact could be resolved"
}

# lib_init is the gate's: the pending write's own bytes.
lib_init() {
  lib_setup

  payload="$(cat)"
  field() { printf '%s' "$payload" | jq -r "$1" 2>/dev/null; }

  path="$(field '.event.path // empty')"
  if [ -z "$path" ]; then
    refuse "task-evidence-resolves: the event named no path, so there is nothing to check"
  fi

  # The tree artifacts resolve against: the project's working tree, which is right
  # for the gate ONLY. A file-guard entry never calls lib_init: it sets root to
  # SR_TREE, the committed snapshot. No "." fallback.
  root="${SR_WORKSPACE:-}"
  [ -n "$root" ] || refuse "task-evidence-resolves: SR_WORKSPACE is not set, so the project's tree could not be read"
}

lib_check() {

# THE SCHEMA. --emit prints the validated frontmatter as JSON on success, which is
# what lets the lists be read below without parsing these bytes a second time.
if ! doc="$(printf '%s' "$content" | sr-file validate - --as .md --schema "$schema" --emit 2>&1)"; then
  detail="$(printf '%s' "$doc" | sed "s|^-:|$path:|g")"
  # The static tail is a QUOTED heredoc (no expansion); the dynamic head is joined
  # by an ordinary double-quoted string. $detail is DATA in a quoted expansion, so a
  # `$(...)` or backtick in the validator output cannot execute.
  IFS= read -r -d '' tail <<'EOF' || true

A task's frontmatter is exactly:

    ---
    status: backlog | to_do | in_progress | in_review | blocked
    priority: P0 | P1 | P2 | P3
    artifacts:  ["src/foo.go:10-60"]          # repo-relative, in_review only
    depends_on: ["<group>/<task-name>"]       # optional
    ---

There is no `done`. A task the reviewer approves is DELETED, folder and all, by
the reviewer — so an agent marking its own work done is the one move this schema
exists to refuse. There is no transcript path in a task either: the proof that
the work happened is tool output cited on the in_review write
(sr-file … --cite:tool_result '<exact output>', or a Sloprail-Cites-Tool: trailer
in the commit), never a field in the file.
EOF
  refuse "TASK FRONTMATTER INVALID: $path does not satisfy .sloprail/schemas/task.cue.

$detail
$tail"
fi

status="$(printf '%s' "$doc" | jq -r '.status // empty' 2>/dev/null)"

# EVERY ARTIFACT MUST RESOLVE in the tree at $root, in whatever status it appears. A
# fabricated artifact in an in_progress task is as false as one in a review.
art_lines="$(printf '%s' "$doc" | jq -r '(.artifacts // [])[]' 2>/dev/null)"
n_art="$(printf '%s' "$doc" | jq -r '(.artifacts // []) | length' 2>/dev/null)"
problems=""
i=0
while IFS= read -r art; do
  [ -n "$art" ] || continue
  if reason="$(artifact_resolve "$art" "$root")"; then :; else
    problems="${problems}  artifacts[$i] ${reason}
"
  fi
  i=$((i + 1))
done <<EOF
$art_lines
EOF

if [ -n "$problems" ]; then
  IFS= read -r -d '' tail <<'EOF' || true
An ARTIFACT is <repo-relative-file>:<ranges> pointing at a file the work produced
or changed, at the lines that changed, and must exist in the tree. Fix the paths
or the ranges.
EOF
  refuse "EVIDENCE DOES NOT RESOLVE: $path names artifacts that are not there.

$problems
$tail"
fi

[ "$status" = "in_review" ] || return 0

# The proof that the work happened — tool output cited on the move into
# in_review — is the guard's declared `require`, conditioned on
# `in-review.sh --entering`; it never reaches this script uncited. What is left
# here is where the result is.
if [ "${n_art:-0}" -eq 0 ]; then
  refuse "EVIDENCE REQUIRED: $path is in_review but names no artifacts. An in_review task claims the work is finished; the reviewer weighs that claim against evidence, so it must say where the result is — the files the work produced or changed, repo-relative, at the lines that changed:  artifacts: [\"src/foo.go:10-60\"]"
fi

return 0
}

check_task_lib_loaded=1
