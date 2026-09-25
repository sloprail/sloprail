# sloprail-content — the plugin's own end-to-end tests

A **separate Go module** (`marketplace/plugins/sloprail-content/tests/go.mod`,
module `github.com/sloprail/sloprail-content/tests`), nested inside the plugin
it tests — mirrors `sloprail-tasks/tests` exactly. It is not part of the main
repo's module; it has its own `go.mod` so the plugin is a unit that carries its
own verification.

It reuses the main repo's **shared e2e harness**
(`github.com/sloprail/sloprail/tests/e2e/harness`) via a `replace` directive
pointing at the repo root:

```
require github.com/sloprail/sloprail v0.0.0-00010101000000-000000000000
replace github.com/sloprail/sloprail => ../../../..
```

## How the tests drive the guardrails

Each test:

1. stands up an isolated project with the base `sloprail` plugin enabled
   (`harness.New(t)` + `e.Project()`);
2. **installs this plugin's own `.sloprail` tree** into the project verbatim
   (`installPluginTree`), preserving execute bits, and commits it so the guard
   scripts are part of the session baseline rather than the first cycle's diff;
3. drives the **a10n-claude-mock** through the harness (`e.Run(proj, sess,
   prompt, Turns(...))`);
4. asserts the outcome: `res.Refused()` for a preventive Pre refusal,
   `e.BlockingErrorsFrom(proj, sess, "Stop")` for an after-check Stop block,
   `e.Exists(...)` for whether a write landed, and `res.Saw(...)` for the
   reason reaching the agent.

Run with `CLAUDECODE`/`CLAUDE_CODE_ENTRYPOINT` ambient-unset to reproduce CI:

```
cd marketplace/plugins/sloprail-content/tests
go build ./...
env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT go test ./... -count=1
```

## Citations are grounded, not stubbed

Both citation kinds this plugin uses are resolved for real:

- **a rule's `transcript_paths`** — the cited LINE itself must be a real user
  message, grounded via `sr-session trajectory cite --source-types user` over
  the line's own text (`content-rule-is-grounded`'s check-rule.sh). Tests cite
  the harness's own real root prompt at its real physical line
  (`e.RootMessageLine(sess)` — the mock's preamble means the root prompt is
  **not** physical line 1).
- **a unit's `approved:`** — a `[quote](jsonl)` citation link, grounded the
  same way sloprail-tasks's task body is (`unit-publish-approved`'s vendored
  `cite-links.sh`). A fabricated quote exercises the real "does not ground"
  path.

## What is stubbed, and why

The only stub is the **judge model verdict** (`InstallJudgeClaude`), exactly
as the main suite's judge e2e do — the model call is the one thing a mock
cannot supply for sr-agent's judge path. `pass: true` admits, `pass: false`
refuses and the reasoning reaches the agent. The judge always runs, even when
no judge rule applies (the "NONE" sentinel passes trivially on it — there is
no permit-without-judging in the `judge:` check contract), so every test that
reaches `unit-satisfies-rules`'s Stop after-check installs a stub, even a
control expecting no block.

## Per-file coverage

**test_unit_rules_test.go — unit-satisfies-rules** (file-guard, Stop
after-check, script+judge)
- a global judge rule (no `applies_to`) applies to a unit with no channels
- a channel-scoped rule applies only when the unit's own channel matches
- an over-limit X thread is refused by the `char-limit` script rule; an
  in-limit one passes
- a banned phrase is refused by the `banned-phrases` script rule
- a unit selecting no applicable rule at all passes (the guard does not block
  by default)

**test_publish_gate_test.go — unit-publish-approved** (file-guard, preventive,
script)
- `status: published` with no `approved:` is refused
- an `approved:` citation whose quote does not ground to a real user message
  is refused
- a grounded `approved:` + `published_url:` passes and lands
- `approved:` with no `published_url:` is refused
- a non-`published` status is unaffected by the gate

**test_content_rule_grounded_test.go — content-rule-is-grounded** (file-guard,
preventive, script)
- a rule with no `transcript_paths` at all is refused
- a `transcript_paths` entry that does not resolve (nonexistent transcript) is
  refused
- a script rule naming an unconditionally-permitting script is refused, even
  with a grounded citation
- a grounded rule with a real (non-trivial) script passes
