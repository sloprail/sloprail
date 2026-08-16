---
hooks:
  PreFileCreate:
    - matcher: path contains ".sloprail/guardrails/" && path endsWith ".sh"
      hooks:
        - type: command
          command: ./check-rules.sh
  PreFileUpdate:
    - matcher: path contains ".sloprail/guardrails/" && path endsWith ".sh"
      hooks:
        - type: command
          command: ./check-rules.sh
---

# No slop in the guardrails themselves

sloprail exists to stop an agent producing work whose claim to being done is its
own say-so. A guardrail written that way is the sharpest version of the problem:
it loads, it validates, it sits in the project looking enforced, and it admits
everything.

This rule is that argument applied to sloprail's own artifacts. Every entry below
is a mistake made in a real hook, found by measurement rather than review, and
each one produced a rule that looked correct and enforced nothing.

## One rule, one file

    rules/<name>/RULE.md

Each rule is its own folder, and that is not filing tidiness: a rule is the unit
that gets added, argued, enforced or not, and cited in a refusal. A list in one
document has no such unit — a rule cannot be pointed at, its enforcement status
cannot be read off it, and adding one means editing a file everything else lives
in.

Each `RULE.md` carries `enforced: true|false` in its frontmatter. Some of these
are decidable by reading a script and are checked; some are claims about how the
author verified, or about the body's reasoning, and are documented and left to
review. Both belong here — a rule that cannot be automated is still the thing the
next author needs to know.

Adding one is the point. When a hook is found to be silently inert, the fix is
two things: repair that hook, and write the shape down so the next one is caught
at authoring time.

## Why a matcher, not a judge

Every rule here is decidable by reading the script. `grep` for a tool-name
allowlist is exact; asking a model whether a hook is "well written" is not, and a
rule that fires on taste gets switched off within a day.

The check is deliberately conservative: it flags shapes that are wrong in every
case it has been measured on, and stays silent where a human might disagree.
Cheap and certain beats broad and arguable — the same trade the engine makes
between a matcher and a judge everywhere else.

## Scope

Only shell hooks under `.sloprail/guardrails/`. It does not judge the rules a
project writes, only how they are written — what a guardrail *should* enforce is
the project's business; whether the enforcement can fire at all is sloprail's.
