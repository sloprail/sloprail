# sloprail-tasks

Guardrails for a `memories/tasks/` workflow, shipped as a self-testing plugin —
the first use-case plugin that carries its own end-to-end suite (`tests/`).

A **task** is a `memories/tasks/<category>/<name>/TASK.md`: a frontmatter (`status`,
`priority`, optionally `depends_on`) plus a body that states the human's ask, and
optionally a sibling `gates/` directory of start-condition files. The lifecycle has
no `done` — a task the reviewer approves is deleted, folder and all. These nine
guardrails keep that lifecycle honest: a task folder holds nothing until its own
`TASK.md` exists, a task cannot start before what it depends on is finished,
cannot start before its own start conditions hold, and none of the mechanisms
that enforce either can be quietly defeated by editing the frontmatter or the
gate files themselves.

All nine are in the **nature format**: file-guards and gates under `.sloprail/`,
with `match:` / `checks:` / `on:`, flat `.event` fields on the check payload, and
refusals delivered as a non-zero exit carrying `{"reason": …}` on stdout (a judge's
verdict is the engine's `{"pass", "reasoning"}`).

## The citation model

A task file holds **derived text only**. Nothing in a `TASK.md` names a session
transcript, because a transcript path resolves on no other machine. What grounds a
task in the session — the user's words, the tool output proving the work — rides
on the **action** that changes the file, and sloprail verifies it there.

Three things ground a task's lifecycle, and keeping them apart is the point of the
review:

| what | where it lives | how it is given | checked by |
|------|----------------|-----------------|------------|
| the **ask** — the user's own words | on the write that creates the task or changes its body | `sr-file write\|edit … --cite:user '<exact words>'` | task-body-is-human-authored |
| **proof it happened** — tool output | on the write that moves the task **into** `in_review` | `sr-file edit … --cite:tool_result '<exact output>'` | task-evidence-resolves (present), task-review (substantiates) |
| **where the result is** — produced files | frontmatter `artifacts:` | `<repo-relative-file>:<ranges>` | task-evidence-resolves (resolves in the tree), task-review |

A cited change is made with `sr-file`, which takes the Write/Edit tools' arguments
plus `--cite:<pool> '<quote>'` (repeatable), run **on its own** in the Bash line —
nothing else on it but `sr-file` calls, `&&` and `echo` — so the pre-tool hook can
dry-run it and hand every rule the exact resulting bytes:

```bash
sr-file write memories/tasks/auth/migrate-tokens/TASK.md --cite:user 'migrate the auth module' <<'TASK'
---
status: to_do
priority: P1
---

Migrate the auth module to the new token format.
TASK

sr-file edit memories/tasks/auth/migrate-tokens/TASK.md \
  --old-string 'status: in_progress' --new-string 'status: in_review' \
  --cite:tool_result 'ok  sloprail/auth  0.42s'
```

Before any rule sees the event, the session resolves each quote against its own
record — the `user` pool is the user's own messages (never the agent's output, a
tool result, or a harness-injected `<system-reminder>`/`<task-notification>`), the
`tool_result` pool is what tools returned (never an AskUserQuestion answer, which is
the user's words, nor a hook's refusal) — and puts the ones that resolve on
`.event.citations` as `{quote, sourceTypes, path, line, message}`. A quote that matches nothing, or more than one
entry, is not a citation. So a rule sees only citations that **exist**; whether one
actually grounds the change is the judges' question.

Why the pools are kept apart: grounding a delivery claim in the user's ask proves
only that the work was *requested*, never that it was *done*.

The grounding is **conditional**, so each guard declares it with a `when:` script
that says whether this write needs it:

```yaml
require:
  - citation: {source_types: [user]}
    when: ./body-changed.sh
```

A write that leaves the body byte-identical — a status, priority or `depends_on`
change — needs no user citation; only the transition **into** `in_review` needs
tool output (`when: ./in-review.sh --entering`). An uncited write that does need
one is refused by the engine before any check runs, naming the `sr-file` form.

The frontmatter shape is pinned by `.sloprail/schemas/task.cue`, which the
deterministic guards read from the plugin's own tree (via `$SR_GUARDRAIL_DIR`,
never copied into the consumer's project) with `sr-file validate`. It is closed:
the old `observations:` list of absolute `.jsonl:<ranges>` strings is gone, and a
task still carrying one is refused. Bodies that still quote the user as inline
`[quote](path)` links are neither required nor refused.

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
`to_do`/`in_progress` — AND that must STILL hold when the task later claims
`in_review`, since a condition true at the start is not guaranteed to still be
true at completion (see "Gates hold twice" under `task-review` below):

