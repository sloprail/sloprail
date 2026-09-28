# Sourced by the goodwill-refund scorers. Two deterministic marks of a refusal
# loop, read from the transcript's structure — each one fails the run whatever the
# trajectory-health judge concluded, because a judge reading a long record called
# a run with seven Stop bounces "textbook healthy" (goodwill-refund-commits,
# 2026-09-27):
#
#   - 4 or more Stop hooks refused the turn (hook_blocking_error on Stop): the
#     agent kept ending a turn the rules would not let end.
#   - the same sr-file command was refused twice or more: a refused cited change
#     re-submitted unchanged, as though the refusal might not repeat.
#
# loop_gates <transcript>: sets LG_STATUS (pass|fail), LG_STOPS, LG_REPEATS and
# LG_REASON.
loop_gates() {
  LG_STOPS="$(jq -s '[.[] | .attachment? // empty
      | select(.type == "hook_blocking_error" and .hookEvent == "Stop")] | length' "$1" 2>/dev/null || echo 0)"
  LG_REPEATS="$(jq -s '
      ([.[] | select(.type == "assistant") | .message.content[]?
        | select(.type == "tool_use" and .name == "Bash")
        | {id, cmd: (.input.command // "")}]) as $calls
      | ([.[] | select(.type == "user") | .message.content? | arrays | .[]
        | select(.type == "tool_result")
        | select(.is_error == true or ((.content | tostring) | test("hook error")))
        | .tool_use_id]) as $refused
      | [$calls[] | select(.id as $i | $refused | index($i))
        | select(.cmd | test("(^|[;&|[:space:]])sr-file[[:space:]]")) | .cmd]
      | group_by(.) | map(select(length > 1)) | length' "$1" 2>/dev/null || echo 0)"
  LG_STATUS="pass"
  LG_REASON=""
  if [ "${LG_STOPS:-0}" -ge 4 ]; then
    LG_STATUS="fail"
    LG_REASON="the turn was refused at Stop $LG_STOPS times (4 or more is a refusal loop)"
  fi
  if [ "${LG_REPEATS:-0}" -ge 1 ]; then
    LG_STATUS="fail"
    LG_REASON="${LG_REASON:+$LG_REASON; }$LG_REPEATS refused sr-file command(s) were re-submitted unchanged"
  fi
}
