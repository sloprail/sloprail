# <img src="docs/assets/logo.svg" alt="" width="28"> sloprail

Your agents slop. Take control.

Deterministic project rules for your coding agent. They kick in when it
touches a file or runs a command, and hold until met: files only where you
allow, right skills loaded first, work proven by diff or logs. Not a
suggestion in CLAUDE.md or AGENTS.md.

<img alt="What sloprail is made of" src="docs/assets/bento.png" width="760">

<!-- HERO GIF -->

## Install

Two steps: get the binaries onto your machine, then install the plugin.

```
curl -fsSL https://raw.githubusercontent.com/sloprail/sloprail/main/install.sh | sh
```

```
/plugin install sloprail@sloprail-marketplace
```

`install.sh` downloads the right prebuilt archive for your platform (macOS or
Linux, Intel or Apple Silicon/arm64), verifies its checksum, and installs
`sr`, `sr-session`, `sr-file`, `sr-mark`, `sr-agent` into `~/.local/bin` — no
Go toolchain needed. Already have Go? `go install ./services/...` from a
checkout is an equivalent fallback.

Once both steps are done, every tool call and turn-end runs through sloprail
— you don't run anything by hand.

## Rules first.

Usually the agent writes first and you fix it after. With sloprail the rules
come first, and they stay.

|   | Usually | With sloprail |
|---|---|---|
| 1 | You ask | You ask |
| 2 | Agent writes it all | **Agent writes the rules first** |
| 3 | You correct it | Agent builds inside them |
| 4 | Added to CLAUDE.md / SKILL.md, if you ask | Checked on every change; the rules stay |

## You've seen these happen

<!-- USE-CASE GRID -->

- [The task gets rewritten to match the work](https://sloprail.com/docs/use-cases/tasks/task-rewritten)
- [After enough compaction, it games the score instead of doing the work](https://sloprail.com/docs/use-cases/long-runs/compaction-gaming)
- [A request fell through the cracks](https://sloprail.com/docs/use-cases/tasks/request-fell-through)
- [The same fact, copied into two files, now disagreeing](https://sloprail.com/docs/use-cases/knowledge/duplicated-knowledge)
- [It acted without loading what it needed first](https://sloprail.com/docs/use-cases/knowledge/acted-without-context)
- [The "mechanical" refactor silently rewrote your code](https://sloprail.com/docs/use-cases/coding/refactor-regenerated)

## Docs → [sloprail.com/docs](https://sloprail.com/docs)

## How it works

A project declares guardrails under `.sloprail/`, one folder per rule, in the
directory named for its kind: `file-guard/` (what a file must hold), `gate/`
(a checkpoint on an action), `context/` (a mode other rules depend on), plus
one `file-guard/structure.yaml` listing where writes may land at all. The
harness calls the session hook points, and the engine runs whichever
guardrails bind to what is about to happen.

Full docs, including how to write a guardrail:
[sloprail.com/docs](https://sloprail.com/docs).
