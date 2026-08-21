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
