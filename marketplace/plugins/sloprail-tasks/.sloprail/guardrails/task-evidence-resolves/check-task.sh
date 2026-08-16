#!/bin/sh
# Refuses a TASK.md whose frontmatter does not satisfy this rule's task.cue, or
# whose citations name something that is not there.
#
# The schema check is `sr-file validate` — the product's own command, which knows
# that the document in a .md is its frontmatter and vets it against CUE. This
# script parses no frontmatter of its own: it decides WHETHER the rule applies,
# hands the bytes over, and then asks the questions CUE structurally cannot.
#
# Exit 0 permits, non-zero refuses. Fails closed throughout — everything it needs
# is local, so a failure here is a defect to surface rather than a flaky model
# call to permit past.

set -u

event="$(cat)"

path="$(printf '%s' "$event" | jq -r '.event.fields.path // empty' 2>/dev/null)"
if [ -z "$path" ]; then
  # The matcher should have kept such an event away, so arriving here means the
  # declaration and this script disagree about what was bound.
  echo "task-evidence-resolves: the event named no path, so there is nothing to judge" >&2
  exit 1
fi

root="${SR_WORKSPACE:-.}"

# The schema ships with this rule, so it is looked for where the rule landed.
#
# SR_PLUGIN_ROOT is the plugin's installation when this rule came from one, and
# is UNSET for a project's own copy — so the fallback selects the tree being
# guarded. Everything after it is identical either way, because a plugin lays
# its sloprail things out exactly as a project does; the variable chooses the
# TREE, never the shape of the path inside it.
#
# Not $SR_WORKSPACE/... unconditionally, which is what this said while the rule
# lived in one repo: a plugin's consumer never had that file, so the rule would
# refuse every TASK.md with "schema not found" the moment it was installed.
schema="${SR_PLUGIN_ROOT:-$root}/.sloprail/schemas/task.cue"
if [ ! -f "$schema" ]; then
  echo "task-evidence-resolves: schema not found at $schema — the rule cannot check anything without it" >&2
  exit 1
fi

# Where this rule's own files are. SR_GUARDRAIL_DIR carries what the payload also
# holds as guardrailDir, without spending a jq call to answer "where am I" — and
# without the ordering trap of needing a parser before the parser is sourced.
lib="${SR_GUARDRAIL_DIR:-.}/citations.sh"
if [ ! -f "$lib" ]; then
  echo "task-evidence-resolves: citations.sh not found beside this hook, so no citation could be resolved" >&2
  exit 1
fi
# shellcheck source=citations.sh
. "$lib"

# WHERE THE BYTES COME FROM depends on the kind, and the two are opposites.
#
# A Pre event carries `content` because the write has not happened — $path does
# not exist yet (or still holds the OLD bytes, validating which would pass a file
# whose replacement is about to violate the schema).
#
# A Post event carries no content: the write HAS happened, so the file on disk is
# the answer, and the better one — it is what actually landed rather than what was
# predicted.
kind="$(printf '%s' "$event" | jq -r '.event.kind // ""' 2>/dev/null)"
case "$kind" in
  Post*)
    abs="$root/$path"
    # Written and then removed within the cycle: nothing to check, nothing wrong.
    [ -f "$abs" ] || exit 0
    content="$(cat "$abs" 2>/dev/null)" || exit 0
    ;;
  PreFileUpdate)
    # AN UPDATE CARRIES ITS BYTES IN `result`, NOT `content`.
    #
    # This branch did not exist, and its absence was the single worst defect in
    # this rule: PreFileUpdate fell into the `*)` case below, which asks for a
    # `content` field an update never has, found none, and exited 0. So every
    # UPDATE to a task was unguarded at Pre time — measured end to end, a task
    # rewritten to `status: done` with no evidence at all was permitted, and the
    # rule looked installed the whole time.
    #
    # `resultKnown` is the guard, and it is checked BEFORE reading `result`,
    # because a declared field the event omits arrives as its zero value: an
    # update whose outcome the engine could not derive is indistinguishable from
    # one that emptied the file. Deferred rather than refused, because the Post
    # binding judges what actually landed.
    if [ "$(printf '%s' "$event" | jq -r '.event.fields.resultKnown // false' 2>/dev/null)" != "true" ]; then
      exit 0
    fi
    content="$(printf '%s' "$event" | jq -r '.event.fields.result // ""' 2>/dev/null)"
    ;;
  *)
    # PreFileCreate. ABSENT content is not empty content. A create whose bytes
    # the engine could not derive OMITS the field; a `touch` produces it as the
    # empty string. Read with `// ""` both arrive as "" and the underivable one
    # would be refused for having no frontmatter — a verdict on bytes nobody has
    # seen.
    #
    # Skipped rather than refused, because the Post binding judges the same file a
    # moment later against what actually landed.
    if [ "$(printf '%s' "$event" | jq -r 'has("event") and (.event.fields | has("content"))' 2>/dev/null)" != "true" ]; then
      exit 0
    fi
    content="$(printf '%s' "$event" | jq -r '.event.fields.content' 2>/dev/null)"
    ;;
