---
name: deterministic-refactoring
description: Use before moving code between files (splitting a file, extracting a function/class elsewhere) — a bare Write/Edit of moved code is refused here; moves must be declared and marked.
---

# Deterministic Refactoring

Moves in this project (splitting one file into several, relocating a
function or class) go through a two-step protocol so the tooling can verify
what actually landed.

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
  `sed -n` or your editor before declaring the range.

Declare the whole scope up front, even if you have not read every line yet —
the scope is what gets checked once the moves land, not a running log.

## 2. Mark each destination file

Every file you write with moved content must carry, as its own line, one
marker per block you moved INTO it:

```
// sr:moved-from <path>@<sha>:<start>-<end>
```

(use the origin language's own comment syntax — `//`, `#`, whichever the file
you are writing uses). The fqn after `sr:moved-from` must match the
corresponding `scope=` entry you declared in step 1, exactly.

A move whose destination carries no marker, or whose marker's fqn does not
match what you declared, is not recognized as complete — the tooling has
nothing to verify it against.
