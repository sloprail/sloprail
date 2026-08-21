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

Evidence and the body's ask are grounded in the user's **own words**, as markdown
links `[<quote>](<jsonl-path>)`: the link text is the quote, the href is the
transcript it was said in. A guardrail validates each link with **one command** —
`sr-session trajectory cite --path <jsonl-path> "<quote>"` — which resolves the
quote to a real user message or refuses it. `cite` is the single validator: it
excludes the agent's own output, tool results, and harness-injected user-role
messages (`<system-reminder>`, `<task-notification>`, `<local-command>`, …), so a
quote that is not the person's own typing fails to ground. The guardrails
themselves walk no transcript and filter no tags — they grep the links and trust
`cite`.

The frontmatter shape is pinned by `.sloprail/schemas/task.cue` (installed under
the consumer's project), which the deterministic guards read with `sr-file
validate`.

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
   `cite` to a real user message. No citation, or one that does not ground, is
   refused deterministically before the model.
2. **Judge** — the body must correspond to the cited words and hold that **and
   nothing else**. A valid citation wrapped in agent-authored elaboration —
   inferred requirements, a suggested approach, invented rationale — is slop around
   a legitimate reference, and the judge rejects it. Structural restatement (a
   heading, bullets, the ask in fewer words) is fine and the judge is told so.

Preventive: an edit to the ask must be refused **before** it lands, because a
post-write refusal reports damage already done to the oracle. The Stop after-check
backstops writes the engine cannot derive at Pre.

### task-evidence-resolves — file-guard, preventive

The deterministic floor. Every `TASK.md` must satisfy the schema, and every
`[quote](jsonl)` evidence link must ground. It spends no model call. An `in_review`
task must additionally carry at least one grounded citation — a claim of finished
work with no evidence is refused. The schema is closed and has no `done`; a task
written into `done` is refused at the point of writing.

### task-review — file-guard, after-check

`in_review` is a **claim**; this guard judges it. Bound as an after-check (Post /
Stop only, never Pre — a judged rule evadable by writing the file a different way
would be worse than none), it reads the task's stated outcome and its grounded
evidence and asks a model whether the evidence **substantiates** the claim.

- A **script pre-flight** gates on `in_review` (most task writes cost nothing) and
  refuses deterministically if a citation does not ground — there is nothing to
  review until the evidence resolves.
- The **judge** decides substantiation. Not substantiated → the turn is refused
  (the task stays `in_review`; a Post refusal does not advance the read mark, so it
  is re-judged next cycle until fixed). Substantiated → the write is permitted, and
  the accepted task is deleted by the agent in a commit of its own.

  *Migration note:* the old-format rule refused **both** verdicts — REJECTED to fix
  the evidence, and APPROVED with an instruction to delete the folder. The engine's
  judge is pass/permit or fail/refuse, so the approve-and-force-deletion behaviour
  does not survive the judge-check migration: a PASS is a permit. Deleting an
  approved task is the agent's own next step, as it always was (the old hook ran no
  git either).

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
