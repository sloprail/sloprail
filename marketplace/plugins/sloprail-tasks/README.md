# sloprail-tasks

Guardrails for a `memories/tasks/` workflow, shipped as a self-testing plugin —
the first use-case plugin that carries its own end-to-end suite (`tests/`).

A **task** is a `memories/tasks/<category>/<name>/TASK.md`: a frontmatter (`status`,
`priority`, optionally `depends_on`) plus a body that states the human's ask, and
optionally a sibling `gates/` directory of start-condition files. The lifecycle has
no `done` — a task the reviewer approves is deleted, folder and all. These eight
guardrails keep that lifecycle honest: a task cannot start before what it depends
on is finished, cannot start before its own start conditions hold, and none of the
mechanisms that enforce either can be quietly defeated by editing the frontmatter
or the gate files themselves.

All eight are in the **nature format**: file-guards and gates under `.sloprail/`,
with `match:` / `checks:` / `on:`, flat `.event` fields on the check payload, and
refusals delivered as a non-zero exit carrying `{"reason": …}` on stdout (a judge's
verdict is the engine's `{"pass", "reasoning"}`).

## The citation model

There are **three** citation kinds, and keeping them apart is the point of the
whole review lifecycle. Two live in the frontmatter and one in the body, and they
resolve against different things:

| kind | where | shape | path base | resolves by |
|------|-------|-------|-----------|-------------|
| the **ask** | body | `[quote](jsonl)` link | absolute `.jsonl` | `cite --source-types user` — the quote is the user's own words |
| an **observation** | frontmatter `observations:` | `<jsonl>:<ranges>` string | **absolute** `.jsonl` | `trajectory tool-result --line` — every cited line is a tool_result |
| an **artifact** | frontmatter `artifacts:` | `<file>:<ranges>` string | **repo-relative** tree file | the file-citation checker — the file exists under the repo, the lines exist |

The **body** cites the human's *ask* as inline `[quote](jsonl)` markdown links,
grounded against the `user` pool: `cite --source-types user` resolves the quote to
a real user message and refuses the agent's own output, a tool result, or a
harness-injected user-role message (`<system-reminder>`, `<task-notification>`,
`<local-command>`, …). This is task-body-is-human-authored's subject, and nothing
else's.

The **frontmatter** carries the *delivery evidence* as two typed citation lists —
proof the work was **done**, weighed against the `in_review` claim. An
**observation** is proof the work happened, cited into the session transcript by
**absolute** `.jsonl` path; each cited line must be a **tool_result** the session
produced (a test that came back green), confirmed by `sr-session trajectory
tool-result --line`. An **artifact** is where the result is, cited **repo-relative**
into the working tree; the file must exist under the repo and the cited lines must
exist. The two path bases are load-bearing: an observation is absolute and a
transcript, an artifact is repo-relative and a tree file, and one wearing the
other's shape is a mis-filed citation the schema or the checker refuses.

Why grounding a delivery claim in the user's ask is the bug this fixes: it proves
only that the work was *requested*, never that it was *done*. The `--source-types`
mirror is what separates them — a tool-output quote grounds under `tool_result` and
is refused under `user`, and vice versa.

The frontmatter shape is pinned by `.sloprail/schemas/task.cue` (installed under
the consumer's project), which the deterministic guards read with `sr-file
validate`; its `observations` and `artifacts` are two **separate** citation types
(absolute `.jsonl`; repo-relative tree path), required only in `in_review`.

## Dependencies and start conditions

Two more concepts sit alongside the citation model, both about WHEN a task may
start rather than what proves it finished:

**`depends_on`** — an optional frontmatter list of `<group>/<task-name>` ids
naming OTHER tasks that must be done first:

```yaml
---
status: to_do
priority: P1
depends_on: ["infra/setup-ci", "auth/design-token-format"]
---
```

A task cannot move to `to_do`, `in_progress`, or `in_review` while any task it
depends on still exists — "still exists" IS "not done yet", because a task the
reviewer approves is deleted, folder and all. `depends_on` does not try to
tell a genuinely unfinished dependency apart from a mistyped id: both read as
"this id resolves to nothing right now" from a live folder scan, and the fix
is the same either way (wait, or correct the id). What makes that safe is the
**no-dangling-dependencies invariant**: deleting a task and leaving another
task's `depends_on` still naming it is refused at the end of the turn, so an
id can never legitimately mean "used to exist, now done" while looking
identical to "never existed" — see `no-dangling-dependencies-at-turn-end`
below. Deleting a task and stripping it from every dependent's `depends_on`
must happen as ONE change.

A dependency CYCLE (A depends on B depends on A, directly or through several
hops) is refused at write time.

**`gates/`** — an optional sibling directory,
`memories/tasks/<group>/<name>/gates/<gate-name>.{sh,md}`, one file per START
CONDITION that must hold before the task may leave `backlog`/`blocked` for
`to_do`/`in_progress`:

```
memories/tasks/web/launch-page/
├── TASK.md
└── gates/
    ├── repo-is-public.sh     # deterministic: exit 0 = holds, non-zero = does not
    └── launch-video-exists.md # judgment: a prompt describing the condition
```

A `.sh` gate is an ordinary executable — `task-gates-hold` runs it and treats
`exit 0` as the condition holding, no special grammar, no frontmatter
indirection. A `.md` gate is a judge prompt in plain language ("the launch
video exists and shows a block") — `task-gates-hold`'s judge check decides,
by actually looking (it may Read files), whether it currently holds; the
task's own claim that it holds is not evidence. Every gate must pass before
the transition; the first failing one is named in the refusal.

Why files and not a `ready_when:` frontmatter list: a `.sh` gate IS the
deterministic check, run literally, in the plugin's own native executable
unit — there is no string for a script to re-interpret and no indirection
that could itself be gamed. A `.md` gate IS the judge's prompt text, not a
description of one that some other layer renders.

**Gates are agent-writable, so gates must be protected too.** Nothing stops
an agent from writing a `.sh` gate that always `exit 0`s, or a `.md` gate that
describes a condition the user never asked for (or, worse, a `.md` that
describes something LESS than what they actually need to hold). Both are the
same failure `task-body-is-human-authored` exists to prevent for the ask
itself — a spec quietly softened until every later check passes against the
softened version — relocated to gate files. `task-gate-is-grounded` is the
protection: a gate must be TRACEABLE to the task's own cited ask, must not
CONTRADICT it, must not INVENT a condition the ask does not support, and (for
a `.sh` gate specifically) must not be TRIVIAL — a check that can never
actually fail. See that guard's own section below.

## The eight guardrails

### task-body-is-human-authored — file-guard, preventive

The body of a `TASK.md` is the human's ask, cited, and **nothing else**. This is
the one guard that protects the **oracle** rather than an artifact: the
specification every other check is checked against. The failure it prevents has no
detector once it happens — an agent implements 70% of an ask, edits the body to
describe that 70%, and from then on every verification passes because the spec was
rewritten to match the work.

Two checks, cheap first:
1. **Script** — the body must carry a `[quote](jsonl)` link whose quote grounds via
   `cite --source-types user` to a real user message. No citation, or one that does
   not ground, is refused deterministically before the model. (This is the **ask**,
   the `user` pool — distinct from the delivery evidence in the frontmatter, which
   task-review grounds against the `tool_result` pool and the tree.)
2. **Judge** — the body must correspond to the cited words and hold that **and
   nothing else**. A valid citation wrapped in agent-authored elaboration —
   inferred requirements, a suggested approach, invented rationale — is slop around
   a legitimate reference, and the judge rejects it. Structural restatement (a
   heading, bullets, the ask in fewer words) is fine and the judge is told so.

Preventive: an edit to the ask must be refused **before** it lands, because a
post-write refusal reports damage already done to the oracle. The Stop after-check
backstops writes the engine cannot derive at Pre.

### task-evidence-resolves — file-guard, preventive

The **deterministic floor** — the `in_review` split's structural half, no model.
Every `TASK.md` must satisfy the schema, and every delivery-evidence citation in
its frontmatter must **resolve to the thing its kind promises**:

- an **observation** `<abs-jsonl>:<ranges>` — every cited transcript line is a
  **tool_result** the session produced (via `trajectory tool-result --line`);
- an **artifact** `<repo-relative-file>:<ranges>` — the file exists under the repo
  and every cited line exists.

It spends no model call and makes **no judgement** about whether the evidence
*substantiates* the claim — that is task-review's. It also does **not** check the
body's citation of the ask — that is task-body-is-human-authored's. An `in_review`
task must additionally carry **both** kinds (proof it happened *and* where the
result is) — a claim of finished work missing either is refused. The schema is
closed and has no `done`; a task written into `done` is refused at the point of
writing.

### task-review — file-guard, after-check

`in_review` is a **claim**; this guard judges it — the split's **judged** half. It
proves **delivery**, not the ask (it used to re-cite the user, a duplicate of
task-body — that conflation is the bug this fixes). Bound as an after-check (Post /
Stop only, never Pre — a judged rule evadable by writing the file a different way
would be worse than none), it reads the task's stated outcome and its **delivery
evidence** and asks a model whether the evidence **substantiates** the claim.

- A **script pre-flight** gates on `in_review` (most task writes cost nothing) and
  refuses deterministically if an observation is not a tool_result or an artifact
  does not resolve — there is nothing to review until the evidence resolves.
- The **prepare** gates on `in_review` a **second** time — it is a separate check
  from the pre-flight, and a passing pre-flight does not stop it, so without its own
  gate a to_do task would still pay for the model call. For a non-in_review task it
  emits `{"skip": true}` and the judge check **abstains** (no model call, no verdict).
  For an in_review task it expands the frontmatter evidence to the bytes: each
  observation to the **tool_result content** at its cited line, each artifact to the
  **cited tree lines**. The **judge** weighs that delivered evidence against the claim — a task
  claiming X with evidence showing X *happening* is approved even if X was a poor
  idea; the judge checks the gap between what the task says was done and what the
  evidence shows. Not substantiated → the turn is refused (the task stays
  `in_review`; a Post refusal does not advance the read mark, so it is re-judged next
  cycle until fixed). Substantiated → the write is permitted, and the accepted task
  is deleted by the agent in a commit of its own.

  *Migration note:* the old-format rule refused **both** verdicts — REJECTED to fix
  the evidence, and APPROVED with an instruction to delete the folder. The engine's
  judge is pass/permit or fail/refuse, so the approve-and-force-deletion behaviour
  does not survive the judge-check migration: a PASS is a permit. Deleting an
  approved task is the agent's own next step, as it always was (the old hook ran no
  git either).

**task-evidence-resolves vs task-review — the crisp split.** Both look at the same
two frontmatter lists, but ask different questions. task-evidence-resolves is
**structural**: do the citations *resolve* — is the observation line a tool_result,
does the artifact file+lines exist. task-review is **judged**: given they resolve,
does the delivered evidence *substantiate* the `in_review` claim. The first is a
script; the second is a model call gated behind it.

### task-dependencies-resolve — file-guard, preventive

`depends_on` enforced at the moment a task tries to enter `to_do`,
`in_progress` or `in_review`. Two deterministic questions, no model:

1. **still exists = not done.** Every id in `depends_on` must resolve to a
   task folder that no longer exists. Deliberately does **not** try to tell
   "genuinely unfinished" apart from "never a real task" (a typo) — both read
   identically as "this id resolves to nothing right now" from a live scan,
   and under the no-dangling invariant (below) that is the only question that
   needs asking: an id can never legitimately point at a task that used to
   exist and is now done while looking the same as one that never existed,
   because deleting a task without stripping it from dependents is itself
   refused.
2. **no cycles.** The `depends_on` graph — every `TASK.md` on disk, plus this
   pending write's own edges — must have no path back to this task's own id.

### task-gates-hold — file-guard, preventive (script + judge)

Every file under a task's `gates/` must hold before the task may leave
`backlog`/`blocked` for `to_do`/`in_progress`. Two checks, cheap first:

1. **Script**: every `gates/*.sh`, run in name order with `SR_WORKSPACE` set.
   `exit 0` passes; the first failing (or non-executable, or unrunnable) gate
   refuses, naming it. `.md` files are untouched here.
2. **Prepare + judge**: reached only once every `.sh` gate passed. The prepare
   collects every `gates/*.md` file's text (skipping the model call entirely
   — `{"skip": true}` — when none exist, the same transition-gate discipline
   the script uses); the judge decides, by actually looking (it may Read
   files the gate names), whether each currently holds — the task's own claim
   is not evidence.

This guard **trusts** that everything under `gates/` already cleared the
grounding bar — it runs/judges gates faithfully but makes no judgement about
whether a gate is a *meaningful* test. That is `task-gate-is-grounded`'s job,
at write time, so a trivial or fabricated gate never reaches this guard to be
faithfully "passed."

### task-gate-is-grounded — file-guard, preventive (script + judge)

The protection against weakening, rubber-stamping, or fabricating a gate.
Fires on a write to any `gates/*.sh` or `gates/*.md` file — the same failure
`task-body-is-human-authored` guards for the ask itself, relocated to gate
files: nothing else re-checks a gate's *content* once it exists, only whether
it currently passes.

1. **Script**: the gate's sibling `TASK.md` must carry at least one grounded
   `[quote](jsonl)` body citation — without a cited ask there is nothing to
   trace a gate against, refused before the judge is paid for.
2. **Prepare + judge**: assembles the cited ask (ground truth) and the gate's
   own content and kind. The judge rejects a gate that is not **traceable**
   to the cited ask, that **contradicts** it, that **invents** a condition
   the ask does not support, or — for a `.sh` gate specifically — that is
   **trivial**: a script whose control flow can never actually fail (a bare
   `exit 0`, or one shaped to look like a check but never reaches a failing
   exit). The trivial-gate rejection is what stops a `.sh` gate — an ordinary
   agent-writable executable `task-gates-hold` will literally run and trust —
   from being a rubber stamp.

Reusable deterministic helpers a `.sh` gate may source (`file_exists`,
`task_is_done`, `unit_has_status`, `gh_repo_visibility_is`) live in
`task-gates-hold/lib-checks/` — plain shell functions, not a named-check
dispatch layer; see that directory's README for why using them does not
exempt a gate from this judge.

### no-unfinished-work-at-turn-end — gate, Stop

A turn may not end while work is open. Bound to `Stop` with no matcher (Stop
carries no fields), it walks the task tree and refuses the turn if any task is
`to_do` or `in_progress`. A gate is what BLOCKS a Stop; a context bound to Stop only
tracks state.

The refusal set is exactly `{to_do, in_progress}` — `backlog`, `blocked` and
`in_review` are allowed to rest, because the question is not "finished / unfinished"
but "may this rest unattended". The refusal names **every** open task and its
status (a Stop refusal repeats every cycle until acted on, so it must be a work
queue, not an alarm) and spells out all four ways to end a turn honestly: finish it
(→ `in_review` with evidence), `blocked` with a named blocker, `backlog`, or
`in_review`.

**Fail direction:** the logic fails **closed** (an open task is a refusal); the
plumbing fails **open** (no workspace, no tasks dir, no `sr-file`/`jq` → permit,
naming the path on stderr), because none of those is evidence the work is finished
and refusing on them would wedge a session the agent cannot un-wedge.

### no-dangling-dependencies-at-turn-end — gate, Stop

A turn may not end while any task's `depends_on` names a task folder that no
longer exists. Bound to `Stop` with no matcher, it walks the whole task tree
and, for every `depends_on` id on every task, confirms `memories/tasks/<id>`
still exists.

This is what makes deleting a task and stripping it from every dependent's
`depends_on` **one change** rather than two: `task-dependencies-resolve`
refuses a *dependent* from entering `to_do`/`in_progress`/`in_review` while
one of its own dependencies is missing, but nothing until this gate re-checked
`depends_on` on a task that had **already** passed that gate earlier. Without
it, deleting `memories/tasks/auth/migrate` while
`memories/tasks/web/launch/TASK.md` still lists `"auth/migrate"` in its
`depends_on` would leave a reference to nothing sitting in the tree — the
reviewer who deletes a task is the one who knows it is done and who depended
on it, so the strip belongs in the same turn as the deletion, not left for
whoever next reads the dependent.

**Fail direction:** the same split as `no-unfinished-work-at-turn-end` — the
logic fails **closed** (a dangling reference is a refusal), the plumbing fails
**open** (no workspace, no tasks dir, no `sr-file`/`jq` → permit, naming the
path on stderr).

## Installing

The guardrails live under `.sloprail/` and load as a plugin. The schema
(`.sloprail/schemas/task.cue`) must sit under the consumer project's own
`.sloprail/schemas/` — the guards read it from `$SR_WORKSPACE`, because a task lives
in the consumer's tree.

## Tests

`tests/` is the plugin's own end-to-end module — a separate Go module that drives
the a10n-claude-mock through the shared harness and asserts each guardrail's
refusals and permits. See `tests/README.md`.


## The plugin's own structure gate

`.sloprail/file-guard/structure.yaml` is this plugin's piece of the structure
gate — the allowlist for what may be written under `memories/tasks/`, scoped
to that subtree with `scope: [{glob: "memories/tasks/**"}]` so it composes
with a project's own structure gate (and any other plugin's) rather than
replacing it. It allows exactly the shapes this README describes:

```yaml
scope:
  - glob: "memories/tasks/**"
allow:
  - regex: '^memories/tasks/README\.md$'
  - regex: '^memories/tasks/[a-z0-9-]+/[a-z0-9-]+/TASK\.md$'
  - regex: '^memories/tasks/[a-z0-9-]+/[a-z0-9-]+/gates/[a-z0-9-]+\.(md|sh)$'
  - regex: '^memories/tasks/[a-z0-9-]+/[a-z0-9-]+/[^/].*$'
```

In order: an optional top-level `memories/tasks/README.md`; a task's own
`TASK.md`; a gate file under its `gates/` directory (`.md` or `.sh`, the two
kinds `task-gates-hold` and `task-gate-is-grounded` recognize); and, last and
broadest, anything else directly under a task's own folder — supporting
material a task keeps beside its `TASK.md` (notes, a linked spec) that is not
itself a gate. The gate-name segment is deliberately the SAME
`[a-z0-9-]+` character class the group/task-name segments use, for one
convention across the whole tree rather than a bespoke one per level.

**Composition note.** The engine composes a project's own structure gate with
each installed plugin's `scope`d piece — each plugin owns the allowlist for
its own subtree, so installing this plugin does not require a consumer to
hand-copy its shapes into their own `structure.yaml`. As of this writing, the
engine on `main` does not yet apply a plugin's structure gate when the
CONSUMER project has one of its own (composition is landing separately); this
file is still correct and self-checking (`sr-file declarations` parses it) —
only the cross-plugin composition step is pending elsewhere.
