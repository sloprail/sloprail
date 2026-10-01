---
name: task-workflow
description: Use when tracking a piece of work as a task in this repo — how a task is created, worked, and handed in for review under memories/tasks/.
---

# Tasks in this repo

A task is `memories/tasks/<category>/<name>/TASK.md`: frontmatter plus a body
stating what the user asked, in their terms.

```
---
status: to_do        # backlog | to_do | in_progress | in_review | blocked
priority: P1         # P0..P3
---

<the ask, in the user's terms>
```

## Creating it

The body is the user's ask, so the write cites their exact words. Use
`sr-file`, on its own in the command:

```bash
sr-file write memories/tasks/<category>/<name>/TASK.md \
  --cite:user '<exact words from the user message>' <<'TASK'
---
status: to_do
priority: P1
---

<the ask>
TASK
```

A quote must match exactly one user message; check one with
`sr-session trajectory cite '<quote>'`.

## Working it

Move it to `in_progress` when you start (a status change needs no citation).
There is no `done`: when the work is finished, hand it in for review.

## Handing it in

Run what proves the work (the tests), then move the task to `in_review`,
citing that output and listing where the result is:

```bash
sr-file edit memories/tasks/<category>/<name>/TASK.md \
  --old-string 'status: in_progress' \
  --new-string 'status: in_review
artifacts: ["src/file.py:3-7"]' \
  --cite:tool_result '<exact line of the test output>'
```

`artifacts` are repo-relative `file:lines` of what the work changed. A task
must not be left in `to_do` or `in_progress` at the end of a turn.

## Committing it

Every change to TASK.md is also judged from the commits at the end of the turn,
and the citations ride on the commit as trailers, never in the file. A commit
that changes TASK.md carries the trailers for what that change needs: the user's
words (`Sloprail-Cites-User:`) when it creates the task or changes its body, and
the test output (`Sloprail-Cites-Tool:`) when it moves the task to `in_review`:

```bash
git add memories/tasks/<category>/<name>/TASK.md src tests
git commit -m 'Fix the bug and hand the task in' \
  --trailer 'Sloprail-Cites-User: <the same exact words>' \
  --trailer 'Sloprail-Cites-Tool: <the same exact test-output line>'
```

- The trailers must be in the commit that last changed TASK.md. If you commit
  the task more than once, each commit that touches it repeats the user's words,
  and the one that moves it to `in_review` also carries the test output.
- Trailers live in the LAST paragraph of the message. If you add a
  `Co-Authored-By:` line, keep it in that same paragraph (`--trailer` for it
  too): a `-m 'Co-Authored-By: …'` after a `-m 'Sloprail-Cites-…: …'` is a new
  paragraph, and the citations stop counting.
