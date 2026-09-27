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
