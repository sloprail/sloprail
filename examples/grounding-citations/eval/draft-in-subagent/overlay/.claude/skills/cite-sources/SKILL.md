---
name: cite-sources
description: Use when writing any markdown file in this repo — every such file restates what other files say, so the write must cite the exact source output it comes from, and the commit that lands it must cite it too.
---

# Citing Sources in Markdown

Every markdown file in this repo restates what other files say. Read the source
first (Read, or `cat`), then write the markdown with `sr-file`, citing the exact
words of that output on the command — never inside the file, which holds only
plain prose:

```bash
sr-file write MIGRATION.md \
  --cite:tool_result '<exact words from the source output>' \
  --cite:tool_result '<another exact fragment, for another claim>' <<'EOF'
<the summary, in plain prose>
EOF
```

- Cite each claim's source: `--cite:tool_result` repeats.
- Each quote must match exactly one tool output of this session; check one with
  `sr-session trajectory cite --source-types tool_result '<quote>'`.
- Run `sr-file` on its own in the command (nothing else in the line but
  `sr-file` calls, `&&` and `echo`).
- Say only what the cited output says — not a looser or sharper version of it.

Then commit the file. The commit is judged at the end of the turn, and it carries
the same quotes as `Sloprail-Cites-Tool:` trailers, one per quote:

```bash
git add MIGRATION.md
git commit -m 'Add the migration note' \
  --trailer 'Sloprail-Cites-Tool: <exact words from the source output>' \
  --trailer 'Sloprail-Cites-Tool: <another exact fragment>'
```

- The trailers must be in the commit that last changed the file. An empty commit
  carrying only trailers does not count.
- Trailers live in the LAST paragraph of the message. If you add a
  `Co-Authored-By:` line, keep it in that same paragraph as the trailers (use
  `--trailer` for it too). A `-m 'Co-Authored-By: …'` after a
  `-m 'Sloprail-Cites-Tool: …'` is a new paragraph, and the citations stop
  counting.

A markdown write without a citation is refused, and so is a claim the cited
output does not support.
