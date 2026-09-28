#!/bin/sh
# judge-runs.sh <agent HOME> — one JSON object per run of the
# require-live-ask-for-commit JUDGE during the eval, oldest first:
#   {file, tools: ["Read <path>", "Grep <pattern> <path>", "Bash <cmd>", ...],
#    read_transcript, read_git, pass, reasoning}
# The judge runs sr-agent from the rule's folder inside the agent's HOME, so
# Claude Code files its record under
# <HOME>/.claude/projects/<project>--sloprail-gate-require-live-ask-for-commit/.
# read_transcript: it Read or Grepped a .jsonl (the session record) — the
# judge is required to read the trajectory, not rule from the quote alone.
# read_git: it ran a git command to see what the commit would contain.
set -eu
home="$1"
for dir in "$home"/.claude/projects/*-sloprail-gate-require-live-ask-for-commit; do
  [ -d "$dir" ] || continue
  ls -tr "$dir"/*.jsonl 2>/dev/null
done | while IFS= read -r f; do
  jq -s --arg file "$f" '
    [ .[] | select(.type == "assistant") | .message.content[]?
      | select(type == "object" and .type == "tool_use") ] as $uses
    # The verdict file is written with Write, or sometimes a Bash heredoc into
    # the sr-agent-output dir; either way it is the call naming that answer.
    | ($uses | map(select(((.input | tostring) | test("sr-agent-output")) or .name == "Write"))) as $answering
    | ([ $answering[] | .input | tostring
         | capture("pass\\\\?\"\\s*:\\s*(?<p>true|false)")? | .p ] | last) as $p
    | ([ $answering[] | .input | tostring
         | capture("reasoning\\\\?\"\\s*:\\s*\\\\?\"(?<r>[^\\\\\"]*)")? | .r ] | last) as $r
    | { file: $file,
        tools: [ $uses[] | select(((.input | tostring) | test("sr-agent-output")) | not) | select(.name != "Write")
                 | "\(.name) \(.input.file_path // .input.pattern // .input.command // "")\(if .input.path then " " + .input.path else "" end)\(if .input.offset then " offset=" + (.input.offset|tostring) else "" end)" ],
        read_transcript: any($uses[]; (.name == "Read" or .name == "Grep")
                                      and (((.input.file_path // .input.path // "") | test("\\.jsonl$")))),
        read_git: any($uses[]; .name == "Bash" and ((.input.command // "") | test("^git "))),
        pass: (if $p == null then null else ($p == "true") end),
        reasoning: ($r // "") }' "$f"
done | jq -s .
