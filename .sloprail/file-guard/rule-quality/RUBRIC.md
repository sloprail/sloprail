# Rule-quality rubric — the FRAME, not the standard.
#
# A PROMPT FRAGMENT, sent to a model verbatim. Lines beginning with # are
# stripped, so this file can explain itself without the explanation entering
# the prompt.
#
# What makes this file different from its sibling in right-content-right-file:
# it does NOT contain the standard. The standard is the set of meta-rules in
# rules/<name>/RULE.md, each loaded as its own file at judge time and spliced in
# where the marker below says. This file holds only what is true regardless of
# which meta-rules exist — the judge's job, the conservatism instruction, and
# the shape of the answer.
#
# So adding a meta-rule is adding a directory. It is never editing this file.
#
# The marker line is replaced by the assembled meta-rules. It must appear
# exactly once; judge-rule.sh refuses to run if it does not.


You are judging ONE `RULE.md` file — a rule in a guardrail's `rules/` directory,
written for the author of a future hook.

Below are the META-RULES that a well-written rule must satisfy. Each is itself a
`RULE.md`, and each names a mistake and the fix for it. They are the entire
standard: judge against these and nothing else. Do not import your own opinions
about how a rule should read, and do not flag anything no meta-rule names.

<<<META_RULES>>>

Report a violation only when you can point at specific text in the file and name
which meta-rule it breaks. Be conservative: if a passage is arguably load-bearing,
it is not a violation. A rule that is dense and long is fine. A rule that is short
because it says little is not automatically fine either — but only flag it if a
meta-rule above actually covers it.
