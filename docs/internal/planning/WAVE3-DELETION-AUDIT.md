# Wave 3 deletion audit — d26789e (delete old GUARDRAIL.md format)

Adversarial behavior-preservation audit of the old-format deletion (50 files,
+839/−9995). Every deleted piece mapped to its new-format successor, classified:
**A** = replaced · **B** = moved (faithful) · **C** = obsolete-by-design · **D** = LOST.

**VERDICT: no ENFORCEMENT behavior lost.** "Only the config format changed; the
surrounding logic/behavior is preserved" holds for everything that protects the
user. The machinery (tree-diff, revalidation skip/re-fire, tag events, refusal
collection, mark-holding, fail-closed) was MOVED, not rewritten. The migration even
FIXED two latent bugs (a PreFileUpdate revalidation bypass; the matcher fail-open).
Losses are confined to **diagnostic wording** and **test-coverage**, not shipped
enforcement.

## ⚠️ D — genuinely lost (all non-enforcement)

### D1 — `remedy()` origin-aware repair guidance (diagnostic-only, LOW)
- OLD: `session_pre_tool.go:remedy` + `remedy_test.go`. A broken **plugin** rule's
  load-failure report said *"not yours to fix — ships in plugin X at <root>; disable
  via `disabled: [<qualified>]` in <dotdir>/config"* — steering away from editing an
  install cache.
- NEW: `nature_dispatch.go:reportNatureInvalid` prints attribution + bare fault, **no
  disable hint**. Data retained (`declaration.Invalid.Qualified()` exists; the sibling
  shadow-report still prints the `disabled:[...]` remedy), so it's reconstructable.
- **Restore** — tracked. No enforcement impact.

### D2 — test-coverage gaps (behavior upheld in prod; exact unit test gone)
1. `remedy_test.go` wording matrix — no successor.
2. `boundkinds_test.go` (disabled-asks-nothing / broken-kinds-collected) — no successor.
3. Hook-cannot-START (NUL byte) → refuse — prod upholds (`exec.go:127`); no unit test.
4. Multi-refuser collection at Stop — re-tested e2e (`session/024`) but not unit.
5. Post-hook declared-order / hooks-after-refusal — re-tested at *check* level, not *hook* level.
6. subagent 014 T014_05/06/08 (format-neutral harness plumbing: mixed isolated/shared
   worktree-leak, refusing root completes across delegation, dispatch in a NON-git repo)
   — no analog.
7. Post-matcher-error fail-closed — re-tested e2e (`session/027`), not unit.

## Area-by-area (A/B/C)

### 1. Post/Stop dispatch (dispatch_post.go 733L → nature_stop.go + nature_fileguard.go + post_events.go + revalidation.go)
- tree-diff / readdOutstanding / tagEvents / boundTo → `post_events.go` (verbatim) — **B**
- per-guardrail fingerprint skip/re-fire, blessed-vs-not Record → `runFileGuardsPost` + `revalidation.go` — **A/B** (key namespaced `file-guard:<name>`)
- report-all-objections-at-once → `nature_stop.go:joinRefusals`, e2e 024 — **A**
- mark-holding on refusal → `session_stop.go` blocks before advanceReadMark — **A**
- **subdirectory subject-resolution DEFECT** → structurally fixed (`post_events.go` returns repo root → `rev.Subject(e, root)`); re-tested + strengthened e2e `session/025` — **A**
- PostTagWrite→refusal: gates can't bind Post (loader refuses); NO old GUARDRAIL.md used PostTagWrite as a hook; blocking-on-tag preserved via tag→context→Stop-gate (research-rigor) — **C**
- reportBrokenAtStop kind-scoped → unscoped reportNatureInvalid — **C** (see §7)

### 2. Pre-tool dispatch (session_pre_tool.go −1112 → nature_dispatch.go / nature_pre_tool.go / nature_fileguard.go / internal/dispatch)
- load (guardrailStore→guardrail.Store) → newNatureDeclarations→declaration.Store.Load(+NewWithPlugins) — **A**
- runHooks/process-group-kill/timeout → internal/dispatch/exec.go — **B**
- refusalReason → scriptRefusalReason (near-verbatim) — **B**
- reportInvalid/Shadowed/Unresolved → reportNature* (last verbatim) — **A**
- **fail-closed matcher-error** → firstMatchingEvent/runFileGuards*/structure/require all refuse on err; regressed to fail-OPEN, caught + fixed (7c2d3d2), re-proven e2e 014/027 — **A**
- boundKinds → naturePreToolBoundKinds/natureBoundKinds — **A** (invalid-kinds-inclusion sub-property is D2)
- remedy → **D1**

### 3. Fail-open/fail-closed (dispatch_post_failopen_test.go 380L)
- broken decl blocks nothing but IS reported → **A** (nature_reportinvalid_test + e2e 013)
- matcher errors fail closed → **A** (e2e 014/027)
- hook cannot run → refuse → behavior **A** (exec.go:127); unit test **D2**
- does-not-disarm-a-sound-neighbor → **A** (e2e 013)

### 4. Killed-by-signal (refusal_reason_killed_test.go 99L)
- **A**, re-tested twice: unit `internal/dispatch/exec_test.go` (recovers signal behind Go's -1; says "killed" not "exit -1") + e2e `fileguard/036`.

### 5. subagent/014 deletion
- **C** verified: CheckPayload structurally omits agent_id/agent_transcript_path, so 014's raw-payload "agent=[none]" assertion is meaningless in the new format → correctly non-migratable. Sub-agent-own-cycle coverage lives in **subagent/015** (12 tests, stronger). T014_05/06/08 (harness plumbing) → **D2**.

### 6. session_start / session_stop / session_subagent_stop
- start −72: old load-report → newNatureDeclarations (reports at start) — **A**
- stop −53: old dispatch → natureStopDispatch; order + mark-holding preserved — **A**
- subagent_stop: was a nil stub → now calls the SAME completeCycle as root Stop — **A, strengthened**

### 7. report* diagnostics
- reportInvalid/Shadowed/Unresolved → reportNature* — **A** (shadow still prints `disabled:[...]`)
- **reportBroken kind-scoped → C obsolete:** it said "guardrail X bound to <kind> could not load, NOT guarding this action" — scoped via a Problem.Event field that existed ONLY because old GUARDRAIL.md listed kinds under `hooks:`. New file-guard has no author-declared kinds; new Problem has no kind field → the scoped message has nothing to carry. No enforcement loss. ⚠ minor: a broken GATE/CONTEXT *does* carry explicit `on:` kinds — could scope the not-loaded message there (optional).

### Misc
- SplitFrontmatter → `sr-file/frontmatter.go` (verbatim) — **B**
- examples/deprecated/ → survive as new-format examples (041, 051) — **C**
- internal/guardrail/{matcher,matcherenv,scopes}.go — the fail-closed anchors, intentionally kept
- dispatch_post_revalidation_test.go (548L, 9 invariants) → fully re-tested + EXPANDED in revalidation_test.go (19 tests), which adds two tests fixing a real PreFileUpdate refusal-bypass — net GAIN

Full findings (with line refs) from the audit agent; recorded as the durable map.
