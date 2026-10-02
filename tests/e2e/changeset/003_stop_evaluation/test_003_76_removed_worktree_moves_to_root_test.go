package e2e

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/internal/sessionstate"
	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T003_76: a sub-agent's worktree is removed while its branch still exists in the session's
// repository. The range does not vanish with the folder: it moves to the root's folder, and the
// root's Stop verifies it there, so the violation the sub-agent left is still refused.
func TestT003_76_ARemovedWorktreeWhoseBranchExistsMovesItsRangeToTheRoot(t *testing.T) {
	e, proj, _ := project(t, docsRule)
	const sess = "s-003-76"
	sub := harness.SubagentScript(t, Turns("sub done",
		Bash("b1", "git switch -q -c sub-a"),
		harness.CommitFile("c1", "docs/a.md", "FORBIDDEN words", "sub adds a"),
	))
	e.Run(proj, sess, "delegate", Turns("root done", harness.Dispatch("d1", "write the docs", sub, "worktree")))
	wt, _ := subagentFolder(t, e, proj, sess)

	root, err := filepath.EvalSymlinks(proj)
	if err != nil {
		t.Fatal(err)
	}
	tracked := func() map[string]string { // head -> folder, tracked ranges only
		r := e.CLIDirectEnv(proj, e.SessionEnv(sess), "sr-session", "refs", "list", "--json")
		if r.Code != 0 {
			t.Fatalf("refs list: exit %d:\n%s", r.Code, r.Output)
		}
		var rs []sessionstate.TrackedRange
		if err := json.Unmarshal([]byte(r.Output), &rs); err != nil {
			t.Fatalf("refs list --json is not JSON (%v):\n%s", err, r.Output)
		}
		out := map[string]string{}
		for _, x := range rs {
			if x.Tracked() {
				out[x.Head] = x.Folder
			}
		}
		return out
	}
	if got := tracked()["sub-a"]; got == "" || got == root {
		t.Fatalf("premise: sub-a is tracked in the sub-agent's worktree, got folder %q", got)
	}

	e.Git(proj, "worktree", "remove", "--force", wt)
	payload, _ := json.Marshal(map[string]any{
		"session_id": sess, "transcript_path": e.TranscriptPath(proj, sess), "cwd": proj,
		"hook_event_name": "WorktreeRemove", "worktree_path": wt,
	})
	if r := e.CLIDirectStdinEnv(proj, string(payload), e.SessionEnv(""), "sr-session", "worktree-remove"); r.Code != 0 {
		t.Fatalf("the hook blocked the removal: exit %d\n%s", r.Code, r.Output)
	}
	if got := tracked()["sub-a"]; got != root {
		t.Fatalf("the range of the surviving branch did not move to the root's folder %s: %q", proj, got)
	}
	if r := e.StopNow(proj, sess, false); !harness.Blocked(r) || !strings.Contains(r.Output, "sub-a") {
		t.Fatalf("the root's Stop did not verify the moved range and refuse sub-a:\n%s", r.Output)
	}
}
