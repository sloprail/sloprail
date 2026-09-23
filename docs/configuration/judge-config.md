---
title: Judge configuration
description: The model, timeout, and tools a judge check runs with.
kind: reference
related:
  - reference/judge-checks
  - reference/nature-shapes
---

A [judge check](/reference/judge-checks) runs a model. Three fields on the check
configure how:

```yaml
checks:
  - judge: ./judge.md.j2
    model: size-md
    timeout: 60s
    allowed_tools: [Read]
```

| Field | Meaning |
|---|---|
| `model` | The model size the judge runs at — match it to how hard the judgment is; a cheap model for a simple call, a larger one for a subtle property. |
| `timeout` | How long to wait before the judge is treated as **failed**. A judge that times out is a refusal, not a pass. |
| `allowed_tools` | The tools the judge may use while deciding — for example `[Read]` to let it inspect a file it's ruling on. |

## Fail-closed applies

None of these change the [refusal contract](/concepts/refusal-contract): a judge
that errors or times out refuses. `timeout` bounds how long you wait for that
verdict; it never turns a non-answer into a permit.

For the full check surface, see [Judge checks](/reference/judge-checks) and
[Nature YAML shapes](/reference/nature-shapes).
