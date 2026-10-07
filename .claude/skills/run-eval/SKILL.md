---
name: run-eval
description: Use when writing, running, or verifying an sr-eval fixture — proving a guardrail (or anything else `sr-eval run --fixture <dir>` can seed) holds up against a REAL, unscripted agent, not just a mock. Covers the fixture.yaml format, the trajectory-health scoring model, --keep verification, and the archive.
---

# Running evals with sr-eval

`sr-eval` proves a guardrail's use-case is true of a REAL agent — the
engine's own `tests/e2e/` suite (see [test-cli-command](../test-cli-command/SKILL.md))
proves a rule fires against a scripted, mocked `claude`, which proves the
wiring, not that a real, unscripted model given a realistic prompt actually
runs into the rule and recovers the way its use-case doc claims. `sr-eval`
runs the second proof.

`sr-eval` is fixture-directory-agnostic — `--fixture <dir>` works against any
directory shaped right, not only ones under `examples/`. It happens to be
used almost entirely for `examples/*/eval/<case>/` fixtures today. Those
examples live in [sloprail-community](https://github.com/sloprail/sloprail-community)
(every `examples/` path in this skill is relative to that repo), whose
[document-example](https://github.com/sloprail/sloprail-community/blob/main/.claude/skills/document-example/SKILL.md)
skill covers how a fixture relates to its shipped example. It is the one `sr-eval`-specific feature
(`exampleSloprail`) that assumes that layout — everything else here applies
to a fixture anywhere.

- **[fixture-format.md](fixture-format.md)** — every `fixture.yaml` field,
  Seed vs. Repo, Overlay, the copy order, `exampleSloprail`
- **[scoring.md](scoring.md)** — the trajectory-health model (plan-2026-09-24):
  what PASS/FAIL actually means, the shared `trajectory-health.sh` harness,
  writing a `score.sh`
- **[verifying.md](verifying.md)** — running a fixture, reading the result,
  `--keep` to inspect the actual workspace tree (not just the score — a
  guardrail can be silently absent from a workspace while the run still
  scores PASS)
- **[archiving.md](archiving.md)** — the local git-backed run archive, where
  it lives, and pushing it to the shared remote

## The one-sentence version

Every fixture run is scored on whether the TRAJECTORY looked healthy (no
stuck loops, no unresolved refusal) — not on whether the guardrail
specifically fired — and a fixture is not proven correct until it has
actually been run against a real model, at least once, with the result
inspected (not just the exit code) via `--keep`.

## Quick reference

```
sr-eval run --fixture examples/<name>/eval/<case>            # run, archive
sr-eval run --fixture <dir> --keep --no-archive               # inspect the workspace
sr-eval run --fixture <dir> --model <override>                 # different model, same fixture
```

Exit status: `0` pass, `1` fail, `2` the run itself could not be completed
(nothing to score) — the same three-way contract as a guardrail script check.
