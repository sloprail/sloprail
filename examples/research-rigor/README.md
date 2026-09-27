# research-rigor (context + Stop gate)

**Natures:** context + gate

## The rule

Research declared with `#research` must read real prior art, not skim it: the
run has to `git clone` a repository and read **at least two of that clone's
source files** — not its README, not its docs. A run that searched, fetched a
README and wrote a confident summary is refused at `Stop`, with a reason that
says what is missing and what to do:

> This #research run cloned /…/node-retry but read none of its source files — a
> README or docs file does not count. To finish the research: git clone a real
> repository that implements what you are researching, then read at least 2 of
> its source files (not only the README or docs) with Read, Grep, cat, sed, grep
> or rg. Reads of directories this run did not clone do not count.

The convention it enforces is the one a project writes down (the eval seed's
`NOTES.md`: "clone at least one real repo that implements retry/backoff logic,
not just a README"). The gate measures that sentence and nothing it does not say.

## Why a context + a Stop gate

- **The context (`research-run`)** is the declaration. It activates on a
  `#research` tag the agent writes (`PostTagWrite`), or on an `Agent`/`Task`
  dispatch whose prompt carries `#research` (`PreToolUse`) — research handed to a
  sub-agent is still the dispatching run's research, and a real Haiku run put the
  tag only in the sub-agent's prompt. It never blocks; its `exit` reads the
  gate's verdict and closes the run once the gate passes, so a run that fails
  stays open and the gate fires again on the next turn.
- **The gate (`depth-check`, on `Stop`)** is the judgement. Depth is a property
  of the *finished* research — a clone first and reads later, possibly across a
  sub-agent and its dispatcher — so no single pre-action event can decide it.
  `Stop` is the one moment the whole run is on the record. `match:
  context["research-run"].active` keeps ordinary turns out of it, and `require:
  context: research-run` orders the context's enter before the check.

## What "depth" means here, and why

Depth is **reading what you cloned**:

1. **A clone this run made.** A `git clone` invocation, from this trajectory or a
   sub-agent's (`sr-session trajectory describe` lists them), that did not fail.
   Its destination is computed, not guessed: the explicit directory argument, or
   the name git derives from the repository (`…/node-retry.git` → `node-retry`),
   placed in the directory the invocation ran in — the record's own `cwd`, moved
   by the engine's per-invocation `.cwd` through the line's `cd`s (`cd x && git
   clone`, a `(cd x && …)` subshell that does not leak), then by `git -C <dir>`.
   Options that take a value (`--depth 1`, `-b main`, …) are skipped so their
   value is not read as the repository or destination.
2. **Source read inside it.** At least two distinct paths under a cloned
   directory, read by a call that did not error: the Read tool's `file_path`,
   the Grep tool's `path`, or the operands of `cat`, `head`, `tail`, `less`,
   `bat`, `nl`, `sed`, `awk`, `grep` and `rg` (their option values and their
   pattern/program argument set aside — `head -n 20 f` reads `f`, not `20`).
   READMEs, changelogs, licences, `*.md`/`*.rst`/`*.txt`, and anything under
   `docs/` do not count. A search over a source directory counts as one read.

**Why two.** One file can be an entry point that only re-exports; a second means
the reading followed the implementation past it. It is a floor that separates
"opened the repository" from "read how it works" — not a quality score, and
padding past it still costs real reading of the right repository.

**Why "this run cloned".** A real run's clone failed (it `cd`'d into a
scratchpad that did not exist yet), and the agent then ran `ls /tmp`, found
repositories *earlier sessions* had cloned there, and "researched" those. The
previous gate saw a `git clone` invocation and credited it. Now a clone counts
only if it succeeded — a non-error tool result, and no `fatal:` naming its
destination or repository (a `git clone … | tail` pipeline exits 0 even when git
refused) — and a read counts only inside a directory such a clone produced. A
checkout that was already on disk is named in the refusal as not counting.

**Why the gh page count was dropped.** The previous gate also demanded `gh`
calls "covering at least 5 pages", counted from `--limit N` / `--paginate`.
Nothing in the convention says that, so the refusal ("No gh CLI calls found …
nothing establishes how many pages were actually covered") told the agent about a
requirement it had no way to know, and did not say how to meet it. And the count
measured nothing: `--limit 100` "covered" 100 pages in one call, `gh repo view`
added one, and the real run did exactly that — padded with `gh repo view` calls,
was refused again at 3 < 5, and ended with the refusal unresolved. A proxy that is
satisfied by padding and unexplained by the convention is worse than none; the
page requirement is gone rather than restated.

## The mechanism

- **`gate/depth-check/research-facts.jq`** — per trajectory, over `sr-session
  trajectory normalize --events PreCommandInvoke`: the clones (destination,
  repository), the clones whose destination cannot be placed, and every path
  read. Command lines are read through the engine's parsed invocations
  (`.bin`, `.argv`, `.cwd`), never by regex over the raw string. A tool call's
  result is joined by its `tool_use_id` to drop failed calls.
- **`gate/depth-check/verify-depth.sh`** — gathers those facts for the
  trajectory and every sub-agent trajectory, decides, and writes the refusal:
  what the run cloned, what source it read there, what it read elsewhere, and
  what to do. It keeps the older check that a research sub-agent ran as its own
  agent, not in a trajectory shared with siblings.

## What it does not catch, and the tradeoffs

- **A destination spelled through a variable** (`git clone url "$TMPDIR/x"`)
  cannot be placed: the engine expands words against an empty environment rather
  than guess one, so argv holds `/x`. A clone on a line that expands a variable or
  substitution and names its own destination (or `-C`) is treated as unplaceable,
  and the refusal asks for a literal path. Over-cautious on a line like
  `git clone url dir && echo $HOME`; the cost is one re-clone.
- **Whether the repository is relevant** is not judged — "implements retry" is a
  semantic question for a judge, not this deterministic floor.
- **The run's span** is the whole transcript (root and sub-agents), not just the
  turns since `#research`: a clone made earlier in the same session counts.
- **`cat f || true`** on a path that does not exist still reads as a read — the
  error is swallowed before the gate sees it. Gaming it that way is deliberate,
  not accidental, and still needs a real clone to aim at.
- **A literal `/tmp`** is shared with every other process on the machine. The
  eval harness gives the agent its own `TMPDIR` and `CLAUDE_CODE_TMPDIR` (Claude
  Code's scratchpad) inside the workspace, but only a sandbox could stop `ls
  /tmp`; the gate is what makes stale clones not count.

## Proof

- **E2e:** `tests/e2e/examples/039_research_rigor/` — activation, a shallow run
  refused, clone + source reads admitted, README/docs-only refused, reads of an
  uncloned checkout refused, a failed clone into an existing directory refused,
  sub-agent research aggregated for the dispatcher, and each command shape above.
- **Eval:** `eval/shallow-research-temptation/` — Haiku asked to research
  retry-with-backoff under the NOTES.md convention, scored on trajectory health.
