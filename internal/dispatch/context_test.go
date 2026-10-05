package dispatch

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/natures"
)

// These cover the context lifecycle scripts' contract — what EnterContext and
// ExitContext do with a clean exit, a decline, unreadable output, and a
// could-not-run — and the payload each assembles. The script substrate is
// stubbed, so what is under test is the enter-replaces-payload and
// exit-is-lifecycle-only semantics, not the exec itself (exec.go's own tests
// cover that).

// enterReq builds a minimal ContextEnterRequest with the given current state.
func enterReq(current natures.ContextState) ContextEnterRequest {
	return ContextEnterRequest{
		Enter:          "enter.sh",
		Event:          event.Event{Kind: "PostFileCreate", Fields: map[string]any{"path": "goal.yaml"}},
		TranscriptPath: "/tmp/rec.jsonl",
		Gates:          map[string]natures.GateState{"goal-verify": {Status: natures.GateStatusPass}},
		CurrentContext: current,
		Dir:            "/guard",
		Name:           "goal-tracking",
	}
}

// A clean enter with stdout REPLACES the payload and sets the context active.
func TestEnterContext_ReplacesPayload(t *testing.T) {
	r := Runner{
		runScript: func(s scriptCall) (scriptResult, error) {
			assert.Equal(t, "enter.sh", s.Script)
			return scriptResult{Passed: true, Stdout: []byte(`{"scope":"src/","goals":["a","b"]}`)}, nil
		},
	}
	payload, active, v, err := r.EnterContext(enterReq(natures.ContextState{Active: false, Payload: map[string]any{"old": 1}}))
	require.NoError(t, err)
	assert.False(t, v.Refused)
	assert.True(t, active, "a clean enter activates the context")
	assert.Equal(t, "src/", payload["scope"])
	// REPLACES, not merges: the prior "old" key is gone.
	assert.NotContains(t, payload, "old")
}

// enter's stdin carries ContextEnterPayload — currentContext, gates, the event
// flat, and the transcript path.
func TestEnterContext_PayloadShape(t *testing.T) {
	var stdin []byte
	r := Runner{
		runScript: func(s scriptCall) (scriptResult, error) {
			stdin = s.Stdin
			return scriptResult{Passed: true, Stdout: []byte(`{}`)}, nil
		},
	}
	_, _, _, err := r.EnterContext(enterReq(natures.ContextState{Active: true, Payload: map[string]any{"iter": 3}}))
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(stdin, &got))
	// The event renders FLAT under `event` — kind alongside its fields.
	ev, ok := got["event"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "PostFileCreate", ev["kind"])
	assert.Equal(t, "goal.yaml", ev["path"])
	assert.Equal(t, "/tmp/rec.jsonl", got["transcriptPath"])
	// currentContext carries this context's own last {active, payload}.
	cc, ok := got["currentContext"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, true, cc["active"])
	payload, ok := cc["payload"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, float64(3), payload["iter"])
	// gates is present so enter can gate its own activation on a gate's verdict.
	gates, ok := got["gates"].(map[string]any)
	require.True(t, ok)
	gv, ok := gates["goal-verify"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "pass", gv["status"])
}

// enter that produces NO output leaves the prior payload as it was (spec:
// "Exiting without producing output leaves the context's state as it was") — and
// still activates, because enter ran clean and did not decline.
func TestEnterContext_NoOutputKeepsPayload(t *testing.T) {
	r := Runner{
		runScript: func(scriptCall) (scriptResult, error) {
			return scriptResult{Passed: true, Stdout: []byte("  \n")}, nil
		},
	}
	prior := map[string]any{"scope": "src/"}
	payload, active, v, err := r.EnterContext(enterReq(natures.ContextState{Active: true, Payload: prior}))
	require.NoError(t, err)
	assert.False(t, v.Refused)
	assert.True(t, active)
	assert.Equal(t, "src/", payload["scope"], "no output carries the prior payload forward unchanged")
}

