package dispatch

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/declaration"
	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/natures"
)

// The check-runner is the reusable core every nature decides through, so these
// tests exercise it in ISOLATION — the trajectory reader, the script executor and
// the judge substrate are all substituted, so no transcript file, no subprocess
// and no model call is involved. What is under test is the runner's own logic:
// require-then-checks ordering, first-refusal-ends-it, payload assembly to the
// spec's exact shape, and the two prerequisite kinds. The end-to-end path (real
// scripts, a real judge through the mock, the whole thing wired into a hook) is
// the e2e suite's job.

// gateReq is a minimal gate Request with the collaborators left to the caller to
// override. Kept as a helper so each test states only what it varies.
func gateReq(checks []declaration.Check, require []declaration.Prerequisite) Request {
	return Request{
		Nature:         NatureGate,
		Require:        require,
		Checks:         checks,
		Event:          event.Event{Kind: "PreFileCreate", Fields: map[string]any{"path": "memories/topics/x.md"}},
		TranscriptPath: "/rec.jsonl",
		Dir:            "/guard",
		GuardName:      "the-gate",
	}
}

// script and judge builders for the check list.
func scriptCheck(name string) declaration.Check { return declaration.Check{Script: name} }
func judgeCheck(name string) declaration.Check  { return declaration.Check{Judge: name} }

// recordingRunner captures which scripts/judges ran, and lets a test dictate each
// one's verdict, so ordering and first-refusal are observable.
type recordingRunner struct {
	scriptOrder []string
	judgeOrder  []string
	scriptPass  map[string]bool // by script path; absent means pass
	judgePass   map[string]bool // by template path; absent means pass
	scriptOut   map[string][]byte
}

func (rr *recordingRunner) runner() Runner {
	return Runner{
		skillLoaded: func(string, string) (bool, error) { return true, nil },
		runScript: func(s scriptCall) (scriptResult, error) {
			rr.scriptOrder = append(rr.scriptOrder, s.Script)
			passed := true
			if rr.scriptPass != nil {
				if v, ok := rr.scriptPass[s.Script]; ok {
					passed = v
				}
			}
			out := rr.scriptOut[s.Script]
			if passed {
				return scriptResult{Passed: true, Stdout: out}, nil
			}
			return scriptResult{Passed: false, Reason: s.Script + " refused", Stdout: out}, nil
		},
		runJudge: func(j judgeCall) (Verdict, error) {
			rr.judgeOrder = append(rr.judgeOrder, j.Template)
			passed := true
			if rr.judgePass != nil {
				if v, ok := rr.judgePass[j.Template]; ok {
					passed = v
				}
			}
			if passed {
				return pass(), nil
			}
			return refuse(j.Template + " judged fail"), nil
		},
	}
}

// A pure-checks gate that passes every check admits.
func TestRun_AllChecksPass_Admits(t *testing.T) {
	rr := &recordingRunner{}
	v, err := rr.runner().Run(gateReq([]declaration.Check{scriptCheck("a.sh"), scriptCheck("b.sh")}, nil))
	require.NoError(t, err)
	assert.False(t, v.Refused, "every check passed, so the gate admits")
	assert.Equal(t, []string{"a.sh", "b.sh"}, rr.scriptOrder, "checks run in declaration order")
}

// The first refusing check ends it — later checks do not run.
func TestRun_FirstRefusalEndsIt(t *testing.T) {
	rr := &recordingRunner{scriptPass: map[string]bool{"b.sh": false}}
	v, err := rr.runner().Run(gateReq(
		[]declaration.Check{scriptCheck("a.sh"), scriptCheck("b.sh"), scriptCheck("c.sh")}, nil))
	require.NoError(t, err)
	assert.True(t, v.Refused)
	assert.Contains(t, v.Reason, "b.sh refused")
	assert.Equal(t, []string{"a.sh", "b.sh"}, rr.scriptOrder, "the check after the refusing one does not run")
}

