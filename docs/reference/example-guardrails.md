---
title: Example guardrails
description: The real, working guardrails shipped in the sloprail repo — the source the guides show.
kind: reference
related:
  - guides
  - reference/nature-shapes
---

Every [guide](/guides) is built on a real, working guardrail shipped in the
sloprail repo under `examples/`. They are the source of truth: what a guide shows
is loaded from these directories, so it can never drift from what runs.

Each example is a directory with a `.sloprail/` tree of one or more natures and a
`README` explaining the rule. Browse them to see complete, composed guardrails —
not fragments.

## The examples

| Example | Natures | What it enforces |
|---|---|---|
| `deterministic-refactoring-mode` | context + file-guard + gate | A move must carry the origin's bytes; every declared move must land. |
| `business-invariants` | file-guard | A marked invariant must still hold in the code. |
| `no-unasked-deletion` | file-guard (preventive) | An edit must not drop content the user never asked to remove. |
| `content-de-layering` | file-guard | One fact lives in one home; meaning isn't duplicated across files. |
| `doc-conformance` | file-guard | A mock or emulator must match the contract it stands in for. |
| `action-proof` | gate | A claimed action carries verifiable proof it happened. |
| `required-context-precondition` | gate | A write is blocked until the skill that governs it loaded. |
| `research-rigor` | context + gate | A research claim is grounded and deep enough before the turn ends. |
| `completeness-artifact-on-trigger` | context + gate | A declared trigger must produce its required artifact. |
| `intake-nothing-unprocessed` | context + gate | Every intake item is processed and accounted for. |
| `interlinking` | context + gate | Every entity in a set is linked somewhere — no orphans. |
| `keyword-coverage-registry` | context + gate | Declared keywords are all covered in the registry. |
| `marker-anchored-structure` | file-guard | Writes stay inside the marked structure. |
| `eval-loop-maxing` | context + gate | A measure-and-improve loop can't game its own score. |
| `task-management` | file-guard | A task's ask stays human-authored, not rewritten to fit the work. |

## Reading one

Open any example's directory to see the whole thing — the `<nature>.yaml`
declarations, the check scripts, and any judge templates. The
[refactor guide](/guides/coding/refactor-regenerated) walks through
`deterministic-refactoring-mode` in full as a worked example; the others follow
the same shape.
