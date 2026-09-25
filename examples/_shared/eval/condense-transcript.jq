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
      # A plain `tostring | .[0:250]` on the WHOLE input truncates a
      # Write/Edit's file `content` field mid-string — the cut lands inside
      # the file body itself, which then reads exactly like the agent's
      # write got interrupted mid-sentence (measured: a trajectory-health
      # judge flagged a genuinely complete, well-formed file write as "the
      # Write tool call was truncated mid-content", when the actual on-disk
      # file was fine — the truncation was only in what the judge was shown).
      # Truncate the noisy bulk fields (content/new_string/old_string) on
      # their own, generously, with an explicit marker, and keep every other
      # input field (path, etc.) intact and untruncated.
      ((.input // {}) | with_entries(
        if (.key == "content" or .key == "new_string" or .key == "old_string")
           and (.value | type) == "string" and (.value | length) > 500
        then .value = (.value[0:500] + "...(truncated, " + ((.value | length) - 500 | tostring) + " more chars)")
        else . end)) as $shown_input |
      "TOOL_USE " + (.name // "?") + ": " + ($shown_input | tostring | .[0:1200])
    elif .type == "text" then
      "ASSISTANT: " + ((.text // "") | .[0:800])
    else empty end)
elif $m.role == "user" and ($m.content | type) == "string" then
  "USER: " + ($m.content | .[0:800])
else empty end
