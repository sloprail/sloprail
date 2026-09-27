# sloprail-tasks — the plugin's own end-to-end tests

This is the **first plugin-local e2e module** — the concrete instance of the
"each use-case plugin self-tests" model. The `sloprail-tasks` plugin ships its
four guardrails *and* the end-to-end tests that prove them, together, as one
self-contained unit. Anyone lifting this plugin gets the tests that show its
guardrails actually fire.

## What this module is

A **separate Go module** (`marketplace/plugins/sloprail-tasks/tests/go.mod`,
module `github.com/sloprail/sloprail-tasks/tests`), nested inside the plugin it
tests. It is not part of the main repo's module — it has its own `go.mod` — so the
plugin is a unit that carries its own verification.

It reuses the main repo's **shared e2e harness**
(`github.com/sloprail/sloprail/tests/e2e/harness`), the same one
`tests/e2e/session/*` and `tests/e2e/examples/*` drive. That harness is pulled in
with a `replace` directive pointing at the repo root:

```
require github.com/sloprail/sloprail v0.0.0-00010101000000-000000000000
replace github.com/sloprail/sloprail => ../../../..
```

The path `../../../..` is this `tests/` directory up to the repo root that holds
the main `go.mod` (`marketplace/plugins/sloprail-tasks/tests` → four levels up).
`go mod tidy` resolves the harness and its transitive dependencies against the
local checkout, so the harness — and the engine whose hooks the mock reaches — is
exactly the one in this tree.

## How the tests drive the guardrails

Each test:

1. stands up an isolated project with the base `sloprail` plugin enabled
   (`harness.New(t)` + `e.Project()` — the plugin's hooks are what run the nature
   dispatch);
2. **installs this plugin's own `.sloprail` tree** into the project verbatim
   (`installPluginTree`), preserving execute bits, and **commits it** so the guard
   scripts are part of the session baseline rather than the first cycle's diff (the
   base plugin's `authoring-slop` after-check would otherwise judge them with no
   model and fail closed — production installs before the session, and this
   reproduces that);
3. drives the **a10n-claude-mock** through the harness (`e.Run(proj, sess, prompt,
   Turns(...))`) to enact the agent's writes and turn-ends;
4. asserts the outcome: `res.Refused()` / `e.Exists(...)` for a preventive Pre
   refusal, `e.BlockingErrorsFrom(proj, sess, "Stop")` for a Stop-time block, and
   `res.Saw(...)` for the reason reaching the agent.

The harness resolves the mock via `exec.LookPath("a10n-claude-mock")` on `PATH`,
so this module uses the **same installed mock** the main suite does. Run the suite
with `CLAUDECODE` and `CLAUDE_CODE_ENTRYPOINT` **ambient-unset** to reproduce CI
(the mock sets them itself for the hook environment; a developer shell that already
exports `CLAUDECODE` masks the gap CI does not have):

```
cd marketplace/plugins/sloprail-tasks/tests
go build ./...
env -u CLAUDE_CODE_SESSION_ID -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT go test ./... -count=1
```

## Citations are resolved for real, not stubbed

A task file holds derived text only; its grounding rides on the **action**. The
tests make a grounded change the way an agent does — a `Bash` turn running
`sr-file write|edit … --cite:<pool> '<quote>'` on its own (`srWrite` / `srEdit`
in `main_test.go`) — and the session resolves each quote against the transcript
the mock wrote, for real, before any guard sees `.event.citations`:

- **the ask** — `--cite:user 'migrate the auth module'`. The harness seeds the
  run's prompt as the transcript's root user message, so the quote resolves in the
  `user` pool. A test cites user words only in a session's FIRST run: a repeat Run
  appends its prompt as another user message, and a quote matching two entries
  resolves to neither — so a later run that needs a fresh user citation passes a
  prompt of its own and quotes that.
