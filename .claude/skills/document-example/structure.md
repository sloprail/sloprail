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
`examples/no-unasked-commit/README.md` for a full worked example, including
the Install section below):

1. **`# <name> (<nature>)`**, immediately followed by a **short overview** —
   1-3 plain-language sentences on what the rule catches, no heading of its
   own. This is the only thing a skimming reader is guaranteed to see before
   they decide whether to keep reading.
2. **`## Install`** — comes right after the overview and before every
   detailed section below it, so trying the example never requires
   scrolling past the design discussion first. Simple and non-technical by
   design:
   - One line saying to make sure sloprail itself is installed, linking the
     docs [install page](https://sloprail.com/docs/getting-started/install/)
     — never a local verification command here (no `sr-session start`, no
     `--version`; that belongs in the docs page, not every example).
   - One concrete copy command a stranger can paste from their project root
     with no repo already cloned — a shallow clone to a temp dir plus
     `cp -R .../examples/<name>/.sloprail/. .sloprail/` (this single form
     covers an example with several nature/rule folders at once), or an
     equivalent single-line curl/tar. Note the folder name only if it isn't
     obvious from the command.
   - Optionally, one plain sentence: cause the case it guards against and
     confirm you see the refusal. Nothing more — no internal
     verification/debugging steps belong in this section.
3. **`## The rule`** — one paragraph, plain language, what it catches and why
   it matters (a real incident it prevents, if there is one — concrete beats
   abstract)
4. **`## Why <nature>`** (or `Why a file-guard, and why preventive`, etc.) —
   the design justification: why THIS nature and not one of the other two,
   why `preventive`/`deletions:`/whatever nature-specific knobs are set
5. One or more sections on **the mechanism** — what a script checks
   deterministically vs. what a judge decides, in that cheap-first order
6. Optionally, **what it does NOT catch** / **the failure this catches** —
   the boundary of the rule, so a reader doesn't assume it covers more than
   it does

A README that only restates the YAML in prose is not done — the "why this
nature, why these knobs" reasoning is the part a reader cannot get from the
config alone. Same for Install: a README whose install section reads like a
CLI reference (multiple verification commands, internal debugging steps) is
not done either — a stranger with no context on sloprail's internals should
be able to follow it.
