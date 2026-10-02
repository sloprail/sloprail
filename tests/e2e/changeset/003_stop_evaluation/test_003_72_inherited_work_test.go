package e2e

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/internal/sessionstate"
	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// Orphaned work. Each agent judges its own folders, so a sub-agent's work under a folder that is
// gone (the harness removes agent worktrees, or `git worktree remove`) would be judged by
// nobody. The PARENT inherits it: its next Stop judges each tip on its own tree and says,
// at the top, that it now owns it. A LIVE sub-agent's folder is never claimed.

var backticked = regexp.MustCompile("`([^`]+)`")

// commandIn returns the first backticked command of text that contains all of parts.
func commandIn(t *testing.T, text string, parts ...string) string {
	t.Helper()
	for _, m := range backticked.FindAllStringSubmatch(text, -1) {
		ok := true
		for _, p := range parts {
			ok = ok && strings.Contains(m[1], p)
		}
		if ok {
			return m[1]
		}
	}
	t.Fatalf("no backticked command containing %q in:\n%s", parts, text)
	return ""
}

// runCommand runs a command a refusal named, exactly as written, the way an agent would.
func runCommand(t *testing.T, dir, command string) {
	t.Helper()
	cmd := exec.Command("sh", "-c", command)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the command the refusal named does not work: %s\n%v\n%s", command, err, out)
	}
}

// subagentFolder is the worktree the sub-agent was dispatched into, and its agent id.
func subagentFolder(t *testing.T, e *Env, proj, sess string) (path, agent string) {
	t.Helper()
	for _, f := range e.SessionFolders(proj, sess) {
		if f.Role == sessionstate.FolderSubagentWorktree {
			return f.Path, f.AgentID
		}
	}
	t.Fatalf("no sub-agent worktree folder is registered: %+v", e.SessionFolders(proj, sess))
	return "", ""
}

// T003_72: the WorktreeRemove hook keeps what the folder holds (its range moves to the root
// folder) and never blocks the removal, whatever it is handed.
func TestT003_72_TheWorktreeRemoveHookKeepsTheFoldersWorkAndNeverBlocks(t *testing.T) {
	e, proj, _ := project(t, docsRule)
	const sess = "s-003-72h"
	sub := harness.SubagentScript(t, Turns("sub done",
		Bash("b1", "git switch -q -c sub-a"),
		harness.CommitFile("c1", "docs/a.md", "FORBIDDEN words", "sub adds a"),
	))
	e.Run(proj, sess, "delegate", Turns("root done", harness.Dispatch("d1", "write the docs", sub, "worktree")))
	wt, _ := subagentFolder(t, e, proj, sess)

	hook := func(payload map[string]any) harness.Result {
		b, _ := json.Marshal(payload)
		return e.CLIDirectStdinEnv(proj, string(b), e.SessionEnv(""), "sr-session", "worktree-remove")
	}
	for _, bad := range []map[string]any{
		{},
		{"worktree_path": filepath.Join(proj, "nowhere")},
		{"session_id": "unknown", "worktree_path": wt},
	} {
		if r := hook(bad); r.Code != 0 {
			t.Fatalf("the hook blocked a removal it could not act on (payload %v): exit %d\n%s", bad, r.Code, r.Output)
		}
	}
	if rs := sessionRanges(t, e, proj, sess); !trackedIn(rs, wt, "sub-a") {
		t.Fatalf("a payload for another session changed the folder's range: %+v", rs)
	}
	r := hook(map[string]any{
		"session_id": sess, "transcript_path": e.TranscriptPath(proj, sess), "cwd": proj,
		"hook_event_name": "WorktreeRemove", "worktree_path": wt,
	})
	if r.Code != 0 {
		t.Fatalf("the hook blocked the removal: exit %d\n%s", r.Code, r.Output)
	}
	// The removed folder's range is settled, not lost: its branch still exists in the session's
	// own repository, so the range moves to the root folder, and the folder's is untracked saying so.
	rs := sessionRanges(t, e, proj, sess)
	if !trackedIn(rs, proj, "sub-a") {
		t.Fatalf("the removed worktree's range did not move to the root folder: %+v", rs)
	}
	if trackedIn(rs, wt, "sub-a") {
		t.Fatalf("the removed worktree's range is still tracked in the removed folder: %+v", rs)
	}
	// The root's next Stop verifies it there: the work is kept, and judged.
	if r := e.StopNow(proj, sess, false); !harness.Blocked(r) || !strings.Contains(r.Output, "sub-a") {
		t.Fatalf("a folder the harness reported removed was not kept and verified at the root:\n%s", r.Output)
	}
}

