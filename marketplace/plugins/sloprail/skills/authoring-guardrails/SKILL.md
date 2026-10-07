---
name: authoring-guardrails
description: Use when adding, fixing, or turning off a guardrail in a project that has sloprail installed — a rule under .sloprail/ that refuses an agent's action. Covers the three natures (file-guard, gate, context), their YAML, the flat event model, the check contract, and how to prove a rule actually fires.
---

# Authoring Guardrails

## A guardrail is one of three natures

A rule is not "a hook on an event". It is one of three **natures**, and the
first decision is which one — because the nature fixes the directory, the YAML
keys, what is in scope for its match, and when it fires.

- **file-guard** — judges a **file's state**. "Every file under `memories/`
  carries frontmatter." It judges the *committed* result: `sr-checks run --base
  --head` hands it `merge-base(base, head)..head` as one changeset and stores the
  verdicts on the `sloprail/checks` branch; Stop and CI only verify them (no
  model). Stop shows failures only (a stored FAIL, a rule that does not load, an
  error), never "not judged yet": run `sr-checks run` before a PR is ready, CI's
  required `sr-checks verify` requires it (the push gate ships off). It never acts before a write — prevention is a
  gate's job. `deletions: include`/`only` when losing the file is the rule's
  business too (by default a deleted file is skipped).
  → [file-guard.md](file-guard.md)

- **gate** — a **checkpoint on an event**. "Block a write under
  `memories/decisions/` unless `document-strategy` was loaded." "Refuse the
  deletion of a pinned file." "On Stop, the turn must have produced the artifact
  it promised." It fires once per event (once per file a call changes), blocks,
  and is done. **Prevention is always a gate**: to refuse a write or a delete
  before it lands, bind a gate to `PreFileWrite` / `PreFileDelete`.
  → [gate.md](gate.md)

- **context** — an **activatable scope**. "A refactor was declared; stay in
  refactoring mode until it is finished." It activates and deactivates on
  triggers, accumulates what it sees, and other rules `require` it or read its
  `active`/`payload` in a match.
  → [context.md](context.md)

Pick by the shape of the question. Is it about **what a file holds when the
turn is done** → file-guard. Is it about **whether an event may happen** (a write,
a delete, a command) **or whether a turn is done** → gate. A rule that must both
refuse a bad write *and* keep judging what settled is two rules with one name: a
`PreFileWrite` gate plus a plain file-guard (the old `preventive: true` on a
file-guard was removed; a declaration still carrying it is refused at load). Is it a **mode that other rules depend on** → context.

One more thing lives alongside the three, and it is **not** a nature: the
**structure gate** — an allowlist of paths that may be written, everything else
denied. Reach for it when the rule is "writes only land inside this shape", not a
per-file or per-event check. It is one file, `.sloprail/file-guard/structure.yaml`, not a per-name folder:
the project's covers the whole tree, and a plugin's covers only the
`scope` (folders) it declares it owns — all of them combine.
→ [structure-gate.md](structure-gate.md)

## Where each nature lives

```
.sloprail/file-guard/structure.yaml          # the structure gate (one file, not a folder)
.sloprail/file-guard/<name>/file-guard.yaml
.sloprail/gate/<name>/gate.yaml
.sloprail/context/<name>/context.yaml
.sloprail/config.yaml                        # project settings (disabled:, …), not a rule