// A mixed script+judge list runs in order; a judge refusal is a refusal.
func TestRun_MixedScriptAndJudge_OrderAndVerdict(t *testing.T) {
	rr := &recordingRunner{judgePass: map[string]bool{"j.md.j2": false}}
	v, err := rr.runner().Run(gateReq(
		[]declaration.Check{scriptCheck("a.sh"), judgeCheck("j.md.j2"), scriptCheck("z.sh")}, nil))
	require.NoError(t, err)
	assert.True(t, v.Refused)
	assert.Contains(t, v.Reason, "j.md.j2 judged fail")
	assert.Equal(t, []string{"a.sh"}, rr.scriptOrder)
	assert.Equal(t, []string{"j.md.j2"}, rr.judgeOrder, "the judge ran")
	assert.NotContains(t, rr.scriptOrder, "z.sh", "the check after the failing judge does not run")
}

// require is evaluated BEFORE any check — a missing prerequisite refuses without
// paying for a check.
func TestRun_RequireRefusesBeforeChecks(t *testing.T) {
	rr := &recordingRunner{}
	r := rr.runner()
	// A skill require that is not met.
	r.skillLoaded = func(string, string) (bool, error) { return false, nil }
	v, err := r.Run(gateReq(
		[]declaration.Check{scriptCheck("a.sh")},
		[]declaration.Prerequisite{{Skill: "document-topic"}}))
	require.NoError(t, err)
	assert.True(t, v.Refused)
	assert.Contains(t, v.Reason, "document-topic")
	assert.Empty(t, rr.scriptOrder, "no check runs when a prerequisite fails")
}

// A pure-require gate (no checks) that is met admits.
func TestRun_PureRequireMet_Admits(t *testing.T) {
	rr := &recordingRunner{}
	v, err := rr.runner().Run(gateReq(nil, []declaration.Prerequisite{{Skill: "document-topic"}}))
	require.NoError(t, err)
	assert.False(t, v.Refused)
}

// -- require: skill --

func TestCheckSkill_LoadedAdmits_MissingRefuses(t *testing.T) {
	// Loaded.
	r := Runner{skillLoaded: func(_ string, s string) (bool, error) {
		return s == "document-topic", nil
	}}
	v, err := r.Run(gateReq(nil, []declaration.Prerequisite{{Skill: "document-topic"}}))
	require.NoError(t, err)
	assert.False(t, v.Refused)

	// Missing — refuses with the remedy naming the skill and that the record, not a
	// claim, is what is checked.
	v, err = r.Run(gateReq(nil, []declaration.Prerequisite{{Skill: "some-other"}}))
	require.NoError(t, err)
	assert.True(t, v.Refused)
	assert.Contains(t, v.Reason, "some-other")
	assert.Contains(t, strings.ToLower(v.Reason), "record")
}

// A skill require fails CLOSED when the transcript cannot be read — a precondition
// that could not be checked is not one that passed.
func TestCheckSkill_UnreadableTranscript_FailsClosed(t *testing.T) {
	r := Runner{skillLoaded: func(string, string) (bool, error) {
		return false, assertAnError{}
	}}
	v, err := r.Run(gateReq(nil, []declaration.Prerequisite{{Skill: "document-topic"}}))
	require.NoError(t, err)
	assert.True(t, v.Refused, "an unreadable record refuses rather than admits")
	assert.Contains(t, v.Reason, "document-topic")
}

// A skill require fails CLOSED when no transcript path was resolved at all.
func TestCheckSkill_NoTranscriptPath_FailsClosed(t *testing.T) {
	req := gateReq(nil, []declaration.Prerequisite{{Skill: "document-topic"}})
	req.TranscriptPath = ""
	v, err := (Runner{}).Run(req)
	require.NoError(t, err)
	assert.True(t, v.Refused)
	assert.Contains(t, strings.ToLower(v.Reason), "could not be located")
}

// -- require: context --

func TestCheckContext_ActiveAdmits_InactiveRefuses(t *testing.T) {
	req := gateReq(nil, []declaration.Prerequisite{{Context: "research-run"}})

	// Active in the map: admits.
	req.Context = map[string]natures.ContextState{"research-run": {Active: true}}
	v, err := (Runner{}).Run(req)
	require.NoError(t, err)
	assert.False(t, v.Refused)

	// Present but inactive: refuses.
	req.Context = map[string]natures.ContextState{"research-run": {Active: false}}
	v, err = (Runner{}).Run(req)
	require.NoError(t, err)
	assert.True(t, v.Refused)
	assert.Contains(t, v.Reason, "research-run")

	// Absent entirely: refuses (the context never entered).
	req.Context = nil
	v, err = (Runner{}).Run(req)
	require.NoError(t, err)
	assert.True(t, v.Refused)
}