```
memories/tasks/web/launch-page/
├── TASK.md
└── gates/
    ├── repo-is-public.sh     # deterministic: exit 0 = holds, non-zero = does not
    └── launch-video-exists.md # judgment: a prompt describing the condition
```

A `.sh` gate is an ordinary executable — `task-gates-hold` (at the start) and
`task-review` (at the completion claim) both run it and treat `exit 0` as the
condition holding, no special grammar, no frontmatter indirection. A `.md`
gate is a judge prompt in plain language ("the launch video exists and shows
a block") — a judge decides, by actually looking (it may Read files), whether
it currently holds; the task's own claim that it holds is not evidence. Every
gate must pass before the transition, and every gate must still pass at the
`in_review` claim; the first failing one is named in the refusal.

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
softened version — relocated to gate files. A gate carries no citations of
its own; only a write that sets a `TASK.md`'s body cites the user's words, and
that grounding is validated separately, every time the body is written. `task-gate-is-
grounded` is the protection: a judge is handed the gate file and the task's
own content (as it stands) and decides whether the gate is DERIVED from that
task — TRACEABLE to it, not CONTRADICTING it, not INVENTING a condition it
does not support, and (for a `.sh` gate specifically) not TRIVIAL — a check
that can never actually fail. See that guard's own section below.

## The nine guardrails

### task-md-first — PreFileWrite gate + file-guard

*Prevention is a gate, the settled-state check a plain file-guard: every rule below of this shape ships two folders of the same name, `gate/<name>/` (refuses the write before it lands; a write whose result the engine cannot compute — `sed -i`, a notebook create — is refused by the gate's script) and `file-guard/<name>/` (the same check on the settled file at Stop). A refusal names `(gate <name>)` at pre-tool and `(file-guard <name>)` at Stop.*

Over any file inside a task folder (`memories/tasks/<group>/<task>/`, gate
files under its `gates/` included) other than `TASK.md` itself. A task folder
with no `TASK.md` names no ask, no status, no priority — every other guard in
this plugin keys off `TASK.md`, so a file written before it exists is
orphaned. The check is purely path-based: it computes the task folder as the
first two segments after `memories/tasks/` (not simply the file's own parent,
since a `gates/*.sh`/`gates/*.md` file sits one level deeper than `TASK.md`)
and refuses the write unless `<task folder>/TASK.md` already exists on disk.

Deletions are not this guard's business (`deletions` is left at its default,
`skip`): the reviewer deletes an **approved** task's whole folder — `TASK.md`
included — as its own commit once `task-review` passes (see that guard's
"approve path" below), and a default file-guard is never even dispatched a
`PreFileDelete`/`PostFileDelete` for its match to consider.

The same failure `unit-md-first` guards for a content unit in
`sloprail-content`, relocated to the task shape.

### task-body-is-human-authored — PreFileWrite gate + file-guard

The body of a `TASK.md` is the human's ask, grounded, and **nothing else**. This
is the one guard that protects the **oracle** rather than an artifact: the
specification every other check is checked against. The failure it prevents has no
detector once it happens — an agent implements 70% of an ask, edits the body to
describe that 70%, and from then on every verification passes because the spec was
rewritten to match the work.

Two checks, cheap first:
1. **Script** — a write that **creates** the task, or **changes its body** (the
   prose after the frontmatter, compared with the file on disk at Pre and with the
   session baseline at Stop), must carry at least one citation of the user's own
   words (`--cite:user`). None is refused deterministically before the model, and
   the refusal spells out the `sr-file` form to use. A write that leaves the body
   byte-identical — a status change — is permitted uncited.
2. **Judge** — the body must correspond to the cited words and hold that **and
   nothing else**. It is handed each cited quote and the transcript `path:line` it
   resolved to. A valid citation wrapped in agent-authored elaboration — inferred
   requirements, a suggested approach, invented rationale — is slop around a
   legitimate reference, and the judge rejects it. Structural restatement (a
   heading, bullets, the ask in fewer words) is fine and the judge is told so. The
   prepare skips the judge when the body did not change, so a status edit costs no
   model call.

Preventive: an edit to the ask must be refused **before** it lands, because a
post-write refusal reports damage already done to the oracle. The Stop after-check
backstops writes that reached the tree without passing pre-tool.

*Uncited frontmatter edits and the Stop check.* The engine hands a Stop event
the citations of every cited change that landed on the path this session, and a
citation grounds only the change it rode on, in the pool it was cited in: each
part of the file's change the agent made that no such citation rode on must be
one the rule's `when` waives (a change the agent did not make, such as your own
edit between turns, is not charged). A status edit leaves the
body unchanged, so `body-changed.sh` waives it, and a task created with a
citation and then moved to `in_progress` with a plain edit still reaches Stop
with its ask's citation. Each uncited *body* change is refused at pre-tool, and
at Stop should one reach the tree another way.

