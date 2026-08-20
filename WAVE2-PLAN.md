# Wave 2 — per-use-case e2e (GOAL.md §Wave 2)

Each of the 16 use-case examples under `examples/<name>/.sloprail/` gets its OWN
multi-scenario e2e under `tests/e2e/examples/<NNN>_<name>/`, driving the compiled
sr-session through a10n-claude-mock against a sandbox that installs the SHIPPED
example verbatim (read off disk, not restated). Pass paths AND the violation paths
the unit exists to catch; critical AND non-critical edges.

## Pattern to follow (GREEN templates)
- `tests/e2e/gate/032_gate_dispatch/` (gate: pre + Stop + judge + flat-event)
- `tests/e2e/fileguard/034_fileguard_dispatch/` (file-guard: after-check + preventive + match + judge + RE-FIRE)
- `tests/e2e/context/035_context_dispatch/` (context: enter/exit lifecycle + composite)
- `tests/e2e/structure/033_structure_gate/` (structure gate: allow/deny)
- Harness: `tests/e2e/harness/{harness.go,scenario.go}`. main_test.go wires
  `New/Turns/Write/Bash/Skill/Say/Dispatch`; `e.Gate/FileGuard/Context/StructureGate/Guardrail`,
  `e.Run`, `res.Refused()/res.Saw()`, `e.Exists/Wrote`, `e.GateState/ContextState`, `e.Meta`,
  `e.BlockingErrorsFrom(proj,sess,"Stop")`.
- DO NOT follow `tests/e2e/examples/013_required_context_precondition/` — it references a
  non-existent `examples/guardrails/<name>` path and is pre-existing RED. Fix or supersede it.

## INSTALL-PATH note (important)
The shipped examples live at `examples/<name>/.sloprail/{gate,file-guard,context}/<rule>/…`
and (optionally) `structure.yaml`. The e2e must copy that WHOLE `.sloprail/` tree into the
sandbox project (the harness `e.Gate/FileGuard/Context` helpers write a single rule; for a
multi-rule example, either call them per-rule or add an `installExampleTree` helper that
copies `examples/<name>/.sloprail` verbatim — preferred, matches the "lift the real file"
intent). `examples/013`'s helper copies `examples/guardrails/<name>` (wrong) — supersede it.

## Judge verdicts in e2e
Use cases with a `judge:` check need a model verdict. TODAY: `e.InstallJudgeClaude(verdict)`
(the proven stub — writes {"pass":bool,"reasoning":…} to sr-agent's answer file). The reviewer
(C4/D3) wants the a10n-claude-mock to stand in for the judge's claude instead — that is BLOCKED
on a10n-cli#470 merging + the new mock binary. So: write judge e2e on `InstallJudgeClaude` now
with a comment `// TODO(D3): drive verdict via a10n-claude-mock once #470 lands`; the D3/D4 slice
swaps the stub → mock later. This unblocks all 16 use cases immediately.

## The 16 use cases (nature, events, checks, turn-shapes needed)
| use case | natures | events | judge | script | turn shapes needed |
|---|---|---|---|---|---|
| action-proof | gate | Stop | 1 | 0 | Write; InstallJudgeClaude |
| business-invariants | file-guard | (state) | 1 | 1 | Write; marker sr:i; InstallJudgeClaude |
| completeness-artifact-on-trigger | context+file-guard+gate | Post{File,Tag}Write,Stop | 0 | 2 | Write, Say(#tag) |
| content-de-layering | file-guard | (state) | 1 | 0 | Write; InstallJudgeClaude |
| deterministic-refactoring-mode | context+file-guard | PreToolUse | 0 | 1 | Write/Bash, marker sr:m |
| doc-conformance | file-guard | (state) | 1 | 0 | Write; marker sr:c; InstallJudgeClaude |
| eval-loop-maxing | context+gate | PostFileWrite,PreCommandInvoke,Stop | 0 | 2 | Write, Bash; (COVERED by context/035 composite — add example-level e2e or cross-ref) |
| grounding-citations | file-guard | (state) | 1 | 1 | Write; InstallJudgeClaude |
| intake-nothing-unprocessed | context+gate | Stop | 0 | 1 | Write/Say |
| interlinking | context+gate | PostFileCreate,PostFileDelete,Stop | 0 | 1 | Write, delete (Bash rm) |
| keyword-coverage-registry | context+gate | PostFile{Create,Update},Stop | 0 | 1 | Write |
| marker-anchored-structure | file-guard | (state) | 1 | 1 | Write; file markers; InstallJudgeClaude |
| no-unasked-deletion | file-guard | (state) | 2 | 1 | Write, delete; marker sr:a; InstallJudgeClaude |
| required-context-precondition | file-guard+gate | PreFileWrite | 0 | 0 | Skill, Write; (gate half COVERED by gate/032_03/04 — add example-level e2e) |
| research-rigor | context+gate | PostTagWrite,Stop | 0 | 1 | Say(#tag), Write |
| task-management | file-guard | (state) | 1 | 1 | Write; InstallJudgeClaude |

## Batches (independent — each e2e is its own package dir)
- **B1 (judge-free lifecycle):** intake-nothing-unprocessed, interlinking, keyword-coverage-registry,
  research-rigor, completeness-artifact-on-trigger, deterministic-refactoring-mode.
- **B2 (judge, InstallJudgeClaude):** action-proof, content-de-layering, doc-conformance, task-management.
- **B3 (judge + markers/deletes, trickier):** business-invariants, grounding-citations,
  marker-anchored-structure, no-unasked-deletion.
- **B4 (already-partially-covered composites):** eval-loop-maxing (example-level e2e on top of
  context/035), required-context-precondition (example-level e2e superseding examples/013).

## Numbering
Continue from 035: examples/036_… upward, one dir per use case. Keep the `NNN_<snake_name>` convention.

## Gate (per GOAL.md, sharper reviewer focus)
Reviewer checks ALL critical AND non-critical edge paths are tested. Loop reviewer→zero; all tests
green; CI green. Merge each into draft/fileguard-format; remove worktree.

## Status
- [ ] B1 · [ ] B2 · [ ] B3 · [ ] B4
