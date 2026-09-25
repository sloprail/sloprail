# When a fixture reuses the shipped .sloprail/, and when it doesn't

An eval fixture at `examples/<name>/eval/<case>/` needs the guardrail present
in its isolated workspace to test anything. There are two ways to get it
there, and picking wrong either ships silent duplication or silently drops
the guardrail from the run.

## `exampleSloprail: true` — the default choice

Set this in `fixture.yaml` when the fixture is testing the **shipped rule,
unmodified**. `sr-eval` copies `examples/<name>/.sloprail/` into the isolated
project before applying the fixture's own `overlay/` (see
[run-eval/fixture-format.md](../run-eval/fixture-format.md) for the field and
the copy order). The fixture then needs **no** `overlay/.sloprail/` of its
own — only `overlay/.claude/skills/` if the scenario also needs a taught
skill, or other files the scenario requires.

```yaml
seed: seed
exampleSloprail: true
model: haiku
score: score.sh
```

**A fixture with `exampleSloprail: true` must not ALSO carry
`overlay/.sloprail/`** — `LoadFixture` refuses to load it if both are
present, because that is the exact duplication this field exists to remove,
now silently doubled: the shipped copy applies first, the stale overlay copy
applies on top and wins, and a fix to the shipped guardrail stops reaching
the fixture with nothing telling you so.

This should be the default for a new fixture testing an existing example's
rule as-shipped. Most fixtures belong here.

## A fixture's own `overlay/.sloprail/` — the exception

Write the fixture's own copy of `.sloprail/` (full or partial) instead when
the fixture is not testing the shipped rule unmodified. Three real cases in
this repo, and the reasoning that makes each legitimate rather than drift:

**The fixture's scenario needs different SURROUNDING config.**
`completeness-artifact-on-trigger/eval/note-after-change` needs its
`structure.yaml` to additionally allow `src/**` (the shipped example's
demo scenario never writes there, but this fixture's prompt requires fixing
a real bug in `src/`). Diagnose this by asking: does the fixture's prompt or
seed require an action the shipped config's OWN surrounding rules (not the
guardrail under test itself) would block? If yes, the fixture needs its own
copy with that one thing changed — not `exampleSloprail`.

**The shipped example ships a demo helper the fixture doesn't need.**
`eval-loop-maxing/eval/converging-goal`'s shipped `.sloprail/context/recording/`
folder includes a `dummy-eval.sh` — standalone scaffolding for a human trying
the example by hand, never invoked by any `context.yaml`/`gate.yaml` the
engine reads. The fixture's own `seed/eval` is a real, working equivalent the
guardrail's `match:` actually dispatches on. Diagnose this by checking
whether the file in question is actually referenced by any `on:`/`match:`/
`checks:`/`enter:`/`exit:`/`require:` in the shipped config, or only
mentioned in the README — if only the README, it's demo scaffolding a
fixture is free to replace with its own real equivalent.

**The fixture proves the PATTERN generalizes to a new domain, not the
shipped instance.** `required-context-precondition`'s two fixtures each seed
a real, unprimed ~1100-file clone of `pydantic/pydantic-ai` and write their
own new gate (`require-skill-tests`, `require-skill-credential-inventory`)
targeting paths that exist in THAT tree — the shipped demo gates target
`memories/decisions/`/`memories/topics/`, paths that don't exist there at
all. Diagnose this by checking whether the shipped gate's `match:` path
prefix exists in the fixture's seed/repo tree — if it doesn't, including the
shipped gate would be dead, never-firing config, worse than a fixture that
supplies its own real instance of the same pattern.

## The check before writing a new fixture

1. Does this fixture test the shipped rule as-is, against a seed/repo where
   the shipped `.sloprail/`'s own `match:`/paths are reachable? →
   `exampleSloprail: true`, no `overlay/.sloprail/`.
2. Does it need the shipped rule PLUS something the shipped surrounding
   config doesn't allow? → `exampleSloprail: true` is still fine if the
   *guardrail under test* is unmodified — only the case above (a DIFFERENT
   surrounding rule like `structure.yaml` needs to change) needs a full own
   copy instead.
3. Does it prove the pattern generalizes to paths/domains the shipped config
   doesn't cover at all? → write its own `.sloprail/`, no `exampleSloprail`.

When in doubt, try `exampleSloprail: true` first and run the fixture — a
`LoadFixture` refusal or a run where the guardrail visibly never fires (check
`--keep`'s workspace tree, not just the score — see
[run-eval/verifying.md](../run-eval/verifying.md)) tells you which case you're
in.
