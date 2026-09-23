---
title: Markers
description: Labels on code and files that anchor a rule to a line — written with sr mark, surviving renames.
kind: reference
related:
  - concepts/marker
  - reference/tags
  - reference/cli
---

A **marker** is a `// sr:<kind> <fqn>` label placed on a static artifact. It
anchors a rule to a position that survives edits and renames, so a
[file-guard](/concepts/file-guard) can find the thing it guards.

## Writing and removing markers

Use `sr mark` — don't hand-edit them, so the format and anchoring stay correct:

```bash
sr mark apply <kind> --<name>=<path>:<line>
sr mark delete <kind> <name>
```

`sr mark` writes the marker as a comment in the file's own comment syntax (`//`,
`#`, `--`), so it's inert to tooling but readable by the engine.

## What a file-guard sees

In a file-guard's `match`, a file's markers are a **list**, and a marker test is a
quantifier over it:

```yaml
match: any(markers, .kind == "invariant")
```

That's how a rule says "any file carrying an `invariant` marker" — the marker is
the selector for *which* files the guard covers.

## Marker kinds in practice

- `invariant` — a property that must keep holding at this spot.
- `moved-from <path>@<sha>:<lines>` — an origin a moved block must reconcile
  against ([reconciliation](/concepts/reconciliation)).
- `conforms` — a link from an artifact to the spec it must match ([grounding](/concepts/grounding)).

For the `sr mark` surface, see the [CLI reference](/reference/cli). For why
markers and tags are separate, see [marker](/concepts/marker).
