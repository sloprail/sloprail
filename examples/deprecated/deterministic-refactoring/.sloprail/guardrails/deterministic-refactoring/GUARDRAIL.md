---
hooks:
  PreFileCreate:
    - matcher: any(newMarkers, .kind == "moved-from")
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

## Declare the origin with a marker

A new file that carries moved code must carry an `sr:moved-from` marker:

    // sr:moved-from <path>@<sha>:<start>-<end>

Write it with `sr-mark` rather than by hand:

    sr-mark apply moved-from --src/big.go@$(git rev-parse HEAD):9-11=src/beta.go:1

Optionally, one or more rename markers may accompany it, each declaring a
single identifier rename applied throughout the moved text:

    // sr:moved-rename <old>=<new>

`sr-mark apply moved-rename --Beta=Gamma=src/beta.go:2`

The marker lines are not themselves part of what is compared; the hook removes
them by the line numbers the event reports before diffing.

## Why a marker, and not an annotation of this rule's own

Markers are the engine's abstraction for "this piece of text says something
about itself". The scanner reads every `sr:<kind> <fqn>` line out of the pending
`newContent`, and the event carries them as a list of `{kind, fqn, line}` under
`newMarkers`. That buys three things this rule would otherwise have had to build:

- **The matcher can select on them.** `any(newMarkers, .kind == "moved-from")`
  is type-checked when the guardrail loads — a typo inside the predicate is
  refused there, rather than compiling into a rule that quietly never fires. The
  old `newContent contains "sloprail:moved-from"` matched a substring anywhere in
  the file, including inside a string literal or a comment about the rule.
- **The hook reads a field, not text.** The origin arrives parsed. There is no
  second grammar in the shell script to keep in step with the writer's.
- **`sr-mark` writes them.** One tool, one written form, the right comment
  leader for the file's language, and idempotent.

## Where the origin range lives, and what that cannot express

A marker has two carriers: `fqn`, one URL-safe token, and `line`, which is
where the marker SITS — not an extent. The engine will not say where a function
ends, because that is a language question it would answer wrong for every
language it had not been told about. So the origin's range cannot come from
`line`.

**The decision: all four parts live in the fqn, as `<path>@<sha>:<start>-<end>`.**

This works because the fqn admits every character a URL does — `/`, `@`, `:`,
`-` and `.` are all legal — so the whole locator is one token with no
whitespace, which is the only constraint the written form actually imposes. The
alternatives were considered and rejected:

- **The marker's own `line` plus a convention** — say, "the marker sits at the
  first moved line, and the extent runs to the end of the file". That reads the
  range off a rule rather than off a declaration, and it cannot express a move
  that lands in the middle of a file, or two moves into one file. It also makes
  the range unverifiable: a convention has nothing to be wrong about.
- **A companion annotation** carrying the range beside the marker. That is the
  thing this finding was about — inventing a second grammar next to the one the
  engine already has, and hand-parsing it.

**What the fqn form cannot express:**

- **A non-contiguous origin.** One start and one end, so code lifted from two
  separated blocks of the source cannot be declared as one move. Two markers on
  the file is the workaround, but the hook refuses more than one origin rather
  than guessing at how to concatenate them — the order and the joining are
  choices, and a check that guessed at them would be verifying its own guess.
- **A path containing `@` after the last `/` in a way that is ambiguous with
  the sha separator.** The split takes the LAST `@`, so `src/a@b.go@<sha>:1-2`
  resolves correctly, but a path whose final component ends in `@` does not.
- **A column range.** Lines only. A move of part of a line is not expressible,
  which matches what the check does anyway: it compares whole lines.
- **A sha that is not a commit.** A tree or blob sha is refused; the locator
  names a commit, because that is what `git show <sha>:<path>` resolves a path
  against.

## Why the commit, and not just the path

An origin naming only `path:start-end` describes the file **as it is now**. If
the source moved on after the copy was taken — someone edited it, or an earlier
step of the same refactor already deleted the moved lines — the comparison runs
against the wrong bytes, and nothing says so. It fails a correct move, or it
passes a wrong one whose origin has drifted into agreement with it.

A sha pins what was actually copied. The hook reads
`git show <sha>:<path>` rather than the working tree, so the check asks git what
was there rather than asking the file system what is there now. The sha is also
a drift signal in its own right: a move declared against a commit that is no
longer an ancestor is visible as such, where a bare path is not.

**When the sha is absent or unreachable, the rule refuses.** Not a fallback to
the working tree — that fallback would be taken exactly when the pin mattered
most, which is when the sha names something this checkout does not have. A
marker with no `@`, a commit this repository does not hold, a project that is
not a git repository at all, and a path absent from the commit are four separate
refusals with four separate messages, and none of them is a pass.

## What is checked

The hook reads the source out of git at the pinned commit, takes the declared
line range, applies the declared renames in order, and compares the result to
what you are writing byte for byte. Identical, and the write proceeds. Different
in any way, and it is refused with a diff showing where the bytes parted
company.

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
already exists is not checked at all — this rule binds `PreFileCreate` alone, so
appending moved code to an existing file passes unexamined. `PreFileUpdate` does
carry markers, split into `oldMarkers` (the bytes the write is about to REPLACE)
and `newMarkers` (the result), and it carries the result in `newContent` — so a
future version could bind there and read `newMarkers`/`newContent`. This one does
not, which is the stated limit rather than an oversight.

**It only fires on files that carry a marker.** A new file with no
`sr:moved-from` marker is not examined. An agent that moves code and simply
omits the marker is not caught by this rule; the marker is what invites the
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

**Uncommitted source cannot be an origin.** The origin is a commit, so code
moved out of a file whose current state was never committed has no sha to pin.
Commit the source first. This is a real cost and it is the deliberate one: the
alternative is an origin that means "whatever that file happens to say when
someone checks", which is the thing the sha exists to rule out.

## What to do when this refuses you

Do not edit the marker to make the check pass. The marker describes what you
did; changing it to match code you regenerated is the exact dishonesty the rule
is looking for.

Copy the declared lines out of the source at the declared commit —
`git show <sha>:<path>` is what this check read — then apply only renames you
have declared. If the code genuinely needs to change, that is a separate edit —
make the move first, let it verify, and change it afterwards where the diff will
show it as a change rather than hiding it inside a move.
