---
title: Write a guardrail
description: The folder, the declaration, the check — the anatomy of a rule you author yourself.
kind: guide
related:
  - authoring/prove-it-fires
  - concepts/file-guard
  - reference/nature-shapes
---

A guardrail is a **folder** under `.sloprail/`, and the folder name *is* the rule
name. Writing one is three files, or fewer.

## 1. Pick a nature

Decide what the rule is about, and that picks the [nature](/concepts/file-guard):

- Protecting a **file**'s state → [file-guard](/concepts/file-guard).
- Blocking a **turn** on a condition → [gate](/concepts/gate).
- Remembering something **across** the turn → [context](/concepts/context) +
  a gate.

The folder goes under `.sloprail/<nature>/<rule-name>/`.

## 2. Write the declaration

The `<nature>.yaml` says when the rule runs and what it checks. A file-guard:

```yaml
# .sloprail/file-guard/no-todo-in-committed-code/file-guard.yaml
match: path endsWith ".ts"
checks:
  - script: ./check.sh
```

The `match` is the coarse filter — which files, which events, under which
contexts. See [Nature YAML shapes](/reference/nature-shapes) for every field.

## 3. Write the check

A [script check](/reference/script-checks): exit code is the verdict, and
a printed reason is what the agent sees.

```bash
# .sloprail/file-guard/no-todo-in-committed-code/check.sh
#!/usr/bin/env bash
input="$(cat)"
if grep -q "TODO(no-ship)" "$SR_FILE"; then
  echo '{"reason": "This file has a TODO(no-ship) marker — resolve it before writing."}'
  exit 1
fi
```

## 4. Confirm it loaded

Run the load check to confirm the declaration is valid and bound to a real event:

```bash
sr session start < /dev/null
```

A binding to an event this build doesn't produce is named here, not silently
ignored.

## Then — and this is not optional

Loading is not firing. Go
[prove it fires](/authoring/prove-it-fires): make the bad thing happen
and watch the rule refuse. A rule you haven't watched refuse is still a hope.
