# proposal.jq — what "the findings" are in this example: a proposal section.
# NOTES.md's convention is to research real prior art BEFORE proposing an
# approach there, so a write that ADDS such a section is the proposal, whether
# or not the run declared #research.
#
# A section is a line that STARTS with the proposal's title, in any letter
# case — "Proposed approach(es)", "Proposed solution/design/plan", "Proposal",
# "Recommended approach", "Recommendation(s)" — and is marked as a title:
#
#   a heading           ## Proposed approach: exponential backoff with jitter
#                       ## 1. Proposed approach      <h2>Proposed approach</h2>
#   an emphasised label **Proposed approach:** use backoff    *Proposed approach*
#                       - **Proposed approach:** …
#   a label             Proposed approach: use backoff        - Recommendation: …
#   the title alone     Proposed approach
#
# A sentence that merely contains the words ("The proposed approach will come
# after research.", "Proposal to follow.") is not a section.
def proposal_title:
  "(propos(ed\\s+(approach(es)?|solutions?|designs?|plans?)|als?)|recommend(ed\\s+approach(es)?|ations?))\\b";

def proposal_line:
  proposal_title as $t
  | test("^\\s*([-*+]\\s+)?(#{1,6}\\s*|<h[1-6][^>]*>\\s*)(\\*\\*|__|\\*|_)?\\s*(\\d+[.)]\\s*)?" + $t; "i")
    or test("^\\s*([-*+]\\s+)?(\\d+[.)]\\s*)?(\\*\\*|__|\\*|_)\\s*(\\d+[.)]\\s*)?" + $t; "i")
    or test("^\\s*([-*+]\\s+|\\d+[.)]\\s*)?" + $t + "\\s*:"; "i")
    or test("^\\s*" + $t + "\\s*:?\\s*$"; "i");

def proposals:
  [ splits("\n") | select(proposal_line) ] | length;

# Whether a file event adds a proposal section: more of them after than
# before. A create has no before.
def adds_proposal:
  ((.newContent // "") | proposals) > ((.oldContent // "") | proposals);
