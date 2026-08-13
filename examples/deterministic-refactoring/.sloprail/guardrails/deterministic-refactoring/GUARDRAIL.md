---
hooks:
  PreFileCreate:
    - matcher: content contains "sloprail:moved-from"
      hooks:
        - type: command
          command: ./verify-move.sh
---

# A move must be mechanical, and provable

When you move code — split one file into several, lift one function into
another file — the bytes that land must be the bytes you took. Not code that
does the same thing. Not code you wrote again from memory of what the original
did. The same bytes.

Regenerating while calling it a move is the failure this rule exists to catch.
It looks like a refactor in the diff and reads like one in the summary, and the
behaviour that changed is discovered later by whoever trusted the word "move".

## Declare the origin in the file you are creating

A new file that carries moved code must open with a provenance header:

    // sloprail:moved-from <path>:<start>-<end>

`<path>` is the source file, relative to the project root. `<start>-<end>` is
an inclusive 1-based line range in it. Everything after the header block is
compared against exactly those lines.

Optionally, one or more rename lines may follow, each declaring a single
identifier rename applied throughout the moved text:

    // sloprail:rename <old>=<new>

The header lines must come first, before any moved content, and they are not
themselves part of what is compared.

The comment marker may be `//` or `#`, so the same header works in Go, shell,
Python, and anything else using either.

## What is checked

The hook reads the source file off disk, takes the declared line range, applies
the declared renames in order, and compares the result to what you are writing
byte for byte. Identical, and the write proceeds. Different in any way, and it
is refused with a diff showing where the bytes parted company.

A rename is only accepted for an identifier — `[A-Za-z_][A-Za-z0-9_]*`. A
rename naming anything else is refused rather than applied, because the
substitution is a regular-expression replacement and a pattern containing
metacharacters would silently match more than the name it appears to.

Renames apply to every occurrence on every line, including inside strings and
comments. That is a real consequence: if the identifier you are renaming also
appears in a string literal, the check expects it renamed there too.

## What this cannot catch

Read this part before relying on the rule.

**It only sees files being created.** A move that lands by editing a file that
already exists is not checked at all — the engine's create event is the only
one carrying the content to compare, so appending moved code to an existing
file passes unexamined.

**It only fires on files that declare a header.** A new file with no
`sloprail:moved-from` line is not examined. An agent that moves code and simply
omits the header is not caught by this rule; the header is what invites the
check, so the rule catches dishonest moves that were declared, not undeclared
ones. This is a cooperative rule and it is worth having as one: it makes the
claim "this was moved" mean something, so that making the claim falsely fails
loudly. It does not make the claim mandatory.

**It does not check that the source was removed.** Verifying the other half of
a move — that the original lines are gone from where they were — needs an event
that fires after the cycle settles, and nothing dispatches those yet.

**It does not understand code.** The comparison is over bytes. Re-indenting,
reordering, adjusting an import block, or changing a package clause are all
refusals, not tolerated derivations. Only the two operations named above —
taking a contiguous line range, and renaming identifiers — are verifiable here,
because they are the two whose result can be recomputed from the source and
checked, rather than judged.

## What to do when this refuses you

Do not edit the header to make the check pass. The header describes what you
did; changing it to match code you regenerated is the exact dishonesty the rule
is looking for.

Copy the declared lines out of the source file unmodified, then apply only
renames you have declared. If the code genuinely needs to change, that is a
separate edit — make the move first, let it verify, and change it afterwards
where the diff will show it as a change rather than hiding it inside a move.
