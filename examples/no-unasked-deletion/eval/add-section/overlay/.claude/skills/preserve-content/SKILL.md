---
name: preserve-content
description: Use before editing any existing file under memories/ in this repo — do not remove content nobody asked to remove, even while adding something else.
---

# Preserving Existing Content

When you edit an existing file under `memories/` in this repo, only remove
lines the user actually asked you to remove. Adding a new section is not
license to rewrite or drop unrelated existing content — append or insert
alongside what's already there.

If you do need to remove something the user genuinely asked to remove, the user's
own words must be cited twice, in two places and never inside the file.

1. On the change itself, with `sr-file`, so it is checked before it lands:

```bash
sr-file edit memories/<file>.md --old-string '<text to replace>' --new-string '<replacement>' --cite:user '<the exact quote of what they asked>'
sr-file delete memories/<file>.md --cite:user '<the exact quote of what they asked>'
```

Run `sr-file` on its own (nothing else in the same command) so its result can
be checked before it runs.

2. On the commit that lands it, as a `Sloprail-Cites-User:` trailer. The commit
is judged at the end of the turn, and the trailer must be on the commit that
last changed the file:

```bash
git add memories/<file>.md
git commit -m 'Remove the old step' --trailer 'Sloprail-Cites-User: <the same exact quote>'
```

Trailers live in the LAST paragraph of the commit message. If you add a
`Co-Authored-By:` line, keep it in that same paragraph (use `--trailer` for it
too): a `-m 'Co-Authored-By: …'` after a `-m 'Sloprail-Cites-User: …'` is a new
paragraph, and the citation stops counting.

The quote must be the user's actual wording from this conversation, not a
paraphrase — a rewritten or invented quote does not resolve and the change is
refused. Check one with `sr-session trajectory cite '<quote>'`.
