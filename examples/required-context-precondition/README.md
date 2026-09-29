# required-context-precondition (gate)

Before touching an artifact of a given kind, the agent must have actually
loaded the required context first — a skill, doc, or rulebook — checked
against the real session record, not a claim.

## Install

Make sure sloprail is installed — see the docs
[install page](https://sloprail.com/docs/getting-started/install/).

Then copy this example into your project:

```bash
git clone --depth 1 https://github.com/sloprail/sloprail /tmp/sloprail-clone && mkdir -p .sloprail && cp -R /tmp/sloprail-clone/examples/required-context-precondition/.sloprail/. .sloprail/ && rm -rf /tmp/sloprail-clone
```

Then touch the guarded artifact without loading the required context first,
and confirm you see the refusal below.

## The rule

Before touching an artifact of a given kind, the required context must have
been loaded first — a skill, a doc, a rulebook. No skill loaded = the write is
denied, not warned. Ground truth is the trajectory: a real `Skill` tool_use in
this session's record, not a claim.

## Why this is the gate nature — and why it earns its keep over a bare hook

This is the example that answers "how is a gate different from a hand-written
hook?" The live hand-written version of exactly this rule ships in
[`examples/deprecated/guardrails/required-context-precondition/`](../deprecated/guardrails/required-context-precondition/require-skill.sh)
— it is ~137 lines. About 20 are the rule (a prefix→skill table, "was the
skill loaded"). The other ~115 are trajectory plumbing every hook author would
otherwise rewrite:

- how to read the session record (`sr-session query`, not hand-parsed JSONL)
- why `SR_TRANSCRIPT`, not the look-alike `SR_SESSION_ID`
- failing closed when the transcript is unreadable
- excluding sub-agent entries
- `.input.skill` with no fallback, and why `type` not `kind`
- an expression gotcha that only shows up on a real session's user turns

The gate absorbs all of it into one field:

```yaml
require:
  - skill: document-topic
```

The consumer names a skill; the engine knows how to verify it was loaded.
**That absorption is the value** — the gate is not "a hook we wrapped," it's
the ~115 lines a consumer never writes.

## The parts

Two gates, one per guarded prefix:

- **`gate/require-skill-topics/gate.yaml`** — `on: [{event: PreFileWrite,
  match: path startsWith "memories/topics/"}]`, `require: [{skill:
  document-topic}]`. No checks — `require` is the whole rule. `PreFileWrite` is
  the alias the engine expands to PreFileCreate + PreFileUpdate, so the match
  is written once rather than duplicated across two triggers.
- **`gate/require-skill-decisions/gate.yaml`** — same shape for
  `memories/decisions/` → `document-strategy`.

Two gates rather than one gate with an internal prefix→skill table, because
`require` binds to the whole gate: which prefix demands which skill is
structure, not a lookup. The hand-written hook needed the table because a hook
is one script; gates split cleanly along the thing that actually differs.

## Scope: pre-action only

Both gates bind pre-file events. A gate's whole job is to block before the
action lands — a Post event is too late, the file would already be written by
an agent that had not read how to write it. This is the pre-only scope that
defines the nature.
