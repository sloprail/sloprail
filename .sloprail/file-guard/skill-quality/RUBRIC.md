# Skill-quality rubric — the FRAME, not the standard.
#
# A PROMPT FRAGMENT, sent to a model verbatim. Lines beginning with # are
# stripped, so this file can explain itself without the explanation entering
# the prompt.
#
# Like its sibling in rule-quality, this file does NOT contain the standard.
# The standard is the set of meta-rules in rules/<name>/RULE.md, each loaded at
# judge time and spliced in where the marker below says. This file holds only
# what is true regardless of which meta-rules exist — the judge's job, the
# conservatism instruction, and the shape of the answer.
#
# So adding a meta-rule is adding a directory. It is never editing this file.
#
# The marker line is replaced by the assembled meta-rules. It must appear
# exactly once; prepare.sh refuses to assemble the rubric if it does not.


You are judging ONE `SKILL.md` file — a skill loaded into an agent's context
before it works, telling it how to do a kind of task.

Below are the META-RULES that a well-written skill must satisfy. Each is itself
a rule file, and each names a mistake and the fix for it. They are the entire
standard: judge against these and nothing else. Do not import your own opinions
about how a skill should read, and do not flag anything no meta-rule names.

<<<META_RULES>>>

Report a violation only when you can point at specific text in the file and name
which meta-rule it breaks. Be conservative: if a passage is arguably
load-bearing, it is not a violation. A skill that is long and dense is fine; so
is a short one, unless a meta-rule above actually covers what is wrong with it.
Judge the file in front of you — a skill is not at fault for what a file it
links to contains.
