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
env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT go test ./... -count=1
```

## Citations are grounded, not stubbed

The guardrails ground a claim in the user's own words by resolving a
`[quote](jsonl-path)` markdown link with `sr-session trajectory cite`. The tests
do **not** stub that: the harness seeds the session's transcript with the run's
prompt as the line-1 user message, and a test that cites a substring of that
prompt (`cite("migrate the auth module", transcriptPath, 1)`) has the guardrail
resolve it for real against the streamed transcript. A test that cites a
fabricated string exercises cite's real "no match" path — including cite's own
exclusion of tool results and harness-injected user-role messages
(`<system-reminder>` / `<task-notification>` / …), which is what makes the
evidence links safe.

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
- ✓ human-authored body (grounded citation + judge PASS) admits and lands
- ✓ slop body (grounded citation + judge FAIL) refused, reasoning reaches agent
- ✓ body with no citation refused by the deterministic script before the judge
- *not covered:* the queued-message / envelope citation shapes; the fail-open
  reconciliation branches (a judge-machinery failure now fails closed, which is the
  engine's behaviour, not this guard's to re-prove).

**task-evidence-resolves** (file-guard, preventive; script)
- ✓ grounded citation permits and lands
- ✓ fabricated citation refused (cite finds no match), naming the quote
- ✓ invalid frontmatter (`status: done`) refused by the schema
- ✓ in_review with no citation refused for missing evidence
- *not covered:* multi-citation tasks where some resolve and some do not; the
  underivable-Pre defer path (unit-tested behaviour of the kind dispatch, exercised
  indirectly).

**task-review** (file-guard, after-check; script + judge)
- ✓ substantiated in_review task permits — review runs at the Post/Stop after-check
- ✓ unsubstantiated in_review task blocked at Stop, rejection reaches the agent
- ✓ ungrounded in_review evidence refused deterministically (pre-flight / Pre)
- *not covered:* the re-fire-every-cycle loop across multiple cycles (the block is
  shown once); the approve-path's "delete the folder" instruction (that is agent
  workflow, and the binary judge's PASS is a permit — see the guard's file-guard.yaml
  for why that old behaviour does not survive the judge-check migration).

**no-unfinished-work-at-turn-end** (gate, Stop; script)
- ✓ a turn with an open (to_do) task blocked at Stop, naming the task and status
- ✓ a turn whose task is at a resting status (in_review) permits
- *not covered:* the blocked/backlog resting statuses individually (in_review stands
  in for the resting set); the plumbing fail-open branches (a missing schema / tool
  permits — deterministic and unit-shaped, not driven here).

The coverage is **focused, not exhaustive**: one real end-to-end per guardrail's
key cases, honest about the branches it leaves to the deterministic layer and the
engine. Scaling this to the full "each use-case has exhaustive e2e" model is P3
work; this establishes the pattern and proves it runs.
