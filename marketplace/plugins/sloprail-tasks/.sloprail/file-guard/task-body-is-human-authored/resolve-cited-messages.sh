#!/usr/bin/env bash
# prepare for stage 2 of task-body-is-human-authored: hand the judge the user's own
# words the write cited, so judge-body.md.j2 never reads a transcript itself.
# Receives the SAME CheckPayload stage 1 (body-change-is-cited.sh) did.
#
# THE GROUND TRUTH is `.event.citations`, the `user`-pool entries: each is a quote
# the session already resolved to exactly one message the user wrote, with the
# transcript `path` and `line` it resolved to. The judge is shown each quote and
# its `path:line`; for a quote that is an AskUserQuestion answer, `cite
# --include-envelope` adds the question it answered, which the answer alone does
# not carry (a judge cannot weigh "the second option" without the question).
#
# SKIPS THE JUDGE when no grounding was required — a status/frontmatter-only change
# leaves the body byte-identical, and stage 1 permitted it without a citation — so
# no model call is spent on a write that changed nothing the judge rules on. Also
# skips an underivable Pre write (resultKnown false): the settled bytes are judged
# at Stop instead.
#
# Output nests under `additionalContext` (the one key the engine reads from a
# prepare): .cited_messages (the assembled ground truth), .cited_ok (whether any
# citation was found) and .body (the prose the judge rules on). `{"skip": true}`
# abstains. A non-zero exit fails the check closed.
set -uo pipefail

skip() { printf '{"skip": true}\n'; exit 0; }

command -v jq >/dev/null 2>&1 || {
  echo "task-body-is-human-authored: jq is not on PATH, so the cited messages could not be assembled" >&2
  exit 1
}

payload="$(cat)"
field() { printf '%s' "$payload" | jq -r "$1" 2>/dev/null; }

path="$(field '.event.path // empty')"
kind="$(field '.event.kind // ""')"

lib="${SR_GUARDRAIL_DIR:-.}/lib-body.sh"
if [ ! -f "$lib" ]; then
  echo "task-body-is-human-authored: lib-body.sh not found at $lib, so the cited messages could not be assembled" >&2
  exit 1
fi
# shellcheck source=lib-body.sh
. "$lib"

# The same kind dispatch as stage 1, so the two never disagree about which bytes
# are the body. resultKnown is consulted on both Pre kinds before newContent is read.
case "$kind" in
  PreFileCreate | PreFileUpdate)
    [ "$(field '.event.resultKnown // false')" = "true" ] || skip
    content="$(field '.event.newContent // ""')"
    ;;
  PostFileCreate | PostFileUpdate)
    abs="${SR_WORKSPACE:-.}/$path"
    [ -f "$abs" ] || skip
    content="$(cat "$abs")" || {
      echo "task-body-is-human-authored: could not read $path to assemble the judge's input" >&2
      exit 1
    }
    ;;
  *)
    skip
    ;;
esac

body="$(task_body "$content")"

# Grounding not required: the body is unchanged, so there is nothing to judge.
case "$kind" in
  PreFileUpdate | PostFileUpdate)
    [ "$body" = "$(task_body "$(field '.event.oldContent // ""')")" ] && skip
    ;;
esac

cites="$(user_citations "$payload")"

# One block per citation: the quote, where it resolved, and — for an answer to a
# question — the question behind it. The envelope lookup is best-effort: the quote
# already resolved (the session did it), so an empty envelope means a plain message.
cited_messages=""
while IFS= read -r c; do
  [ -n "$c" ] || continue
  quote="$(printf '%s' "$c" | jq -r '.quote')"
  cpath="$(printf '%s' "$c" | jq -r '.path')"
  cline="$(printf '%s' "$c" | jq -r '.line')"
  envelope=""
  if [ -f "$cpath" ]; then
    envelope="$(sr-session trajectory cite --include-envelope --path "$cpath" "$quote" 2>/dev/null | tail -n +3)"
  fi
  cited_messages="${cited_messages}--- the user said (cited ${cpath}:${cline}):
${quote}
"
  if [ -n "$envelope" ]; then
    cited_messages="${cited_messages}(this was an answer to a question; the full exchange was:)
${envelope}
"
  fi
  cited_messages="${cited_messages}
"
done <<EOF
$(printf '%s' "${cites:-[]}" | jq -c '.[]' 2>/dev/null)
EOF

cited_ok=false
[ -n "$cited_messages" ] && cited_ok=true

jq -n --arg msgs "$cited_messages" --argjson ok "$cited_ok" --arg body "$body" \
  '{additionalContext: {cited_messages: $msgs, cited_ok: $ok, body: $body}}'
