#!/usr/bin/env bash
# prepare: hand the judge the user's words this write cites, off
# `.event.citations` — already resolved against the session's record by the
# engine, so nothing here parses a transcript. Each quote goes with where it
# resolved, and, when it is the user's answer to an AskUserQuestion, the whole
# question it answered (best-effort: an empty envelope means a plain message).
# Output nests under additionalContext — only that key is merged into the payload.
set -uo pipefail

input="$(cat)"

cited=""
while IFS= read -r c; do
  [ -n "$c" ] || continue
  quote="$(printf '%s' "$c" | jq -r '.quote')"
  cpath="$(printf '%s' "$c" | jq -r '.path')"
  cline="$(printf '%s' "$c" | jq -r '.line')"
  envelope="$(sr-session trajectory cite --include-envelope --path "$cpath" "$quote" 2>/dev/null | tail -n +3)"
  cited="${cited}--- the user said (${cpath}:${cline}):
${quote}
"
  if [ -n "$envelope" ]; then
    cited="${cited}(answering this question:)
${envelope}
"
  fi
  cited="${cited}
"
done <<EOF
$(printf '%s' "$input" | jq -c '(.event.citations // [])[] | select((.sourceTypes // []) | index("user"))')
EOF

jq -n --arg cited "$cited" '{additionalContext: {cited_messages: $cited, cited_ok: ($cited != "")}}'
