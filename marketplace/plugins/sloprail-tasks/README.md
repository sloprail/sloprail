# sloprail-tasks

Guardrails for a `memories/tasks/` workflow, shipped as a self-testing plugin —
the first use-case plugin that carries its own end-to-end suite (`tests/`).

A **task** is a `memories/tasks/<category>/<name>/TASK.md`: a frontmatter (`status`,
`priority`) plus a body that states the human's ask. The lifecycle has no `done` —
a task the reviewer approves is deleted, folder and all. These four guardrails keep
that lifecycle honest.

All four are in the **nature format**: file-guards and a gate under `.sloprail/`,
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

## The four guardrails

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
- The **prepare** expands the frontmatter evidence to the bytes: each observation to
  the **tool_result content** at its cited line, each artifact to the **cited tree
  lines**. The **judge** weighs that delivered evidence against the claim — a task
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

## Installing

The guardrails live under `.sloprail/` and load as a plugin. The schema
(`.sloprail/schemas/task.cue`) must sit under the consumer project's own
`.sloprail/schemas/` — the guards read it from `$SR_WORKSPACE`, because a task lives
in the consumer's tree.

## Tests

`tests/` is the plugin's own end-to-end module — a separate Go module that drives
the a10n-claude-mock through the shared harness and asserts each guardrail's
refusals and permits. See `tests/README.md`.
