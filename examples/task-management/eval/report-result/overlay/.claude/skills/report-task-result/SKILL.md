---
name: report-task-result
description: Use when starting or finishing a piece of work in this repo — every task gets its own ASK.md (what was actually asked, written citing the user's exact words) and RESULT.md (what was done), kept as separate files.
---

# Recording a Task's Ask and Result

Before starting non-trivial work, create
`memories/tasks/<category>/<short-name>/ASK.md` holding the request, in the
user's terms. Write it with `sr-file`, citing the user's exact words on the
command (never inside the file), and run it on its own in the command:

```bash
sr-file write memories/tasks/<category>/<short-name>/ASK.md \
  --cite:user '<a short exact quote from the user's message>' <<'ASK'
<the request, in the user's terms>
ASK
```

The quote must match exactly one of the user's messages; check it first with
`sr-session trajectory cite '<quote>'`.

Once the work is done, write
`memories/tasks/<category>/<short-name>/RESULT.md` describing what was
done.

**ASK.md changes only when the user changes the ask** — they add to it, or
take something out of it — and then with `sr-file edit`, citing their words for
the change. It is never edited to match the work: an ask stays what was asked, so
it stays a real check on whether the result matches it. Progress, findings, what
was deferred, or a revised understanding of scope go in RESULT.md.
