package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/internal/sessionstate"
	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T003_61 (a): when the reflog cannot say where the session began (its record carries no
// timestamps), the start falls back, deterministically and wide, to where HEAD leaves the
// remote default branch: nothing wedges, nobody is told to delete state, and the work
// since that anchor is judged.
func TestT003_61_AnUnderivableStartFallsBackToTheRemoteDefaultBranch(t *testing.T) {
	e, proj, _ := project(t, docsRule)
	remote := filepath.Join(t.TempDir(), "remote.git")
	e.Git(proj, "init", "-q", "--bare", remote)
	e.Git(proj, "remote", "add", "origin", remote)
	e.Git(proj, "push", "-q", "origin", "main")
	e.Git(proj, "remote", "set-head", "origin", "main")
	anchor := e.Git(proj, "rev-parse", "HEAD")

	const sess = "s-003-61a"
	e.Run(proj, sess, "clean", Turns("done", harness.CommitFile("c1", "docs/a.md", "FORBIDDEN words", "add a")))
	e.DeleteMeta(proj, sess, sessionstate.MetaSessionStart)
	e.RemoveCheckResults(proj, sess)
	// A record with no timestamps says nothing about when the session began.
	record := e.TranscriptPath(proj, sess)
	body, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	var kept []string
	for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		var rec map[string]any
		if json.Unmarshal([]byte(line), &rec) == nil {
			delete(rec, "timestamp")
			b, _ := json.Marshal(rec)
			line = string(b)
		}
		kept = append(kept, line)
	}
	if err := os.WriteFile(record, []byte(strings.Join(kept, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res := e.StopNow(proj, sess, false)
	if !harness.Blocked(res) || !strings.Contains(res.Output, refusalText) || !strings.Contains(res.Output, "docs/a.md") {
		t.Fatalf("the work since the remote anchor was not judged:\n%s", res.Output)
	}
	if strings.Contains(res.Output, "rm -f") || strings.Contains(res.Output, "did not keep") {
		t.Fatalf("the refusal wedges or tells the agent to delete state:\n%s", res.Output)
	}
	if got := e.Meta(proj, sess, sessionstate.MetaSessionStart); got != anchor {
		t.Fatalf("the start fell back to %q, want the merge base with origin/HEAD %s", got, anchor)
	}

	// Released: fixing the work passes, nothing else is needed.
	e.WriteFile(proj, "docs/a.md", "clean words")
	e.CommitAll(proj, "fix a")
	if res := e.StopNow(proj, sess, false); harness.Blocked(res) {
		t.Fatalf("the session was not released once its work was fixed:\n%s", res.Output)
	}
}

// T003_61 (b): a SubagentStop whose identity cannot be resolved does not pass silently: its
// folder's file-guards run with what is known and refuse what they find.
func TestT003_61_ASubagentWithoutAnIdentityIsStillJudged(t *testing.T) {
	e, proj, _ := project(t, docsRule)
	e.WriteFile(proj, "docs/b.md", "FORBIDDEN words")
	e.CommitAll(proj, "add b")

	payload, _ := json.Marshal(map[string]any{
		"cwd": proj, "agent_id": "a-unresolvable", "agent_transcript_path": filepath.Join(t.TempDir(), "gone.jsonl"),
		"stop_hook_active": false, "hook_event_name": "SubagentStop",
	})
	res := e.CLIDirectStdinEnv(proj, string(payload), e.SessionEnv(""), "sr-session", "subagent-stop")
	if !harness.Blocked(res) || !strings.Contains(res.Output, refusalText) {
		t.Fatalf("a sub-agent that could not be identified went unjudged:\n%s", res.Output)
	}
}
