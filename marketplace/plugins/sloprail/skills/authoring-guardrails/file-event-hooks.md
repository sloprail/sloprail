# File event hooks

The file module's kinds are about one file: what is about to be written to it,
and what a cycle turned out to have done to it. Get the exact names and fields
from the load check — this page is what the names do not tell you.

## Pre and Post are two different questions

A `Pre` kind is a **prediction**: what a tool call or a parsed command says it
is about to do. It fires at the pre-tool hook point, and refusing prevents the
write.

A `Post` kind is an **observation**, established by diffing the tree against the
baseline taken at session start rather than by trusting what any action
announced. It is dispatched at the end of a cycle, from the `Stop` and
`SubagentStop` hook points, so the change is already on disk — refusing does not
undo it, it tells the agent the cycle is not finished and it must fix what it
did.

That makes `Post` the right kind for a rule about the *result* of a turn ("every
new file under `memories/` has frontmatter") and the wrong one for a rule about
permission to act at all. A `Post` refusal is reported to the agent as a
blocking error on the cycle, and the cycle's read mark does not advance — so the
next `Stop` judges the same span again, and a rule that stays unsatisfied stays
reported rather than scrolling away.

Revalidation keeps this from re-judging content that has not changed: a file
already judged against the same fingerprint is skipped.

Each Post kind mirrors its Pre counterpart's shape — the same `path`,
`oldContent`/`newContent` and `oldMarkers`/`newMarkers` fields — so one hook
bound to both costs one script rather than two kept in step.

A Post hook is also where a cycle-wide rule does its **recording** — the engine
has already established that the path really changed, so the hook writes down
what it was told and lets a cycle-bound hook judge:
[state-management.md](state-management.md).

## The pending bytes

Create and update differ in how they answer "what will this file hold
afterwards", and the difference decides which field a rule reads.

A **create** carries the pending body in `newContent`. The file does not exist
yet, so a rule that wants to look at what would be written has nowhere else to
look; on the other kinds it is already on disk. A create's `newContent` is its
whole result — there are no prior bytes for a replacement to be relative to, so
a create has no `oldContent`.

An **update** carries the file's current bytes in `oldContent` and the post-edit
bytes in `newContent`, paired with a `resultKnown` boolean saying whether the
engine could work `newContent` out. The pair exists because absence cannot say
it: a declared field the event omits is filled with its type's zero value, so an
uncomputable `newContent` and a genuinely emptied file would be the same
observation. A `sed -i` whose outcome is unknowable would look like a command
that empties the file.

So guard on the boolean, then read the value (a PreFileUpdate matcher):

```
resultKnown && !(newContent contains "---")   refuse a write that would strip
                                              the frontmatter, and say nothing
                                              where the engine cannot see
!resultKnown                                  catch the underivable cases
                                              deliberately
```

A rule that reads `newContent` without guarding on `resultKnown` gets the empty
string on the underivable cases. That is stated here so it is a choice rather
than a surprise.

A judge hook bound to an update should defer when `newContent` is not known
(`!resultKnown`) and let the Post binding judge what actually landed. Neither
kind alone covers the ground.

**A delete carries `oldContent` and `oldMarkers` — the bytes about to be lost
and their markers — but no `newContent` or `newMarkers`.** Nothing remains, so
there is no result to read and no new text to scan markers out of.

## Markers

A file event carries the `// sr:<kind>` markers as a list whose elements have a
declared shape — so a mistyped key inside the predicate is refused at load rather
than evaluating false forever. The field name says which text the markers were
scanned from:

- `newMarkers` — the markers the written result would carry. On a **create** it
  is the only markers field (there is no prior file); on an **update** it is the
  markers of `newContent`. `any(newMarkers, .kind == "decision")` asks whether
  the result would carry a marker; `len(newMarkers) == 0` whether it carries
  none.
- `oldMarkers` — the markers the file carries NOW, before the change. On an
  **update** and a **delete**; a create has none. `any(oldMarkers, .kind ==
  "asked")` asks whether the file already carries a marker.

```
any(newMarkers, .kind == "decision")
len(newMarkers) == 0
any(oldMarkers, .kind == "asked")
```

Write markers with `sr-mark`; see its `--help`.
