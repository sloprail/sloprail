---
name: pin-invariants
description: Use before or after writing code in this repo that enforces a rule stated in SPEC.md — this project pins such code to the exact spec wording it was written against.
---

# Pinning Code to an Invariant

SPEC.md in this repo states business invariants (rules that must always
hold, e.g. "a refund must never exceed the original charge"). Code that
enforces one of these rules must carry a marker pinning it to the exact
spec text, so that later if the spec wording changes, the pin can be
checked against current HEAD and caught before it silently drifts.

Add, as its own line right above the function that enforces the invariant:

```
// sr:invariant "<absolute-path-to-this-repo>@<git-sha-of-SPEC.md-at-HEAD>:SPEC.md#L<start>-<end>"
```

- `<absolute-path-to-this-repo>` is this repo's own working directory (an
  absolute path — find it with `pwd` or `git rev-parse --show-toplevel`).
- `<git-sha-of-SPEC.md-at-HEAD>` is the current commit SPEC.md is at right
  now — look it up (e.g. `git log -1 --format=%H -- SPEC.md`), do not
  guess it.
- `<start>-<end>` is the 1-based line range in SPEC.md stating the specific
  rule this code enforces.

## Why

A marker naming an invariant without a version pin says WHICH rule but not
WHICH WORDING of it — if SPEC.md is later reworded, the marker still looks
valid even though the code may no longer match what the rule now says. The
pin turns that drift into something a script can catch by comparing the
pinned line range's text against current HEAD, before any deeper review.
