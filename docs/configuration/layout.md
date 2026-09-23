---
title: The .sloprail/ layout
description: Where rules live on disk — a folder per rule, grouped by nature.
kind: reference
related:
  - reference/nature-shapes
  - configuration/config
---

A project declares its guardrails under `.sloprail/`. The layout is a folder per
rule, and the **folder name is the rule name** — a name recorded once, so it can't
disagree with itself.

```
.sloprail/
  file-guard/
    <rule-name>/
      file-guard.yaml
      check.sh
  gate/
    <rule-name>/
      gate.yaml
      verify.sh
  context/
    <rule-name>/
      context.yaml
      enter.sh
      exit.sh
  structure-gate/
    <rule-name>/
      structure.yaml
  schemas/
    <name>.cue
```

Each rule folder holds its `<nature>.yaml` declaration and the scripts and judge
prompts it names — all resolved relative to the folder, so a rule is
self-contained and moves as a unit.

## A project with no rules

A project with no `.sloprail/` is an ordinary project — the first declaration is
what makes it a guarded one. There's nothing to opt into beyond writing a rule.

## Plugins ship the same layout

A plugin ships rules the same way, in its own `guardrails/` directory at its root.
Installing the plugin puts them into force; they load through the same engine as a
project's. See [resolution](/configuration/resolution) for how the two combine.

For the fields inside each `<nature>.yaml`, see
[Nature YAML shapes](/reference/nature-shapes).
