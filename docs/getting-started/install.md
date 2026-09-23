---
title: Install
description: Get sloprail into your harness as a plugin, and confirm it loaded.
kind: tutorial
related:
  - getting-started/quickstart
  - getting-started/harnesses
  - reference/cli
---

sloprail runs inside your agent harness as a **plugin**. Installing it registers
the hooks the engine needs; from then on, every tool call and turn-end runs
through sloprail.

## Install the plugin

In Claude Code:

```bash
/plugin install sloprail@sloprail-marketplace
```

That's it — the hooks are registered. A project with a `.sloprail/` directory is
now guarded; a project with none is an ordinary project until its first rule.

## Confirm it loaded

Run the load check — it reports the event vocabulary this build produces and
validates every declaration:

```bash
sr session start < /dev/null
```

If a rule is bound to an event this build doesn't produce, it's named here rather
than silently ignored. A clean load check means your declarations parsed and bound.

Remember: *loaded* is not *fired*. The load check confirms the rules are valid,
not that they refuse when they should — that's what the
[Quickstart](/getting-started/quickstart) has you prove.

## Installing the binaries directly

If you're working on sloprail itself, or need the `sr` binaries on your `$PATH`
outside a harness:

```bash
go install ./services/...      # the standard path
make distribute-local          # when a copy is already installed (re-signs on macOS)
```

Both land the whole set in one directory, which is what the binaries' sibling
resolution needs. See the [CLI reference](/reference/cli) for the command surface.

## Next

- [Your first guardrail](/getting-started/quickstart) — write one and watch it
  fire.
- [Harnesses](/getting-started/harnesses) — which agents sloprail runs in.
