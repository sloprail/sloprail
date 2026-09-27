# proposal.jq — what "the findings" are in this example: a "Proposed approach"
# section. NOTES.md's convention is to research real prior art BEFORE
# proposing an approach there, so a write that ADDS such a section is the
# proposal, whether or not the run declared #research.
#
# A section is a line that is only the title, in any letter case: a Markdown
# heading (`## Proposed approach`), or a bold line (`**Proposed approach:**`).
# "the proposed approach is …" inside a sentence is not one.
def proposals:
  [ splits("\n")
    | select(test("^[ \t]*(#{1,6}[ \t]*|\\*\\*|__)?[ \t]*proposed approach[ \t]*:?[ \t]*(\\*\\*|__)?[ \t]*:?[ \t]*$"; "i")) ]
  | length;

# Whether a file event adds a proposal section: more of them after than
# before. A create has no before.
def adds_proposal:
  ((.newContent // "") | proposals) > ((.oldContent // "") | proposals);
