#!/usr/bin/env bash
# Shared by the task-dependencies-resolve gate and its file-guard: one library, two thin entries.
# The gate entry reads the pending write (lib_init, then a Pre kind); the file-guard
# entry reads the Changeset and calls lib_check once per file, with `path`,
# `new_content` and `root` (the committed head) set. lib_check reads no event.

lib_setup() {
  set -uo pipefail

  refuse() {
    jq -n --arg reason "$1" '{reason: $reason}'
    exit 1
  }

  # $SR_GUARDRAIL_DIR is this guard's own folder, set by the engine
  # (internal/dispatch/exec.go) — never guessed from $PWD.
  gdir="${SR_GUARDRAIL_DIR:-.}"

  # The schema is the PLUGIN's, read from the plugin's own tree — never copied
  # into a consumer's .sloprail/schemas/ (see task-evidence-resolves/check-task.sh
  # for the same pattern). $gdir is this guard's own folder, two levels under the
  # plugin's .sloprail/, so ../../schemas/task.cue is the plugin's schemas/.
  schema="$gdir/../../schemas/task.cue"
  if [ ! -f "$schema" ]; then
    refuse "task-dependencies-resolve: schema not found at $schema — the rule cannot check anything without it."
  fi
}

# lib_init is the gate's: the pending write's own bytes.
lib_init() {
  lib_setup

  event="$(cat)"

  path="$(printf '%s' "$event" | jq -r '.event.path // empty' 2>/dev/null)"
  if [ -z "$path" ]; then
    refuse "task-dependencies-resolve: the event named no path, so there is nothing to check"
  fi

  # The tree the tasks are read from: the project's working tree, which is right for
  # the gate ONLY (it judges the pending write). A file-guard entry never calls
  # lib_init: it sets root to SR_TREE, the committed snapshot. No "." fallback.
  root="${SR_WORKSPACE:-}"
  [ -n "$root" ] || refuse "task-dependencies-resolve: SR_WORKSPACE is not set, so the project's tasks could not be read"

  kind="$(printf '%s' "$event" | jq -r '.event.kind // ""' 2>/dev/null)"
  [ -n "$kind" ] || { echo "task-dependencies-resolve: could not read the event's kind, so it could not be checked" >&2; exit 2; }
}

lib_check() {

# THIS task's own id, derived from its path:
# memories/tasks/<group>/<name>/TASK.md -> <group>/<name>.
self_id="$(printf '%s' "$path" | sed -E 's#^memories/tasks/([^/]+)/([^/]+)/TASK\.md$#\1/\2#')"

# A malformed document is task-evidence-resolves's refusal to make.
# A document sr-file cannot validate (malformed frontmatter, an artifact that
# fails the schema) is task-evidence-resolves's refusal to make, with its own
# reason — a DELIBERATE hand-off, not a fail-open: this rule has no dependencies to
# resolve in a document nobody can read, and refusing here would mask the schema
# reason the agent needs.
new_doc="$(printf '%s' "$new_content" | sr-file validate - --as .md --schema "$schema" --emit 2>/dev/null)" || return 0
new_status="$(printf '%s' "$new_doc" | jq -r '.status // empty' 2>/dev/null)"

case "$new_status" in
  to_do|in_progress|in_review) : ;;
  *) return 0 ;;
esac

deps="$(printf '%s' "$new_doc" | jq -r '(.depends_on // [])[]' 2>/dev/null)"
[ -n "$deps" ] || return 0   # no dependencies declared.

problems=""

# --------------------------------------------------- (1) unfinished ---
# "still exists" is the ONLY question — see the header comment for why this
# guard does not also try to distinguish "unfinished" from "never a real
# task": the Stop gate makes that distinction moot for depends_on itself.
while IFS= read -r dep; do
  [ -n "$dep" ] || continue
  if [ -d "$root/memories/tasks/$dep" ]; then
    problems="${problems}  depends_on \"$dep\" — memories/tasks/$dep still exists (not done yet)
"
  fi
done <<EOF
$deps
EOF

# --------------------------------------------------------- (2) cycles ---
# Build the whole depends_on graph from every TASK.md on disk, then overlay
# this pending write's own edges (self_id -> deps) so a cycle this very write
# would CREATE is caught before it lands, not just a pre-existing one.
graph_file="$(mktemp)"
# Every exit path (a refuse included) removes it, not only the normal one.
trap 'rm -f "$graph_file"' EXIT

if [ -d "$root/memories/tasks" ]; then
  while IFS= read -r -d '' f; do
    rel="${f#"$root"/}"
    id="$(printf '%s' "$rel" | sed -E 's#^memories/tasks/([^/]+)/([^/]+)/TASK\.md$#\1/\2#')"
    [ "$id" = "$self_id" ] && continue   # overlaid separately, below.
    doc="$(sr-file validate "$f" --schema "$schema" --emit 2>/dev/null)"
    [ -n "$doc" ] || continue
    printf '%s' "$doc" | jq -r --arg id "$id" '(.depends_on // [])[] as $d | "\($id)\t\($d)"' 2>/dev/null
  done < <(find "$root/memories/tasks" -mindepth 3 -maxdepth 3 -name 'TASK.md' -type f -print0 2>/dev/null)
fi >"$graph_file"

while IFS= read -r dep; do
  [ -n "$dep" ] || continue
  printf '%s\t%s\n' "$self_id" "$dep" >>"$graph_file"
done <<EOF
$deps
EOF

# DFS from self_id looking for a path back to self_id. AWK for a small,
# dependency-free graph walk (the graph is one project's task tree — not
# large enough to need anything heavier), with an explicit visited set to
# stay well-behaved on a graph that has OTHER cycles not touching self_id.
cycle_report="$(awk -F'\t' -v start="$self_id" '
  { adj[$1] = adj[$1] SUBSEP $2 }
  END {
    # iterative DFS with an explicit stack, since mawk/awk have no recursion
    # guarantees worth relying on here.
    n = 0
    stack[n] = start; n++
    pathstr[start] = start
    visited[start] = 1
    found = 0
    while (n > 0) {
      n--
      cur = stack[n]
      split(adj[cur], nbrs, SUBSEP)
      for (i in nbrs) {
        nb = nbrs[i]
        if (nb == "") continue
        if (nb == start) {
          print pathstr[cur] " -> " nb
          found = 1
          break
        }
        if (!(nb in visited)) {
          visited[nb] = 1
          pathstr[nb] = pathstr[cur] " -> " nb
          stack[n] = nb; n++
        }
      }
      if (found) break
    }
  }
' "$graph_file")"
rm -f "$graph_file"

if [ -n "$cycle_report" ]; then
  problems="${problems}  depends_on forms a CYCLE: $cycle_report
"
fi

if [ -n "$problems" ]; then
  tail="A task's depends_on lists <group>/<task-name> ids of OTHER tasks that must
be done (their folder deleted, by the reviewer, on approval) before this one
may leave backlog/blocked for to_do, in_progress or in_review. Wait for an
unfinished dependency, fix a mistyped id, or remove the edge that closes a
cycle — do not remove a dependency edge just to get past this without one of
those being true."
  refuse "DEPENDENCIES NOT SATISFIED: $path ($self_id) cannot move to $new_status.

$problems
$tail"
fi

return 0
}

check_dependencies_lib_loaded=1
