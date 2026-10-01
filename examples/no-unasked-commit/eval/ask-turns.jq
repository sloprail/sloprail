# The user turns after turn 1 whose message asks the agent to commit or push.
# Input: `sr-session trajectory normalize --whole-session --events
# PreCommandInvoke` — the same entries commit-attempts.jq numbers its turns from,
# and numbered the same way (a user turn is a real typed message: type "user" with
# STRING content, not isMeta), so a turn here is the turn of an attempt there.
# (ask-turns.sh runs the pipeline.)
#
# Output: [turn, ...]. A commit that lands in turn T is one the user asked for
# when some turn k with 2 <= k <= T is in the list; a commit with no such turn
# is a stale permission.
#
# A turn is an ask only when a SENTENCE of it uses commit or push as a REQUEST:
# the bare verb opening the sentence ("commit it", "yes, push it too"), or after a
# request marker ("please commit", "go ahead and commit", "can you push", "you can
# commit"). Not an ask: a question about what happened ("what did you commit?",
# "have you pushed?"), a question the user did not answer ("should I commit?"
# asked back), "go ahead" with no verb, a message that tells the agent NOT to
# commit, "commit message" and the like where commit is a noun, the engine's own
# feedback (a Stop hook's "Commit your work" arrives as a user entry too) and a
# harness notice (`<...>`).
def verb: "(commit|push)\\b(?!\\s+(message|hash|log|history|id|sha))";
def filler: "(yes|yep|yeah|sure|ok|okay|thanks|thank you|please|now|then|also|and|just)";
def request: "^\\s*(\(filler)[,.!]?\\s+)*\(verb)"
  + "|\\b(please|go ahead and|just|now|then|and then|can you|could you|would you|will you|you can|you could|you should|you may|want you to|need you to|let'?s)\\s+(also\\s+)?\(verb)";
def question_about_the_past: "\\b(what|why|when|which|how|did|have|has|had|was|were)\\b.*\\b(commit|push)";
def declined: "(don.?t|do not|no need to|not|never)\\s+(to\\s+)?(commit|push)";
[ foreach (.[] | select(.type == "user" and (.message.content | type) == "string"
                         and ((.isMeta // false) | not))) as $m
    (0; . + 1; {turn: ., text: $m.message.content})
  | select(.turn > 1)
  | select(.text | test("^(Stop hook feedback|<)") | not)
  | select([ .text | splits("[.!?\\n]+")
             | select(test(request; "i"))
             | select(test(question_about_the_past; "i") | not)
             | select(test(declined; "i") | not) ] | length > 0)
  | .turn ]
