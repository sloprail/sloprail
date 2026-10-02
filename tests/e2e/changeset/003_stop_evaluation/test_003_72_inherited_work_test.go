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

// T003_72: the WorktreeRemove hook keeps what the folder holds and marks it removed; it never
// blocks the removal, whatever it is handed.
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
	if got := e.Meta(proj, sess, "folder_removed:"+wt); got != "" {
		t.Fatalf("a payload for another session marked the folder removed: %q", got)
	}
	r := hook(map[string]any{
		"session_id": sess, "transcript_path": e.TranscriptPath(proj, sess), "cwd": proj,
		"hook_event_name": "WorktreeRemove", "worktree_path": wt,
	})
	if r.Code != 0 {
		t.Fatalf("the hook blocked the removal: exit %d\n%s", r.Code, r.Output)
	}
	if got := e.Meta(proj, sess, "folder_removed:"+wt); got != "1" {
		t.Fatalf("the folder was not marked removed: %q", got)
	}
	if pins := strings.TrimSpace(e.Git(proj, "for-each-ref", "refs/sloprail/pins")); pins == "" {
		t.Fatal("the folder's owed tip was not pinned")
	}
	// Marked removed (the directory has not gone yet): the root's next Stop inherits it.
	if r := e.StopNow(proj, sess, false); !harness.Blocked(r) || !strings.Contains(r.Output, "You now own this") {
		t.Fatalf("a folder the harness reported removed was not inherited:\n%s", r.Output)
	}
}
