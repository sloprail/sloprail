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

### BATCH 1 — COMPLETE ✅ (all 4 slices merged: markers, match-scopes, traj-describe-cite, events-vocab; base 2d0da95, 20 unit pkgs green)
### BATCH 1 — leaves, all independent, fan out together
Grouped into 4 non-overlapping worktrees (file-ownership boundaries to avoid conflicts):
- worktree `events-vocab` (impl/events-vocab): 1a+1b+1c+1d — DONE (61 files, all gate green), IN REVIEW. Notable: 2 NEW modules (tagmod, tooluse); Post events now carry content→feedback-loop caveat handled in fixtures; shipped authoring-slop guardrail updated to newContent; e2e 028_tag_write COLLIDES with 028_trajectory_describe (renumber to 030).
- worktree `markers` (impl/markers): 1e — IMPL DONE (commit 5852a1b), IN REVIEW
- worktree `match-scopes` (impl/match-scopes): 1f — IMPL DONE, rebased, IN REVIEW
- worktree `traj-describe-cite` (impl/traj-describe-cite): 4b+4c — DONE, rebased, IN REVIEW. NOTE: mock writes EMPTY toolUseId, so parentPath correlation is fixture/unit tested, not mock-e2e. Adds internal/transcript/{cite,describe,line}.go + session_trajectory{,_cite,_describe}.go; leaves normalize slot (030).

- [x] 1a  — MERGED (2d0da95) after 3 fix rounds, grep-clean               [events-vocab ✓]
- [x] 1b  — MERGED (2d0da95) after 3 fix rounds, grep-clean               [events-vocab ✓]
- [x] 1c  — MERGED (2d0da95) after 3 fix rounds, grep-clean               [events-vocab ✓]
- [x] 1d  — MERGED (2d0da95) after 3 fix rounds, grep-clean               [events-vocab ✓]
- [x] 1e  marker: quoted + frontmatter fqn — MERGED (b26d2ee), reviewed zero issues     [markers ✓]
- [x] 1f  per-nature match SCOPES + glob shorthand + internal/natures — MERGED (218de70)   [match-scopes ✓]
- [x] 4b  session trajectory describe — MERGED (93d0c52)                               [traj-describe-cite ✓]
- [x] 4c  session trajectory cite — MERGED (93d0c52), re-reviewed zero (40 adversarial+20k fuzz) [traj-describe-cite ✓]

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

### BATCH 2 — COMPLETE ✅ (both slices merged: declaration-loaders, traj-normalize; base 4d7d9cf)
### BATCH 2 — was (base 90ca13a). 2 parallel worktrees:
- worktree `declaration-loaders` (impl/declaration-loaders): 2+2b (loaders + shared check/require/judge types + example-yaml reconciliation) — IMPLEMENTING
- worktree `traj-normalize` (impl/traj-normalize): 4a (trajectory normalize subcommand) — IMPLEMENTING
### BATCH 2 — after batch 1
- [~] 2   loaders (new internal/declaration pkg, 76 unit cases) — review ZERO BLOCKERS, fixing 2 MINORs      [declaration-loaders]
- [x] 2b  shared types — MERGED (4d7d9cf)                                                [declaration-loaders ✓]
- [x] 4a  session trajectory normalize — MERGED, reviewed zero issues              [traj-normalize ✓]

  - [~] 2r  RECONCILED 13 example yamls to spec grammar (marker.kind→any(markers), refactoring.active→context[..], path→event.path, glob-or→path contains) — review confirmed `contains` IS a real op; fixing 2 MINORs (content-de-layering root-dir equivalence, Goal.Enabled required) to the new spec grammar (found by match-scopes review).
      OLD vocab in examples/**/*.yaml that the spec-faithful scopes REFUSE:
      `marker.kind` singular → spec `any(markers, .kind == ...)`; bare `refactoring.active` →
      `context["refactoring"].active`; undeclared `tags` (needs the trigger's own event scope, e.g.
      PostTagWrite.tags); glob-`or`-glob `"**/a" or "**/b"` (neither bare glob nor valid expr).
      This belongs with declaration-loading/dispatch (whoever wires match against real declarations).

