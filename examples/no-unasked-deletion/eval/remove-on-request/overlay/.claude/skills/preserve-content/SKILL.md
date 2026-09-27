---
name: preserve-content
description: Use before editing any existing file under memories/ in this repo — do not remove content nobody asked to remove, even while adding something else.
---

# Preserving Existing Content

When you edit an existing file under `memories/` in this repo, only remove
lines the user actually asked you to remove. Adding a new section is not
license to rewrite or drop unrelated existing content — append or insert
alongside what's already there.

If you do need to remove something the user genuinely asked to remove, make
that change with `sr-file`, citing the user's own words exactly as they wrote
them — the citation rides on the command, never in the file:

```bash
sr-file edit memories/<file>.md --old-string '<text to replace>' --new-string '<replacement>' --cite:user '<the exact quote of what they asked>'
sr-file delete memories/<file>.md --cite:user '<the exact quote of what they asked>'
```

Run `sr-file` on its own (nothing else in the same command) so its result can
be checked before it runs. The quote must be the user's actual wording from
this conversation, not a paraphrase — a rewritten or invented quote does not
resolve and the change is refused. Check one with
`sr-session trajectory cite '<quote>'`.
