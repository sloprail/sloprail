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
		skillLoaded: func(string, string, string) (bool, error) { return true, nil },
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

// A prepare that emits `{"skip": true}` on a guard whose ONLY check is the judge:
// the write is PERMITTED and the model is NEVER invoked. The judge stub would
// REFUSE and record that it ran, so a skip that secretly still called the model
// fails this test twice over (the flag AND the verdict). Permitted because the one
// check abstained and no other check refused — the runner reaches the end of the
// chain with no refusal, which is the clean pass.
// sr:proves checks/prepare-skip-abstains
func TestPrepareSkip_OnlyCheck_PermitsWithoutModelCall(t *testing.T) {
	judgeAsked := false
	r := Runner{
		skillLoaded: func(string, string, string) (bool, error) { return true, nil },
		runScript: func(scriptCall) (scriptResult, error) {
			return scriptResult{Passed: true, Stdout: []byte(`{"skip":true}`)}, nil
		},
		runJudge: func(judgeCall) (Verdict, error) {
			judgeAsked = true
			return refuse("the model was invoked despite skip"), nil
		},
	}
	req := gateReq([]declaration.Check{{Prepare: "prep.sh", Judge: "j.md.j2"}}, nil)
	v, err := r.Run(req)
	require.NoError(t, err)
	assert.False(t, v.Refused, "a lone judge that abstained leaves nothing to refuse — the write is permitted")
	assert.False(t, judgeAsked, "the model must NOT be invoked when prepare skips the judge")
}

// runJudgeCheck returns an ABSTAIN verdict on skip — not an affirmative pass. This
// pins the distinction directly: the verdict carries Abstained, not a plain pass,
// so the check-loop can tell "reached no verdict" from "passed".
// sr:proves checks/prepare-skip-abstains
func TestPrepareSkip_RunJudgeCheckAbstains(t *testing.T) {
	r := Runner{
		skillLoaded: func(string, string, string) (bool, error) { return true, nil },
		runScript: func(scriptCall) (scriptResult, error) {
			return scriptResult{Passed: true, Stdout: []byte(`{"skip":true}`)}, nil
		},
		runJudge: func(judgeCall) (Verdict, error) { return refuse("must not run"), nil },
	}
	v, err := r.runJudgeCheck(gateReq(nil, nil), declaration.Check{Prepare: "prep.sh", Judge: "j.md.j2"})
	require.NoError(t, err)
	assert.True(t, v.Abstained, "a skip must make the judge check ABSTAIN")
	assert.False(t, v.Refused, "an abstain is not a refusal")
}

// The whole point of ABSTAIN over permit: a judge that skips, FOLLOWED BY a second
// check that REFUSES, must still refuse. If skip forced a pass this write would be
// wrongly permitted; because it abstains, the second check still runs and its
// refusal still wins. The judge model is never invoked.
// sr:proves checks/prepare-skip-abstains
func TestPrepareSkip_DoesNotMaskALaterRefusal(t *testing.T) {
	judgeAsked := false
	secondRan := false
	r := Runner{
		skillLoaded: func(string, string, string) (bool, error) { return true, nil },
		runScript: func(s scriptCall) (scriptResult, error) {
			switch s.Script {
			case "prep.sh":
				return scriptResult{Passed: true, Stdout: []byte(`{"skip":true}`)}, nil
			case "veto.sh":
				secondRan = true
				return scriptResult{Passed: false, Reason: "the second check vetoes this write"}, nil
			default:
				return scriptResult{Passed: true}, nil
			}
		},
		runJudge: func(judgeCall) (Verdict, error) {
			judgeAsked = true
			return pass(), nil
		},
	}
	// The judge (with a skipping prepare) FIRST, a refusing script check SECOND.
	req := gateReq([]declaration.Check{
		{Prepare: "prep.sh", Judge: "j.md.j2"},
		{Script: "veto.sh"},
	}, nil)
	v, err := r.Run(req)
	require.NoError(t, err)
	assert.True(t, secondRan, "the check after an abstaining judge must still run")
	assert.True(t, v.Refused, "an abstain must NOT mask a later check's refusal")
	assert.Contains(t, v.Reason, "vetoes")
	assert.False(t, judgeAsked, "the skipped judge's model is still never invoked")
}

