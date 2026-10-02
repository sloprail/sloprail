package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T015_13: a sub-agent whose own session store never held its start is still judged
// from where its folder began, because its worktree is a registered session folder
// found by its path in the root session's store.
//
// The shape: the sub-agent's FIRST tool calls arrive before its own record exists (its
// sidechain is written later), so no store of its own can be opened and no start is
// recorded there; by its Stop the record exists and the store is empty. Without the
// registry that Stop has no start at all and the range fails closed ("not computable"):
// the sub-agent is refused for a reason no commit of its can fix. With it, the range
// starts at the HEAD the folder had at that first call, so only the sub-agent's own
// commits are judged.
//
// Two folders are exercised. The worktree it was dispatched into (cut from main at C,
// after B and C landed with violations): only D is judged. And a repository it then
// cd'd into, which has an old violation of its own: registered as an ad-hoc folder
// started at its HEAD when first stood in, so the old violation is never judged and
// only the commit made after is.
func TestT015_13_AFolderIsFoundByItsPathWhenTheAgentsOwnStoreHasNoStart(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.WriteFile(proj, "docs/seed.md", "seed\n")
	e.EnablePluginShippingFileGuard(proj, "ownstart", "forbidden",
		"match: \"docs/**\"\nchecks:\n  - script: ./check.sh\n",
		map[string]string{"check.sh": ownStartCheck})
	e.CommitAll(proj, "A: the project")

	const sess = "s-015-13"
	e.Run(proj, sess, "go", Turns("root done",
		Bash("b0", "true"), // the parent starts at A
		harness.CommitFile("cb", "docs/b.md", "FORBIDDEN in B", "B: lands on main after A"),
		harness.CommitFile("cc", "docs/c.md", "FORBIDDEN in C", "C: lands on main after A"),
	))

	// B and C have landed on the default branch; the sub-agent's worktree is cut from main at C.
	e.PushBranch(proj, "main")
	wt := filepath.Join(proj, ".claude", "worktrees", "agent-x")
	e.Git(proj, "worktree", "add", "-q", "-b", "worktree-agent-x", wt, "HEAD")
	c := e.Git(proj, "rev-parse", "HEAD")

	// Another repository (the same plugin enabled) with a violation from before anyone
	// touched it.
	other := e.Project()
	e.GitInit(other)
	e.WriteFile(other, "docs/old.md", "FORBIDDEN in OLD")
	e.CommitAll(other, "OLD: before the sub-agent touched this repository")
	otherHead := e.Git(other, "rev-parse", "HEAD")
	e.PushBranch(other, "main") // OLD has landed

	record := e.TranscriptPath(proj, sess)
	payload := func(event, cwd string, extra map[string]any) string {
		m := map[string]any{
			"session_id": sess, "transcript_path": record, "cwd": cwd,
			"agent_id": "agentx", "agent_type": "general-purpose", "hook_event_name": event,
		}
		for k, v := range extra {
			m[k] = v
		}
		b, _ := json.Marshal(m)
		return string(b)
	}
	hook := func(cmd, in, dir string) harness.Result {
		return e.CLIDirectStdinEnv(dir, in, e.SessionEnv(""), "sr-session", cmd)
	}
	tool := map[string]any{"tool_name": "Bash", "tool_input": map[string]any{"command": "true"}}

	// Its first tool calls, the second after it cd'd: no record of its own yet.
	hook("pre-tool", payload("PreToolUse", wt, tool), wt)
	hook("pre-tool", payload("PreToolUse", other, tool), other)

	// Its own work in each, then its record appears.
	e.WriteFile(wt, "docs/d.md", "FORBIDDEN in D")
	e.CommitAll(wt, "D: the sub-agent's own change")
	e.WriteFile(other, "docs/new.md", "FORBIDDEN in NEW")
	e.CommitAll(other, "NEW: the sub-agent's own change")
	sidechain := filepath.Join(strings.TrimSuffix(record, ".jsonl"), "subagents", "agent-agentx.jsonl")
	if err := os.MkdirAll(filepath.Dir(sidechain), 0o755); err != nil {
		t.Fatal(err)
	}
	line, _ := json.Marshal(map[string]any{
		"type": "user", "uuid": "sub-origin", "cwd": wt, "sessionId": sess, "isSidechain": true,
		"agentId": "agentx", "message": map[string]any{"role": "user", "content": "make D"},
	})
	if err := os.WriteFile(sidechain, append(line, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	// It judges what it committed: `sr-checks run` over each folder's range, as the session.
	e.CheckRunRaw(wt, sess, "origin/main", "HEAD")
	e.CheckRunRaw(other, sess, "origin/main", "HEAD")
	stop := func(cwd string) string {
		return hook("subagent-stop", payload("SubagentStop", cwd, map[string]any{"agent_transcript_path": sidechain}), cwd).Output
	}

	if out := stop(wt); !strings.Contains(out, "FORBIDDEN in: docs/d.md") ||
		strings.Contains(out, "docs/b.md") || strings.Contains(out, "docs/c.md") {
		t.Fatalf("the worktree was not judged on exactly its own commit D:\n%s", out)
	}
	if out := stop(other); !strings.Contains(out, "FORBIDDEN in: docs/new.md") || strings.Contains(out, "docs/old.md") {
		t.Fatalf("the repository the sub-agent cd'd into was not judged on exactly the commit made after it was touched:\n%s", out)
	}

	folders := e.SessionFolders(proj, sess)
	if len(folders) != 3 {
		t.Fatalf("session folders = %+v, want the root, the worktree and one ad-hoc folder", folders)
	}
	byRole := map[string]string{}
	for _, f := range folders {
		byRole[f.Role] = f.BaseRef
		if f.Role != "root" && f.AgentID != "agentx" {
			t.Fatalf("folder %+v is not owned by the sub-agent", f)
		}
	}
	if byRole["subagent-worktree"] != c || byRole["ad-hoc"] != otherHead {
		t.Fatalf("folder starts = %v, want the worktree at %s and the ad-hoc repository at its HEAD when first stood in, %s", byRole, c, otherHead)
	}
}
