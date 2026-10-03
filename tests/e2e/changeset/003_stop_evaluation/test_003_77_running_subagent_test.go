package e2e

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// appendRecord adds lines to a session's transcript, as the harness would write them.
func appendRecord(t *testing.T, path string, lines ...string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(strings.Join(lines, "\n") + "\n"); err != nil {
		t.Fatal(err)
	}
}

// T003_77: the parent's Stop does not judge the ranges of a background sub-agent that is still
// running, says so without refusing, and judges them at the first Stop after the agent's
// terminal notification. The agent is held as running by the sub-agent registry (SubagentStart, and the
// launch in the record), not by the record alone.
//
// The claude-mock runs a background Agent concurrently and writes its receipt and
// notification, but the parent's Stop hook output is not observable mid-run (only its
// refusals reach the record), and the sub-agent's own refused Stop keeps it running well
// past the notification. So the work is committed in the mock's foreground dispatch and the
// parent's record then gets the lines a background launch and its notification write (the
// shapes harness-mocks pins: an "Async agent launched" receipt, a queued_command
// attachment with commandMode task-notification), and the Stop is run directly. Missing in
// the mock: a way to read a Stop hook's non-blocking output (systemMessage) from a run.
func TestT003_77_TheParentsStopLeavesARunningBackgroundAgentsRangeForLater(t *testing.T) {
	e, proj, _ := uncitedProject(t, citingRule)
	const sess = "s-003-77"
	sub := harness.SubagentScript(t, Turns("sub done",
		harness.CommitFile("c1", "docs/release.md", "the steps", "document the release"),
	))
	e.Run(proj, sess, handbackPrompt, Turns("root done", harness.Dispatch("d1", "write the docs", sub, "worktree")))
	_, agent := subagentFolder(t, e, proj, sess)
	record := e.TranscriptPath(proj, sess)
	// The agent's branch is tracked once, from its worktree and with its agent_id: a branch
	// checked out in another worktree is never the root folder's row.
	var rows int
	for _, r := range e.SessionRanges(proj, sess) {
		if strings.HasPrefix(r.Head, "worktree-agent-") {
			rows++
		}
	}
	if rows != 1 {
		t.Fatalf("premise: the agent's branch should be tracked once, from its worktree, have %d rows", rows)
	}

	// Premise: with nothing marking the agent as running, the parent's Stop judges its range.
	if r := e.StopNow(proj, sess, false); !harness.Blocked(r) || !strings.Contains(r.Output, "docs/release.md") {
		t.Fatalf("premise: the agent's uncited range is refused at the parent's Stop:\n%s", r.Output)
	}

	// The registry holds the agent as completed (its SubagentStop ended the mock's run). The dispatcher
	// resumes it, as SendMessage does: the harness sends SubagentStart again, so it runs, and the
	// record shows it launched in the background and not yet reported.
	start, _ := json.Marshal(map[string]any{
		"session_id": sess, "transcript_path": record, "cwd": proj,
		"hook_event_name": "SubagentStart", "agent_id": agent, "agent_type": "general-purpose",
	})
	if r := e.CLIDirectStdinEnv(proj, string(start), e.SessionEnv(""), "sr-session", "subagent-start"); r.Code != 0 {
		t.Fatalf("subagent-start failed: exit %d\n%s", r.Code, r.Output)
	}
	appendRecord(t, record,
		`{"type":"assistant","uuid":"bg-a","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_bg","name":"Agent","input":{"prompt":"go","run_in_background":true}}]}}`,
		`{"type":"user","uuid":"bg-r","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_bg","content":"Async agent launched successfully.\nagentId: `+agent+` (internal ID)"}]}}`,
	)
	r := e.StopNow(proj, sess, false)
	if harness.Blocked(r) {
		t.Fatalf("the parent's Stop was refused for a still-running agent's range:\n%s", r.Output)
	}
	if strings.Contains(r.Output, "not judged yet") || strings.Contains(r.Output, "still running") {
		t.Fatalf("the Stop reported the running agent's range, which is no failure:\n%s", r.Output)
	}

	// After the terminal notification the range is judged again.
	appendRecord(t, record,
		`{"type":"attachment","uuid":"bg-n","attachment":{"type":"queued_command","commandMode":"task-notification","prompt":"<task-notification>\n<task-id>`+agent+`</task-id>\n<status>killed</status>\n</task-notification>"}}`,
	)
	if r := e.StopNow(proj, sess, false); !harness.Blocked(r) || !strings.Contains(r.Output, "docs/release.md") {
		t.Fatalf("the agent's range was not judged after its terminal notification:\n%s", r.Output)
	}
}