// -- payload assembly --

// A gate's script receives GateCheckPayload: event, transcriptPath, context — at
// the spec's exact shape, context always an object.
func TestScriptPayload_GateShape(t *testing.T) {
	var captured []byte
	r := Runner{
		skillLoaded: func(string, string) (bool, error) { return true, nil },
		runScript: func(s scriptCall) (scriptResult, error) {
			captured = s.Stdin
			return scriptResult{Passed: true}, nil
		},
	}
	req := gateReq([]declaration.Check{scriptCheck("a.sh")}, nil)
	req.Context = map[string]natures.ContextState{"research-run": {Active: true, Payload: map[string]any{"depth": "3"}}}
	_, err := r.Run(req)
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(captured, &payload))
	// The three declared keys are present.
	assert.Contains(t, payload, "event")
	assert.Contains(t, payload, "transcriptPath")
	assert.Contains(t, payload, "context")
	assert.Equal(t, "/rec.jsonl", payload["transcriptPath"])
	// event carries the fired event's kind AND its fields FLAT — a gate script reads
	// `.event.kind` and `.event.path`, never `.event.fields.path`.
	ev := payload["event"].(map[string]any)
	assert.Equal(t, "PreFileCreate", ev["kind"])
	assert.Equal(t, "memories/topics/x.md", ev["path"], ".event.path must be flat")
	assert.NotContains(t, ev, "fields", "the event must be flat, not the nested {kind, fields} envelope")
	// context is the state map by name, carrying {active, payload}.
	ctx := payload["context"].(map[string]any)
	rr := ctx["research-run"].(map[string]any)
	assert.Equal(t, true, rr["active"])
	// No `gates` on a gate's check payload — the spec gives it none.
	assert.NotContains(t, payload, "gates")
}

// context is always an object on the wire, even with no contexts declared — a
// script indexing it must not meet a null.
func TestScriptPayload_ContextAlwaysObject(t *testing.T) {
	var captured []byte
	r := Runner{
		skillLoaded: func(string, string) (bool, error) { return true, nil },
		runScript: func(s scriptCall) (scriptResult, error) {
			captured = s.Stdin
			return scriptResult{Passed: true}, nil
		},
	}
	req := gateReq([]declaration.Check{scriptCheck("a.sh")}, nil)
	req.Context = nil
	_, err := r.Run(req)
	require.NoError(t, err)
	assert.Contains(t, string(captured), `"context":{}`)
}

// A file-guard's check receives CheckPayload (same fields, file event). The Nature
// selects the shape; this pins that a file-guard assembles its own.
func TestScriptPayload_FileGuardShape(t *testing.T) {
	var captured []byte
	r := Runner{
		skillLoaded: func(string, string) (bool, error) { return true, nil },
		runScript: func(s scriptCall) (scriptResult, error) {
			captured = s.Stdin
			return scriptResult{Passed: true}, nil
		},
	}
	req := Request{
		Nature:         NatureFileGuard,
		Checks:         []declaration.Check{scriptCheck("a.sh")},
		Event:          event.Event{Kind: "PostFileUpdate", Fields: map[string]any{"path": "a.md", "newContent": "hi"}},
		TranscriptPath: "/rec.jsonl",
		Dir:            "/guard",
	}
	_, err := r.Run(req)
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(captured, &payload))
	// A file-guard's script reads the event FLAT: `.event.newContent`, `.event.path`,
	// `.event.kind` — the shape every file-guard example script reads, NOT
	// `.event.fields.newContent`.
	ev := payload["event"].(map[string]any)
	assert.Equal(t, "PostFileUpdate", ev["kind"])
	assert.Equal(t, "hi", ev["newContent"], ".event.newContent must be reachable flat")
	assert.Equal(t, "a.md", ev["path"], ".event.path must be reachable flat")
	assert.NotContains(t, ev, "fields", "the event must be flat, not nested under `fields`")
}

// assertAnError is a stand-in error for the fail-closed transcript test.
type assertAnError struct{}

func (assertAnError) Error() string { return "record unreadable" }
