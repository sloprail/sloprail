# Shared by task-body-is-human-authored's scripts (body-changed.sh, body-is-stated.sh,
# resolve-cited-messages.sh), so they never disagree about what the body IS.
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
