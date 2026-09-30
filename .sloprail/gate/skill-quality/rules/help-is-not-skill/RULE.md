---
enforced: true
---

# A skill teaches when and why to reach for a command, never transcribes its interface

**Mistake:** transcribing a command's interface into the skill — an exhaustive
flag table with types and defaults, an option list, a subcommand inventory, a
pasted usage block. The skill becomes a hand-written second copy of `--help`,
dated the day it was written.

The copy is the one an author trusts, because it is the one already in context
when the question comes up. So it is the copy consulted after a flag is renamed
or a subcommand is deleted, and it is wrong precisely when the tool has changed
— the moment the reader most needed it to be right. `--help` is generated from
the implementation and cannot drift from it; the skill can and does.

**Instead:** send the reader to the command's own `--help`, and spend the skill
on what help cannot say — which command to reach for in a given situation, why
this one rather than the neighbouring one, what order they go in, what the
output means for the decision at hand, and which failure looks like success.

**Judge by SHAPE, and only these shapes:** a table or list enumerating flags or
options; a flag's type, default, or argument spelled out; a subcommand roster; a
verbatim usage block or `Usage:` line. These are recognisable in the text
itself, which is the whole reason the test is written this way — you cannot run
anything, so a test resting on what a command actually prints would leave you
inferring from appearance, and this rule would produce confident verdicts on
evidence you do not have.

The consequence, and it is deliberate: **reference-shaped prose that is not one
of those shapes is NOT a violation.** A project routinely moves a table out of
its binary into the skill on purpose, so there is exactly one copy — that copy
is correct and looks like something to flag. Do not infer from a command's name
what its output must contain. Naming a command, and showing a real invocation in
an example the prose is teaching from, is not a violation either.
