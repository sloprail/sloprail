# Keeping the four pieces in sync

An `examples/<name>/` change almost never stays contained to the file you
touched. Find your change below; do the rest in the same turn or the pieces
drift apart silently (nothing fails until someone notices later, usually by
tracing a confusing eval result back to a stale copy).

| You changed... | Also check / do |
|---|---|
| A script or judge template under `examples/<name>/.sloprail/` | Run `go test -tags fts5 ./tests/e2e/examples/0NN_<name>/...` — does the e2e suite still pass? If the fix changes WHAT the rule catches, does an e2e test need a new case? Does the README's mechanism section still describe the check accurately? If any `eval/<case>/overlay/.sloprail/` is a fixture's own full copy (not `exampleSloprail: true`, see [example-vs-fixture.md](example-vs-fixture.md)), does that copy need the same fix? |
| `file-guard.yaml`/`gate.yaml`/`context.yaml` (match, `deletions`, `on:`, a gate/file-guard split) | Same as above, plus: does this change what the README's "why these knobs" section justifies? A knob change with no README update is a README that no longer explains the shipped config. |
| The README | Does it still match the actual `.sloprail/` files? A README is prose ABOUT the config — it can go stale silently since nothing enforces the match mechanically; re-read the config while editing, don't edit from memory. |
| Added a new example from scratch | All four pieces are required, see [structure.md](structure.md): `.sloprail/` + README + e2e test package (next number) + at least one eval fixture. An example with only shipped config and no e2e test has never been proven to fire. |
| A fixture's `prompt.md`/`seed/`/`overlay/` | Re-run it against real Haiku (see [run-eval](../run-eval/SKILL.md)) — a fixture file edited without a real run is an unverified claim, not a passing eval. |
| `examples/_shared/eval/*` (the shared trajectory-health harness) | This is used by EVERY fixture that calls `trajectory_health_check` — re-run at least one real fixture end to end to confirm the shared script/template still renders and scores correctly; a break here is silent until a fixture run fails in a confusing way. |

## The single most common miss

Fixing a bug in a shipped guardrail script and stopping there — without
checking whether any eval fixture carries its OWN copy of that same script
(pre-`exampleSloprail`, or one of the three legitimate exceptions in
[example-vs-fixture.md](example-vs-fixture.md)) that now needs the identical
fix. `exampleSloprail: true` fixtures are immune to this by construction
(they have no copy to go stale); a fixture with its own full
`overlay/.sloprail/` is not, and there is no automated check that catches the
drift — only re-reading the fixture's own copy when you touch the shipped
one.
