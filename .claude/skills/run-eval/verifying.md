# Running and verifying a fixture

## Running one

```
sr-eval run --fixture examples/<name>/eval/<case>
```

Launches a real agent-under-test through `sr-agent` (so this stays
harness-agnostic — never a hardcoded `claude` invocation), against the
seeded/cloned + overlaid project, scores the resulting transcript, and
archives the run (see [archiving.md](archiving.md)) unless `--no-archive` is
passed.

Flags:

- `--keep` — do not remove the isolated workspace after scoring; print its
  path instead. Use this whenever verifying a NEW or CHANGED fixture, not
  just when something looks wrong.
- `--no-archive` — skip the run archive, for throwaway iteration.
- `--model <override>` — run the same fixture against a different model
  without editing `fixture.yaml`.

## The score alone is not enough — inspect the tree

A run can score PASS while the guardrail under test never actually reached
the workspace at all — the trajectory can look perfectly healthy with the
guardrail silently absent, because "healthy" is about the AGENT's behavior,
not about whether the thing being tested was even present. This is not
hypothetical: it is exactly how a real bug in `exampleSloprail`'s copy
destination was caught during this feature's own rollout — a fixture loaded
cleanly and scored PASS while `.sloprail/` had been flattened into the
project ROOT instead of nested under `project/.sloprail/`, so the engine
never saw it.

After any fixture change (new fixture, edited `overlay/`, changed
`exampleSloprail`), run with `--keep --no-archive` and check the actual tree:

```
sr-eval run --fixture <dir> --keep --no-archive
# sr-eval prints: workspace: /tmp/sr-eval-XXXXXXX (kept)

find /tmp/sr-eval-XXXXXXX/project/.sloprail -type f
diff -rq /tmp/sr-eval-XXXXXXX/project/.sloprail examples/<name>/.sloprail
```

The `diff -rq` should exit 0 for a fixture using `exampleSloprail: true` with
no overlay collisions — if it doesn't, either the copy order broke or the
overlay is silently overriding something it shouldn't. Delete the kept
workspace when done; `sr-eval` never cleans up a `--keep` run for you.

## What counts as "proven"

A fixture file (prompt, seed, overlay, score.sh) edited without a REAL run
against it is an unverified claim, not a passing eval — the same standard
[authoring-guardrails](../authoring-guardrails/SKILL.md) holds a guardrail
script to ("loading is not firing"). Run it, read the actual PASS/FAIL and
reasoning line sr-eval prints, and for anything touching what lands in the
workspace, inspect the tree per above — don't infer correctness from the
fixture files reading correctly.
