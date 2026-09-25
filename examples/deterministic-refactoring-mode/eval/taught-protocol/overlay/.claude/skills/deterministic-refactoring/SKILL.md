---
name: deterministic-refactoring
description: Use before moving code between files (splitting a file, extracting a function/class elsewhere) — a bare Write/Edit of moved code is refused here; moves must be declared and marked.
---

# Deterministic Refactoring

A move (splitting one file into several, relocating a function or class) must
carry the origin's exact bytes, not regenerate them. This project enforces
that mechanically, in two steps.

## 1. Declare the move before you make it

Before writing any of the moved code, send a message containing a `#refactor`
tag naming every move as `path@sha:start-end`, comma-separated — one entry per
contiguous block you are about to relocate:

```
#refactor scope=src/click/exceptions.py@a1b2c3d4:35,src/click/exceptions.py@a1b2c3d4:159-230
```

- `path` is the origin file's repo-relative path.
- `sha` is the commit the origin content is pinned at — use the checkout's
  current `HEAD` (`git rev-parse HEAD`), not a shortened or symbolic ref.
- `start-end` is the origin's 1-indexed line range for that block (a
  single-line move may write just `start`). Read the actual lines with
  `sed -n` or your editor before declaring the range — a range that does not
  match what you actually move fails the check below.

Declare the WHOLE scope up front, even if you have not read every line yet —
the scope is what gets checked once the moves land, not a running log.

## 2. Mark and land each move exactly

Every file you write with moved content must carry, as its own line, one
marker per block you moved INTO it:

```
// sr:moved-from <path>@<sha>:<start>-<end>
```

(use the origin language's own comment syntax — `//`, `#`, whichever the file
you are writing uses). The fqn after `sr:moved-from` must be byte-identical to
the corresponding `scope=` entry you declared in step 1.

The moved code itself — everything below the marker — must be the origin's
bytes, unchanged except for import lines and whitespace (those two are
exempt: fix imports for the new file's location, reformat freely). Do NOT
rephrase, rename, reorder, or "clean up" the moved code while relocating it —
a move is not a rewrite, and a marked file whose body diverges from the
origin at that exact line range is refused before it lands.

## Why

A declared-but-unlanded move, or a move that regenerated the code instead of
copying it, is exactly the failure this project's tooling is watching for.
Skipping the tag, or marking a range that does not match what you actually
wrote, means the check has nothing to verify against and the move goes
unchecked — not "passes," just invisible. Declare accurately, then copy
exactly.
