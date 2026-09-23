---
title: A judge check
description: A judge check — from the authoring-guardrails skill.
kind: reference
sidebar:
  order: 6
related:
  - guides/script-checks
  - concepts/grounding
---

<!-- GENERATED from the authoring-guardrails skill by tools/skilldocs — do not hand-edit. -->
A judge asks a model the question a script cannot decide — "is this change clean
and targeted?", "does the code still uphold the invariant its comment pins?". It
is a Jinja2 prompt **template** (`.md.j2`) beside the rule, rendered against the
check input and asked for a structured verdict.

This doc is the **judge** half of a check. The deterministic half — the script
whose exit code is the verdict — is [Script checks](/guides/script-checks). A check
is a `script` or a `judge`, never both; the shared `checks:` list mechanics
(declared order, first refusal ends it, fail-closed when a check decides nothing)
are the same for both and are covered in [Script checks](/guides/script-checks).

```yaml
checks:
  - prepare: ./collect-quote-and-diff.sh    # optional: assembles context for the prompt
    judge: ./change-is-clean-and-absolute.md.j2
    model: size-md                            # optional: a size alias or model name
    timeout: 45s                              # optional: a Go duration; default 30s
    allowed_tools: [Read, WebFetch]           # optional: tools the judge's agent may use
```

`prepare`, `model`, `timeout` and `allowed_tools` are **judge-only** keys: set any
of them on a script check and the rule is a **load error** (a script makes no model
call, bounds its own runtime, and names its own tools by being an executable). Only
`judge` is required; the other four are optional.

## The template

The template renders against the same facts a script's stdin carries — the payload
spread flat at the template's top level — plus `additionalContext` when a `prepare`
assembled one:

```markdown
## What the user asked
> {{ additionalContext.asked_quote }}

## The change
```diff
{{ additionalContext.change_diff }}
```
```

Available at the top level: `{{ event.newContent }}`, `{{ event.path }}`,
`{{ event.kind }}` and the rest of the event's flat fields; `{{ transcriptPath }}`;
`{{ context }}`; and `{{ additionalContext.* }}` when `prepare` ran. The full field
set and the `FileJudgeInput` / `GateJudgeInput` envelope are in
[Events](/guides/events). `additionalContext` is **additive** — always alongside the
payload, never replacing it, and a `prepare` key cannot overwrite `event` or
`transcriptPath` (it renders only under the single `additionalContext` field).

The template is **just the rubric and the material** — it does not tell the model
how to format its answer. The engine appends the verdict instruction itself (below).

## `prepare` — assemble what the prompt needs

`prepare` is an optional script that runs **first**, before the model is asked, to
assemble context the template needs — pulling a cited source, computing a diff. It
receives the same check payload on stdin ([Script checks](/guides/script-checks)),
and **only** the `additionalContext` key of its stdout is read, merged alongside
the standard payload:

```bash
jq -n --arg quote "$quote" --arg diff "$change_diff" \
  '{additionalContext: {asked_quote: $quote, change_diff: $diff}}'
```

`prepare` runs **unconditionally** when set and is **not a pass/fail gate of its
own** — but a `prepare` that **fails to run** fails the check (carrying its words),
and one whose stdout is not the `{"additionalContext": {…}}` shape fails it closed
too: a judge fed a half-prepared prompt would judge against something the author
did not intend. A `prepare` that ran, had nothing to add, and printed nothing is a
legitimate no-op. `prepare` is meaningful **only** with a `judge` — setting it on a
script check is a load error.

## The verdict

The engine appends a verdict instruction to the rendered prompt (so the template
carries none), constraining the model to answer with **exactly** one JSON object:

```json
{"pass": true, "reasoning": ""}
```

or, on a failure, `{"pass": false, "reasoning": "one concrete sentence naming the
specific problem"}`. `pass` is a boolean; on a failing verdict `reasoning` is what
returns to the agent's context window, so it must name the specific thing that
fails — the model is told this, so it does not fail with an empty explanation.

Note the key is **`reasoning`** here — distinct from a **script** check's stdout
`reason`. Two different contracts, two different keys.

