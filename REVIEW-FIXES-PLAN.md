# PR #19 leftover-review fixes — plan (user: "do everything"), for compaction-safety

Base: draft/fileguard-format @ dce258f. All grounded in EMPIRICAL checks + current code.

## FACTS ESTABLISHED (corrected earlier wrong claims)
- **`CLAUDE_CODE_SESSION_ID` IS in the tool-call/hook env** (measured: =453b70e4… = this session; maps to
  `~/.claude/projects/<encoded-cwd>/<sid>.jsonl`, verified real file). My earlier "no session-id env var"
  was WRONG (looked for wrong name `CLAUDE_SESSION_ID`). Also present: `CLAUDE_CODE_CHILD_SESSION=1`,
  `CLAUDE_CODE_HOST_SESSION_ID=local_…`. `CLAUDE_AGENT_ID`/`CLAUDE_AGENT_TYPE` NOT set (env); agent_id is
  hook-stdin only. NOTE: whether a real Agent-subagent gets its OWN CLAUDE_CODE_SESSION_ID vs inherits
  parent's is UNMEASURED cleanly (my test nested under an SDK child). BUT — user's call: cite only needs
  the ROOT session (citations live there), so CLAUDE_CODE_SESSION_ID is enough; no subagent detection needed.
- **EnvelopeAt** (internal/transcript/envelope.go:72 `EnvelopeAt(path,line)`) is BUILT but has ZERO consumers
  (grep). The "question to the judge" capability exists as a function, not wired into any judge-prepare.
- **028/029/031 are MOSTLY mock-driven now (D4)** BUT hand-authored fixtures REMAIN: 028's `fixtureParent`/
  `newFixtureParent`/`f.subagent("",…)` still used by T028_03 (empty-toolUseId no-parent case); 029's
  `writeTranscript` for multi-human-turn; 031's fixtures for pre-write-tree + no-uuid-preamble. The user's
  screenshots showed the PRE-D4 state but ALSO these genuinely-remaining fixtures.
- **Judge verdict is stubbed via a bespoke `.sh`** (`InstallJudgeClaude`/`stubJudge`/`InstallClaudeShim`,
  A10N_CLAUDE_BIN) across 032/034/042-049/engine_repo_judges — NOT via the a10n-claude-mock binary.
- **rule-quality/skill-quality judge-{rule,skill}.sh call `claude` DIRECTLY** (timeout 25 claude -p --model
  haiku --settings '{…}'), hand-rolling model/timeout/settings/verdict, as a SCRIPT check.
- **sr-agent already takes `--settings` (CC settings JSON passthrough) + `--model`.** allowed-tools flag: verify.

## LAUNCHED (2 agents, running)
- **review/pr-cite-autoresolve** (agent ab560f5): cite auto-resolves current session from CLAUDE_CODE_SESSION_ID
  + encoded-cwd projects dir when no --path/payload; remove failClosedNoPath; fix the FALSE "no session-id
  env" comments; e2e for the no-path agent-call. → its own PR.
- **review/pr-judge-check** (agent a01af22): (D) add `allowed_tools` to judge Check (spec+impl+sr-agent);
  convert rule-quality+skill-quality to `judge:` checks (RUBRIC→prepare/template, model/timeout/allowed_tools,
  settings via sr-agent), preserve rubric-assembly/enforced-only/empty-rules; RECONCILE fail-open-on-machinery
  (judge-timeout is fail-CLOSED in the new path — flag/keep thin wrapper). (EnvelopeAt) wire whole-envelope
  (question+answers) into no-unasked-deletion judge prepare (+ a `trajectory envelope` CLI if none), e2e proves
  the question reaches the judge. Spec→PR#2.