// A NON-ZERO enter declines to (re-)activate on this trigger: active is false, no
// refusal, and the payload it would set is nil (the caller leaves the state).
func TestEnterContext_NonZeroDeclines(t *testing.T) {
	r := Runner{
		runScript: func(scriptCall) (scriptResult, error) {
			return scriptResult{Passed: false, Reason: "not the time yet"}, nil
		},
	}
	payload, active, v, err := r.EnterContext(enterReq(natures.ContextState{Active: false, Payload: map[string]any{}}))
	require.NoError(t, err)
	assert.False(t, v.Refused, "a decline is not a refusal — a context does not block")
	assert.False(t, active, "a non-zero enter does not activate on this trigger")
	assert.Nil(t, payload)
}

// enter that activates but prints output this engine cannot read as a flat object
// is a fail-closed refusal (report and leave state), not a silent empty-payload
// activation.
func TestEnterContext_UnreadableOutputRefuses(t *testing.T) {
	r := Runner{
		runScript: func(scriptCall) (scriptResult, error) {
			return scriptResult{Passed: true, Stdout: []byte(`["not","an","object"]`)}, nil
		},
	}
	_, active, v, err := r.EnterContext(enterReq(natures.ContextState{}))
	require.NoError(t, err)
	assert.True(t, v.Refused, "unreadable enter output fails closed")
	assert.False(t, active)
	assert.Contains(t, v.Reason, "goal-tracking")
}

// exitReq builds a minimal ContextExitRequest.
func exitReq() ContextExitRequest {
	return ContextExitRequest{
		Exit:           "exit.sh",
		Event:          event.Event{Kind: "Stop"},
		TranscriptPath: "/tmp/rec.jsonl",
		Gates:          map[string]natures.GateState{"goal-verify": {Status: natures.GateStatusFail}},
		CurrentContext: natures.ContextState{Active: true, Payload: map[string]any{"iter": 5}},
		Dir:            "/guard",
		Name:           "goal-tracking",
	}
}

// A clean exit (zero) means DONE — the context deactivates (the examples'
// convention: exit 0 = "yes, done").
func TestExitContext_CleanExitIsDone(t *testing.T) {
	r := Runner{
		runScript: func(s scriptCall) (scriptResult, error) {
			assert.Equal(t, "exit.sh", s.Script)
			return scriptResult{Passed: true}, nil
		},
	}
	done, reason, err := r.ExitContext(exitReq())
	require.NoError(t, err)
	assert.True(t, done, "a clean exit marks the context done (deactivate)")
	assert.Empty(t, reason)
}

// A NON-ZERO exit means NOT done — the context stays active for another cycle.
// And exit never refuses a Stop: done is a lifecycle flag, not a block.
func TestExitContext_NonZeroStaysActive(t *testing.T) {
	r := Runner{
		runScript: func(scriptCall) (scriptResult, error) {
			return scriptResult{Passed: false, Reason: "target not yet met"}, nil
		},
	}
	done, _, err := r.ExitContext(exitReq())
	require.NoError(t, err)
	assert.False(t, done, "a non-zero exit keeps the context active (not done)")
}

// An exit killed on its timeout never answered: it stays active AND returns its cause, so the
// Stop refuses instead of passing with the context silently open.
func TestExitContext_TimedOutReturnsTheCause(t *testing.T) {
	r := Runner{
		runScript: func(scriptCall) (scriptResult, error) {
			return scriptResult{Cause: "it was killed after 2s without answering", Unrunnable: true}, nil
		},
	}
	done, fault, err := r.ExitContext(exitReq())
	require.NoError(t, err)
	assert.False(t, done)
	assert.Contains(t, fault, "killed after 2s")
}

// exit's stdin carries ContextExitPayload — the Stop flat, currentContext, and
// gates (so a paired context reads a gate's verdict back).
func TestExitContext_PayloadShape(t *testing.T) {
	var stdin []byte
	r := Runner{
		runScript: func(s scriptCall) (scriptResult, error) {
			stdin = s.Stdin
			return scriptResult{Passed: true}, nil
		},
	}
	_, _, err := r.ExitContext(exitReq())
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(stdin, &got))
	ev, ok := got["event"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "Stop", ev["kind"])
	cc, ok := got["currentContext"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, true, cc["active"])
	gates, ok := got["gates"].(map[string]any)
	require.True(t, ok)
	gv, ok := gates["goal-verify"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "fail", gv["status"], "exit reads a gate's verdict back through gates[]")
}