esac

# THE SCHEMA. --emit prints the validated frontmatter as JSON on success, which is
# what lets the citation checks below read the fields without parsing these bytes
# a second time in a second language.
if ! doc="$(printf '%s' "$content" | sr-file validate - --as .md --schema "$schema" --emit 2>&1)"; then
  detail="$(printf '%s' "$doc" | sed "s|^-:|$path:|g")"
  cat >&2 <<EOF
TASK FRONTMATTER INVALID: $path does not satisfy $schema.

$detail

A task's frontmatter is exactly:

    ---
    status: backlog | to_do | in_progress | in_review | blocked
    priority: P0 | P1 | P2 | P3
    ---

There is no \`done\`. A task the reviewer approves is DELETED, folder and all,
by the reviewer — so an agent marking its own work done is the one move this
schema exists to refuse.
EOF
  exit 1
fi

status="$(printf '%s' "$doc" | jq -r '.status // empty' 2>/dev/null)"

# THE EVIDENCE IS MANDATORY IN in_review, and the schema cannot say so without
# turning the failure into a unification conflict. See GUARDRAIL.md.
if [ "$status" = "in_review" ]; then
  n_obs="$(printf '%s' "$doc" | jq -r '(.observations // []) | length' 2>/dev/null)"
  n_art="$(printf '%s' "$doc" | jq -r '(.artifacts // []) | length' 2>/dev/null)"
  if [ "${n_obs:-0}" -eq 0 ] || [ "${n_art:-0}" -eq 0 ]; then
    cat >&2 <<EOF
EVIDENCE REQUIRED: $path is in_review and must carry BOTH lists.

    observations: ["/abs/session.jsonl:120-140"]   proof the work happened
    artifacts:    ["/abs/src/foo.go:10-60"]        where the result is

observations cite the session record — the test run that came back green, the
command whose output is the evidence. Positions in a record, not a summary
saying it went well, because a summary is exactly what gets written when it did
not.

artifacts cite the files the work produced, with line ranges wherever a range
can be given, so the reviewer reads the lines rather than hunting for them.

A task cannot enter review without both. Attach them and retry.
EOF
    exit 1
  fi
fi

# EVERY CITATION MUST RESOLVE, in whatever status they appear — a stale range in
# a to_do task is as unfollowable as one in a review.
#
# `-c` on jq so each citation is one line: a path may contain spaces, which a
# bare word-split would tear in half.
problems=""

for c in $(printf '%s' "$doc" | jq -r '(.artifacts // [])[]' 2>/dev/null | tr ' ' '\001'); do
  citation="$(printf '%s' "$c" | tr '\001' ' ')"
  if reason="$(cite_check "$citation" "artifacts")"; then :; else
    problems="${problems}  ${reason}
"
  fi
done

# observations get the STRICTER check: the cited entry must be a real user
# message, not a tool result the agent produced itself.
for c in $(printf '%s' "$doc" | jq -r '(.observations // [])[]' 2>/dev/null | tr ' ' '\001'); do
  citation="$(printf '%s' "$c" | tr '\001' ' ')"
  if reason="$(cite_check "$citation" "observations")"; then :; else
    problems="${problems}  ${reason}
"
  fi
done

if [ -n "$problems" ]; then
  cat >&2 <<EOF
CITATION DOES NOT RESOLVE: $path names evidence that is not there.

$problems
A citation is <absolute-path>:<ranges>, ranges being N or N-M, several of them
comma-separated. It has to point at something a reviewer can open and read. Fix
the paths or the ranges and retry.
EOF
  exit 1
fi

exit 0
