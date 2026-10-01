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

This module is discovered and run by the repo's own `make test-plugins-e2e`
(every `marketplace/plugins/*/tests` directory with a `go.mod`, one `go test`
per module — see the root `Makefile`), which CI runs under the `plugins` e2e
shard in `.github/workflows/test.yml`.

## How the tests drive the guardrails

Each test:

1. stands up an isolated project with the base `sloprail` plugin enabled
   (`harness.New(t)` + `e.Project()`);
2. **installs this plugin's own `.sloprail` tree** into the project verbatim
   (`installPluginTree`), preserving execute bits, and commits it so the guard
   scripts are part of the session baseline rather than the first cycle's diff
   — EXCLUDING `structure.yaml`, which is installed separately as a genuine
   plugin structure (`installPluginStructure`, via the harness's
   `EnablePluginShippingStructure`), because copying it into the project's own
   `.sloprail/` would make it the PROJECT's structure gate, which must never
   declare `scope`;
3. drives the **a10n-claude-mock** through the harness (`e.Run(proj, sess,
   prompt, Turns(...))`);
4. asserts the outcome: `res.Refused()` for a gate's pre-write refusal,
   `e.BlockingErrorsFrom(proj, sess, "Stop")` for an after-check Stop block,
   `e.Exists(...)` for whether a write landed, and `res.Saw(...)` for the
   reason reaching the agent.

Run it as is; the harness scrubs the ambient Claude Code session variables, so a run inside a session equals CI:

```
cd marketplace/plugins/sloprail-content/tests
go build ./...
go test ./... -count=1
```

## Citations are grounded, not stubbed

A citation rides on the ACTION, never in a file: a test makes a grounded
change with a `Bash` turn running `sr-file write|edit ... --cite:user
'<quote>'` (built by `srFileWrite` / `srFileEdit` in `main_test.go`), and the
harness really executes it. The quote is words from the scenario's own
prompt, which the harness seeds as the session's root user message, so the
session resolves it for real before any guard sees the event. A quote the
user never said exercises the real "resolves to nothing" path; a `Write`
turn exercises "carries no citation at all".

- **a rule change** — `content-rule-is-grounded` requires a user citation on
  every create and update (`require: [{citation: {source_types: [user]}}]`).
- **a publish** — `unit-publish-approved` requires one on a write that moves
  a unit into `status: published`.

## What is stubbed, and why

The only stub is the **judge model verdict** (`InstallJudgeClaude`), exactly
as the main suite's judge e2e do — the model call is the one thing a mock
cannot supply for sr-agent's judge path. `pass: true` admits, `pass: false`
refuses and the reasoning reaches the agent. The judge always runs, even when
no applicable rule exists (the "NONE" sentinel passes trivially on it — there
is no permit-without-judging in the `judge:` check contract), so every test
that reaches `unit-satisfies-rules`'s Stop after-check installs a stub, even a
control expecting no block.

**A consequence for `unit-satisfies-rules` specifically:** an earlier draft
of this plugin shipped deterministic `.sh` scripts for character limits and
banned phrases, dispatched by a script-rule stage the e2e drove for real (a
genuine bash process, not a stub). That mechanism is gone — a rule needing a
measurement is now rule TEXT asking the judge to run it via the `Bash` tool
the judge is granted. Because the judge's model call is always stubbed in
this suite, these tests **cannot** prove a deterministic rule's measurement
is actually correct (the stub never runs Bash) — see
`test_unit_rules_test.go`'s file header for exactly what they do and do not
prove.

## Per-file coverage

**test_unit_md_first_test.go — unit-md-first** (gate + file-guard,
script)
- a non-entry file (the draft) written into a unit folder before UNIT.md
  exists is refused, naming the missing UNIT.md and how to write it first
- UNIT.md itself, written into a folder that has none yet, is admitted
- a non-entry file written after UNIT.md exists (same run) is admitted
- deleting a non-entry file inside an existing unit folder is not refused

**test_unit_rules_test.go — unit-satisfies-rules** (file-guard, Stop
after-check, one judge check with `allowed_tools: [Read, Bash]`)
- a global rule (no `applies_to`) applies to a unit with no tags
- a tag-scoped rule applies only when the unit's own tags include it
- a rule written to ask for a deterministic measurement (a character limit)
  fires or passes according to the judge stub, proving selection and
  wiring, not the measurement itself
- a unit selecting no applicable rule at all passes (the guard does not block
  by default)

**test_publish_gate_test.go — unit-publish-approved** (gate + file-guard,
script)
- creating a unit at `status: published` with the Write tool (no citation) is
  refused, and the refusal names `sr-file write` and `--cite:user`
- an uncited `sr-file edit` from drafting to published is refused, and the
  refusal hands back the exact cited edit to make
- a transition citing words the user never said is refused
- a cited transition + `published_urls:` lands, the unit stores no approval
  text, and the Stop after-check (reading the recorded citation) passes
- a cited create with two `published_urls:` entries (multi-channel) passes
- a cited transition with no `published_urls:` is refused
- a drafting unit created, or edited, uncited is unaffected by the gate
- an uncited edit of an already-published unit (not a transition) passes

**test_content_rule_grounded_test.go — content-rule-is-grounded** (gate + file-guard,
require citation + script + judge)
- an uncited rule write (the Write tool) is refused by `require` before the
  judge, and the refusal names `sr-file` and `--cite:user`
- a rule citing words the user never said is refused
- a cited rule the judge accepts (PASS) lands, and the captured judge prompt
  holds the cited quote and its transcript path
- a cited rule the judge rejects for adding untraceable scope (FAIL) is
  refused, with the judge's reasoning reaching the agent
- a cited rule with invalid frontmatter is refused by the script
- a cited edit of a rule still carrying a legacy transcript link passes, and
  the judge is handed the body before the change

**test_structure_gate_test.go — this plugin's own structure-gate piece**
- a unit at the plugin's own allowed shape passes
- a stray file under `memories/topics/` (not one of this plugin's declared
  shapes) is refused, naming this plugin's structure gate as the decider
- a write outside the plugin's scope is entirely unaffected by it
