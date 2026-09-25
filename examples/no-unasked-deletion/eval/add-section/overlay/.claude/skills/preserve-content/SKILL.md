---
name: preserve-content
description: Use before editing any existing markdown file in this repo — do not remove content nobody asked to remove, even while adding something else.
---

# Preserving Existing Content

When you edit an existing file in this repo, only remove lines the user
actually asked you to remove. Adding a new section is not license to
rewrite or drop unrelated existing content — append or insert alongside
what's already there.

If you do need to remove something the user genuinely asked to remove, mark
the removal in the file's frontmatter with the user's own words, quoted
exactly as they wrote them:

```
---
# sr:asked "the exact quote of what they asked"
---
```

The quote must be the user's actual wording from this conversation, not a
paraphrase — a rewritten or invented quote will be rejected.
