package e2e

import (
	"os"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// What a tool_result citation may ground in is decided by the call the result
// answers: a result whose call is not in the record is of unknown provenance,
// and TaskOutput's result is tool output only for a background Bash — for a
// background agent it is the agent's own reply. The receipts are the exact
// shapes Claude Code writes when a Bash or an Agent is run in the background;
// the mock does not run tasks in the background, so the scenario emits them.
// A scenario-emitted tool_result ends the mock's turn, so each is the last turn
// of a Run, and the session is resumed for the next step.

const bashReceipt = "Command running in background with ID: bgtask1. Output is being written to: /tmp/tasks/bgtask1.output. You will be notified when it completes."

const agentReceipt = "Async agent launched successfully. (This tool result is internal metadata — never quote or paste any part of it, including the agentId below, into a user-facing reply.)\nagentId: agtask1 (internal ID - do not mention to user.)"

func provenanceProject(t *testing.T) (*harness.Env, string) {
	t.Helper()
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.FileGuard(proj, "grounded-memories", toolResultGuard, map[string]string{"record.sh": citedRecordScript})
	commitAll(t, proj)
	return e, proj
}

// T041_28: a tool_result whose call is not in the record does not ground a
// citation — it could answer a sub-agent dispatch as easily as a command.
func TestT041_28_ResultOfUnknownProvenanceIsNotCitable(t *testing.T) {
	e, proj := provenanceProject(t)
	e.Run(proj, "s-041-28", prompt, Turns("done", harness.ToolResult("elsewhere", "ORPHAN-E2E-5521 all green")))
	if !strings.Contains(readFile(t, e.TranscriptPath(proj, "s-041-28")), "ORPHAN-E2E-5521") {
		t.Fatalf("the orphan result is not in the record, so this would not test it")
	}
	res := e.Run(proj, "s-041-28", "write it down", Turns("done",
		Bash("b1", `sr-file write memories/results.md --cite:tool_result 'ORPHAN-E2E-5521 all green' --content '# results'`),
	))
	if !res.Saw("b1") {
		t.Fatalf("the citing call never ran:\n%s", res.Output)
	}
	if e.Exists(proj, "memories/results.md") {
		t.Fatalf("a result whose call is not in the record grounded a write:\n%s", res.Output)
	}
}

// T041_29: TaskOutput reading a background Bash returns that command's output,
// which grounds a tool_result citation.
func TestT041_29_TaskOutputOfABackgroundBashIsCitable(t *testing.T) {
	e, proj := provenanceProject(t)
	launch, receipt := harness.CallWithOutput("bg1", "Bash", map[string]string{"command": "true", "run_in_background": "true"}, bashReceipt)
	read, output := harness.CallWithOutput("to1", "TaskOutput", map[string]string{"task_id": "bgtask1"}, "TASKBASH-3311 12 passed")
	e.Run(proj, "s-041-29", prompt, Turns("done", launch, receipt))
	e.Run(proj, "s-041-29", "read it", Turns("done", read, output))
	res := e.Run(proj, "s-041-29", "write it down", Turns("done",
		Bash("b1", `sr-file write memories/results.md --cite:tool_result 'TASKBASH-3311 12 passed' --content '# results'`),
	))
	if !e.Exists(proj, "memories/results.md") {
		t.Fatalf("a background Bash's output read through TaskOutput did not ground a write:\n%s", res.Output)
	}
}

// T041_30: TaskOutput reading a background agent returns the agent's reply —
// model-written text — which does not ground a tool_result citation; nor does
// a TaskOutput whose task nothing in the record launched.
func TestT041_30_TaskOutputOfABackgroundAgentIsNotCitable(t *testing.T) {
	e, proj := provenanceProject(t)
	noop := subagentScript(t, harness.Turns("launched"))
	launch, receipt := harness.CallWithOutput("ag1", "Agent",
		map[string]string{"prompt": "run the suite", "description": "background", "script": noop, "run_in_background": "true"}, agentReceipt)
	read, output := harness.CallWithOutput("to1", "TaskOutput", map[string]string{"task_id": "agtask1"}, "TASKAGENT-7702 all 40 tests pass")
	stray, strayOut := harness.CallWithOutput("to2", "TaskOutput", map[string]string{"task_id": "never-launched"}, "TASKSTRAY-1188 all green")
	e.Run(proj, "s-041-30", prompt, Turns("done", launch, receipt))
	e.Run(proj, "s-041-30", "read it", Turns("done", read, output))
	e.Run(proj, "s-041-30", "read the other", Turns("done", stray, strayOut))
	record := readFile(t, e.TranscriptPath(proj, "s-041-30"))
	for _, want := range []string{"agentId: agtask1", "TASKAGENT-7702", "TASKSTRAY-1188"} {
		if !strings.Contains(record, want) {
			t.Fatalf("%q is not in the record, so this would not test it", want)
		}
	}
	res := e.Run(proj, "s-041-30", "write them down", Turns("done",
		Bash("b1", `sr-file write memories/agent.md --cite:tool_result 'TASKAGENT-7702 all 40 tests pass' --content '# results'`),
		Bash("b2", `sr-file write memories/stray.md --cite:tool_result 'TASKSTRAY-1188 all green' --content '# results'`),
	))
	if !res.Saw("b2") {
		t.Fatalf("the citing calls never ran:\n%s", res.Output)
	}
	if e.Exists(proj, "memories/agent.md") {
		t.Errorf("a background agent's reply read through TaskOutput grounded a write:\n%s", res.Output)
	}
	if e.Exists(proj, "memories/stray.md") {
		t.Errorf("TaskOutput of a task nothing launched grounded a write:\n%s", res.Output)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// T041_31: a sub-agent's --cite:user of its own dispatch prompt is refused up
// front — with no rule requiring a citation at all — and told why: the Bash it
// runs cannot tell sr-file it is a sub-agent, the hook can. The root's own
// sr-file failing the same way is left to say its own words.
func TestT041_31_ASubagentIsToldWhyItsUserQuoteFails(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)

	sub := subagentScript(t, harness.Turns("sub done",
		Bash("sb1", `sr-file write notes.md --cite:user 'measure the retry budget' --content '# notes'`),
	))
	e.Run(proj, "s-041-31", prompt, Turns("done",
		harness.Dispatch("d1", "measure the retry budget", sub, ""),
		Bash("b1", `sr-file write root-notes.md --cite:user 'never said by anyone' --content '# notes'`),
	))
	if e.Exists(proj, "notes.md") || e.Exists(proj, "root-notes.md") {
		t.Fatalf("a write citing words the user never said landed")
	}
	record := readFile(t, e.TranscriptPath(proj, "s-041-31"))
	var subResult, rootResult string
	for _, l := range strings.Split(record, "\n") {
		switch {
		case strings.Contains(l, `"tool_use_id":"sb1`):
			subResult = l
		case strings.Contains(l, `"tool_use_id":"b1`):
			rootResult = l
		}
	}
	for _, want := range []string{"hook", "That quote is from your dispatch prompt, written by the parent agent.", "You are a sub-agent: your prompt is the parent agent's, not the user's."} {
		if !strings.Contains(subResult, want) {
			t.Errorf("the sub-agent's refusal does not say %q:\n%s", want, subResult)
		}
	}
	if !strings.Contains(rootResult, "does not resolve") || strings.Contains(rootResult, "sub-agent") || strings.Contains(rootResult, "hook") {
		t.Errorf("the root's own failed citation was not left to sr-file's own words:\n%s", rootResult)
	}
}