// A judge that skips, FOLLOWED BY a second check that PASSES, permits — the abstain
// drops out and the passing check leaves the chain with no refusal.
// sr:proves checks/prepare-skip-abstains
func TestPrepareSkip_FollowedByAPass_Permits(t *testing.T) {
	secondRan := false
	r := Runner{
		skillLoaded: func(string, string, string) (bool, error) { return true, nil },
		runScript: func(s scriptCall) (scriptResult, error) {
			if s.Script == "ok.sh" {
				secondRan = true
			}
			return scriptResult{Passed: true, Stdout: []byte(`{"skip":true}`)}, nil
		},
		runJudge: func(judgeCall) (Verdict, error) { return refuse("must not run"), nil },
	}
	req := gateReq([]declaration.Check{
		{Prepare: "prep.sh", Judge: "j.md.j2"},
		{Script: "ok.sh"},
	}, nil)
	v, err := r.Run(req)
	require.NoError(t, err)
	assert.True(t, secondRan, "the check after an abstaining judge must still run")
	assert.False(t, v.Refused, "abstain + a passing check permits")
}

// skip:false is the unchanged default: the judge RUNS. Pinned so the skip signal's
// OFF state cannot drift into abstaining.
func TestPrepareSkipFalse_JudgeRuns(t *testing.T) {
	judgeAsked := false
	r := Runner{
		skillLoaded: func(string, string, string) (bool, error) { return true, nil },
		runScript: func(scriptCall) (scriptResult, error) {
			return scriptResult{Passed: true, Stdout: []byte(`{"skip":false}`)}, nil
		},
		runJudge: func(judgeCall) (Verdict, error) {
			judgeAsked = true
			return pass(), nil
		},
	}
	req := gateReq([]declaration.Check{{Prepare: "prep.sh", Judge: "j.md.j2"}}, nil)
	_, err := r.Run(req)
	require.NoError(t, err)
	assert.True(t, judgeAsked, "skip:false runs the judge — the unchanged default")
}

// A prepare that produces no output leaves additionalContext absent — a legitimate
// no-op, not an error, and the judge still runs against the standard payload.
func TestPrepareEmptyOutput_NoAdditionalContext(t *testing.T) {
	var judgeInput []byte
	r := Runner{
		skillLoaded: func(string, string, string) (bool, error) { return true, nil },
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
// sr:proves checks/check-that-cannot-answer-refuses
func TestPrepareFailure_FailsCheckClosed(t *testing.T) {
	judgeAsked := false
	r := Runner{
		skillLoaded: func(string, string, string) (bool, error) { return true, nil },
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
// sr:proves checks/check-that-cannot-answer-refuses
func TestPrepareBadShape_FailsClosed(t *testing.T) {
	r := Runner{
		skillLoaded: func(string, string, string) (bool, error) { return true, nil },
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

// parsePreparedContext, directly: both supported keys are read, unrelated keys are
// ignored, empty is a no-op (no context, no skip), malformed is an error.
func TestParsePreparedContext(t *testing.T) {
	// additionalContext is read; a sibling key outside the two of the contract is
	// ignored; skip absent parses as false (the judge-runs default).
	out, err := parsePreparedContext([]byte(`{"additionalContext":{"a":1},"ignored":"x"}`))
	require.NoError(t, err)
	assert.Equal(t, float64(1), out.AdditionalContext["a"])
	assert.False(t, out.Skip, "an absent skip is the judge-runs default")

	// skip:true is read off the same envelope as a real bool.
	out, err = parsePreparedContext([]byte(`{"skip":true}`))
	require.NoError(t, err)
	assert.True(t, out.Skip)
	assert.Nil(t, out.AdditionalContext, "a bare skip carries no additionalContext")

	// skip:false is explicitly the judge-runs default — no skip.
	out, err = parsePreparedContext([]byte(`{"skip":false}`))
	require.NoError(t, err)
	assert.False(t, out.Skip)

	// Both keys on one envelope: the skip signal and a (moot) additionalContext.
	out, err = parsePreparedContext([]byte(`{"skip":true,"additionalContext":{"a":1}}`))
	require.NoError(t, err)
	assert.True(t, out.Skip)
	assert.Equal(t, float64(1), out.AdditionalContext["a"])

	// Empty stdout is no additional context and no skip, not an error.
	out, err = parsePreparedContext([]byte("   \n "))
	require.NoError(t, err)
	assert.Nil(t, out.AdditionalContext)
	assert.False(t, out.Skip, "empty stdout is not a skip — the judge runs")

	// Present but malformed is an error the caller turns into a refusal.
	_, err = parsePreparedContext([]byte(`{oops`))
	require.Error(t, err)
}

// The prepare receives the SAME payload a script would — proven by the Nature
// selecting the gate shape on the prepare's stdin.
func TestPreparePayload_IsCheckPayload(t *testing.T) {
	var prepStdin []byte
	r := Runner{
		skillLoaded: func(string, string, string) (bool, error) { return true, nil },
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
