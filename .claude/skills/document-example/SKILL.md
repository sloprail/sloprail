---
name: document-example
description: Use when adding, fixing, or touching anything under examples/<name>/ in this repo — a shipped guardrail sample. Covers what "done" means for one (shipped config, README, e2e test, eval fixture, all four in sync), when each piece is required, and how to keep them from drifting apart.
---

# Managing examples/

An `examples/<name>/` directory is a **shipped, working sample** of a
guardrail — proof this repo ships, not a scratch demo. Four things make one
complete, and each is checked separately:

1. **The shipped config** — `examples/<name>/.sloprail/` (+ `README.md`)
2. **E2e coverage** — `tests/e2e/examples/0NN_<name>/`, proving the shipped
   config actually fires against a scripted mock
3. **An eval fixture** — `examples/<name>/eval/<case>/`, proving it holds up
   against a REAL agent (see [run-eval](../run-eval/SKILL.md) for the
   mechanics — this skill covers only where the fixture lives and how it
   relates to the shipped config)
4. **A synced README** — states the rule, why this nature, why this design

Touching any one of these without checking the other three is how they drift.
This skill is the checklist; the subdocs below are the detail.

- **[structure.md](structure.md)** — the directory shape, the e2e numbering
  convention, and the README sections a shipped example is expected to have
- **[example-vs-fixture.md](example-vs-fixture.md)** — when an eval fixture
  should reuse the shipped `.sloprail/` (`exampleSloprail: true`) vs. when it
  legitimately needs its own — the three real exceptions found in this repo
  and why each is not drift
- **[keeping-in-sync.md](keeping-in-sync.md)** — the concrete "I changed X,
  what else needs to change" table

## The one-sentence version

A shipped guardrail is not "done" until: the config is under
`.sloprail/`, a scripted e2e test proves it fires, a real-agent eval fixture
proves it holds up against an unscripted model, and the README explains the
rule and the design choices — and a change to any one of those four almost
always means checking the other three, not just the one you touched.

## Before you touch a file under examples/

Read [structure.md](structure.md) first if this is a **new** example (you
need the exact directory shape and e2e numbering). Read
[keeping-in-sync.md](keeping-in-sync.md) first if you are **editing** an
existing one (you need to know what else your change touches). Read
[example-vs-fixture.md](example-vs-fixture.md) before writing a new eval
fixture's `fixture.yaml` (you need to decide `exampleSloprail` correctly,
the first time — getting it wrong either ships silent duplication or silently
drops the guardrail from the fixture's workspace).

## Verifying a change actually holds

Loading is not proof, same as [authoring-guardrails](../authoring-guardrails/SKILL.md)'s
own rule for a single guardrail:

```
go test -tags fts5 ./tests/e2e/examples/...
```

proves the shipped config fires against the mock. Only a real
`sr-eval run --fixture examples/<name>/eval/<case>` (see
[run-eval](../run-eval/SKILL.md)) proves it holds against a real, unscripted
model — the e2e suite alone is not sufficient signal that an example is
correct, only that it is not obviously broken against a scripted trajectory
that already knows the expected shape.
