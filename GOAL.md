# GOAL — implement everything designed this session, end to end

Everything specced across this session (and the many compacted ones before it,
now crystallised in the **spec**, the **strategy**, and the **use-case
examples**) has to be **implemented**, tested end-to-end, and proven to actually
work — fully autonomously, without stopping to ask questions, because it is all
already spec'd.

## Sources of truth (strict precedence)

1. **Spec** (`sloprail/spec`, `spec/three-natures-and-matcher`, PR #2) — the
   PRIMARY source of truth for FORMATS, INTERFACES, ARCHITECTURE. When an
   example and the spec disagree on a command's flags/shape, the spec wins.
2. **Strategy** (`sloprail/strategy`, decision docs + unit raw material) — the
   source of the BUSINESS LOGIC of each unit: the original user quotes, and the
   reasoning behind every decision.
3. **Use-case examples** (`examples/` on `draft/fileguard-format`) — the frozen
   INTENT of each unit, translated into concrete YAML+scripts. They freeze WHAT
   each guardrail must do, but their exact command arguments / interface usage
   may be slightly wrong or hallucinated — **fixing those to match the real spec
   interfaces is explicitly allowed and expected**, and is part of this wave's
   gate (not only a reviewer's job).

## Branching / PR model

- **Base branch (unchanged): `draft/fileguard-format`** in `sloprail/sloprail`
  (PR #19) — all implementation work-tree branches are cut from it and merge
  back into it.
- Work happens in **git work-trees**, one per slice, cut exclusively from the
  current base branch.
- **PR #19** (`sloprail/sloprail`) absorbs all CODE changes.
- **PR #2** (`sloprail/spec`) absorbs all SPEC changes — including the final
  cleanup of superseded spec once the implementation cleanup lands.

### Worktree procedure (MANDATORY — do NOT use the Agent tool's `isolation:worktree`)

This session's own branch differs from the base branch. If a sub-agent creates
its own isolation worktree it branches from the WRONG base. So the ORCHESTRATOR
(me) creates each worktree EXPLICITLY, off `draft/fileguard-format`, and hands
the agent the ready path:

1. `git worktree add /Users/nsviridenko/ws/sloprail/wt/<slice> -b impl/<slice> draft/fileguard-format`
   (run from `/Users/nsviridenko/ws/sloprail/sloprail`). Worktree convention:
   `/Users/nsviridenko/ws/sloprail/wt/<slice>` on branch `impl/<slice>`.
2. Spawn the implementing agent with **`cwd` = that path**, and **NO
   `isolation` flag** — the worktree already exists on the correct base.
3. Agent implements + tests there, commits to `impl/<slice>`.
4. Reviewer agent (same worktree). Loop fix→review until ZERO issues + green
   tests + green build.
5. Merge `impl/<slice>` → `draft/fileguard-format`, then `git worktree remove`
   the slice + `git branch -D impl/<slice>`.

   **MERGE MECHANICS (learned the hard way):** `draft/fileguard-format` is
   checked out in the `wt/fileguard-format` worktree, so you CANNOT
   `git checkout draft/fileguard-format` from the main repo — it errors and
   leaves HEAD on `main`, and a following `git merge` lands on `main` by
   accident. ALWAYS run the merge from INSIDE `/Users/nsviridenko/ws/sloprail/wt/fileguard-format`
   (that worktree already has the base branch checked out):
     `cd /Users/nsviridenko/ws/sloprail/wt/fileguard-format && git merge --no-ff impl/<slice> -m "..."`
   Then build+test there, `git push`, then from the main repo
   `git worktree remove wt/<slice>` and `git branch -D impl/<slice>` (use -D:
   git measures "merged" against `main`, not the base branch, so -d refuses a
   branch that IS merged into draft/fileguard-format).

## The waves

### Wave 1 — implement the missing engine functionality (may be multi-stage)

Implement every slice of functionality the spec defines and this session
designed but the Go engine does not yet have. Multi-stage because some slices
depend on others (e.g. `trajectory normalize`/`describe`/`cite` before the
examples that call them; the event vocabulary and match-expression evaluation
before the guards that use them). Build ON TOP of the current engine logic.

- **Parallelise aggressively.** Fan out sub-agents per independent slice. Each
  sub-agent will NOT have the full context of this session, so **progressively
  hand each one everything it needs**: the relevant quotes, file paths WITH line
  ranges, the exact spec constructs, which skills to use
  (`implement-cli-command`, `implement-file-store`, `authoring-guardrails`,
  `test-cli-command`), and the acceptance criteria.
- **Tests:** prefer **end-to-end tests at the CLI-command level** (the
  `test-cli-command` skill — compiled binary as a subprocess against a sandboxed
  git repo, the Claude Code mock). Complex isolated logic (like the expression
  language already was) may ALSO be unit-tested.
- **Per-work-tree gate before merge:**
  1. A **reviewer agent** reviews the whole work-tree. If it finds issues, they
     are fixed — by launching another agent (or handing the review report to the
     implementing sub-agent) — and re-reviewed, **looping until the review
     returns ZERO issues**.
  2. **All tests related to that functionality pass**, and the **PR is green
     (CI green)**.
  Green review (zero issues) + green tests/CI = the gate. Nothing merges without
  both.

### Wave 2 — implement + e2e-test every use case (after Wave 1)

Each use-case example gets its OWN end-to-end tests, actually exercising the
guardrail through the CLI/mock in **multiple scenarios** — proper multi-turn
tests that really prove the unit works (pass paths AND the violation paths it
exists to catch). We already have examples of this pattern to follow. Build on
top of the Wave-1 engine.

- Same parallelisation discipline: fan out per use case, hand each sub-agent the
  unit's quotes, the example's files + line ranges, the spec interfaces, the
  skills.
- **Same gate, with a sharper reviewer focus:** the reviewer of a use case's
  e2e tests focuses on whether **all critical AND non-critical edge paths are
  well tested** — so we genuinely know each sloprail use case works. Loop the
  reviewer until zero issues; all tests green; CI green.

### Wave 3 — cleanup (SEPARATE PRs, at the very end, only once everything works)

Only after Waves 1–2 are fully green:

- **Delete the old `GUARDRAIL.md` + hooks format** from the implementation, and
  the **duplicated old e2e tests** of the superseded use cases (the ones under
  the deprecated/duplicated folder — they will make CI red once superseded,
  which is why they are removed only at the very end; until then they stay as
  **reference examples of how to test this lifecycle**).
- **Clean up the superseded spec** in `sloprail/spec` (PR #2) — done AFTER the
  implementation cleanup, because we first implement everything missing, then
  remove everything now superseded.
- Cleanup is its own request(s), layered ON TOP, so the working system is proven
  before anything is deleted. Reviewers again; green gate again.

## Plugin / distribution

Unchanged: **one marketplace plugin** — the existing `sloprail` plugin that
installs the hooks. Installing it is what makes the whole thing start working.
No new plugins.

## Standing rules for the whole effort

- **Fully autonomous. Do not stop, do not ask questions until everything is
  delivered** — it is all spec'd; this is implementation, not design.
- **Parallelise as much as possible** at every wave.
- **The gate everywhere:** green reviewer (zero issues, loop until zero) + all
  related tests passing + CI green. E2e-at-the-CLI-level is the preferred test
  form; unit tests for complex isolated logic.
- Spec is truth for interfaces; examples are truth for intent; fixing an
  example's wrong interface usage to match the spec is allowed and expected.
