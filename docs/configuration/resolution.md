---
title: Rule resolution & precedence
description: How a project's rules combine with those a plugin ships, and who wins when they collide.
kind: reference
related:
  - configuration/config
  - concepts/refusal-contract
---

A guardrail can come from the **project** or arrive with a **plugin**. Both are
loaded and dispatched by the same engine — the same declaration in a different
place. When they meet, these rules decide what happens.

## The repo decides which plugins apply

Whether a plugin's rules apply is decided by what the repository *wrote down* — the
project's own settings — not by which plugins happened to fire a hook. Installing a
plugin is a recorded decision, so the settings are the truth about it, even for a
plugin that ships rules without any hooks.

## When names collide, the project wins

If a project and a plugin both have a rule of one name:

- **The project wins** — it can always override a rule it didn't write.
- **The shadowing is reported** — displacing a shipped rule is surfaced, not
  silent.
- **A refusal from a shipped rule names the plugin** — `("some-rule" from plugin
  "sloprail")` — because the bare name would point at the project's own rules and
  mislead you about the source.

## Disabling is qualified

A project can switch off a plugin's rule, and the disable is **qualified by the
plugin**, so it can't accidentally hit a project rule — or another plugin's — of
the same name. The disable also survives a broken shipped rule, so one bad rule
can't wedge every project that installed the plugin.

## A missing plugin is fatal

If the settings enable a plugin the engine then **cannot locate** — a moved
marketplace, a changed manifest — it refuses rather than continuing with less
protection than you asked for. It can't know whether the missing plugin shipped
rules, so it fails closed, the same stance as [the refusal
contract](/concepts/refusal-contract), and tells you exactly which plugin went
missing.

See [Configuration](/configuration/config) for where enabling and disabling are
written.
