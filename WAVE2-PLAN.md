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

## Findings during Wave 2
- **eval-loop-maxing EXAMPLE BUG (fixed on base d9f175c):** the goal-verify gate had a bare
  `require:[{context: goal-tracking}]`, which BLOCKS an unmet require → froze EVERY goal-free Stop
  (the loop could never end in a session with no goal). Fixed by adding `match: context["goal-tracking"].active`
  (the same match-skip the sibling keyword-coverage-registry gate uses), realizing run-verify.sh's
  own "not active → permit" intent (previously dead code). Found by B4; B4's T050_02 updated to assert
  the goal-free Stop is now PERMITTED.
- **`tests/e2e/examples/deterministic_refactoring/` is a pre-existing RED OLD-FORMAT DUPLICATE**
  (reads `examples/deterministic-refactoring/.sloprail/guardrails/…` — a path that no longer exists;
  the example is now `deterministic-refactoring-mode/` new-format; last touched by 2af83b4, not a Wave-2
  branch). It is the "duplicated old e2e of a superseded use case" GOAL.md Wave 3 deletes. B1 builds the
  NEW replacement (dir 041 deterministic-refactoring-mode). → DELETE this old dir right after B1's 041
  lands green (do NOT leave to Wave 3 — it's already broken and blocks `./tests/e2e/examples/...` green;
  but delete only once the replacement exists, so coverage never drops).

## Status (updated 2026-08-20, later)
- [x] JUDGE-CONFIG MERGED (dba20b5): gonja renderer + per-judge model/timeout + preventive-create
      fail-closed. Reviewer: ZERO major, 2 minor (doc, fixed). Spec pushed to PR #2 (7b34f87).
- [~] B1 (036-041 lifecycle) — DONE but found 4 BROKEN examples needing engine `--owner` + fixes:
      * interlinking/keyword-coverage/completeness gates read a sibling context via nonexistent
        `state list --owner <ctx>` → silently disabled. USER DECIDED: build `--owner`. Slice
        impl/owner-and-b1fix RUNNING (engine+spec+tests only).
      * mechanical example fixes still to do (in a follow-up after --owner): SR_WORKSPACE-anchor
        relative greps (intake tasks/, interlinking updates/ decisions/, keyword scanners/);
        research-rigor .toolUseResult jq on wrong entry; interlinking gate needs match:context[].active
        (require-blocks bug, same as eval-loop-maxing); intake skip channel redesign (user: decide with
        --owner — likely a tag/marker the gate reads, or a readable namespace).
      * THEN B1's e2e (036-041) flips its bug-pin tests → real assertions. B1 branch impl/w2-b1-lifecycle
        holds the e2e (with bug-pins); rebase on --owner+fixes, flip, review, merge.
- [x] B2 (042-045 judge) — DONE, reconciled (installExampleTree verbatim; 042 skips→real; 20 tests 0 skip),
      rebased on merged base (harness.go conflict with judge-config's recording shim RESOLVED — kept both
      helpers), green. Under B2+B3 reviewer.
- [x] B3 (046-049 judge+marker) — DONE, reconciled (removed exec-bit/cite bug-pins→tripwires; verbatim NUD
      paths), rebased, green (26 tests). Under B2+B3 reviewer.
- [x] B4 (050-051 composite, 013 deleted) — DONE, green, rebased; T050_02 corrected. Under B4 reviewer.

## Merge order (once reviewers green): B2, B3, B4 (independent e2e-only) → then --owner slice →
   then B1 fixes+e2e (depends on --owner) → then delete old-dup deterministic_refactoring after 041 →
   then delete internal/declaration/examples_test.go (superseded by Wave-2 e2e) → then D3/D4 → Wave 3.

## MERGED so far (base draft/fileguard-format)
- [x] judge-config (dba20b5) — gonja + model/timeout + preventive-create.
- [x] --owner (b8608c3) — cross-guardrail state read. Reviewer: zero issues, isolation mutation-verified.
- [x] B4 (3f4c26d) — 050 eval-loop-maxing + 051 required-context-precondition e2e; 013 deleted.
      (B4's automated reviewer got stuck chasing a harness snapshot rabbit-hole — killed it; merged on
      my own review: 050/051 cover happy/violation/re-fire/controls non-vacuously.)

## STILL IN FLIGHT
- [~] B1 example-fix (impl/w2-b1-lifecycle): fix 4 broken examples using --owner (+ jq -s + SR_WORKSPACE),
      research-rigor toolUseResult jq, interlinking match, intake skip-channel redesign (tag/marker + a
      skip-context read via --owner); then flip 036-041 bug-pins → real assertions; simplify installExampleTree.
- [~] B2-v2 (impl/w2-b2-judge): close reviewer gaps — action-proof toolUseResult happy-path (needs mock
      toolUseResult or a builder), message_id test (example fixed a1dd86e), TODO(D3) header placement.
- [~] B3-v2 (impl/w2-b3-judgemark): close reviewer gaps — prepare→template wiring proofs (047/049 via
      capturing shim), 047 re-fire, 049 cite-rc2 branch, doc nits. (Adds InstallJudgeClaudeCapturing to
      harness.go — SAME as B2 adds; resolve the overlap at merge.)

## Example bugs FIXED on base by the Wave-2 effort (running tally)
8bd1f77 14 non-exec scripts · 4baaa00 cite --path + action-proof jq · d9f175c eval-loop-maxing require→match
· a1dd86e task-management message_id string-content jq. (B1 slice adds: interlinking/keyword/completeness
--owner+jq-s+SR_WORKSPACE, research-rigor toolUseResult, intake skip.)

## a10n-cli#470 — MERGED ✅ (2026-08-20, squash 6614d1062, user-approved). New a10n-claude-mock BUILT +
  installed to /Users/nsviridenko/.local/bin/a10n-claude-mock (codesigned) — verified it accepts
  sr-agent's flags (--model/--allowed-tools/--permission-mode/--settings/--append-system-prompt, all
  "accepted for CLI compatibility"), no longer cobra-rejects them. **D3/D4 UNBLOCKED.**

## MORE SHIPPED-EXAMPLE BUGS found by Wave-2 (all FIXED on base — hallucinated impl details vs intent)
- **14 non-executable hook scripts** (mode 100644 despite shebangs) → engine refuses unrunnable checks →
  6 examples never ran. FIXED (8bd1f77): git update-index --chmod=+x all 14.
- **no-unasked-deletion cite --path** (4baaa00): removal-has-a-grounded-ask.sh called `cite` with no
  --path → fails closed → refused EVERY removal. Now passes the CheckPayload transcriptPath.
- **action-proof jq string-content crash** (4baaa00): find-action-and-proof.sh iterated .content with []
  (crashes on a string-content message = every first turn) → prepare failed closed → judge unreachable.
  Now guards `if type=="array"`.
- Wave-2 e2e agents (B2/B3) worked AROUND these with install-time chmod + t.Skip bug-pins; those e2e
  tests should now be simplified to drop the workarounds + flip the skips to real assertions (fold into
  each batch's reviewer pass, OR a small follow-up once the batches merge on the fixed base).
