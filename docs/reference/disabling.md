---
title: Disabling rules
description: How a project switches a guardrail off — its own, or one a plugin shipped.
kind: reference
related:
  - reference/resolution
  - authoring/disable-a-guardrail
---

Most configuration is the rules themselves. The one thing a project configures
*about* a rule is whether it's on.

## Turning a rule off

A guardrail can be disabled — including one a plugin shipped. The disable is
**qualified by the plugin**, so switching off a plugin's rule can't accidentally
hit a project rule of the same name, and it survives a shipped rule that won't
load (otherwise one broken rule would wedge every project that installed the
plugin).

## Disabling is a decision, not a default

Turning a guardrail off removes a guarantee — a real choice. Record *why* alongside
the disable so the next person knows it was deliberate. And don't disable a rule by
making it pass loosely: a rule that loads but never refuses is worse than no rule,
because it looks like protection. Turn it off explicitly, or fix it.

See [Disable a guardrail](/authoring/disable-a-guardrail) for the workflow, and
[resolution](/reference/resolution) for how project and plugin rules combine.
