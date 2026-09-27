# Shared by task-body-is-human-authored's script check (body-change-is-cited.sh)
# and its judge's prepare (resolve-cited-messages.sh), so the two never disagree
# about what the body IS or which citations ground it.
#
# A pure library: sourced, never run. Nothing here exits, reads stdin or
# dispatches on an event kind — each function prints and returns, and the calling
# script decides what a failure means.

# task_body "<file content>"  ->  the prose after the frontmatter.
#
# The frontmatter is task-evidence-resolves's subject; this guard judges only the
# ask written beneath it. A file with no frontmatter yields the whole file — the
# honest reading: everything in it is prose the agent wrote.
task_body() {
  printf '%s\n' "$1" | awk '
    BEGIN { seen = 0 }
    NR == 1 && $0 == "---" { seen = 1; next }
    seen == 1 && $0 == "---" { seen = 2; next }
    seen == 1 { next }
    { print }
  '
}

# user_citations "<check payload>"  ->  a JSON array of the event's citations whose
# sourceTypes include `user` — the user's own words.
#
# The session resolved every one against its record before any rule ran, so each
# entry EXISTS (a quote that resolved nowhere is simply absent). Whether the words
# actually ground this body is the judge's question, not this function's. A
# tool_result citation is not the ask and is not counted.
user_citations() {
  printf '%s' "$1" | jq -c '[(.event.citations // [])[] | select(((.sourceTypes // []) | index("user")) != null)]' 2>/dev/null
}