### task-evidence-resolves — PreFileWrite gate + file-guard

The **deterministic floor** — the `in_review` split's structural half, no model.

- Every `TASK.md` must satisfy the schema (closed; no `done`, no `observations`).
- Every **artifact** `<repo-relative-file>:<ranges>` must resolve — the file exists
  under the repo and every cited line exists — and an `in_review` task must name
  at least one.
- A write that moves the task **into** `in_review` (old status not `in_review`, new
  status `in_review`; a create straight into `in_review` counts) must carry at
  least one citation whose `sourceTypes` include `tool_result` — the output of the
  test run or build that proves the work, cited on the claim itself. The refusal
  names the `sr-file edit … --cite:tool_result` form.

It spends no model call and makes **no judgement** about whether the evidence
*substantiates* the claim — that is task-review's. It also does **not** check the
body's grounding in the ask — that is task-body-is-human-authored's. A task written
into `done` is refused at the point of writing.

### task-review — file-guard, after-check

`in_review` is a **claim**; this guard judges it — the split's **judged** half. It
proves **delivery**, not the ask (it used to re-cite the user, a duplicate of
task-body — that conflation is the bug this fixes). Bound as an after-check (Post /
Stop only, never Pre — a judged rule evadable by writing the file a different way
would be worse than none), it reads the task's stated outcome and its **delivery
evidence** and asks a model whether the evidence **substantiates** the claim.

- A **script pre-flight** gates on `in_review` (most task writes cost nothing),
  re-runs every `gates/*.sh` under the task (the SAME start conditions
  `task-gates-hold` held at the beginning of work — see "Gates hold twice"
  below), and refuses deterministically if a gate fails, if no tool output is
  cited on record for the claim (a task already in_review when the session
  began, edited with no tool output cited), or if an artifact is missing or
  does not resolve — there is nothing to review until the evidence is there and
  the gates still hold. The tool output must ground the claim as it stands: an
  in_review task edited again without citing tool output after its cited
  transition is refused at Stop, since a citation grounds only the change it
  rode on, and only in the pool it was cited in.
- The **prepare** gates on `in_review` a **second** time — it is a separate check
  from the pre-flight, and a passing pre-flight does not stop it, so without its own
  gate a to_do task would still pay for the model call. For a non-in_review task it
  emits `{"skip": true}` and the judge check **abstains** (no model call, no verdict).
  For an in_review task it expands the evidence to the bytes: each `tool_result`
  citation on the event to its **quote and the full tool result** at its line
  (`sr-session trajectory tool-result --path --line` — the reviewer judges the whole
  output, not only the words the agent chose to quote), each artifact to the
  **cited tree lines** — and collects every `gates/*.md` judgment gate's text. The
  **judge** weighs the delivered evidence against the claim AND decides whether
  every judgment gate still holds, in the SAME model call — a task claiming X
  with evidence showing X *happening* is approved even if X was a poor idea; the
  judge checks the gap between what the task says was done and what the
  evidence shows, and whether each gate's condition is still true right now. Not
  substantiated, or a gate no longer holding → the turn is refused (the task
  stays `in_review`; a Post refusal does not advance the read mark, so it is
  re-judged next cycle until fixed). Substantiated, gates holding → the write is
  permitted, and the accepted task is deleted by the agent in a commit of its
  own.

**Gates hold twice: at the start, and again at completion.** A task's `gates/`
files are its own declared preconditions. `task-gates-hold` holds them once,
at the moment work BEGINS (`backlog`/`blocked` → `to_do`/`in_progress`) — but
a condition true then is not guaranteed to still be true when the agent later
claims `in_review`: a repo made private again, a dependency that regressed. So
`task-review` holds the SAME gates a second time, at the completion claim —
`gates/*.sh` deterministically in its pre-flight (the identical faithful run
`task-gates-hold` performs), `gates/*.md` folded into the SAME judge call that
weighs the delivery evidence, costing no second model call. `depends_on` is
NOT re-held here: it names *other* tasks' completion, not a condition of this
task's own that could regress, so it stays a start-only check
(`task-dependencies-resolve`).

  *Migration note:* the old-format rule refused **both** verdicts — REJECTED to fix
  the evidence, and APPROVED with an instruction to delete the folder. The engine's
  judge is pass/permit or fail/refuse, so the approve-and-force-deletion behaviour
  does not survive the judge-check migration: a PASS is a permit. Deleting an
  approved task is the agent's own next step, as it always was (the old hook ran no
  git either).

