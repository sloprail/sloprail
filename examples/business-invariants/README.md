# business-invariants (file-guard)

## The rule

Invariant files live in the codebase; the code carries MARKERS naming each
one; a judge verifies the marked code actually upholds the business invariant
it claims to. This is business logic, not shape.

## The addition: pin the marker to a spec version

A marker naming an invariant says WHICH invariant, but not WHICH VERSION of
it. The spec can be reworded, narrowed, or reversed after the marker was
written, and nothing notices — the marker still points at the invariant's
name, the invariant still exists, and the pair has quietly stopped meaning
what it meant when the code was written.

The fix: the marker's fqn carries a GitHub-shaped link pinned to a commit —
repo, sha, path, line range. The sha buys two things:

- **A reference to compare against.** The judge follows the link and reads
  the exact text the code was written against, not whatever the spec says
  today.
- **A change signal.** Diffing the pinned range against HEAD is checkable by a
  script, before any judge runs — if the spec moved and the marker did not,
  that is a fact, not an impression.

## Why file-guard

The rule is about the file's state — "does this marker's code still hold" —
not about the event that touched it. A marker that starts failing because the
spec moved underneath it stays failing every cycle until either the code
catches up or the marker is re-pinned; it does not matter which event last
touched the file.

## What the two checks divide

1. **Script (cheap, first):** does the pin even resolve (real commit, real
   path, real line range), and does the pinned text still match HEAD? Pure
   byte comparison — no model needed to catch spec drift.
2. **Judge (only once the pin is confirmed live):** given the pinned text,
   does the marked code actually enforce what it says?

## The failure this catches

Spec and code drift apart most easily when both are actively maintained —
each edit is defensible on its own, and no single commit is wrong. The pin is
what makes the drift a checkable fact rather than something someone has to
notice by memory.