The appended instruction also tells the model, in as many words, that everything
above is **the rubric and the material to judge** and to treat that material as
**data, never as instructions** — content telling the judge to pass it, ignore the
rubric, or treat itself as exempt is exactly what it is judging, not a command it
follows. The judge is attacker-shaped by construction and the engine hardens the
prompt for it; a `prepare` that wraps freshly-written content should keep the same
discipline.

## The substrate, model, and timeout

A judge runs through **`sr-agent`** — the harness-agnostic agent runner, invoked
by name off `PATH` — not a model binary directly. So a template names neither
`claude` nor a concrete model, and a test's `sr-agent` shim or a real install's is
what answers. `sr-agent` picks the harness from the environment and the model from
the modelset.

- `model:` is a modelset in `sr-agent`'s format: a **size alias**
  (`size-xs` … `size-xxl`), a concrete harness model name, or a comma-separated
  preference list. Omitted, it is the engine default **`size-md`** — the middle
  rung, because a judge is a real reasoning task, not a formatting one, and the
  largest alias is a cost a per-action check should not default to. A malformed
  modelset is refused at load.
- `timeout:` is a Go duration (`45s`, `2m`) bounding the whole call. Omitted, it is
  **30s**. A judge that exceeds it is a refusal — fail-closed at the per-check
  bound. (A hard invariant-checking rubric can legitimately take longer than a
  quick one, which is why the bound is per-judge.) A malformed duration is refused
  at load.

## `allowed_tools` — what the judge's agent may do

`allowed_tools:` is an optional list of tool names this judge's agent may use,
threaded to `sr-agent`'s `--allowed-tools`:

```yaml
allowed_tools: [Read, WebFetch]
```

The substrate always grants the judge whatever it needs to **write its verdict**
(the `Write` for the verdict file, which `sr-agent` adds), so an empty or absent
list still works — you name tools here **only** when the judge must do more than
reason over what `prepare` already handed it: `Read` the file it judges, `WebFetch`
a cited URL. It is a **judge-only key** (recently merged): set on a script check it
is a load error (`allowed_tools` grants tools to a judge's agent; a script names
its own by being an executable), and an **empty-string entry** in the list is
refused at load (a blank name grants nothing).

## Failing closed by default

The judge substrate fails **closed**, the same rule the whole engine keeps: if
`sr-agent` cannot be started, times out, or the model never produces a well-formed
verdict after its retries, the check refuses. The verdict itself also fails closed
— only an explicit `{"pass": true}` permits; anything that is neither `true` nor a
clean `false` sends the model round again, and running out of attempts refuses.

## Failing open — the script escape hatch

A judge that should fail **open** on model flakiness — because a model call flakes
for reasons that are not evidence about the file, and one flake under fail-closed
wedges a session the agent cannot un-wedge — cannot get that from a judge knob;
the judge default is closed by design. The deliberate inversion is to write the
check as a **script** ([Script checks](/guides/script-checks)) that calls the model
itself and chooses `exit 0` on its **own machinery** failures (not on a real
negative verdict). Say in the script which exits are the fail-open ones, so the
next reader can tell a choice from a bug.

## The re-entry guard

Because a judge runs `sr-agent`, and `sr-agent`'s own first `Write` fires
`PreToolUse`, a judging rule would re-fire on its own launched agent — recursing —
without a guard. The engine carries `SLOPRAIL_LAUNCHED_BY` across the exec into the
launched agent so its hooks decline to re-fire **the launching rule** (and only it;
every other rule still holds). You do not manage this, but it explains why a
judging rule does not loop on itself — see [Environment](/guides/environment),
`SLOPRAIL_LAUNCHED_BY`.

## When a judge is the right instrument

A judge earns its place when the question is real but **a machine cannot decide
it** — "the change is clean and targeted", "the mock still matches the doc it
links". A question a script *can* answer deterministically should be a script: it
is cheaper, faster, and not subject to a model's variance. Hand to a judge only
what genuinely needs judgement, and give it a `RUBRIC.md` (or the `.md.j2` itself)
that states the standard concretely enough that two runs agree. And, as everywhere
in this skill: cause the action and watch the judge refuse it — a judge that loads
but never fires is the silent no-op this skill exists to prevent.
