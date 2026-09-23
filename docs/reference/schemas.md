---
title: Schemas
description: Validating a file's frontmatter against a CUE schema with sr file.
kind: reference
related:
  - reference/cli
  - concepts/file-guard
---

A file can be required to conform to a **CUE schema** — a precise, typed
description of what its frontmatter must contain. `sr file` checks a file against
one.

## Validating a file

```bash
sr file validate TASK.md --schema .sloprail/schemas/task.cue
```

The exit code is the verdict: `0` if the file conforms, non-zero if it doesn't —
the same [refusal contract](/concepts/refusal-contract) a check follows, so this
plugs straight into a [file-guard](/concepts/file-guard)'s `script` check.

## Listing declarations

```bash
sr file declarations .sloprail/
```

Lists and validates the guardrail declarations in a directory — useful when
authoring, to confirm every `<nature>.yaml` in a tree parses.

## Where schemas live

A schema is an ordinary file in the project — for example under
`.sloprail/schemas/`. A plugin can ship one alongside its rules; the file-guard
that enforces it names the schema by path. There is nothing special about the
location beyond the path the check points at.

For the full `sr file` surface, see the [CLI reference](/reference/cli).
