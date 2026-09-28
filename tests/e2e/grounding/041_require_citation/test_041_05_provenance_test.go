package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// What a tool_result citation may ground in is decided by the call the result
// answers: a result whose call is not in the record is of unknown provenance,
// and a background agent's reply is model-written wherever it arrives. The mock
// runs Bash and Agent calls in the background the way Claude Code does — the
// receipt naming the task and its output file, the <task-notification> when it
// finishes — so these drive the real shapes rather than emitting them.
//
// The mock no longer answers TaskOutput (real transcripts hold no call to it —
// harness-mocks EVIDENCE.md), so the mock-driven cases read a background
// command's output the way a real agent does: with Read, on the output file
// its receipt names. Production still classifies a TaskOutput result
// (internal/transcript/provenance.go, taskReaders), so its own two cases stay
// covered too — emitted by the scenario (harness.CallWithOutput), the same way
// the "task nothing launched" case always was, since the mock cannot produce a
// tool it no longer implements.

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

// T041_29: a background Bash's output, read from the output file its receipt
// names, is the Read tool's output and grounds a tool_result citation.
func TestT041_29_ABackgroundBashsOutputReadFromItsFileIsCitable(t *testing.T) {
	e, proj := provenanceProject(t)
	e.Run(proj, "s-041-29", prompt, Turns("done",
		harness.Background("bg1", "Bash", map[string]string{"command": "echo 'TASKBASH-3311 12 passed'", "description": "run the suite"}),
		harness.ReadLaunchedOutput("r1"),
	))
	record := readFile(t, e.TranscriptPath(proj, "s-041-29"))
	for _, want := range []string{"Command running in background with ID: ", "TASKBASH-3311 12 passed"} {
		if !strings.Contains(record, want) {
			t.Fatalf("%q is not in the record, so this would not test it", want)
		}
	}
	res := e.Run(proj, "s-041-29", "write it down", Turns("done",
		Bash("b1", `sr-file write memories/results.md --cite:tool_result 'TASKBASH-3311 12 passed' --content '# results'`),
	))
	if !e.Exists(proj, "memories/results.md") {
		t.Fatalf("a background Bash's output read from its output file did not ground a write:\n%s", res.Output)
	}
}

// T041_29B: production still classifies a TaskOutput result by what launched
// its task (internal/transcript/provenance.go, taskReaders) — a background
// Bash's is genuine tool output, citable the same as T041_29's file read. The
// mock no longer implements TaskOutput as a tool, so the call and its result
// are emitted by the scenario (harness.TaskOutputOfLaunched) against the real
// task id the mock's own background-launch receipt named, rather than driven
// through the mock or made up.
func TestT041_29B_TaskOutputOfABackgroundBashIsStillCitable(t *testing.T) {
	e, proj := provenanceProject(t)
	call, output := harness.TaskOutputOfLaunched("to1", "TASKOUT-6621 12 passed")
	e.Run(proj, "s-041-29b", prompt, Turns("done",
		harness.Background("bg1", "Bash", map[string]string{"command": "echo hi", "description": "run the suite"}),
		call, output,
	))
	record := readFile(t, e.TranscriptPath(proj, "s-041-29b"))
	for _, want := range []string{"Command running in background with ID: ", "TASKOUT-6621"} {
		if !strings.Contains(record, want) {
			t.Fatalf("%q is not in the record, so this would not test it", want)
		}
	}
	res := e.Run(proj, "s-041-29b", "write it down", Turns("done",
		Bash("b1", `sr-file write memories/results.md --cite:tool_result 'TASKOUT-6621 12 passed' --content '# results'`),
	))
	if !e.Exists(proj, "memories/results.md") {
		t.Fatalf("a TaskOutput result classified as a background Bash's output did not ground a write:\n%s", res.Output)
	}
}

