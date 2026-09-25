# Condenses a raw Claude Code transcript (.jsonl, one JSON object per line,
# read with `jq -r -f` so each line is processed independently rather than
# slurped) into a compact, readable narrative for a trajectory-health judge.
#
# Measured necessary: a real fixture's raw transcript (a ~14k-line repo,
# extensive tool output) ran ~600KB / ~230K tokens — well past a judge
# model's context window ("Prompt is too long ... 231714 tokens (limit
# 200000)"). Every entry's cache/token/attachment/diagnostic bookkeeping
# fields (repeated on nearly every line) are pure noise for "did this look
# stuck" — this keeps only what a person skimming the session would actually
# read: the human prompt, the agent's own prose, which tool it called with
# what, and a truncated look at what came back.
#
# Each output line is prefixed by its role so a judge (or a person) can scan
# for repetition — the same TOOL_USE line appearing 4+ times in a row is
# exactly the retry-loop shape trajectory-health.md asks the judge to flag.
select(.type == "user" or .type == "assistant") |
(.message // {}) as $m |
if $m.role == "user" and ($m.content | type) == "array" then
  ($m.content[]? | select(.type == "tool_result") |
    "TOOL_RESULT: " + ((.content | if type == "string" then . else ([.[]? | .text?] | join(" ")) end) // "" | tostring | .[0:300]))
elif $m.role == "assistant" and ($m.content | type) == "array" then
  ($m.content[]? |
    if .type == "tool_use" then
      "TOOL_USE " + (.name // "?") + ": " + ((.input // {}) | tostring | .[0:250])
    elif .type == "text" then
      "ASSISTANT: " + ((.text // "") | .[0:800])
    else empty end)
elif $m.role == "user" and ($m.content | type) == "string" then
  "USER: " + ($m.content | .[0:800])
else empty end
