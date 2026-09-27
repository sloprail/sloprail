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
   byte comparison — no model needed to catch spec drift. The fqn is written
   by the agent being judged, so `pin.sh` checks it before git reads anything
   with it: the sha must be hex (an fqn whose "sha" is `--output=<file>` would
   have git write that file) and a prefix of the commit it resolves to (a
   branch named like a short sha would otherwise shadow it); the path must be a
   spec (`SPEC.md`, or a `.md` under `specs/` — the files pinned-spec-holds
   guards); and the range must be `L<start>-<end>` with start ≤ end, inside the
   file, holding text (a pin past the end of the spec pins nothing, and a judge
   handed an empty `<pinned>` rules against nothing).
2. **Judge (only once the pin is confirmed live):** given the pinned text,
   does the marked code actually enforce what it says? A `prepare`
   (`pinned-text.sh`) reads each pin and hands the judge the pinned lines and
   the current spec, so the judge reads nothing itself: it runs with the
   rule's folder as its working directory, and a spec outside that folder
   would cost it a round of permission denials before it found the text.
   A deleted file has no code left to judge, so the prepare skips the judge on
   a delete; whether the delete may drop the file's pins is pinned-spec-holds'
   question.

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

`pinned-spec-holds` closes that. A write that changes what a marker pins must
cite the user's own words (`require: citation`, `when: changes-pinned-lines.sh`),
and a judge checks those words ask for the rule itself to change, not merely for
a feature that conflicts with it. Whether a business rule changes is the user's
decision, made knowingly; an agent whose task conflicts with one keeps the rule,
undoes any code that breaks it, and tells the user. It is preventive, so the rule
is refused before it changes.

### Which files it watches

A preventive guard refuses a write whose result the engine cannot work out ahead
(`sed -i`, `>`, `cp`, `tee`) on any file it matches, so the guard matches only
the files a pin can involve — matching every path refused every shell edit in
the project:

- **Specs, by convention:** `SPEC.md` at any depth, or a `.md` file under a
  `specs/` directory. `pinned-invariant` holds pins to the same convention
  (`pin.sh` refuses a pin into any other file), so no accepted pin names a file
  this guard does not watch. To use another layout, change the guard's `match`
  and `SPEC_PATH_RE` in `pin.sh` together.
- **Files carrying an `sr:invariant` marker, before or after the write** —
  `any(oldMarkers, …)` is what sees a write that removes the marker, which
  `any(markers, …)` alone reads as a file with none.

Which lines of a spec are pinned is still decided by the markers pointing at
them, not by the file name.

### What it refuses

A write changes what a marker pins in either of two ways, and the predicate
refuses to waive the citation for both:

- **It changes a pinned spec line** — an edit, a delete, or a create at a path
  HEAD still holds (`git mv SPEC.md SPEC.old` is not seen as a delete, so the
  Write that puts a relaxed SPEC.md back is a create). The pinned lines are read
  from every marker in the working tree **and at HEAD**, so dropping or moving
  the marker first does not unpin the rule. Markers are read with the engine's
  own grammar (quoted or bare fqn, any whitespace), and a pin's path is
  normalized, so `./SPEC.md` pins `SPEC.md`. A line is compared byte for byte, as
  `pin-still-matches-head.sh` compares it: a whitespace-only or line-ending change
  to a pinned line is a change (and would make every pin to it stale).
- **It moves the code off the wording it was pinned to** — a marker removed (or
  its file deleted), or re-pinned to different text. A re-pin to the same text at
  a new place (a line inserted above the rule) changes nothing and needs nothing.
  Only the pins the file held before the session's work count — HEAD's before a
  write, the session baseline's at Stop — so a pin the agent wrote this session
  can be corrected freely, and so can a pin that is not a real one (it pinned
  nothing).

**Moving marked code is not dropping its pin.** A pin that leaves one file while
another file in the working tree carries it (the same fqn, or a pin to the same
text) is held: write the code with its marker in the new place first, then
remove it from the old one. `git mv` is the same move. A plain `mv` is seen as a
delete before the new file exists, and is refused with that advice. Copying the
marker onto something that is not the code does not help: `pinned-invariant`
judges the marked code wherever the marker lands.

**An exception on a line of its own** ("3a. Goodwill refunds are exempt from rule
2") is admitted: no pin names that line, and the code's pin still names rule 2
alone, which `pinned-invariant` keeps judging it against. The risk is the next
step — re-pinning the code to take the exception in — and that is refused
without the user's words.

### What it cannot decide

Everything the predicate cannot decide applies the citation: it is a `when`, and
only a decided waiver exits 1 (with a `{"waived": …}` sentinel on stdout; any
other exit 1, such as a crash, is turned into 0). The judge's `prepare` skips the
model only on that sentinel. So: a shell edit of a pinned spec or a marked file
is refused before it lands (and one the engine does not see as a write at all —
a script rewriting the file — is refused at Stop); missing `jq` or `git` applies
the citation. Some answers are decided, and waive:

- **Not a git work tree:** a pin names `<repo>@<sha>`, so nothing can be pinned.
- **A pin that is not a real one** — a placeholder in a skill's example, a sha
  that is not hex or that a branch of the same name shadows, a range that is not
  `1 <= start <= end` — pins nothing: it cannot pass `pinned-invariant`.

Markers inside a git submodule are not seen (`git grep` does not enter one).
