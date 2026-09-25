# The shape of an example

## Directory layout

```
examples/<name>/
  .sloprail/<nature>/<rule-name>/...   # the shipped guardrail — see authoring-guardrails
  README.md                            # the rule, why this nature, the design
  eval/<case>/                         # zero or more real-agent fixtures — see run-eval
    fixture.yaml
    prompt.md
    score.sh
    seed/  (or a repo: + ref: pin)
    overlay/                           # optional — see example-vs-fixture.md

tests/e2e/examples/0NN_<name>/
  main_test.go                         # shim — copy an existing one verbatim, see below
  test_0NN_01_<thing>_test.go
  test_0NN_02_<thing>_test.go
  ...
```

`<name>` is kebab-case and matches the directory under `examples/` exactly —
the e2e test package name embeds it (`036_intake_nothing_unprocessed`), so
pick the name once and use it everywhere: the example dir, the README title,
the e2e package, the `.sloprail` rule folder names inside it don't have to
match but usually echo it.

## Numbering a new e2e test package

`tests/e2e/examples/` packages are numbered sequentially, oldest first, no
gaps, no reuse. Find the next number:

```
ls tests/e2e/examples/ | sort -t_ -k1 -n | tail -1
```

and increment. As of this writing the highest is `051_required_context_precondition`,
so a new example's e2e package is `052_<name>`.

## The e2e shim

Every `tests/e2e/examples/0NN_<name>/main_test.go` is the same boilerplate —
copy an existing one (e.g. `046_business_invariants/main_test.go`) verbatim
and just check the imports/helpers it uses match what your test files need
(`Write`, `Bash`, `Turns`, etc. — see [test-cli-command](../test-cli-command/SKILL.md)
for the harness helpers in general). The shim's own doc comment states the
load-bearing property: these tests drive the **shipped** `.sloprail/`
installed verbatim, never a copy embedded in the test file — a test carrying
its own copy of the rule would keep passing after the shipped one broke.

## What the e2e tests must cover

At minimum, per guardrail nature (see [authoring-guardrails](../authoring-guardrails/SKILL.md)):

- **The rule fires** on the case it's meant to catch (a real refusal reaches
  the agent, worded the way the check actually words it — not a paraphrase)
- **The rule does NOT fire** on an adjacent case it's not about (a file that
  doesn't match, a value that's fine) — a guardrail an author only tested the
  positive case for silently swallows a `match:` typo that fires on
  everything
- **The specific mechanism** — a script's exact refusal reason, a judge
  prompt actually carrying the fields it claims to (see e.g.
  `TestT046_08_EventContentReachesJudgePrompt`-style wiring tests), a
  `deletions:`/`preventive:` edge if the guard declares one

## README sections a shipped example is expected to have

Every current `examples/*/README.md` follows roughly this shape (see
`examples/no-unasked-deletion/README.md` for a full worked example):

1. **`# <name> (<nature>)`** — title names the nature up front
2. **`## The rule`** — one paragraph, plain language, what it catches and why
   it matters (a real incident it prevents, if there is one — concrete beats
   abstract)
3. **`## Why <nature>`** (or `Why a file-guard, and why preventive`, etc.) —
   the design justification: why THIS nature and not one of the other two,
   why `preventive`/`deletions:`/whatever nature-specific knobs are set
4. One or more sections on **the mechanism** — what a script checks
   deterministically vs. what a judge decides, in that cheap-first order
5. Optionally, **what it does NOT catch** / **the failure this catches** —
   the boundary of the rule, so a reader doesn't assume it covers more than
   it does

A README that only restates the YAML in prose is not done — the "why this
nature, why these knobs" reasoning is the part a reader cannot get from the
config alone.
