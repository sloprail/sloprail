---
enforced: true
---

# A skill teaches when and why to reach for a command, never transcribes its interface

**Mistake:** transcribing a command's interface into the skill — an exhaustive
flag table with types and defaults, an option list, a subcommand inventory, a
pasted usage block. The skill becomes a hand-written second copy of `--help`,
dated the day it was written.

**Instead:** send the reader to the command's own `--help`, and spend the skill
on what help cannot say — which command to reach for in a given situation, why
this one rather than the neighbouring one, what order they go in, what the
output means for the decision at hand, and which failure looks like success.

**Judge by SHAPE, and only these shapes:** a table or list enumerating flags or
options; a flag's type, default, or argument spelled out; a subcommand roster; a
verbatim usage block or `Usage:` line.
