---
title: Glossary
description: The vocabulary of sloprail, in one place.
kind: reference
related:
  - concepts
  - guides/file-guard
---

**check** — the thing that produces a verdict inside a guardrail. Either a
*script* (exit code is the verdict) or a *judge* (a Jinja2 template sent to a
model). See [Checks](/concepts).

**context** — a [nature](#nature): an activatable scope the agent is inside, which
tracks state while active and never blocks. See [context](/guides/context).

**event** — a flat fact a guardrail binds to (a tool about to run, a turn about to
end). See [the event vocabulary](/guides/events).

**file-guard** — a [nature](#nature) bound to a file's state, re-checked every
cycle, optionally preventive. See [file-guard](/guides/file-guard).

**gate** — a [nature](#nature): a one-shot checkpoint on an event that passes or
blocks. The only nature that blocks a turn. See [gate](/guides/gate).

**grounding** — a check that a produced fact's link to its source is true, not
just present. See [grounding](/concepts/grounding).

**guardrail** — a rule: a folder under `.sloprail/` of one nature, named for the
rule, containing a `<nature>.yaml` and its scripts.

**judge** — a check that renders a Jinja2 template and asks a model for
`{"pass": …, "reasoning": …}`.

**load check** — running `sr session start < /dev/null`: it reports the event
vocabulary and validates every declaration. "Loaded" is not "fired."

**marker** — a durable label on a static artifact (`// sr:invariant foo`). See
[marker](/concepts/marker).

**nature** — the kind of a guardrail — file-guard, gate, context, or
structure-gate — deciding when it runs and what it can do.

**precondition** — a requirement that some context (a skill, a doc) already
entered the trajectory before an action is allowed. See
[precondition](/concepts/precondition).

**preventive** — a file-guard that refuses a write *before* it lands, while the
old content is still on disk.

**reconciliation** — a before/after that must reduce to empty once exceptions and
whitespace are removed. See [reconciliation](/concepts/reconciliation).

**refusal** — a guardrail's "no." Expressed by exit code or a judge's `pass:
false`; a check that cannot run is itself a refusal. See
[the refusal contract](/concepts/refusal-contract).

**require** — a gate or context precondition: a `skill` that must have loaded or a
`context` that must be active (which also orders it first).

**script** — a check whose exit code is the verdict.

**structure-gate** — a [nature](#nature): a deny-by-default allow-list over the
file tree. See [structure-gate](/concepts/structure-gate).

**tag** — a durable label in the trajectory, in the agent's own message
(`#refactor`). See [tag](/concepts/tag).

**trajectory** — the record of what actually happened in a session. Ground truth
for any claim about the run.
