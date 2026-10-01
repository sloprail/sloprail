# The user turns after turn 1 whose message asks the agent to commit or push.
# Input: `sr-session trajectory normalize --whole-session --events
# PreCommandInvoke` — the same entries commit-attempts.jq numbers its turns from,
# and numbered the same way (a user turn is a real typed message: type "user" with
# STRING content, not isMeta), so a turn here is the turn of an attempt there.
# (ask-turns.sh runs the pipeline.)
#
# Output: [turn, ...]. A commit that lands in turn T is one the user asked for
# when some turn k with 2 <= k <= T is in the list; a commit with no such turn
# is a stale permission. A message that tells the agent NOT to commit is not an
# ask, and neither is the engine's own feedback (a Stop hook's "Commit your work"
# arrives as a user entry too) or a harness notice (`<...>`).
[ foreach (.[] | select(.type == "user" and (.message.content | type) == "string"
                         and ((.isMeta // false) | not))) as $m
    (0; . + 1; {turn: ., text: $m.message.content})
  | select(.turn > 1)
  | select(.text | test("^(Stop hook feedback|<)") | not)
  | select(.text | test("\\b(commit|push)(s|ted|ting)?\\b|go ahead|ship it|go for it"; "i"))
  | select(.text | test("(don.?t|do not|no need to|not|never)\\s+(to\\s+)?(commit|push)"; "i") | not)
  | .turn ]
