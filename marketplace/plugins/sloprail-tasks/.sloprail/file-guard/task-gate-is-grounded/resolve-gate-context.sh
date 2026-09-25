#!/usr/bin/env bash
# prepare for stage 2 of task-gate-is-grounded: hand the judge the gate file's
# own text, the sibling TASK.md's cited ask (the grounded quotes, resolved
# exactly the way task-body-is-human-authored's own prepare does), and the
# gate's kind (.sh or .md) — so judge-gate.md.j2 never has to read the tree or
# a transcript itself.
#
# Reached only once stage 1 (has-cited-body.sh) passed, so the sibling
# TASK.md is known to carry at least one grounded citation.
#
# Output nests under `additionalContext`. Emits .gate_path, .gate_kind
# (script|prompt), .gate_content (the gate file's own bytes), .cited_messages
# (the task's grounded ask, the ground truth), and .cited_ok.
#
# THE PREPARE CONTRACT: exit 0 with additionalContext proceeds to the judge; a
# non-zero exit fails the check closed.
set -uo pipefail

event="$(cat)"

path="$(printf '%s' "$event" | jq -r '.event.path // empty' 2>/dev/null)"
root="${SR_WORKSPACE:-.}"
gdir="${SR_GUARDRAIL_DIR:-.}"

lib="$gdir/../task-evidence-resolves/cite-links.sh"
if [ ! -f "$lib" ]; then
  echo "task-gate-is-grounded: cite-links.sh not found at $lib, so the cited ask could not be resolved" >&2
  exit 1
fi
# shellcheck source=../task-evidence-resolves/cite-links.sh
. "$lib"

# WHERE THE GATE'S OWN BYTES COME FROM — same Pre/Post dispatch every guard in
# this plugin uses.
kind="$(printf '%s' "$event" | jq -r '.event.kind // ""' 2>/dev/null)"
case "$kind" in
  PostFileCreate|PostFileUpdate)
    abs="$root/$path"
    gate_content="$(cat "$abs" 2>/dev/null || true)"
    ;;
  PreFileCreate|PreFileUpdate)
    known="$(printf '%s' "$event" | jq -r '.event.resultKnown // false' 2>/dev/null)"
    if [ "$known" != "true" ]; then
      gate_content=""
    else
      gate_content="$(printf '%s' "$event" | jq -r '.event.newContent // ""' 2>/dev/null)"
    fi
    ;;
  *)
    gate_content=""
    ;;
esac

case "$path" in
  *.sh) gate_kind="script" ;;
  *.md) gate_kind="prompt" ;;
  *)    gate_kind="unknown" ;;
esac

gates_dir="$(dirname "$path")"
task_dir="$(dirname "$gates_dir")"
task_md="$root/$task_dir/TASK.md"
task_content="$(cat "$task_md" 2>/dev/null || true)"

body="$(printf '%s\n' "$task_content" | awk '
  BEGIN { seen = 0 }
  NR == 1 && $0 == "---" { seen = 1; next }
  seen == 1 && $0 == "---" { seen = 2; next }
  seen == 1 { next }
  { print }
')"

cited_messages=""
while IFS="$(printf '\t')" read -r href quote; do
  [ -n "$href" ] || continue
  cpath="$(cite_link_href_path "$href")"
  case "$cpath" in
    /*) : ;;
    *)  cpath="$root/$cpath" ;;
  esac
  envelope=""
  if [ -f "$cpath" ]; then
    cite_out="$(sr-session trajectory cite --include-envelope --path "$cpath" "$quote" 2>/dev/null || true)"
    envelope="$(printf '%s\n' "$cite_out" | tail -n +3)"
  fi
  cited_messages="${cited_messages}--- the user said (cited ${href}):
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
$(cite_links_extract "$body")
EOF

cited_ok=false
[ -n "$cited_messages" ] && cited_ok=true

jq -n \
  --arg path "$path" \
  --arg kind "$gate_kind" \
  --arg gate "$gate_content" \
  --arg msgs "$cited_messages" \
  --argjson ok "$cited_ok" \
  '{additionalContext: {gate_path: $path, gate_kind: $kind, gate_content: $gate, cited_messages: $msgs, cited_ok: $ok}}'