**task-evidence-resolves vs task-review — the crisp split.** Both look at the same
evidence — the tool output cited on the claim and the frontmatter artifacts — but
ask different questions. task-evidence-resolves is **structural**: is the evidence
*there* — does the transition cite tool output, does each artifact's file and lines
exist. task-review is **judged**: given it is there, does the delivered evidence
*substantiate* the `in_review` claim. The first is a script; the second is a model
call gated behind it.

### task-dependencies-resolve — PreFileWrite gate + file-guard

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

### task-gates-hold — PreFileWrite gate + file-guard (script + judge)

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

### task-gate-is-grounded — PreFileWrite gate + file-guard (judge)

The protection against weakening, rubber-stamping, or fabricating a gate.
Fires on a write to any `gates/*.sh` or `gates/*.md` file — the same failure
`task-body-is-human-authored` guards for the ask itself, relocated to gate
files: nothing else re-checks a gate's *content* once it exists, only whether
it currently passes.

A gate carries **no citations of its own** — only a write that sets a
`TASK.md`'s body cites the user's words, and that grounding is
`task-body-is-human-authored`'s subject, validated separately every time the
body is written. So this guard
is **one check, no script stage**: prepare hands the judge the gate file's
own content and kind, and the sibling `TASK.md`'s full content as it stands
(frontmatter and body together) — no second citation-extraction pipeline
duplicating what already validated the task body. The judge rejects a gate
that is not **traceable** to the task, that **contradicts** it, that
**invents** a condition the task does not support, or — for a `.sh` gate
specifically — that is **trivial**: a script whose control flow can never
actually fail (a bare `exit 0`, or one shaped to look like a check but never
reaches a failing exit). The trivial-gate rejection is what stops a `.sh`
gate — an ordinary agent-writable executable `task-gates-hold` will literally
run and trust — from being a rubber stamp.

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
(→ `in_review` via `sr-file edit … --cite:tool_result`, naming the artifacts),
`blocked` with a named blocker, `backlog`, or `in_review`.

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
(`.sloprail/schemas/task.cue`) ships inside the plugin's own tree — the guards
read it via `$SR_GUARDRAIL_DIR` (their own folder), never a copy under the
consumer project. Installing the plugin is the only step; nothing needs to be
copied into the consumer's `.sloprail/`.

## Tests

`tests/` is the plugin's own end-to-end module — a separate Go module that drives
the a10n-claude-mock through the shared harness and asserts each guardrail's
refusals and permits. See `tests/README.md`.


## The plugin's own structure gate

`.sloprail/file-guard/structure.yaml` is this plugin's piece of the structure
gate — the allowlist for what may be written under `memories/tasks/`. It
declares that folder as its `scope` (a literal folder, ending in `/` — a
plugin's structure gate must name what it owns, per
`structure-gate.md`), and its `allow`/`deny` decide only paths inside it. It
composes with the project's own structure gate and any other plugin's rather
than replacing them — see "How a write is decided" in
`marketplace/plugins/sloprail/skills/authoring-guardrails/structure-gate.md`
for the full ownership/veto rules.

```yaml
scope:
  - glob: "memories/tasks/"
allow:
  - glob: "memories/tasks/README.md"
  - regex: '^memories/tasks/[a-z0-9-]+/[a-z0-9-]+/TASK\.md$'
  - regex: '^memories/tasks/[a-z0-9-]+/[a-z0-9-]+/gates/[a-z0-9-]+\.(md|sh)$'
  - regex: '^memories/tasks/[a-z0-9-]+/[a-z0-9-]+/[^/]+$'
```

In order: an optional top-level `memories/tasks/README.md`; a task's own
`TASK.md`; a gate file under its `gates/` directory (`.md` or `.sh`, the two
kinds `task-gates-hold` and `task-gate-is-grounded` recognize); and, last and
broadest, any file sitting DIRECTLY under a task's own folder — supporting
material a task keeps beside its `TASK.md` (notes, a linked spec) that is not
itself a gate. `[^/]+`, not `.*`: a dot in a regex matches a `/` too, so the
naive `[^/].*` this used to read let ANY depth under a task folder through
(measured); the fix is one more level of nesting refused, not permitted. The
gate-name segment is deliberately the SAME `[a-z0-9-]+` character class the
group/task-name segments use, for one convention across the whole tree rather
than a bespoke one per level.

Because the plugin's `allow` decides everything inside its scope — the
project's `allow` does not widen it, only its `deny` can veto inside — a
write to `memories/tasks/<group>/<task>/random/deep/file.bin` is refused by
THIS plugin even if a project's own structure would otherwise have allowed
it: nested more than one level below the task folder matches none of the
four entries above.

Validate what this file ships before installing it, the same way a consumer
would:

```
sr-file declarations --plugin sloprail-tasks marketplace/plugins/sloprail-tasks
```
