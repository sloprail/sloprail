package dispatch

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/declaration"
)

// These cover the judge check's prepare→additionalContext wiring and the payload
// a prepare receives — the parts the spec is most explicit about and the parts a
// judge template depends on. The judge substrate itself is stubbed; what is under
// test is that prepare's output reaches the judge input under `additionalContext`,
// alongside the standard payload rather than replacing it.

// A prepare's `additionalContext` reaches the judge input under
// `additionalContext`, and the standard payload is still present beside it.
func TestPrepareFeedsAdditionalContext(t *testing.T) {
	var judgeInput []byte
	r := Runner{
		skillLoaded: func(string, string) (bool, error) { return true, nil },
		runScript: func(s scriptCall) (scriptResult, error) {
			// This is the prepare step. Return the additionalContext envelope.
			assert.Equal(t, "prep.sh", s.Script)
			return scriptResult{
				Passed: true,
				Stdout: []byte(`{"additionalContext":{"doc_text":"the resolved excerpt","proof":true}}`),
			}, nil
		},
		runJudge: func(j judgeCall) (Verdict, error) {
			judgeInput = j.InputJSON
			return pass(), nil
		},
	}

	req := gateReq([]declaration.Check{{Prepare: "prep.sh", Judge: "j.md.j2"}}, nil)
	v, err := r.Run(req)
	require.NoError(t, err)
	assert.False(t, v.Refused)

	var input map[string]any
	require.NoError(t, json.Unmarshal(judgeInput, &input))
	// The standard payload's fields are present, spread flat.
	assert.Contains(t, input, "event")
	assert.Contains(t, input, "transcriptPath")
	assert.Contains(t, input, "context")
	// additionalContext is present, carrying exactly what prepare returned.
	ac, ok := input["additionalContext"].(map[string]any)
	require.True(t, ok, "additionalContext must be present when prepare returned one")
	assert.Equal(t, "the resolved excerpt", ac["doc_text"])
	assert.Equal(t, true, ac["proof"])
}

// A prepare that produces no output leaves additionalContext absent — a legitimate
// no-op, not an error, and the judge still runs against the standard payload.
func TestPrepareEmptyOutput_NoAdditionalContext(t *testing.T) {
	var judgeInput []byte
	r := Runner{
		skillLoaded: func(string, string) (bool, error) { return true, nil },
		runScript:   func(scriptCall) (scriptResult, error) { return scriptResult{Passed: true, Stdout: nil}, nil },
		runJudge: func(j judgeCall) (Verdict, error) {
			judgeInput = j.InputJSON
			return pass(), nil
		},
	}
	req := gateReq([]declaration.Check{{Prepare: "prep.sh", Judge: "j.md.j2"}}, nil)
	_, err := r.Run(req)
	require.NoError(t, err)

	var input map[string]any
	require.NoError(t, json.Unmarshal(judgeInput, &input))
	_, present := input["additionalContext"]
	assert.False(t, present, "an empty prepare adds no additionalContext")
}

// A prepare that FAILS to run fails the check closed — the judge is never asked.
func TestPrepareFailure_FailsCheckClosed(t *testing.T) {
	judgeAsked := false
	r := Runner{
		skillLoaded: func(string, string) (bool, error) { return true, nil },
		runScript: func(scriptCall) (scriptResult, error) {
			return scriptResult{Passed: false, Reason: "prepare blew up"}, nil
		},
		runJudge: func(judgeCall) (Verdict, error) {
			judgeAsked = true
			return pass(), nil
		},
	}
	req := gateReq([]declaration.Check{{Prepare: "prep.sh", Judge: "j.md.j2"}}, nil)
	v, err := r.Run(req)
	require.NoError(t, err)
	assert.True(t, v.Refused, "a prepare failure fails the check")
	assert.Contains(t, v.Reason, "prepare blew up")
	assert.False(t, judgeAsked, "the model is not asked when prepare failed")
}

// A prepare whose stdout is not the {additionalContext:{...}} shape fails closed —
// a half-prepared prompt must not reach the model.
func TestPrepareBadShape_FailsClosed(t *testing.T) {
	r := Runner{
		skillLoaded: func(string, string) (bool, error) { return true, nil },
		runScript: func(scriptCall) (scriptResult, error) {
			return scriptResult{Passed: true, Stdout: []byte(`not json at all`)}, nil
		},
		runJudge: func(judgeCall) (Verdict, error) { return pass(), nil },
	}
	req := gateReq([]declaration.Check{{Prepare: "prep.sh", Judge: "j.md.j2"}}, nil)
	v, err := r.Run(req)
	require.NoError(t, err)
	assert.True(t, v.Refused)
	assert.Contains(t, v.Reason, "additionalContext")
}

// parsePreparedContext, directly: the one supported key is read, extra keys are
// ignored, empty is a no-op, malformed is an error.
func TestParsePreparedContext(t *testing.T) {
	// The key is read; a sibling key outside additionalContext is ignored.
	ac, err := parsePreparedContext([]byte(`{"additionalContext":{"a":1},"ignored":"x"}`))
	require.NoError(t, err)
	assert.Equal(t, float64(1), ac["a"])

	// Empty stdout is no additional context, not an error.
	ac, err = parsePreparedContext([]byte("   \n "))
	require.NoError(t, err)
	assert.Nil(t, ac)

	// Present but malformed is an error the caller turns into a refusal.
	_, err = parsePreparedContext([]byte(`{oops`))
	require.Error(t, err)
}

// The prepare receives the SAME payload a script would — proven by the Nature
// selecting the gate shape on the prepare's stdin.
func TestPreparePayload_IsCheckPayload(t *testing.T) {
	var prepStdin []byte
	r := Runner{
		skillLoaded: func(string, string) (bool, error) { return true, nil },
		runScript: func(s scriptCall) (scriptResult, error) {
			prepStdin = s.Stdin
			return scriptResult{Passed: true}, nil
		},
		runJudge: func(judgeCall) (Verdict, error) { return pass(), nil },
	}
	req := gateReq([]declaration.Check{{Prepare: "prep.sh", Judge: "j.md.j2"}}, nil)
	_, err := r.Run(req)
	require.NoError(t, err)

	var payload declaration.GateCheckPayload
	require.NoError(t, json.Unmarshal(prepStdin, &payload))
	assert.Equal(t, "PreFileCreate", payload.Event.Kind)
	assert.Equal(t, "/rec.jsonl", payload.TranscriptPath)
}
