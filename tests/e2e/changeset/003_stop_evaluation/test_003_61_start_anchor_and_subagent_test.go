package e2e

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T003_61 (b): a SubagentStop whose identity cannot be resolved does not pass silently: its
// folder's file-guards run with what is known and refuse what they find.
func TestT003_61_ASubagentWithoutAnIdentityIsStillJudged(t *testing.T) {
	e, proj, _ := project(t, docsRule)
	// The judging a real agent asks for before it stops runs inside its session (a run without one
	// stores no refusal: it cannot know what a check reading the transcript would say).
	e.Run(proj, "s-003-61b", "add b", Turns("done", harness.CommitFile("c1", "docs/b.md", "FORBIDDEN words", "add b")))

	payload, _ := json.Marshal(map[string]any{
		"cwd": proj, "agent_id": "a-unresolvable", "agent_transcript_path": filepath.Join(t.TempDir(), "gone.jsonl"),
		"stop_hook_active": false, "hook_event_name": "SubagentStop",
	})
	res := e.CLIDirectStdinEnv(proj, string(payload), e.SessionEnv(""), "sr-session", "subagent-stop")
	if !harness.Blocked(res) || !strings.Contains(res.Output, refusalText) {
		t.Fatalf("a sub-agent that could not be identified went unjudged:\n%s", res.Output)
	}
}