// T041_45: a tool that reads an agent's transcript — here a Read of a
// sub-agent-shaped record, and a cat of a background agent's tasks/<id>.output
// link to it — returns model-written text, which does not ground a tool_result
// citation; the failure says the file is an agent transcript. An ordinary file
// read the same way does ground one.
func TestT041_45_AnAgentTranscriptReadBackIsNotToolOutput(t *testing.T) {
	e, proj := provenanceProject(t)
	elsewhere := t.TempDir()
	record := filepath.Join(elsewhere, "agent-bg1.jsonl")
	if err := os.WriteFile(record, []byte(agentRecordText("AGENTTEXT-5150 all 40 tests pass")), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(elsewhere, "tasks", "bg1.output")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(record, link); err != nil {
		t.Fatal(err)
	}
	e.WriteFile(proj, "plain.txt", "PLAINTEXT-5151 ok\n")

	res := e.Run(proj, "s-041-45", prompt, Turns("done",
		harness.ToolUse("r1", "Read", map[string]string{"file_path": record}),
		Bash("c1", "cat "+link),
		harness.ToolUse("r2", "Read", map[string]string{"file_path": filepath.Join(proj, "plain.txt")}),
		Bash("b1", `sr-file write memories/read.md --cite:tool_result 'AGENTTEXT-5150 all 40 tests pass' --content '# x'`),
		Bash("b2", `sr-file write memories/plain.md --cite:tool_result 'PLAINTEXT-5151 ok' --content '# y'`),
	))
	if !res.Saw("b2") {
		t.Fatalf("the citing calls never ran:\n%s", res.Output)
	}
	if e.Exists(proj, "memories/read.md") {
		t.Errorf("an agent transcript read back grounded a write:\n%s", res.Output)
	}
	if !res.Saw("agent transcript") {
		t.Errorf("the failed citation does not say the file is an agent transcript:\n%s", res.Output)
	}
	if !e.Exists(proj, "memories/plain.md") {
		t.Errorf("an ordinary file read did not ground a write:\n%s", res.Output)
	}
}

// T041_30: a background agent's reply — model-written text — arrives in the
// <task-notification> that hands the finished task back (its <result>). That is
// a harness-injected user turn, not a tool's output, and does not ground a
// tool_result citation.
func TestT041_30_ABackgroundAgentsReplyIsNotCitable(t *testing.T) {
	e, proj := provenanceProject(t)
	reply := subagentScript(t, harness.Turns("TASKAGENT-7702 all 40 tests pass"))
	e.Run(proj, "s-041-30", prompt, Turns("done",
		harness.Background("ag1", "Agent", map[string]string{"prompt": "run the suite", "description": "background", "script": reply}),
	))
	record := readFile(t, e.TranscriptPath(proj, "s-041-30"))
	for _, want := range []string{"Async agent launched successfully.", "<task-notification>", "TASKAGENT-7702"} {
		if !strings.Contains(record, want) {
			t.Fatalf("%q is not in the record, so this would not test it", want)
		}
	}
	res := e.Run(proj, "s-041-30", "write it down", Turns("done",
		Bash("b1", `sr-file write memories/agent.md --cite:tool_result 'TASKAGENT-7702 all 40 tests pass' --content '# results'`),
	))
	if !res.Saw("b1") {
		t.Fatalf("the citing call never ran:\n%s", res.Output)
	}
	if e.Exists(proj, "memories/agent.md") {
		t.Errorf("a background agent's reply grounded a write:\n%s", res.Output)
	}
}

// T041_30B: production classifies a TaskOutput result as a background AGENT's
// reply only when the record shows an Agent launched that task id — model-
// written text, so still not citable — and refuses to classify a TaskOutput
// whose task nothing in the record launched at all (unknown provenance, same
// as T041_28). Both emitted by the scenario: the agent case against the real
// task id the mock's own launch receipt named (harness.TaskOutputOfLaunched),
// the stray case against one nothing ever launched (harness.CallWithOutput).
func TestT041_30B_TaskOutputOfABackgroundAgentAndOfAnUnlaunchedTaskAreNotCitable(t *testing.T) {
	e, proj := provenanceProject(t)
	reply := subagentScript(t, harness.Turns("TASKAGENT-8813 all 40 tests pass"))
	agentCall, agentOut := harness.TaskOutputOfLaunched("to1", "TASKAGENT-8813 all 40 tests pass")
	strayCall, strayOut := harness.CallWithOutput("to2", "TaskOutput", map[string]string{"task_id": "never-launched"}, "TASKSTRAY-9924 all green")
	e.Run(proj, "s-041-30b", prompt, Turns("done",
		harness.Background("ag1", "Agent", map[string]string{"prompt": "run the suite", "description": "background", "script": reply}),
	))
	e.Run(proj, "s-041-30b", "read the agent's task", Turns("done", agentCall, agentOut))
	e.Run(proj, "s-041-30b", "read the other", Turns("done", strayCall, strayOut))
	record := readFile(t, e.TranscriptPath(proj, "s-041-30b"))
	for _, want := range []string{"Async agent launched successfully.", "TASKAGENT-8813", "TASKSTRAY-9924"} {
		if !strings.Contains(record, want) {
			t.Fatalf("%q is not in the record, so this would not test it", want)
		}
	}
	res := e.Run(proj, "s-041-30b", "write them down", Turns("done",
		Bash("b1", `sr-file write memories/agent.md --cite:tool_result 'TASKAGENT-8813 all 40 tests pass' --content '# results'`),
		Bash("b2", `sr-file write memories/stray.md --cite:tool_result 'TASKSTRAY-9924 all green' --content '# results'`),
	))
	if !res.Saw("b2") {
		t.Fatalf("the citing calls never ran:\n%s", res.Output)
	}
	if e.Exists(proj, "memories/agent.md") {
		t.Errorf("a TaskOutput result classified as a background agent's reply grounded a write:\n%s", res.Output)
	}
	if e.Exists(proj, "memories/stray.md") {
		t.Errorf("a TaskOutput of a task nothing launched grounded a write:\n%s", res.Output)
	}
}

// T041_32: an AskUserQuestion answer is the user's words, never a tool's
// output. The answer sits in a tool_result block answering the AskUserQuestion
// call, so only the answer-envelope check keeps it out of the tool-output pool:
// it grounds a --cite:user, and does not ground a --cite:tool_result.
func TestT041_32_AnAnswerIsNotToolOutput(t *testing.T) {
	e, proj := provenanceProject(t)
	ask, answer := harness.AskUserQuestion("q1", "which retry budget?", "ANSWER-E2E-6120 five retries")
	e.Run(proj, "s-041-32", prompt, Turns("done", ask, answer))
	record := readFile(t, e.TranscriptPath(proj, "s-041-32"))
	for _, want := range []string{`"name":"AskUserQuestion"`, "ANSWER-E2E-6120"} {
		if !strings.Contains(record, want) {
			t.Fatalf("%q is not in the record, so this would not test it", want)
		}
	}
	res := e.Run(proj, "s-041-32", "write it down", Turns("done",
		Bash("b1", `sr-file write memories/budget.md --cite:tool_result 'ANSWER-E2E-6120 five retries' --content '# budget'`),
		Bash("b2", `sr-file write notes/budget.md --cite:user 'ANSWER-E2E-6120 five retries' --content '# budget'`),
	))
	if !res.Saw("b2") {
		t.Fatalf("the citing calls never ran:\n%s", res.Output)
	}
	if e.Exists(proj, "memories/budget.md") {
		t.Errorf("the user's answer grounded a --cite:tool_result write:\n%s", res.Output)
	}
	if !e.Exists(proj, "notes/budget.md") {
		t.Errorf("the user's answer did not ground a --cite:user write:\n%s", res.Output)
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
	// The sub-agent's own call and its result are in its own record, the root's
	// in the root's.
	record := readFile(t, e.TranscriptPath(proj, "s-041-31"))
	for _, sub := range e.SubagentRecordPaths(proj, "s-041-31") {
		record += "\n" + readFile(t, sub)
	}
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

// agentRecordText is a sub-agent's record as the harness writes one.
func agentRecordText(said string) string {
	return `{"parentUuid":null,"isSidechain":true,"userType":"external","cwd":"/w","sessionId":"sess-bg","version":"2.1.282","type":"user","uuid":"s0","message":{"role":"user","content":"go"}}` + "\n" +
		`{"parentUuid":"s0","isSidechain":true,"userType":"external","cwd":"/w","sessionId":"sess-bg","version":"2.1.282","type":"assistant","uuid":"s1","message":{"role":"assistant","content":[{"type":"text","text":"` + said + `"}]}}` + "\n"
}

// T041_48 (P14, P12): an agent transcript read back is recognised by its
// text — a cat of a record the command then deletes, a cat by a relative path
// — and does not ground a tool_result citation. Output that merely mentions the
// projects directory in a comment, or an ordinary JSON-lines data file, does.
func TestT041_48_ATranscriptReadBackIsRecognisedByItsText(t *testing.T) {
	e, proj := provenanceProject(t)
	elsewhere := t.TempDir()
	gone := filepath.Join(elsewhere, "agent.jsonl")
	if err := os.WriteFile(gone, []byte(agentRecordText("GONETEXT-6601 all green")), 0o644); err != nil {
		t.Fatal(err)
	}
	e.WriteFile(proj, "rel-agent.jsonl", agentRecordText("RELTEXT-6602 all green"))
	e.WriteFile(proj, "data.jsonl", `{"uuid":"d1","type":"user","message":{"n":1},"value":"DATAVALUE-6603"}`+"\n")

	res := e.Run(proj, "s-041-48", prompt, Turns("done",
		Bash("c1", "cat "+gone+" && rm "+gone),
		Bash("c2", "cat rel-agent.jsonl"),
		Bash("c3", "cat data.jsonl"),
		Bash("c4", "echo BUILD-OK-5512 # not reading "+filepath.Join(e.ConfigDir(), "projects")),
		Bash("b1", `sr-file write memories/gone.md --cite:tool_result 'GONETEXT-6601 all green' --content '# x'`),
		Bash("b2", `sr-file write memories/rel.md --cite:tool_result 'RELTEXT-6602 all green' --content '# x'`),
		Bash("b3", `sr-file write memories/data.md --cite:tool_result 'DATAVALUE-6603' --content '# x'`),
		Bash("b4", `sr-file write memories/echo.md --cite:tool_result 'BUILD-OK-5512' --content '# x'`),
	))
	if !res.Saw("b4") {
		t.Fatalf("the citing calls never ran:\n%s", res.Output)
	}
	for _, f := range []string{"memories/gone.md", "memories/rel.md"} {
		if e.Exists(proj, f) {
			t.Errorf("%s: an agent transcript read back grounded a write:\n%s", f, res.Output)
		}
	}
	for _, f := range []string{"memories/data.md", "memories/echo.md"} {
		if !e.Exists(proj, f) {
			t.Errorf("%s: ordinary tool output did not ground a write:\n%s", f, res.Output)
		}
	}
}