### BATCH 3 — COMPLETE ✅ (gate+structure+file-guard+context all enforce; base abde4df). DISPATCH ENGINE DONE.
### BATCH 3 — was (base 9342d8b). SEQUENTIAL (natures build on core's check-runner + gates map):
- worktree `dispatch-core` (impl/dispatch-core): CHECK-RUNNER + gate (3a) + structure (3s) — DONE (~4832 lines), IN REVIEW. Delivers internal/dispatch pkg with Runner.Run(Request)→Verdict API (the natures slice imports this), Jinja2-subset renderer (all 8 example .md.j2), judge-via-sr-agent, gates[] map in sessionstate under reserved !sloprail:gates keyspace. Caught+fixed a real jq `.pass // empty` fail-open bug.
- worktree `dispatch-natures` (impl/dispatch-natures): file-guard (3c) + context (3b) — IMPLEMENTING (goal REMOVED per PR#19). On sessionstate KV (SQLite deferred per PR#19 #3).
  (grouped this way because all natures share the dispatch entry points session_pre_tool/session_stop.go +
   the check-runner; parallel worktrees would conflict heavily on those. gate+structure first because gate
   establishes the gates[] map context needs, and both are the simplest natures to prove the runner e2e.)
### BATCH 3 — dispatch engine (after batch 2)
- [x] 3a  gate dispatch — MERGED (19bd373), reviewed, flat-event fixed                [dispatch-core ✓]
- [x] 3c  file-guard dispatch — MERGED (abde4df), reviewed no-blockers                [dispatch-natures ✓]
- [x] 3s  structure-gate — MERGED (19bd373)                                            [dispatch-core ✓]
- [x] 3b  context lifecycle — MERGED (abde4df) — DISPATCH ENGINE COMPLETE ✅          [dispatch-natures ✓]
- [x] 3d  goal — REMOVED (PR#19: not a primitive; user-side composite via /goal + context). No engine dispatch.

### WAVE 2 (GOAL.md) — per-use-case e2e tests (after all dispatch)
- [ ] one e2e suite per example in examples/, multi-scenario (pass + violation paths), via claude-mock harness.

KNOWN PRE-EXISTING RED (base branch, NOT a regression — deferred to Wave 3 deletion):
  tests/e2e/examples/013_required_context_precondition + deterministic_refactoring are OLD-FORMAT
  (GUARDRAIL.md/hooks, expect examples/guardrails/... path layout that no longer exists post-migration).
  New-format equivalents already built: examples/required-context-precondition,
  examples/deterministic-refactoring-mode. Per GOAL.md these superseded old e2e are DELETED in Wave 3
  (kept now as reference). They make `go test ./...` red today; that is expected, not caused by any slice.

### WAVE 2 (GOAL.md) — per-use-case e2e — COMPLETE ✅ (base a78978e)
All 16 use cases e2e'd (036-051), 0 FAILs. Engine slices merged: judge-config (gonja+model/timeout+
preventive-create, dba20b5), --owner cross-guardrail state read (b8608c3). E2e batches: B1 036-041
(7736651), B2 042-045 (9a3b011), B3 046-049 (5dfcab5), B4 050-051 (3f4c26d). Superseded old-dup
deterministic_refactoring e2e + internal/declaration/examples_test.go deleted (a78978e). See WAVE2-PLAN.md.
Shipped-example bugs found+fixed by the effort: 14 non-exec scripts; cite --path; action-proof jq;
eval-loop-maxing require→match; task-management message_id jq; interlinking/keyword/completeness
--owner+jq-s+SR_WORKSPACE; research-rigor clone-from-invocation + .fields wire-form; intake skip-context.
Spec additions pushed to PR #2: Check model?/timeout?, PreFileCreate.newContent optional, state list --owner.

### D3/D4 (review comments) — NOW UNBLOCKED (mock #470 merged + INSTALLED). REFINED SCOPE:
Investigation changed the picture — much of what looked like "real claude / stub" is NOT:
- engine_repo_judges ALREADY drives the MOCK (its own comment); it uses a MODEL-VERDICT stub, which is
  the CORRECT deterministic way to test SCRIPT behavior (not a race against a model) — NOT a deficiency.
  The judge VERDICT stub (InstallJudgeClaude) across the whole judge suite is likewise the deterministic
  verdict-injection mechanism; swapping it for the mock is NOT an improvement (you'd still inject the
  verdict). → The TODO(D3) markers should be RECLASSIFIED as "the stub is the deterministic verdict
  channel", not "migrate to mock". Do NOT churn the judge suite onto the mock for verdicts.
- RunReal (ACTUAL real claude) is used by exactly ONE test: pre_tool/015_06_real_agent, GATED behind
  SLOPRAIL_REAL_AGENT=1 (skipped in CI, bills operator). It's an opt-in smoke — leave or delete; not a
  CI concern.
- THE REAL D4 WORK = the trajectory tests (session/028_describe, 029_cite, 031_normalize) keep MINIMAL
  hand-authored jsonl fixtures for shapes the OLD mock couldn't produce. #470 (now INSTALLED, built from
  wt-470-rebase) threads a non-empty toolUseId (seedSubagentTranscript takes toolUseID → meta toolUseId)
  and accepts tool_result-in-user records. So 028's sub-agent-parentPath fixture and 029's answer-envelope
  (tool_result) fixtures are likely NOW producible by the mock → migrate them, drop the fixtures. STILL
  possibly impossible (verify empirically against the INSTALLED mock): 029 multi-human-turn (mock has one
  human turn), 031 write-before-read (mock applies writes before normalize), 031 multi-block/preamble-no-uuid.
  → A D4 slice: empirically test the INSTALLED mock per kept fixture; migrate the now-producible ones;
  keep only the genuinely-impossible with UPDATED measured comments; if a remaining gap is worth closing,
  it's another a10n-cli mock PR (e.g. multi-human-turn, or synthesize toolUseResult for a registered tool
  — the latter also unblocks B2's ToolUseWithResult from relying on a passed-through scenario record).

