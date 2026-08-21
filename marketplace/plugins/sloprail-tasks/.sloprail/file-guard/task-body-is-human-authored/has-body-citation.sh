#!/usr/bin/env bash
# Stage 1 of task-body-is-human-authored: the DETERMINISTIC half, no model.
#
# The body (the prose after the frontmatter) must carry at least one
# `[quote](jsonl-path)` citation LINK, and every one must GROUND — the quote must
# resolve, via `sr-session trajectory cite`, to the user's own words in the named
# transcript. cite is the SINGLE validator: it already excludes tool results AND
# harness-injected user-role messages (<system-reminder>/<task-notification>/…), so
# this script walks no transcript and filters no tags — the substitution the old
# rule guarded against by hand (citing a tool result or an injected message as the
# human's ask) is now cite's job. No citation, or a citation that does not ground:
# refused HERE, and the stage-2 judge is never paid for.
#
# THE REFUSAL CONTRACT (internal/dispatch/exec.go): exit 0 permits; non-zero
# refuses with `{"reason": "..."}` on stdout. Fails CLOSED throughout — it reads
# the event, the filesystem and cite, none of which can flake for a reason
# unrelated to the task.
set -uo pipefail

# refuse emits `{"reason": ...}` and exits non-zero, in the CURRENT shell — never
# behind a pipe (`… | refuse` runs it in a subshell and its exit would not stop the
# script, silently PERMITTING). Callers build the reason into a variable first: a
# STATIC tail via a QUOTED heredoc (no expansion) joined to a dynamic head in an
# ordinary double-quoted string. A double-quoted `$var` expansion is not recursive,
# so a `$(...)` or backtick inside a task body or a user quote is inert data, never
# executed.
refuse() {
  jq -n --arg reason "$1" '{reason: $reason}'
  exit 1
}

event="$(cat)"

# Flat event fields.
path="$(printf '%s' "$event" | jq -r '.event.path // empty' 2>/dev/null)"
if [ -z "$path" ]; then
  refuse "task-body-is-human-authored: the event named no path, so there is nothing to judge"
fi

root="${SR_WORKSPACE:-.}"
gdir="${SR_GUARDRAIL_DIR:-.}"

# cite-links.sh is SOURCED from the sibling task-evidence-resolves guard, not
# reimplemented. The body guard and the evidence guard must agree exactly about
# what a citation link IS and how it grounds; two copies would drift and a task
# would satisfy one and be refused by the other.
lib="$gdir/../task-evidence-resolves/cite-links.sh"
if [ ! -f "$lib" ]; then
  refuse "task-body-is-human-authored: cite-links.sh not found at $lib — the deterministic half cannot run without the sibling guard's citation library"
fi
# shellcheck source=../task-evidence-resolves/cite-links.sh
. "$lib"

# WHERE THE BYTES COME FROM depends on the kind. resultKnown is consulted on BOTH
# Pre kinds before newContent is read — an underivable result is deferred to the
# Post kind (exit 0), which the Stop after-check judges.
kind="$(printf '%s' "$event" | jq -r '.event.kind // ""' 2>/dev/null)"
case "$kind" in
  PostFileCreate|PostFileUpdate)
    abs="$root/$path"
    [ -f "$abs" ] || exit 0
    content="$(cat "$abs" 2>/dev/null)" || exit 0
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

# THE BODY IS THE PROSE AFTER THE FRONTMATTER. The frontmatter is
# task-evidence-resolves's subject; re-reading it here would be two rules drifting
# apart. A file with no frontmatter yields the whole file as the body — the honest
# reading: everything in it is prose the agent wrote.
body="$(printf '%s\n' "$content" | awk '
  BEGIN { seen = 0 }
  NR == 1 && $0 == "---" { seen = 1; next }
  seen == 1 && $0 == "---" { seen = 2; next }
  seen == 1 { next }
  { print }
')"

body_trimmed="$(printf '%s' "$body" | tr -d '[:space:]')"
if [ -z "$body_trimmed" ]; then
  IFS= read -r -d '' tail <<'EOF' || true

A task's body states the human's ask and cites their own words for it:

    The user asked to [migrate the auth module](/abs/session.jsonl:120).

Write the ask in the user's own terms and attach the citation link.
EOF
  refuse "TASK BODY IS EMPTY: $path has no body to attribute to anyone.
$tail"
fi

# EVERY CITATION LINK IN THE BODY MUST GROUND. cite is the authority — a quote that
# is a paraphrase, a fabrication, a tool result, or a harness-injected message all
# fail it. This is the substitution the rule exists to prevent, now enforced by
# cite rather than a hand-rolled entry-type check.
problems=""
n_ok=0
while IFS="$(printf '\t')" read -r href quote; do
  [ -n "$href" ] || continue
  cpath="$(cite_link_href_path "$href")"
  case "$cpath" in
    /*) : ;;
    *)  cpath="$root/$cpath" ;;
  esac
  if reason="$(cite_ground "$cpath" "$quote")"; then
    n_ok=$((n_ok + 1))
  else
    problems="${problems}  ${reason}
"
  fi
done <<EOF
$(cite_links_extract "$body")
EOF

if [ "$n_ok" -eq 0 ] && [ -z "$problems" ]; then
  # No links at all: the body is not attributed.
  IFS= read -r -d '' tail <<'EOF' || true

A task body must be derived from what the user actually said, and must quote and
link it:

    The user asked to [their exact words](/abs/session.jsonl:120).

The quote must resolve — via sr-session trajectory cite — to a real user message.
This is the one rule protecting the specification itself: an ask nobody can trace
to a human is indistinguishable from an ask an agent invented, and once the task
is softened every later check passes against the softened version. Cite the
message and state the ask in the user's own terms.
EOF
  refuse "TASK BODY IS NOT ATTRIBUTED: $path carries no citation of a user message.
$tail"
fi

if [ -n "$problems" ]; then
  IFS= read -r -d '' tail <<'EOF' || true
A body citation is a markdown link [<quote>](<jsonl-path>) whose quote is the
user's own words, verbatim, and whose href is the transcript they said them in. It
must resolve — via cite — to a real user message. A paraphrase, a fabrication, a
tool result, or a harness-injected message is not the human's ask. Cite the line
where the user actually spoke.
EOF
  refuse "TASK BODY CITES SOMETHING THE USER DID NOT SAY: $path

$problems
$tail"
fi

exit 0
