---
title: Judge checks
description: A model as a check — a Jinja2 template that returns pass or fail, for the properties only a model can rule on.
kind: reference
related:
  - reference/script-checks
  - concepts/refusal-contract
  - reference/nature-shapes
---

A **judge check** uses a model to reach a verdict. It renders a Jinja2 template
into a prompt, sends it to `sr-agent`, and reads back
`{"pass": …, "reasoning": …}`. `pass: false` is the refusal.

```yaml
checks:
  - judge: ./judge.md.j2
    prepare: ./collect.sh
    model: size-md
    timeout: 60s
    allowed_tools: [Read]
```

## When to reach for a judge

Use a judge only for what a script *can't* decide — a change is clean and
targeted; a mock actually matches the contract in spirit; a claim reads as
genuinely supported. Anything mechanical belongs in a
[script check](/reference/script-checks), which is cheaper and can't be
argued with. The usual shape is script-first, judge-second: the script disposes of
the clear cases and only the ambiguous one reaches the model.

## `prepare` — gather what the template needs

A judge often needs context the template can't fetch itself — a diff, a quote, a
slice of the trajectory. The optional `prepare` step runs first and collects it,
so the template renders against real data.

## Configuration

| Field | Meaning |
|---|---|
| `model` | Which model size the judge runs at. |
| `timeout` | How long to wait before the judge is treated as failed. |
| `allowed_tools` | The tools the judge may use while deciding. |

## A judge is still fail-closed

A judge that times out or errors is a refusal, not a pass — the same
[contract](/concepts/refusal-contract) as a script. A model being slow or
unavailable never silently lets an action through.

For the exact fields, see [Nature YAML shapes](/reference/nature-shapes).