### WAVE 3 (GOAL.md) — cleanup, SEPARATE PRs, at the very end
- [ ] delete old GUARDRAIL.md format + old dispatch + cyclemod TurnEnd + session query + old/duplicated e2e.
      KEEP matcher.go/matcherenv.go. Migrate examples. (The two smallest superseded-e2e deletions already
      done as Wave-2 finalization; the big old-format/dispatch removal is Wave 3.)
- [ ] spec cleanup in PR #2 (remove superseded), AFTER impl cleanup.

## LESSONS (persist across compaction)
- `rg`/ripgrep IGNORES dot-dirs (.sloprail/) and hidden files BY DEFAULT. When searching for
  consumers of renamed fields / removed APIs, ALWAYS use `grep -rn` or `rg --no-ignore --hidden`.
  This hid two live guardrails (judge-skill.sh, judge-rule.sh) from the events-vocab field rename.
- Every reviewer must grep the WHOLE repo (grep -rn) for old names when a field/API is renamed,
  including .sloprail/guardrails/*, marketplace/, examples/.

- PAYLOAD `event` MUST serialize FLAT: `.event.newContent`/`.event.path`, NOT `.event.fields.*`.
  event.Event.MarshalJSON writes nested {kind,fields} — the assembler must flatten it. The spec
  (`{{ event.newContent }}`) and all example scripts/templates read flat. dispatch-core fixed this in
  checks.go/payload.go + ContextEnter/ExitPayload; dispatch-natures MUST rely on the flat shape.
- TEST-MASKING lesson: a test that hand-crafts an input that doesn't match the REAL runtime
  serialization HIDES a serialization bug. Tests must assert against the payload the actual assembler
  produces (build through the real code path), not a convenient hand-built map. (Nearly shipped a
  nested-event payload that would have broken the file-guard/context slice.)
- Example-loading tests (examples_test) can trip on UNTRACKED stray empty dirs in the worktree
  (e.g. an empty gate/verify-all-linked/ with no gate.yaml) — git ignores empty dirs so they're
  invisible to `git status` but the loader flags them as incomplete declarations. When such a
  test fails post-merge, check `git ls-files`/`git ls-tree` — if the dir is untracked cruft, rmdir it
  (CI on a clean checkout never sees it). Removed one for interlinking during the batch-2 merge.

## FOLLOW-UP TASKS (later, not blockers)
- [dispatch-natures review #1] preventive file-guard fails-closed on underivable UPDATE but NOT CREATE
  (nature_fileguard.go:132 checks KindPreFileUpdate+!resultKnown; a NotebookEdit-created new .ipynb emits
  PreFileCreate w/ empty newContent → preventive check false-passes → write lands transiently. After-check
  at Stop still catches it (no permanent bypass), so preventive degrades to non-preventive for that case.
  FIX: also refuse an underivable-empty preventive CREATE, or narrow the doc claim to updates.
- [dispatch-natures review #2 latent] at Stop, file-guards run BEFORE context enters, so a file-guard gated on
  a POST-entering context sees stale state (enters→file-guards→gates→exits would be the natural order).
  Unreachable via shipped examples (only context-gated file-guard enters on PreToolUse=persisted). Reorder if
  a Post-entering + file-guard-gated case ever appears.
- [dispatch-natures review #4 nit] nature_pre_tool.go:15-18 comment says new dispatch runs AFTER old; it runs FIRST. Fix comment.
- FIX example bug (flagged by dispatch-natures): examples/eval-loop-maxing/.sloprail/context/goal-tracking/
  exit.sh reads stale `.event.gates[...]` but the spec made gates a SIBLING of event → should be `.gates[...]`
  (same fix run-verify.sh already got). Pre-existing; composite e2e passes despite it. Trivial follow-up.
- WAVE-2 unit-17 (no-unasked-deletion) judge: consume cite's CitationMatch.Grounding (the WHOLE
  AskUserQuestion envelope, question+answer) into the judge's additionalContext — so an answer-grounded
  change's judge sees the question, not just the extracted answer (PR#19 #4 decision). The enabling
  primitive (Grounding field) is merged; the dispatch-side consumption is unbuilt.
- Author-facing guidance: a guardrail that writes an in-repo ledger AND binds Post events feeds its
  own growing content back as newContent (Post newContent is spec-mandated non-optional). The blessed
  record pattern dodges this via out-of-tree sr-session state. Add a warning to authoring skill docs
  (state-management.md / file-event-hooks.md). Surfaced by events-vocab review; only documented in
  session/015 test comments today.

## Gate for every slice
green reviewer (loop fix→review until ZERO issues) + `make test-unit test-services` (+ relevant e2e via
local a10n-claude-mock) green + CI green on push. Then merge impl/<slice> → draft/fileguard-format, remove worktree.

## Spec divergences the impl must reconcile (spec is truth)
- filemod PreFileUpdate: Go `result`/`resultKnown` → spec `newContent?`/`oldContent`.
- filemod Post file kinds: Go declares only `path` → spec carries old/newContent + old/newMarkers.
- create field `content` (Go) → `newContent` (spec).
- cyclemod `TurnEnd` → spec `Stop`.
- marker fqn single-token → spec allows quoted multi-word (`sr:asked "..."`).