.sloprail/<nature>/<name>/tests/<case>/test.sh     # sr-test cases of that rule
.sloprail/file-guard/structure.tests/<case>/test.sh  # sr-test cases of the structure gate
```

Nothing else under `.sloprail/` is read as a declaration. In particular nothing under a
rule's `tests/` or under `structure.tests/` is: a case is data (it may carry
declaration-named fixture files), and there is no top-level `.sloprail/tests/`.

### A rule's cases live in its folder

An end-to-end case (`sr-test`) sits in the folder of the ONE rule it proves: the rule's
`tests/<case>/test.sh`, or `file-guard/structure.tests/<case>/test.sh` for the structure
gate (one file, so it owns the sibling folder). Many cases to one rule is the normal
shape. A case that would prove two rules is split in two, duplicating its setup; the folder
a case sits in is its owner. `sr-test run` prints one JSONL line per case, with
`owner` (`gate/<rule>`, `file-guard/structure`) and `subject` (`<owner>:<case>`, prefixed
by the nested folder for a `.sloprail/` below the root: `marketplace/plugins/p:gate/<rule>:<case>`);
`sr-test run --rule gate/<rule>` runs one rule's cases, `--only <text>` those whose subject
contains the text. `sr-test doctor` lists, deterministically, every rule with no case
(`uncovered: <nature>:<rule>`).

One folder per rule, under the directory named for its nature. **The folder name
IS the rule's name** — it is not repeated in a `name:` field, because a name
recorded twice can disagree with itself. The engine appends it to every refusal,
so it is what a user sees when the rule fires. Pick something that reads well
there; use kebab-case.

Check scripts, judge templates (`.md.j2`), prompt frames (`RUBRIC.md`) and any
`rules/` the rule assembles sit **beside** the YAML in the same folder. A
script's command is resolved relative to that folder, and it runs with the
folder as its working directory. Nothing scaffolds this — create the directories
yourself.

The nature is part of a rule's fully-qualified name, `<plugin>/<nature>/<name>`,
because a gate and a context may share a bare name. That qualified form is what a
refusal cites and what `.sloprail/config.yaml`'s `disabled:` list names.

## The event vocabulary

Every event kind, its fields, and the `PreFileWrite` / `PostFileWrite` aliases
are in [events.md](events.md).

A **gate** may trigger on any pre-action kind **plus `Stop`**; a **context** may
trigger on any pre-action kind **plus the `PostFile*` / `PostTagWrite`** kinds,
but **not `Stop`** (a context's `exit` is always checked on Stop anyway — see
[context.md](context.md)). A **file-guard** does not name a kind at all — it
binds to a range of commits by nature.

## The flat event model

Every check and every match reads the event **flat**. A script gets, on stdin, a
`CheckPayload` whose event's own fields are **direct under `.event`** —
`.event.path`, `.event.newContent`, `.event.kind`, `.event.resultKnown`,
`.event.invocations`, `.event.tags`. There is **no** `.event.fields.*` nesting,
and no `guardrailDir` field. Alongside the event, the `CheckPayload` carries
`.transcriptPath` (the session record, for reading what the event does not carry)
and `.context` (every declared context by name, `{active, payload}`).

```json
{"event":{"kind":"PreFileCreate","path":"memories/a.md","newContent":"…","newMarkers":[]},
 "transcriptPath":"/abs/…session.jsonl",
 "context":{"some-context":{"active":true,"payload":{…}}}}
```

The full field set for every event kind, the per-kind tables, the payload
envelopes, and the flat-vs-nested distinction are in **[events.md](events.md)** —
the single reference the nature and check docs point to rather than re-listing.
A file-guard's checks are the exception: they get a `Changeset` payload
(`.changeset.files[]`, no per-file event) — [file-guard.md](file-guard.md).

A **match expression** reads the same facts, but the three scopes differ in
shape — this is the one asymmetry to keep straight:

- A **file-guard**'s match sees the file's own facts **bare**: `path`,
  `markers`, `oldMarkers` (what it carried before the change), `context`.
  `path endsWith "SKILL.md"`.
- A **gate**'s and a **context**'s match **nest** the event under `event`:
  `event.path`, `event.invocations`, `event.tags`, plus `context`.
  `event.path startsWith "memories/decisions/"`.

Full treatment, operators, the glob shorthand (file-guard only), and the
fail-closed rule: [matchers.md](matchers.md).

## Checks: what a rule runs to reach a verdict

Under `checks:` a rule lists **checks**, run in declared order, first refusal
ending it. A check is a **script** or a **judge**, never both:

```yaml
checks:
  - script: ./deterministic-check.sh
  - prepare: ./assemble-context.sh      # optional, feeds the judge
    judge: ./is-it-good.md.j2
    model: size-md                       # optional; a size alias or model name
    allowed_tools: [WebFetch]            # optional; judge-only, tools beyond reading the project
