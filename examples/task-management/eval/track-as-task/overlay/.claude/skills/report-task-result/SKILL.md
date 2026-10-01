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

The write is judged before it lands (that is what `--cite:user` is for), and the
commit that carries it is judged again at Stop: commit ASK.md with the same words
as a trailer, never inside the file, or the turn is refused:

```bash
git add memories/tasks/<category>/<short-name>/ASK.md
git commit -m 'Record the ask' --trailer 'Sloprail-Cites-User: <the same exact quote>'
```

The trailer must be in the LAST paragraph of the commit message, and in the commit
that last changed ASK.md. If you add a `Co-Authored-By:` line, keep it in that same
paragraph (`--trailer` for it too): a `-m 'Co-Authored-By: …'` after a
`-m 'Sloprail-Cites-User: …'` is a new paragraph, and the citation stops counting.

Once the work is done, write
`memories/tasks/<category>/<short-name>/RESULT.md` describing what was
done. **Never edit ASK.md once it exists** — a task's ask must stay
exactly what was originally asked, so it stays a real check on whether the
result actually matches it. Report progress, findings, or a revised
understanding of scope in RESULT.md, never by rewriting ASK.md.
