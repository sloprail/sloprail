package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T002_09: uncommitted guarded work in a folder where a still-running BACKGROUND sub-agent works
// is that agent's half-done work, which the root must not commit: the root's Stop does not refuse
// it (it names the agent), the same policy the tracked ranges follow. Fail closed: a foreground
// agent, one the registry does not hold as running, one that stopped, or a folder the agent never
// worked in are refused as ever; the root's own uncommitted work beside it is refused too.

func appendLines(t *testing.T, path string, lines ...string) {
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

func startAgent(t *testing.T, e *Env, proj, sess, record, sub string) {
	t.Helper()
	payload, _ := json.Marshal(map[string]any{
		"session_id": sess, "transcript_path": record, "cwd": proj,
		"hook_event_name": "SubagentStart", "agent_id": sub, "agent_type": "general-purpose",
	})
	if r := e.CLIDirectStdinEnv(proj, string(payload), e.SessionEnv(""), "sr-session", "subagent-start"); r.Code != 0 {
		t.Fatalf("subagent-start failed: exit %d\n%s", r.Code, r.Output)
	}
}

// busyFolder stages a root session whose registered folder `other` holds an uncommitted guarded
// file a sub-agent wrote, and returns the sub-agent's id.
func busyFolder(t *testing.T, sess string) (e *Env, proj, other, record, agent string) {
	t.Helper()
	e, proj = project(t)
	other = e.Project()
	e.GitInit(other)
	e.WriteFile(other, "docs/seed.md", "seed\n")
	e.FileGuard(other, "docs", rule, map[string]string{"check.sh": passing})
	e.CommitAll(other, "the other project and its rule")
	wip := filepath.Join(other, "docs", "wip.md")
	sub := harness.SubagentScript(t, Turns("sub done", Write("sb1", wip, "half-resolved merge\n")))
	e.Run(proj, sess, "delegate", Turns("root done",
		// The root registers `other` as its own folder by moving history there.
		Bash("b1", "git -C "+other+" commit -q --allow-empty -m 'the root was here'"),
		Dispatch("d1", "resolve the merge", sub, ""),
	))
	if _, err := os.Stat(wip); err != nil {
		t.Fatalf("premise: the sub-agent did not write its file: %v", err)
	}
	e.DeleteMeta(proj, sess, "commit_required") // the mock's own Stops spent the loop breaker; these Stops start it afresh
	record = e.TranscriptPath(proj, sess)
	rows := e.SubagentRecordPaths(proj, sess)
	if len(rows) == 0 {
		t.Fatal("premise: no sub-agent record")
	}
	agent = strings.TrimSuffix(strings.TrimPrefix(filepath.Base(rows[0]), "agent-"), ".jsonl")
	return
}

func launchBackground(t *testing.T, record, agent string) {
	t.Helper()
	appendLines(t, record,
		`{"type":"assistant","uuid":"bg-a","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_bg","name":"Agent","input":{"prompt":"go","run_in_background":true}}]}}`,
		`{"type":"user","uuid":"bg-r","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_bg","content":"Async agent launched successfully.\nagentId: `+agent+` (internal ID)"}]}}`,
	)
}

func TestT002_09_ARunningBackgroundSubagentsFolderIsNamedNotRefusedAndOwedOnceItStops(t *testing.T) {
	// The step this harness cannot take: launching a sub-agent in the background and having its
	// record show the launch and the end (an Agent call with run_in_background, a task
	// notification). No recorded transcript of this harness holds that shape.
	harness.RequireCap(t, harness.CapBackgroundTasks)
	const sess = "s-002-09"
	e, proj, other, record, agent := busyFolder(t, sess)

	// Premise: the agent has finished (the mock ran it to its end), so its work is the root's.
	if r := e.StopNow(proj, sess, false); !harness.Blocked(r) || !strings.Contains(r.Output, "docs/wip.md") {
		t.Fatalf("premise: a finished agent's uncommitted file in a registered folder is refused:\n%s", r.Output)
	}

	// The dispatcher resumes it in the background: it runs again.
	startAgent(t, e, proj, sess, record, agent)
	launchBackground(t, record, agent)
	r := e.StopNow(proj, sess, false)
	if harness.Blocked(r) {
		t.Fatalf("the root was refused for a running sub-agent's half-done work:\n%s", r.Output)
	}
	if !strings.Contains(r.Output, "being worked on by sub-agent "+agent) || !strings.Contains(r.Output, other) {
		t.Fatalf("the Stop did not name the agent working in the folder:\n%s", r.Output)
	}

	// The root's own uncommitted work in its tree is refused beside it.
	e.WriteFile(proj, "docs/mine.md", "mine\n")
	if r := e.StopNow(proj, sess, false); !harness.Blocked(r) || !strings.Contains(r.Output, "docs/mine.md") || strings.Contains(r.Output, "wip.md") {
		t.Fatalf("the root's own file was not refused alone:\n%s", r.Output)
	}
	if err := os.Remove(filepath.Join(proj, "docs", "mine.md")); err != nil {
		t.Fatal(err)
	}

	// The agent stops: its SubagentStop ends the run, and the folder is the root's again.
	stop, _ := json.Marshal(map[string]any{
		"session_id": sess, "transcript_path": record, "cwd": proj,
		"hook_event_name": "SubagentStop", "agent_id": agent, "agent_type": "general-purpose",
	})
	e.CLIDirectStdinEnv(proj, string(stop), e.SessionEnv(""), "sr-session", "subagent-stop")
	appendLines(t, record,
		`{"type":"attachment","uuid":"bg-n","attachment":{"type":"queued_command","commandMode":"task-notification","prompt":"<task-notification>\n<task-id>`+agent+`</task-id>\n<status>completed</status>\n</task-notification>"}}`)
	if r := e.StopNow(proj, sess, false); !harness.Blocked(r) || !strings.Contains(r.Output, "docs/wip.md") {
		t.Fatalf("the folder was not owed again once the sub-agent stopped:\n%s", r.Output)
	}
}

func TestT002_09_AForegroundOrUnrelatedAgentDoesNotExcuseTheFolder(t *testing.T) {
	const sess = "s-002-09b"
	e, proj, _, record, agent := busyFolder(t, sess)

	// Running, but the record never showed it launched in the background: judged.
	startAgent(t, e, proj, sess, record, agent)
	if r := e.StopNow(proj, sess, false); !harness.Blocked(r) || !strings.Contains(r.Output, "docs/wip.md") {
		t.Fatalf("a foreground agent excused the folder:\n%s", r.Output)
	}

	// A background agent that never worked in the folder does not excuse it either.
	startAgent(t, e, proj, sess, record, "other-agent")
	appendLines(t, record,
		`{"type":"assistant","uuid":"bg2-a","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_bg2","name":"Agent","input":{"prompt":"go","run_in_background":true}}]}}`,
		`{"type":"user","uuid":"bg2-r","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_bg2","content":"Async agent launched successfully.\nagentId: other-agent (internal ID)"}]}}`,
	)
	if r := e.StopNow(proj, sess, false); !harness.Blocked(r) || !strings.Contains(r.Output, "docs/wip.md") {
		t.Fatalf("an agent that never worked in the folder excused it:\n%s", r.Output)
	}
}
