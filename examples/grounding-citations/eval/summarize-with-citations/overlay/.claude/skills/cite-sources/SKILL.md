---
name: cite-sources
description: Use when writing any markdown file in this repo that makes a factual claim about what another file says — every such claim must cite the exact source lines it comes from.
---

# Citing Sources in Markdown

Any factual claim in a markdown file you write — "X now does Y", "the
default changed to Z" — must be followed by a citation to the exact lines
of the source file it comes from, in this format:

```
[the exact quoted text](/absolute/path/to/file:start-end)
```

- The path must be absolute.
- `start-end` is the 1-based line range in that file containing the claim.
- The quoted text should closely match what the source actually says at
  that range — not a looser paraphrase presented as if it were exact.

A claim with no citation, or a citation pointing at the wrong lines or a
range that doesn't exist, will be flagged.