// sessionRanges is `sr-session refs list --json` as the session.
func sessionRanges(t *testing.T, e *Env, proj, sess string) []sessionstate.TrackedRange {
	t.Helper()
	r := e.CLIDirectEnv(proj, e.SessionEnv(sess), "sr-session", "refs", "list", "--json")
	if r.Code != 0 {
		t.Fatalf("refs list: exit %d:\n%s", r.Code, r.Output)
	}
	var out []sessionstate.TrackedRange
	if err := json.Unmarshal([]byte(r.Output), &out); err != nil {
		t.Fatalf("refs list --json is not JSON (%v):\n%s", err, r.Output)
	}
	return out
}

func trackedIn(rs []sessionstate.TrackedRange, folder, head string) bool {
	for _, r := range rs {
		if r.Head == head && r.Tracked() && filepath.Clean(r.Folder) == filepath.Clean(folder) {
			return true
		}
	}
	return false
}

// worktreeRemoved returns the WorktreeRemove hook call for a worktree, as the harness sends it.
func worktreeRemoved(t *testing.T, e *Env, proj, sess, wt string) func() harness.Result {
	t.Helper()
	payload, _ := json.Marshal(map[string]any{
		"session_id": sess, "transcript_path": e.TranscriptPath(proj, sess), "cwd": proj,
		"hook_event_name": "WorktreeRemove", "worktree_path": wt,
	})
	return func() harness.Result {
		return e.CLIDirectStdinEnv(proj, string(payload), e.SessionEnv(""), "sr-session", "worktree-remove")
	}
}

// T003_72: the harness removes a finished sub-agent's worktree WITHOUT the hook (or the hook
// never reaches the session): the stat fallback finds the folder gone at the root's Stop, moves
// its range to the root, and verifies it there; fixing the branch there passes.
func TestT003_72_ARemovedSubagentFolderWithoutTheHookIsFoundByStatAndFixingItPasses(t *testing.T) {
	e, proj, _ := project(t, docsRule)
	const sess = "s-003-72s"
	main := e.Git(proj, "branch", "--show-current")
	sub := harness.SubagentScript(t, Turns("sub done",
		Bash("b1", "git switch -q -c sub-a"),
		harness.CommitFile("c1", "docs/a.md", "FORBIDDEN words", "sub adds a"),
	))
	e.Run(proj, sess, "delegate", Turns("root done", harness.Dispatch("d1", "write the docs", sub, "worktree")))
	wt, _ := subagentFolder(t, e, proj, sess)

	e.Git(proj, "worktree", "remove", "--force", wt)
	r := e.StopNow(proj, sess, false)
	if !harness.Blocked(r) || !strings.Contains(r.Output, "sub-a") || !strings.Contains(r.Output, "docs/a.md") {
		t.Fatalf("a removed sub-agent folder's range was not kept and verified at the root:\n%s", r.Output)
	}
	root, err := filepath.EvalSymlinks(proj)
	if err != nil {
		t.Fatal(err)
	}
	if rs := sessionRanges(t, e, proj, sess); !trackedIn(rs, root, "sub-a") || trackedIn(rs, wt, "sub-a") {
		t.Fatalf("the range did not move from the removed folder %s to the root: %+v", wt, rs)
	}

	e.Git(proj, "switch", "-q", "sub-a")
	e.WriteFile(proj, "docs/a.md", "clean words\n")
	e.CommitAll(proj, "fix a")
	e.Git(proj, "switch", "-q", main)
	e.JudgeTracked(proj, sess, false)
	if r := e.StopNow(proj, sess, false); harness.Blocked(r) {
		t.Fatalf("the fixed branch was still refused:\n%s", r.Output)
	}
}
