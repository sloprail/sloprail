package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runSubagentStop drives the command with a payload on stdin, as a harness does.
func runSubagentStop(t *testing.T, payload string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := newSessionSubagentStopCmd()
	var out, errOut bytes.Buffer
	cmd.SetIn(strings.NewReader(payload))
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(nil)
	err = cmd.Execute()
	return out.String(), errOut.String(), err
}

// TestSubagentStopNeverBlocksOnAnUnplaceableCycle is the property that keeps a
// stub from bricking delegation.
//
// A non-zero exit from SubagentStop is a BLOCK: the harness re-runs the
// sub-agent's turn. So a failure that will not change on a retry — a payload
// naming no sub-agent, a record that cannot be read — must not be returned as an
// error, or every delegated task is trapped in a loop it cannot leave.
//
// Declining to judge is the safe failure here. Refusing to let work finish is
// not: a guardrail engine that bricks delegation is worse than one that says so
// and stands down.
func TestSubagentStopNeverBlocksOnAnUnplaceableCycle(t *testing.T) {
	for _, tc := range []struct{ name, payload, wants string }{
		{
			name:    "no sub-agent on the payload",
			payload: `{"transcript_path":"/nowhere/session.jsonl"}`,
			wants:   "not judging this cycle",
		},
		{
			name:    "a sub-agent whose record cannot be read",
			payload: `{"transcript_path":"/nowhere/session.jsonl","agent_id":"abc","agent_transcript_path":"/nowhere/subagents/agent-abc.jsonl"}`,
			wants:   "went unjudged",
		},
		{
			name:    "an agent id that is not a name",
			payload: `{"transcript_path":"/nowhere/session.jsonl","agent_id":"../../elsewhere"}`,
			wants:   "went unjudged",
		},
		{
			name:    "nothing at all",
			payload: `{}`,
			wants:   "not judging this cycle",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, stderr, err := runSubagentStop(t, tc.payload)
			require.NoError(t, err,
				"a non-zero exit here blocks the sub-agent and the harness re-runs its turn — an unplaceable cycle would loop forever")
			assert.Contains(t, stderr, tc.wants,
				"standing down must be reported, or the gap is invisible")
		})
	}
}

// TestSubagentStopStandsDownRatherThanActingAsTheParent: the one thing that must
// never happen is a sub-agent's cycle being judged under the identity of the
// session that dispatched it. A payload naming no sub-agent gets no judgement at
// all, rather than the parent's.
func TestSubagentStopStandsDownRatherThanActingAsTheParent(t *testing.T) {
	_, stderr, err := runSubagentStop(t, `{"transcript_path":"/nowhere/session.jsonl"}`)
	require.NoError(t, err)
	assert.Contains(t, stderr, "rather than judging it as the session that dispatched it")
}

// TestSubagentStopHonoursStopHookActive: already refused once this cycle, so
// refusing again would be a loop the sub-agent cannot leave.
func TestSubagentStopHonoursStopHookActive(t *testing.T) {
	_, stderr, err := runSubagentStop(t,
		`{"transcript_path":"/nowhere/s.jsonl","agent_id":"abc","stop_hook_active":true}`)
	require.NoError(t, err)
	assert.Empty(t, stderr, "a cycle already refused once must be left alone entirely")
}
