---
title: Script checks
description: A program whose exit code is the verdict — the deterministic half of a guardrail.
kind: reference
related:
  - reference/judge-checks
  - concepts/refusal-contract
  - reference/nature-shapes
---

A **script check** is a program. Its **exit code is the verdict**: `0` permits,
any non-zero refuses. That's the whole protocol — no framework, no return object.

```yaml
checks:
  - script: ./check.sh
```

## Reading the input, writing the reason

A check receives the event and file details on **stdin** as JSON. When it refuses,
it prints a reason; sloprail looks for one in order:

1. `{"reason": "..."}` JSON on stdout — the clean path.
2. Plain stdout.
3. stderr.
4. A generated fallback.

So the minimum viable refusal is: exit non-zero, print a reason.

```bash
#!/usr/bin/env bash
input="$(cat)"
# ...decide...
if violated; then
  echo '{"reason": "what went wrong, and what to do about it."}'
  exit 1
fi
exit 0
```

## Fail-closed by construction

A script that errors — not executable, missing a dependency, a bad expression —
does **not** let the action through. The engine treats "could not run" as a
refusal. Write scripts so that the only way to reach `exit 0` is that the check
genuinely passed; never swallow an error into a permit. See
[the refusal contract](/concepts/refusal-contract).

## Where scripts run

A check script lives in the guardrail's own directory and is named by a relative
path (`./check.sh`). A shipped guardrail's `./check.sh` resolves to the installed
copy, so a rule works the same whether it's the project's or a plugin's.

For the exact `checks` fields, see [Nature YAML shapes](/reference/nature-shapes).
