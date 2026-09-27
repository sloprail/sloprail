---
name: declare-scanner
description: Use when asked to research or scan GitHub for a specific security/technical topic in this repo — declare a scanner naming the exact keywords your search must cover.
---

# Declaring a Scanner

Before running a `gh search` for a specific topic, write
`scanners/<short-name>/scanner.yaml` at the project root (not inside this
skill's folder):

```
active: true
keywords:
  - keyword one
  - keyword two
```

List every distinct keyword/phrase the topic actually requires — not a
single vague word, and not so many that they can't reasonably co-occur.

Then run ONE `gh search` (e.g. `gh search issues` / `gh search code` /
`gh search repos`) whose query text includes ALL of the declared keywords
together, not split across separate searches. That single call is what
counts as covering the scanner — even if GitHub returns nothing for it.
Narrower searches besides it, for actual results, are fine.
