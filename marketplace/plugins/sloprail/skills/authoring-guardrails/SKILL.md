---
name: authoring-guardrails
description: Use when adding, fixing, or turning off a guardrail in a project that has sloprail installed — a rule under .sloprail/ that refuses an agent's action. Covers the three natures (file-guard, gate, context), their YAML, the flat event model, the check contract, and how to prove a rule actually fires.
---

# Authoring guardrails

A guardrail is a rule under `.sloprail/` that refuses something an agent does. The work is
not writing a plausible declaration; it is writing one and then proving it refused
something. A rule that loads cleanly and admits everything is worse than no rule: it looks
like protection and is not.

## Is the rule worth writing?

Write it only when all of these hold. If one does not, say so instead of writing a weaker
rule.

- **The violation is real and recurring.** You have seen it, or the user named it. A rule
  against something nobody does costs every session and catches nothing.
- **A machine can tell.** A script decides what is decidable ("a write under
  `memories/decisions/` before the `document-strategy` skill was loaded"). A judge, a model
  with a rubric, decides what needs judgement ("the change is clean and targeted"). Hand a
  judge only what a script cannot decide.
- **Refusing is the right response.** If the honest response is "note it and move on", a
  guardrail is the wrong tool.
- **A check can reach the answer.** Besides the event itself, a check can read the
  repository, what earlier cycles recorded in `sr-session state`
  ([state-management.md](state-management.md)), and the session transcript
  ([script-checks.md](script-checks.md#asking-what-the-agent-did)).

## Which nature is it?

Every rule is one of three natures. The nature fixes its folder, its YAML keys and when it
runs.

| The question | Nature | Example |
|---|---|---|
| Is what was committed right? | [file-guard](file-guard.md) | "Every file under `memories/` has frontmatter." |
| May this action happen, or may the turn end? | [gate](gate.md) | "Refuse a write under `spec/` until the spec skill was loaded." |
| Is the session in a mode other rules depend on? | [context](context.md) | "A refactor was declared; it stays on until it is finished." |

A file-guard judges commits after the fact; only a gate can refuse an action before it
happens. For a rule that must do both, see [gate.md](gate.md#preventing-a-write-or-a-delete).

Alongside the three natures there is the [structure gate](structure-gate.md): one allowlist of
the paths that may be written at all.

## Where a rule lives

```
.sloprail/file-guard/<name>/file-guard.yaml
.sloprail/gate/<name>/gate.yaml
.sloprail/context/<name>/context.yaml
.sloprail/file-guard/structure.yaml        # the structure gate: one file
.sloprail/config.yaml                      # project settings, not a rule
.sloprail/<nature>/<name>/tests/<case>/test.sh
```

The folder name is the rule's name, and a refusal ends with it, so pick one that reads well
there; kebab-case is the convention. The rule's scripts and judge templates sit beside its
YAML. Create the folders yourself; nothing scaffolds them.

A rule's full name is `<plugin>/<nature>/<name>` (`sloprail/gate/cite-before-commit`), or
`<nature>/<name>` for the project's own. The nature is part of it because a gate and a
context may share a name.

## How a rule decides

A rule narrows itself to the events or files it is about with a `match`
([matchers.md](matchers.md)), may `require` preconditions, and runs its `checks:` in order
until one refuses:

```yaml
checks:
  - script: ./check.sh                 # deterministic: exit 0 permits
  - prepare: ./gather.sh               # optional, assembles what the judge needs
    judge: ./is-it-good.md.j2          # a model answers with a verdict
```

A [script check](script-checks.md) reads one JSON `CheckPayload` on stdin, and its exit code
is the verdict. Start from [check-template.sh](check-template.sh) for a file-guard or
[gate-check-template.sh](gate-check-template.sh) for a gate that prevents a write, and change
only `fine()`. A [judge check](judge-checks.md) is a prompt template a model answers.

What a check reads, every event kind and its fields: [events.md](events.md).

## Prove it fires

A rule that does not load is reported to you at the next hook and at Stop, without your running
anything: the load check ([events.md](events.md)).

Loading is not firing. A rule can load and still never refuse anything: a `match` true of
nothing real, a misspelled key the loader cannot see, a check that permits where it meant to
refuse. So cause the action the rule guards, see it refused, and see a good neighbour
permitted. Keep that proof as a case in the rule's own `tests/<case>/test.sh`, one rule per
case, and run it with `sr-test run --rule <nature>/<name>`. `sr-test doctor` lists the rules
that have no case.

A file-guard is judged over a range of commits with `sr-checks`
([file-guard.md](file-guard.md#running-it)).

## Changing a rule that already stands

A change to a rule that already stands must trace to the user's words or to a tool output
that shows the rule misfiring. Cite it in the commit:

```bash
git commit -m 'relax demo: allow untitled notes' \
  -m 'Sloprail-Cites-User: let the demo rule accept untitled notes'
```

How citations work: [grounding.md](grounding.md).

## Turning a rule off

Keep the folder: its YAML and scripts hold the reasoning someone needs to switch it back on.
Switch it off by its full name in `.sloprail/config.yaml`; turning a rule off is a rule change,
cited like any other.

```yaml
disabled:
  - sloprail/file-guard/authoring-slop
```

This is also the way to switch off a plugin's rule, whose files a reinstall would overwrite,
and a plugin's rule that does not load, which you cannot fix. A gate that ships off
(`enabled: false` in its YAML) is switched on the same way, under `enabled:`. Disabling a
context leaves every gate that `require`s it refusing, so disable those too.