```

- A **script** is the deterministic half: the check payload on stdin, and its
  **exit code is the verdict** — `0` permits, non-zero refuses.
  → [script-checks.md](script-checks.md). **For a file-guard, start from
  [check-template.sh](check-template.sh)** (loops `.changeset.files[]`, fails closed on an
  unreadable changeset); **for a pre-write gate, from
  [gate-check-template.sh](gate-check-template.sh)** (refuses an unknown
  `resultKnown`). Copy it beside the YAML and change only `fine()`. It already
  reads each event kind correctly, which a script written from scratch almost
  never does first time.
- A **judge** is the model half: a Jinja2 prompt template rendered against the
  payload (and any `additionalContext` a `prepare` script assembled), asked for a
  `{"pass": true|false, "reasoning": "…"}` verdict via `sr-agent`. Its file
  tools can read the whole project and write nothing but its verdict; a
  `Bash` grant can widen that.
  → [judge-checks.md](judge-checks.md)

The two check kinds are documented separately because their contracts differ — a
script's `reason`/exit-code verdict and fail-closed-on-cannot-run, versus a
judge's `reasoning` verdict, `prepare` + `.md.j2` + `model`/`allowed_tools`,
and the fail-open-via-a-script escape hatch.

## The refusal contract

A check **refuses by exiting non-zero**, and the engine finds the reason to show
the agent in this order:

1. `{"reason":"…"}` as JSON on stdout — the preferred form
2. plain text on stdout
3. plain text on stderr — `echo "…" >&2; exit 1` is an ordinary refusal
4. failing all that, a message naming the check and its exit status

`exit 0` permits; print nothing, silence is consent. A check that **cannot run
at all** — missing, not executable, an internal error, a timeout — is a
**refusal**, deliberately: a rule that could not be checked must not read as
approval. This is fail-closed, and it is the safe direction. Write a reason
addressed to the agent whose action was blocked, saying what to do instead; the
engine appends the rule's name. (A judge check's verdict key is `reasoning`, not
`reason` — the per-kind contracts are in [script-checks.md](script-checks.md) and
[judge-checks.md](judge-checks.md).)

## The failure this skill exists to prevent

A guardrail that never fires is worse than no guardrail. No guardrail is an
absence someone can notice. A rule that loads cleanly, sits in the project
looking enforced, and silently admits everything is a project believing it is
protected while it is not.

The work is not "write a plausible declaration" — it is "write one, then prove
it refused something."

## Is this rule worth writing

A guardrail earns its place when all of these hold.

**The violation is real and recurring.** You have seen it happen, or the user
named it. A rule against something nobody does costs every session and catches
nothing.

**A machine can tell.** "Writes under `memories/decisions/` without having
loaded `document-strategy`" is decidable by a script. "The change is clean and
targeted" is not — unless you hand it to a judge, which is what a `judge` check
and its `RUBRIC.md` are for.

**Refusing is the right response.** A pre-action gate prevents the action. A
file-guard, or a Stop gate, reports after the fact and sends the agent round
again. If the honest response is neither
— "note it and move on" — a guardrail is the wrong instrument.

**The question is answerable from what a check can reach.** That is more than the
event's own fields: also `sr-session state` for what earlier cycles recorded
([state-management.md](state-management.md)), `sr-session trajectory` /
`sr-session query` for the transcript (via `.transcriptPath`), and the tree
itself. Check them before concluding a rule is unwritable. If the answer
genuinely is not reachable from any of them, say so — writing it anyway produces
the silent no-op.

If a rule fails any of these, say so rather than writing a weaker version.

## Prove it fires

Loading is not firing. A rule that fails to load — an unknown event kind, a
match naming a field the kind does not carry, a check that names neither a
script nor a judge, a duplicate key, a check script that cannot be run — is
reported at Stop, every turn.

Loading clean is not the same as firing. What still never fires:

- a match that is valid but true of nothing real
- a mistyped key **inside** a list element, or a flag read off an open map
- a check whose logic permits where it meant to refuse

For a file-guard, `sr-checks changeset --rule <name> --base <rev> --head <rev>` prints the
range and the payload its checks will get without running anything
([file-guard.md](file-guard.md#seeing-what-a-rule-will-be-handed)), and
`sr-checks run --base <rev> --head <rev>` judges it (`sr-checks verify` re-reads the stored
verdicts without asking a model). Run `sr-checks run` in the foreground and wait for it: it reports
progress on stderr (a heartbeat every 30s) and is safe to run in parallel (judges share a machine-wide
limit, identical checks are judged once, a second run of the same range waits and reuses), so never
poll with `pgrep`.

So cause the action the rule guards and see the refusal. If you cannot make it
refuse, you have not written a working guardrail — you have written a file. Keep that proof
as a case in the rule's own `tests/<case>/test.sh` (see "A rule's cases live in its folder").

## Changing a rule that already stands

The plugin's `grounded-rule-changes` rule judges a change to what already stands in the
project's `.sloprail/`. A change that cites what the user said, or a tool output from
this session, is a grounded one:

```bash
git commit -m 'relax demo: allow untitled notes' \
  -m 'Sloprail-Cites-User: let the demo rule accept untitled notes'
