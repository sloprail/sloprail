---
title: Your first guardrail
description: Write a rule that refuses an agent action, and prove it fires — in five minutes.
kind: tutorial
---

By the end of this you'll have a working guardrail that refuses a real
agent action, and you'll have *watched it fire* — because a rule that loads
but never refuses is worse than no rule at all.

## Before you start

You need sloprail installed as a plugin in your harness. If you haven't:

```bash
# Claude Code
/plugin install sloprail@sloprail-marketplace
```

That registers the hooks. From now on, every tool call and every turn-end
runs through sloprail.

## 1. Write the rule

A guardrail is a folder. The folder name *is* the rule name. Create one:

```bash
mkdir -p .sloprail/file-guard/no-todo-in-committed-code
```

Add its declaration — a `file-guard`, because we're judging one file's
state:

```yaml
# .sloprail/file-guard/no-todo-in-committed-code/file-guard.yaml
match: path endsWith ".ts"
checks:
  - script: ./check.sh
```

And the check — a script whose exit code is the verdict (0 permits,
non-zero refuses):

```bash
# .sloprail/file-guard/no-todo-in-committed-code/check.sh
#!/usr/bin/env bash
if grep -q "TODO(no-ship)" "$SR_FILE"; then
  echo '{"reason": "This file has a TODO(no-ship) marker — resolve it before writing."}'
  exit 1
fi
```

## 2. Prove it fires

This is the part most people skip, and it's the whole point. Ask your agent
to write a `.ts` file containing `TODO(no-ship)`. The write is **refused**,
and the reason you wrote is shown back to the agent.

Now ask it to write a `.ts` file *without* that marker. The write lands.

If both happened, your guardrail is real. If the bad write went through,
your rule loaded but didn't fire — which is the failure this whole product
exists to prevent.

## What you just learned

- A guardrail is a **folder under `.sloprail/`**, named for the rule.
- It has a **nature** (here, `file-guard`) that decides when it runs.
- Its **check** is a script (exit code = verdict) or a judge.
- **Loading is not firing** — always confirm the refusal actually happens.

## Next

- [The three rule kinds →](/guides/file-guard) — file-guard,
  gate, context, and when to use each.
- [Guides →](/guides) — real failures and the guardrails that catch them.
