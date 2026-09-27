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

- **A reference to compare against.** The judge rules against the exact text
  the code was written against, not whatever the spec says today.
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
   does the marked code actually enforce what it says? A `prepare`
   (`pinned-text.sh`) reads each pin and hands the judge the pinned lines and
   the current spec, so the judge reads nothing itself: it runs with the
   rule's folder as its working directory, and a spec outside that folder
   would cost it a round of permission denials before it found the text.

## The failure this catches

Spec and code drift apart most easily when both are actively maintained —
each edit is defensible on its own, and no single commit is wrong. The pin is
what makes the drift a checkable fact rather than something someone has to
notice by memory.

## The second rule: a pinned line holds

`pinned-invariant` checks that the code upholds the pinned text. It cannot
notice the text itself being rewritten to agree with the code, and a real
Haiku run did exactly that twice: asked for goodwill refunds above the charge,
it relaxed "a refund must never exceed the original charge" in SPEC.md and
re-pinned its code to the new wording, so code and pin agreed.

`pinned-spec-holds` closes that. A write that changes a line some
`sr:invariant` marker pins must cite the user's own words
(`require: citation`, `when: changes-pinned-lines.sh`), and a judge checks
those words ask for the rule itself to change, not merely for a feature that
conflicts with it. Whether a business rule changes is the user's decision,
made knowingly; an agent whose task conflicts with one keeps the rule and says
so. It is preventive, so the rule is refused before it changes. Which files
are specs is decided by the markers pointing at them, not by a file name.
