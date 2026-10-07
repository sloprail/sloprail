package e2e

import (
	"encoding/json"
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

	// A sub-agent's record is tied to its parent in the harness's own way, and its hooks are
	// spelt in the harness's own fields (Env.SubagentRecordPath, Env.SubagentHookPayload). A
	// harness that records no link from a sub-agent to its parent (no CapSubagentParentLink:
	// Cursor's sub-agent is a conversation of its own, and its subagent hooks are not
	// attributable) has no sub-agent worktree to find: it registers none, the root alone.
	sidechain := e.SubagentRecordPath(proj, sess, "agentx")
	if !harness.HasCap(t, harness.CapSubagentParentLink) {
		if sidechain != "" || e.ForgeSubagentRecord(proj, sess, "agentx", wt, "make D") != "" {
			t.Fatalf("a harness that cannot tie a sub-agent to its parent has a record path for one: %q", sidechain)
		}
		if folders := e.SessionFolders(proj, sess); len(folders) != 1 || folders[0].Role != "root" {
			t.Fatalf("session folders = %+v, want the root alone: no sub-agent can be told from the session", folders)
		}
		return
	}
	payload := func(event, cwd string, extra map[string]any) string {
		return e.SubagentHookPayload(proj, sess, "agentx", event, cwd, extra)
	}
	// the id the harness's hooks name the sub-agent by (Codex's is its thread id)
	var named struct {
		AgentID string `json:"agent_id"`
	}
	if err := json.Unmarshal([]byte(payload("PreToolUse", wt, nil)), &named); err != nil || named.AgentID == "" {
		t.Fatalf("the sub-agent's hook names no agent: %v", err)
	}
	hookAgentID := named.AgentID
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
	if got := e.ForgeSubagentRecord(proj, sess, "agentx", wt, "make D"); got != sidechain {
		t.Fatalf("the sub-agent's record was written at %q, not where it is kept, %q", got, sidechain)
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
		if f.Role != "root" && f.AgentID != hookAgentID {
			t.Fatalf("folder %+v is not owned by the sub-agent", f)
		}
	}
	if byRole["subagent-worktree"] != c || byRole["ad-hoc"] != otherHead {
		t.Fatalf("folder starts = %v, want the worktree at %s and the ad-hoc repository at its HEAD when first stood in, %s", byRole, c, otherHead)
	}
}