```

A tool output is cited the same way, with `Sloprail-Cites-Tool:`. How a citation is made
and what a rule sees of it: [grounding.md](grounding.md).

## Turning one off

Keep the folder; the YAML body and the sibling prose hold the reasoning that
produced the rule, which is exactly what someone needs when deciding whether to
switch it back on. Turning a rule off is itself a change to the project's rules, cited
as above.

For a rule in **your own** `.sloprail/`, disable it at its source — see each
nature's doc for the exact key. For a rule that **arrived inside an installed
plugin**, editing the plugin's copy is undone by the next reinstall; switch it
off from your side instead, in `.sloprail/config.yaml`:

```yaml
disabled:
  - sloprail/file-guard/authoring-slop
```

The name is `<plugin>/<nature>/<name>`, exactly what the refusal cites. The
nature is part of the key because a gate and a context may share a name:
`disabled: [sloprail/file-guard/authoring-slop]` switches off the plugin's
file-guard and leaves a file-guard of your own called `authoring-slop` in force.

This also works on a shipped rule that will not **load**. A broken declaration
refuses nothing: it is reported to the agent at the next hook and at Stop, every
turn, until it is fixed or switched off — and when it is a plugin's you cannot fix
the file. Naming it here is the way out that does not mean uninstalling the plugin.

## The cross-cutting references

- [events.md](events.md) — every event kind and its flat fields, the per-kind
  tables, and the payload envelopes a check and a judge template read.
- [environment.md](environment.md) — the `SR_*` variables every guardrail script
  receives (`SR_GUARDRAIL`, `SR_WORKSPACE`, `SR_TRANSCRIPT`, …) and the
  `SLOPRAIL_LAUNCHED_BY` re-entry provenance.
- [matchers.md](matchers.md) — the `match:` expression language, the three
  scopes, the glob shorthand, and the fail-closed rule.
- [script-checks.md](script-checks.md) — the deterministic check: the skeleton,
  reading the flat event off stdin, the exit-code verdict, and fail-closed-on-
  cannot-run.
- [judge-checks.md](judge-checks.md) — the model check: `prepare` + the `.md.j2`
  template + `additionalContext`, the `sr-agent` substrate, `model`/
  `allowed_tools`, and the fail-open escape hatch.
- [grounding.md](grounding.md) — changes that must trace to what the user
  said: citing with `sr-file --cite:` or `cite && <cmd>`, `event.citations`,
  `require: [{citation}]`, and judging a citation.
- [state-management.md](state-management.md) — `sr-session state` across cycles,
  the `--owner` cross-guardrail read a gate uses to read a context's registry,
  and the turn-scoping trap.
