# proposal.jq — what "the findings" are in this example: a proposal section.
# NOTES.md's convention is to research real prior art BEFORE proposing an
# approach there, so a write that ADDS such a section is the proposal, whether
# or not the run declared #research.
#
# A section is a line that STARTS with a proposal's title, in any letter case,
# and is marked as a title:
#
#   a heading           ## Proposed approach: exponential backoff with jitter
#                       ## 1. Proposed approach      <h2>Proposed approach</h2>
#   an emphasised label **Proposed approach:** use backoff    *Proposed approach*
#                       - **Proposed approach:** …
#   a label             Proposed approach: use backoff
#   the title alone     Proposed approach
#
# A sentence that merely contains the words ("The proposed approach will come
# after research.") is not a section.
#
# Which titles depends on WHERE, because only NOTES.md is this project's
# research notes. In NOTES.md (and a link to it) any proposal title counts —
# "Proposed approach/solution/design/plan", "Proposal", "Recommended /
# Suggested / Our approach", "Approach we propose", "Recommendation(s)". In any
# other file only the convention's own title, "Proposed approach(es)", does: an
# ADR's "## Proposal", a changelog's "- Recommendation:", a design doc's
# "## Proposed design" are ordinary documents, not this research's findings.

def proposal_title($notes):
  if $notes then
    "(propos(ed\\s+(approach(es)?|solutions?|designs?|plans?)|als?)|recommend(ed\\s+approach(es)?|ations?)|(suggested|our)\\s+approach(es)?|approach(es)?\\s+(we\\s+)?(propose|recommend)(d|ed)?)\\b"
  else
    "proposed\\s+approach(es)?\\b"
  end;

def proposal_line($notes):
  proposal_title($notes) as $t
  | test("^\\s*([-*+]\\s+)?(#{1,6}\\s*|<h[1-6][^>]*>\\s*)(\\*\\*|__|\\*|_)?\\s*(\\d+[.)]\\s*)?" + $t; "i")
    or test("^\\s*([-*+]\\s+)?(\\d+[.)]\\s*)?(\\*\\*|__|\\*|_)\\s*(\\d+[.)]\\s*)?" + $t; "i")
    or test("^\\s*([-*+]\\s+|\\d+[.)]\\s*)?" + $t + "\\s*:"; "i")
    or test("^\\s*" + $t + "\\s*:?\\s*$"; "i");

def proposals($notes):
  [ splits("\n") | select(proposal_line($notes)) ] | length;

# In NOTES.md, the notes' full set of titles.
def proposals: proposals(true);

# Whether a path is the research notes: NOTES.md, in any letter case.
def is_notes_path: ascii_downcase | test("(^|/)notes\\.md$");

# Whether a file event adds a proposal section: more of them after than
# before. A create has no before. $notes: whether the file is the notes.
def adds_proposal($notes):
  ((.newContent // "") | proposals($notes)) > ((.oldContent // "") | proposals($notes));
def adds_proposal: adds_proposal(.path // "" | is_notes_path);
