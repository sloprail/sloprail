# WAVE PLAN — derived from the gap-map (implements GOAL.md)

Base branch: `draft/fileguard-format` (sloprail repo). Worktrees at
`/Users/nsviridenko/ws/sloprail/wt/<slice>`, branch `impl/<slice>`, created
EXPLICITLY off the base (never Agent isolation).

## What already exists (REUSE, do not rebuild)
- match-expr evaluator: `internal/guardrail/matcher.go` + `matcherenv.go` (expr-lang;
  startsWith/endsWith/==/and/or/not/in/any all work; boolean-or-refuse enforced).
- command extract: `internal/commandmod/extract.go` (ExtractCommand → invocations[].bin/argv/flags).
- file extract: `internal/filemod/extract.go` (pending + git-diff observed).
- marker scan: `internal/filemod/marker.go` (`// # --` leaders; fqn = single token — GAP for quoted).
- transcript: `internal/transcript/` (Read, Entry{IsSidechain}, Since/Mark, Filter/Query --where,
  subagent.go/sibling.go/identity.go).
- OLD dispatch (Wave 5 delete, keep matcher): internal/guardrail/* (except matcher/matcherenv),
  services/sr-session/{dispatch_post,session_pre_tool,session_stop,...}.go, cyclemod TurnEnd, session_query.

## DAG (leaves → dispatch → trajectory → usecases → cleanup)

### BATCH 1 — leaves, all independent, fan out together
Grouped into 4 non-overlapping worktrees (file-ownership boundaries to avoid conflicts):
- worktree `events-vocab` (impl/events-vocab): 1a+1b+1c+1d — RESUMED after API-error (was ~80% done, builds; finishing tests)
- worktree `markers` (impl/markers): 1e — IMPL DONE (commit 5852a1b), IN REVIEW
- worktree `match-scopes` (impl/match-scopes): 1f — IMPL DONE, rebased, IN REVIEW
- worktree `traj-describe-cite` (impl/traj-describe-cite): 4b+4c — DONE, rebased, IN REVIEW. NOTE: mock writes EMPTY toolUseId, so parentPath correlation is fixture/unit tested, not mock-e2e. Adds internal/transcript/{cite,describe,line}.go + session_trajectory{,_cite,_describe}.go; leaves normalize slot (030).

- [ ] 1a  TurnEnd→Stop rename (cyclemod + refs; e2e dir 026 renamed)         [events-vocab]
- [ ] 1b  PostTagWrite event + #tag scanner (new module/kind)                [events-vocab]
- [ ] 1c  PreToolUse semantic event (bindable kind)                          [events-vocab]
- [ ] 1d  Post-file events carry old/newMarkers + old/newContent; naming     [events-vocab]
- [x] 1e  marker: quoted + frontmatter fqn — MERGED (b26d2ee), reviewed zero issues     [markers ✓]
- [x] 1f  per-nature match SCOPES + glob shorthand + internal/natures — MERGED (218de70)   [match-scopes ✓]
- [~] 4b  session trajectory describe — passed; slice RE-REVIEWING cite fix           [traj-describe-cite]
- [~] 4c  session trajectory cite — multi-Q parse FIXED (corpus-grounded), RE-REVIEWING [traj-describe-cite]

NOTE (from markers agent, follow-up not blocker): services/sr-mark/marker.go WriteMarker emits
fqns UNQUOTED — fine today (examples hand-write quoted frontmatter), but if sr-mark must ever WRITE
a quoted multi-word fqn, reconcile then.


### CROSS-SLICE CONTRACTS (from match-scopes agent — Batch 2/3 must honor)
- `ContextState{Active bool, Payload map}`, `GateState{Status GateStatus}`, `GateStatus`(pass|fail)
  now live in NEW package `internal/natures` (json tags active/payload/status). Batch-2 declaration
  loading imports these from internal/natures.
- Runtime env shape the dispatch slices (Batch 3) must assemble:
  * File matcher: event Fields are FLAT — {path, markers, context}.
  * Gate/Context matcher: NESTED — {event: {<kind fields>}, context: {...}} (context top-level, no gates).
- Marker structural shape used by scopes: {kind, fqn, line(int)} — mirror of filemod.Marker.

### BATCH 2 — after batch 1
- [ ] 2   .sloprail/{file-guard,gate,context,goal}/*.yaml + structure.yaml loaders (needs 1b/1c/1f)
- [ ] 2b  require/checks/judge/prepare shared types (Prerequisite, Check, *CheckPayload, *JudgeInput) (needs 2)
- [ ] 4a  session trajectory normalize (needs extractors-per-entry: 1b + command + file)

  - [ ] 2r  RECONCILE example *.yaml match: strings to the new spec grammar (found by match-scopes review).
      OLD vocab in examples/**/*.yaml that the spec-faithful scopes REFUSE:
      `marker.kind` singular → spec `any(markers, .kind == ...)`; bare `refactoring.active` →
      `context["refactoring"].active`; undeclared `tags` (needs the trigger's own event scope, e.g.
      PostTagWrite.tags); glob-`or`-glob `"**/a" or "**/b"` (neither bare glob nor valid expr).
      This belongs with declaration-loading/dispatch (whoever wires match against real declarations).

### BATCH 3 — dispatch engine (after batch 2)
- [ ] 3a  gate dispatch (on→require→checks, pass/fail, one-shot) — needs 1f,2,2b
- [ ] 3c  file-guard dispatch (match file state, preventive, re-fires) — needs 1d,2,2b
- [ ] 3s  structure-gate path-allowlist check — needs 2 only (land with 3c)
- [ ] 3b  context lifecycle (on/enter/exit, context[] & gates[] maps) — needs 1b,2,2b,3a
- [ ] 3d  goal = context+goal.yaml pairing (composite, no engine wiring) — needs 3b

### WAVE 2 (GOAL.md) — per-use-case e2e tests (after all dispatch)
- [ ] one e2e suite per example in examples/, multi-scenario (pass + violation paths), via claude-mock harness.

### WAVE 3 (GOAL.md) — cleanup, SEPARATE PRs, at the very end
- [ ] delete old GUARDRAIL.md format + old dispatch + cyclemod TurnEnd + session query + old/duplicated e2e.
      KEEP matcher.go/matcherenv.go. Migrate examples.
- [ ] spec cleanup in PR #2 (remove superseded), AFTER impl cleanup.

## Gate for every slice
green reviewer (loop fix→review until ZERO issues) + `make test-unit test-services` (+ relevant e2e via
local a10n-claude-mock) green + CI green on push. Then merge impl/<slice> → draft/fileguard-format, remove worktree.

## Spec divergences the impl must reconcile (spec is truth)
- filemod PreFileUpdate: Go `result`/`resultKnown` → spec `newContent?`/`oldContent`.
- filemod Post file kinds: Go declares only `path` → spec carries old/newContent + old/newMarkers.
- create field `content` (Go) → `newContent` (spec).
- cyclemod `TurnEnd` → spec `Stop`.
- marker fqn single-token → spec allows quoted multi-word (`sr:asked "..."`).
