package e2e

import (
	"os"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// NOT RE-VEHICLED onto the new file-guard nature — deliberately, and reported.
//
// The wave that moved the shared e2e coverage off the old GUARDRAIL.md format onto
// the file-guard nature re-vehicled every directory whose observations the new
// dispatch can reproduce. THIS directory is the one it could not, and the reason is
// specific rather than incidental: its probe measures the OLD PreToolUse dispatch
// reading the RAW hook payload, which the new-format check payload structurally does
// not carry.
//
// What is blocked, precisely:
//
//   - T014_01 asserts, for every tool call a shared-tree dispatch makes (the root's
//     writes on both sides AND the sub-agent's own), that the Pre-tool payload
//     carries `agent=[none]` — i.e. names NO `agent_id` / `agent_transcript_path` —
//     and that all of them are judged under the ROOT's SR_SESSION_ID. That is the
//     measurement the whole package's coverage claims rest on ("a shared-tree
//     sub-agent's tool calls are dispatched AS THE PARENT"). A file-guard's check —
//     preventive or after — receives declaration.CheckPayload (event, transcriptPath,
//     context) and a gate's receives GateCheckPayload; NEITHER carries the raw hook
//     payload's `agent_id` / `agent_transcript_path` fields, so the `agent=[none]`
//     tripwire cannot be reproduced by any new-format check. A preventive file-guard
//     could see SR_SESSION_ID and so keep the session-equality half, but dropping the
//     agent-field half would weaken the tripwire, which the re-vehicling must not do.
//
//   - T014_06 counts, in the run's own stream, how many times a PreToolUse hook
//     refused (`this rule says no`, exit 2). A file-guard's after-check refusal is a
//     de-duplicated Stop attachment, not a per-call stream deny, so the count is not
//     reproducible there either.
//
// The dispatch-SHAPE assertions here (how many sub-agents ran, which tree each got,
// that the root's own guardrails stay live across a delegation) are format-neutral
// machinery, but they are observed THROUGH the PreToolUse probe above, and the
// probe's load-bearing tripwire is what cannot move. So this directory keeps
// e.Guardrail until the old dispatch is removed; when that happens this coverage
// needs re-deriving against whatever the new dispatch exposes about a sub-agent's own
// tool calls, not silently weakening. The sub-agent's own CYCLE (its Post cycle,
// which IS a file-guard's after-check) is re-vehicled in 015_subagent_own_cycle; this
// directory's Pre-event probe is the part with no after-check equivalent.

var (
	New      = harness.New
	Turns    = harness.Turns
	Write    = harness.Write
	Bash     = harness.Bash
	Dispatch = harness.Dispatch
)

// TestMain removes the binary build dir when this package's tests finish.
// Without it every e2e package leaks 15M for the life of the machine.
func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}
