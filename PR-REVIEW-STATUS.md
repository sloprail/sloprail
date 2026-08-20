# PR #19 — consolidated review status (as of 2026-08-20)

The reviewer re-posted the FULL review set on 2026-08-20 (16 inline comments). Every one
maps to already-tracked work. This is the single source of truth; the per-review docs
(PR-REVIEW-4982871858/4983286129/4983502552-and-633371) hold the detail.

## Mapping of every 2026-08-20 comment → status

| file:line | comment | status |
|---|---|---|
| declaration.go:71 | remove goal (not a primitive); move to /goal | ✅ DONE — Goal removed from declaration.go/store.go; no `.sloprail/goal` dirs |
| store.go:83 | don't load goals here | ✅ DONE — Store.Load has no Goals |
| examples_test.go:112 | this file is slop — we have e2e for concrete examples | ⏳ DELETE ON WAVE-2 COMPLETE — now a load-level reconcile test; Wave-2 per-use-case e2e (in flight, dirs 036-051) covers each example end-to-end (load AND dispatch), making it redundant. Delete once all Wave-2 batches merge. |
| natures.go:72 | design state storage (sqlite in session folder) | 📋 P2 TASK — strategy/…/state-storage-sqlite-session-folder/TASK.md. Interim sessionstate-KV ships. |
| cite.go:265 | is the QUESTION passed to the judge too (not just answer quote)? | ✅ ANSWERED by D1 direction + WAVE-2 unit-17: EnvelopeAt fetches the WHOLE envelope (question+answers) for judge-prepare. The no-unasked-deletion judge (B3, dir 049) + the unit-17 consumption piece feed the full envelope. |
| cite.go:115 | envelope fetch belongs in judge-prepare, not cite CLI | ✅ DONE (D1, merged 6d4a58b) — Grounding removed from cite → transcript.EnvelopeAt(path,line) for judge-prepare. |
| session_trajectory.go:111 | cite resolves via CLAUDE_SESSION_ID when no path? subagent detection via env? | ✅ DONE (D2, merged) — CC docs confirm NO tool-call env var detects subagent (agent_id is hook-stdin only). No-path/session-id-resolved cite FAILS CLOSED (exit 3). Explicit --path + hook-driven still work. |
| session_trajectory_cite.go:198 | path not always set; session-id resolves to root; how know subagent path | ✅ DONE (D2) — same as above; fail-closed is the honest answer the CC docs force. |
| checks.go:34 | call sr-agent via subprocess to reuse (multi-harness) | ✅ SATISFIED (C1) — judge.go already subprocesses `sr-agent`. |
| exec.go:34 | per-judge configurable timeout + model (modelset format) | 🔧 IN FLIGHT (C2) — judge-config agent: spec Check gains model?/timeout?; impl threads them to sr-agent/exec. |
| template.go:9 | use gonja (see how a10n did that) | 🔧 IN FLIGHT (C3) — judge-config agent: replace hand-rolled renderer with gonja, mirroring a10n's internal/jinja. |
| harness.go:279 | use claude-code mock for judge tests, not the bespoke shim | ⛔ BLOCKED (C4/D3) on a10n-cli#470 (being rebased→mergeable now). Then judge e2e drives the mock. |
| engine_repo_judges:130 | why not claude-code mock (real-claude e2e) | ⛔ BLOCKED (D3) on #470 — engine_repo_judges/ uses A10N_CLAUDE_BIN real claude; folds into the D3/D4 rework. |
| 028_trajectory_describe:100/119 | still hardcodes jsonl; no explicit writeTranscript should remain | ⛔ BLOCKED (D4) on #470 — the mock must EMIT these trajectories. #470 adds tool_result-in-user + meta.toolUseId; remaining shapes (multi-human-turn, multi-block, write-before-read) may need a follow-up mock PR. |
| r3_guardrails:34 | delete review3-like temp tests that use REAL claude; map each e2e to an invariant/use-case | ⛔ FOLDS INTO D3/D4 — the review-N dirs are already gone from the tree; the remaining real-claude/real-agent e2e (engine_repo_judges, pre_tool/015_06_real_agent, session/021_02_nested) get replaced by mock-driven e2e in the D3/D4 rework. |

## Genuinely-new derived work items (added to backlog)
1. **DELETE `internal/declaration/examples_test.go`** once all Wave-2 batches (036-051) merge — Wave-2 e2e supersedes it (it both loads AND dispatches each example). Track as a Wave-2-completion step.
2. **Real-claude e2e → mock (D3/D4 scope expansion):** beyond trajectory+judge, also migrate/delete `tests/e2e/engine_repo_judges/`, `tests/e2e/pre_tool/015_no_reentry/test_015_06_real_agent_test.go`, `tests/e2e/session/021_worktree_noise/test_021_02_nested_worktree...` — everything using RunReal/A10N_CLAUDE_BIN/InstallClaudeShim — so NO e2e depends on real claude. Blocked on the upgraded mock.

## In-flight agents (2026-08-20)
- judge-config (C2/C3 + follow-up #1 preventive-create) → impl/pr-judge-config
- Wave2 B1 lifecycle (036-041) → impl/w2-b1-lifecycle
- Wave2 B2 judge (042-045) → impl/w2-b2-judge
- Wave2 B3 judge+marker (046-049) → impl/w2-b3-judgemark
- Wave2 B4 composite (050-051, deletes broken 013) → impl/w2-b4-composite
- a10n-cli#470 rebase onto main (unblocks D3/D4) → wt-470-rebase
