---
name: report-task-result
description: Use when starting or finishing a piece of work in this repo — every task gets its own ASK.md (what was actually asked, citing the exact transcript line) and RESULT.md (what was done), kept as separate files.
---

# Recording a Task's Ask and Result

Before starting non-trivial work, create
`memories/tasks/<category>/<short-name>/ASK.md` citing the exact line of
this session's transcript where the request was made:

```
jsonl:<line-number>

<the request, in your own words>
```

To find the line number, resolve it with `sr-session trajectory cite
"<a short exact quote from the user's message>"` — it prints
`<path>:<line>`; use the line number.

Once the work is done, write
`memories/tasks/<category>/<short-name>/RESULT.md` describing what was
done. **Never edit ASK.md once it exists** — a task's ask must stay
exactly what was originally asked, so it stays a real check on whether the
result actually matches it. Report progress, findings, or a revised
understanding of scope in RESULT.md, never by rewriting ASK.md.