## TODO — NOT yet launched (launch after current agents drain to avoid oversaturation; ~6 already running)
### C — route the judge's model call through the a10n-claude-mock BINARY (not the bespoke .sh)
- The mock (post-#470) accepts sr-agent's `-p --model --settings --allowed-tools`. Replace InstallJudgeClaude/
  stubJudge/InstallClaudeShim: the judge invokes the MOCK binary, which runs a scenario/.sh you give it to
  EMIT the verdict (verdict CONTENT stays a fixture — test controls pass/fail — only DELIVERY moves to the
  mock binary). Across 032/034/042-049/engine_repo_judges; drop TODO(D3) markers. Clean local binary-path
  override (how the judge's `claude` resolves to the mock). NEEDS: confirm the mock can emit a judge verdict
  to the file sr-agent names (sr-agent tells claude "write your answer to <path>") — a mock scenario that
  writes that file. If the mock can't target sr-agent's answer-file, that's a mock feature (fold into A).
### A — close the LAST hand-crafted fixtures via a10n-cli mock PR(s)  (cross-repo: horizon-37/a10n-cli)
- Mock PR: (1) multi-human-turn injection → unblocks 029 writeTranscript; (2) accept no-uuid preamble record
  types (custom-title/ai-title/mode/queue-operation/last-prompt) → unblocks 031 preamble fixture; (3) seed a
  sub-agent with an EMPTY toolUseId → unblocks 028 T028_03 (so fixtureParent can be DELETED entirely);
  (4) the mock SETS CLAUDECODE=1 + CLAUDE_CODE_ENTRYPOINT + CLAUDE_CODE_SESSION_ID itself (so the harness
  stopgap that sets these can be removed). Then in sloprail: migrate those fixtures onto the mock, delete
  028 fixtureParent, and remove the harness CLAUDECODE/session-id env-set (rely on the mock). → sloprail PR
  after the mock binary ships.
### (separate PR to mock) — CLAUDECODE into the mock
- Same as A(4): move the `CLAUDECODE=1`/`CLAUDE_CODE_ENTRYPOINT=cli` harness env-set INTO the mock (real CC
  sets them; the mock should too). Then delete the harness stopgap. User: "sep PR to mock to move there."

## KEEP AS-IS (confirmed with user)
- **resultKnown** stays a real engine field (NotebookEdit needs the explicit underivable signal). User: "keep as is."
- Verdict CONTENT stays a fixture (only delivery → mock, per C).

## Answers to user's inline questions (for the reply)
- Q "screenshots still not mock-driven?": partly right — most migrated in D4, but 028 T028_03 + 029/031 STILL
  keep hand-authored fixtures for mock-impossible shapes; A's mock PRs close them.
- Q "routed through claude mock binary not dummy .sh?": YES = item C.
- Q "how EnvelopeAt works now?": built (envelope.go:72) but UNCONSUMED; pr-judge-check wires it into
  no-unasked-deletion's judge prepare.

## SAFETY REGRESSION — new dispatch fails OPEN on a match-EVAL error (found via re-vehicling session/027; agent ac8be7ca fixing)

VERIFIED in code, not a test artifact. A match expr that COMPILES at load but ERRORS at EVALUATION
must fail CLOSED (refuse the events the rule was bound to). OLD behavior: guardrail/matcher.go:121
("pre-tool path REFUSES") + :186 ("caller REFUSES"). Spec: entries.tsp:54 "quietly fail to match, it
fails." NEW nature dispatch regressed to fail OPEN (Fprintf stderr + continue = silently SKIP guard).
A broken/adversarial match silently DISABLES a guardrail. Four fail-open sites, all being fixed:
  1. nature_fileguard.go ~122-126 (preventive/pre): fileGuardSelects err -> continue. FIX: return refusal.
  2. nature_fileguard.go ~244-248 (after/post): same -> continue. FIX: append fileGuardResult refusal (blocking err at Stop).
  3. nature_dispatch.go firstMatchingEvent ~417-426 (gate): m.Match err -> continue. FIX: thread err out (helper returns (event,bool) — must add error), caller -> gate refusal.
  4. nature_context.go contextMatchingEvents ~341-345: m.Match err -> continue. Context has NO refusal channel; documented decision (does not activate), not silent.
Also FIX the misleading comments that justify the fail-open (e.g. the firstMatchingEvent block comment).
The two suites that PIN this are OLD-format and being re-vehicled by the same agent:
  - pre_tool/014_matcher_error_never_fails_open (T014_01..08): pre path = GATE; T014_01 uses
    any(invocations, len(.flags.access)>0) (errors: commandmod invocations = TypeList nil Elem).
  - session/027_post_matcher_error (T027_01..04): T027_02's comment IS the ruling — Post side must now
    REFUSE (blocking err names guard + quotes int(path)). Post file event = FILE-GUARD after-check.
Recorded as Task #8. (The earlier attempt to append this via bash heredoc failed on an unescaped paren
`int(event.path)>0` — "parse error near ')'"; this Edit is the durable record.)

## TWO MORE ENGINE GAPS found by pre_tool B (Tasks #10, #11) — also block deleting old dispatch

#10 SLOPRAIL_LAUNCHED_BY: the NEW check-runner (internal/dispatch/exec.go) NEVER sets LaunchedByEnv on
the check's child env — only the OLD hook path does (services/sr-session/hookenv.go appendLaunchedBy). New
dispatch only READS it (isLaunchedBy). So a new-format guard whose check spawns sr-agent RE-ENTERS itself
(runaway to depth 8 — the exact recursion pre_tool/015_no_reentry pins). FIX: new exec sets
SLOPRAIL_LAUNCHED_BY=<guard-name> on the child env. THEN re-vehicle pre_tool/015 (5 files).
#11 killedBySignal (narrow): internal/dispatch/exec.go scriptRefusalReason has no killed-by-signal branch —
a signal-crashed check says "exit -1, no reason" instead of "killed". STILL refuses (safe); only the message
regresses. Just 019 T019_09e left un-re-vehicled. #10 and #11 are BOTH in exec.go → one combined slice
(after matcher-fix agent ac8be7ca, which may also read exec.go, finishes).

## RE-VEHICLE STATUS (all 4 cluster agents DONE + independently verified green under CI env)
session 17/18, pre_tool A 13/13, pre_tool B 8 (016-021,028,029), revalidation+subagent 9/10. Branch
wave3/revehicle-shared-e2e builds+vets clean as a union; sampled dirs from each pass env -u CLAUDECODE.
AUTHORITATIVE remaining old-format callers (12 files/6 dirs): 014+027 (matcher-fix, in flight) ·
015 5 files (#10 blocks) · subagent/014 (un-migratable → DELETE) · authoring/003 2 files + pre_tool/008
T008_03b + session/006 test_006_01 (unassigned stragglers → agent a193fe68 in flight).
CITE-MERGE NOTE: branch already has an EARLIER cite-cleanup 6d4a58b (D1 Grounding->EnvelopeAt, D2 subagent
no-path fail-closed via IsSubagentTranscript). The NEW cite auto-resolve (c50da94) REMOVED failClosedNoPath
but the report says the IsSubagentTranscript subagent guard is preserved — verify they COMPOSE at merge.
