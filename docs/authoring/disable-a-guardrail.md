---
title: Disable a guardrail
description: Switch a rule off — your own, or one a plugin shipped — with the disable qualified so it can't hit the wrong rule.
kind: guide
related:
  - concepts/refusal-contract
  - configuration/config
  - configuration/resolution
---

Sometimes a rule needs to be off — temporarily, or because it doesn't apply to
your project. Disabling is a deliberate, recorded decision.

## Disabling a plugin's rule

A project can switch off a rule a plugin shipped. The disable is **qualified by the
plugin**, so switching off one plugin's rule can't accidentally switch off a
project rule — or another plugin's — of the same name.

The disable also survives a **broken** shipped rule. If a plugin ships a rule that
won't load, you can still disable it — otherwise one broken rule would wedge every
project that installed the plugin, with no remedy but uninstalling.

## A disabled rule is a decision, not a default

Turning a guardrail off removes a guarantee. That's a real choice — record why in
your project's configuration so the next person (or the next you) knows it was
deliberate, not an accident. See [Configuration](/configuration/config) for where
the disable is written and [resolution](/configuration/resolution) for how project
and plugin rules combine.

## What you don't do

You don't disable a rule by making it *pass loosely* — a rule that loads but never
refuses is worse than no rule, because it looks like protection. If a rule
shouldn't apply, turn it off explicitly. If it should apply but is too strict, fix
the rule. Never leave a rule that can't fire pretending to guard.
