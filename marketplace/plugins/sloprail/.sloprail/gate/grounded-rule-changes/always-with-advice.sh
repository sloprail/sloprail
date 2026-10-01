#!/usr/bin/env bash
# `when` for the citation a write to `.sloprail/config.yaml` needs. It always applies
# (exit 0), and says what to do: the grounding is the user's words or a misfire shown
# by a tool's output, never the wish to get past a refusal. THIS IS A PREDICATE, NOT A
# CHECK: it never permits anything, it only applies the requirement and advises.
cat >/dev/null
jq -n '{hint: "A change to .sloprail/config.yaml (a disabled: entry, a setting) must be grounded in one of two things. Either the user asked for it: cite their exact words. Or a bug actually happened: cite the tool output that shows the rule misfiring on correct work (a command result, not the rule refusing your own bad work). If neither is true, ask the user, and fix your work instead of the rule. Never disable, loosen or delete a rule to get past its refusal."}'
exit 0