- **proof it happened** — `--cite:tool_result 'TESTS-PASSED-42'`. `deliveryTurns`
  runs a real `Bash` turn that prints `proofOutput`, so the tool_result is on the
  transcript before the `in_review` write cites it — the work happens, then the task
  claims it, in one run. An AskUserQuestion answer cited as tool output exercises
  the refusal (the answer is the user's words, never a produced result).
- **an artifact** — a frontmatter `<repo-relative-file>:<ranges>`, resolved against
  the working **tree**. `deliveryTurns` writes the real file; an absent file, or an
  absolute path, exercises the refusals.

An uncited write is the plain `Write` tool, which cannot carry a citation.

## What is stubbed, and why

The only stub is the **judge model verdict** (`InstallJudgeClaude`), exactly as the
main suite's judge e2e do: sr-agent's judge path invokes `claude` with
`--model`/`--settings` flags the mock does not accept, so the model's own text is
replaced by a fixed `{"pass": …, "reasoning": …}` while the whole judge path — the
template render, the sr-agent invocation, the verdict parse — runs. `pass: true`
admits, `pass: false` refuses and the reasoning reaches the agent.

One consequence, handled per test: the harness gives **one** verdict to **every**
judge in a run. Where a test needs `task-review`'s judge to FAIL while
`task-body-is-human-authored`'s judge PASSES (they guard the same path), the test
disables `task-body` from the project config (`DisableFileGuard`) so the single
stubbed verdict is the one under test. This is noted in each such test.

## Per-test coverage — and what is deliberately NOT covered

**task-body-is-human-authored** (file-guard, preventive; script + judge)
- ✓ task created with a user citation (judge PASS) admits and lands; the judge is
  handed the cited words and their `path:line`
- ✓ slop body (cited + judge FAIL) refused, reasoning reaches agent
- ✓ uncited create (the Write tool) refused by the script before the judge, naming
  the `sr-file … --cite:user` form
- ✓ uncited BODY change to an existing task refused; the file keeps its body
- ✓ status-only change permitted with no citation, and the judge is never called
- ✓ cited create followed by an uncited status edit is NOT refused at Stop — the
  engine keeps the path's recorded citation through the uncited edit (asserted)
  and the Stop judge is handed it
- *not covered:* the AskUserQuestion envelope shown to the judge; the fail-open
  reconciliation branches (a judge-machinery failure now fails closed, which is the
  engine's behaviour, not this guard's to re-prove).

**task-evidence-resolves** (file-guard, preventive; script) — the deterministic half
- ✓ in_review write citing real tool output, with a real repo-relative artifact,
  permits and lands (and the file carries no transcript path)
- ✓ transition into in_review citing only the USER's words refused, naming the
  `--cite:tool_result` form; the status stays in_progress
- ✓ transition into in_review with the Write tool (no citation at all) refused
- ✓ an AskUserQuestion answer cited as tool output does not move the task
- ✓ artifact absent from the tree refused; artifact with an ABSOLUTE path refused by
  the schema
- ✓ the retired `observations:` field refused by the closed schema
- ✓ invalid frontmatter (`status: done`) refused by the schema
- ✓ in_review with cited proof but no artifact refused
- *not covered:* the Post-moment refusal of a transition that reached the tree
  without passing pre-tool; the underivable-Pre defer path.

**task-review** (file-guard, after-check; script + judge) — the judged half, DELIVERY
- ✓ substantiated in_review task (cited tool output + real artifact, judge PASS)
  permits — and the reviewer is handed the FULL tool output, not only the quote
- ✓ unsubstantiated in_review task blocked at Stop (judge FAIL), rejection reaches
  the agent — the evidence is there but does not show the claimed thing
- ✓ a cited transition followed by an uncited edit in the same session still
  reaches the reviewer with its tool output (citations accumulate)
- ✓ a task already in_review at session start, edited with no tool output cited,
  is refused by the pre-flight at Stop (nothing on record), naming how to cite it
- ✓ a gates/*.sh that regressed, and a gates/*.md the review judge rejects, block
  the in_review claim at Stop
- *not covered:* the re-fire-every-cycle loop across multiple cycles (the block is
  shown once); the approve-path's "delete the folder" instruction (that is agent
  workflow, and the binary judge's PASS is a permit — see the guard's file-guard.yaml
  for why that old behaviour does not survive the judge-check migration).

**no-unfinished-work-at-turn-end** (gate, Stop; script)
- ✓ a turn with an open (to_do) task blocked at Stop, naming the task and status,
  and teaching the cited in_review transition
- ✓ a turn whose task is at a resting status (in_review) permits
- *not covered:* the blocked/backlog resting statuses individually (in_review stands
  in for the resting set); the plumbing fail-open branches (a missing schema / tool
  permits — deterministic and unit-shaped, not driven here).

The coverage is **focused, not exhaustive**: one real end-to-end per guardrail's
key cases, honest about the branches it leaves to the deterministic layer and the
engine. Scaling this to the full "each use-case has exhaustive e2e" model is P3
work; this establishes the pattern and proves it runs.
