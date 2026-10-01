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

// T003_72: a finished sub-agent's refused branch under a removed worktree is inherited.
func TestT003_72_ARemovedSubagentFolderIsInheritedByTheRootAndFixingItPasses(t *testing.T) {
	e, proj, _ := project(t, docsRule)
	const sess = "s-003-72"
	sub := harness.SubagentScript(t, Turns("sub done",
		Bash("b1", "git switch -q -c sub-a"),
		harness.CommitFile("c1", "docs/a.md", "FORBIDDEN words", "sub adds a"),
	))
	res := e.Run(proj, sess, "delegate", Turns("root done",
		harness.Dispatch("d1", "write the docs", sub, "worktree"),
	))
	if !res.AnySubagentStopBlocked() {
		t.Fatalf("premise: the sub-agent's own Stop should refuse its branch:\n%s", res.Output)
	}
	if got := stopRefusals(e, proj, sess); got != "" {
		t.Fatalf("the root claimed the work of a sub-agent whose folder is still there:\n%s", got)
	}
	wt, agent := subagentFolder(t, e, proj, sess)

	// The harness removes the finished sub-agent's worktree.
	e.Git(proj, "worktree", "remove", "--force", wt)
	r := e.StopNow(proj, sess, false)
	if !harness.Blocked(r) {
		t.Fatalf("a removed sub-agent folder's unjudged branch was never judged by anyone:\n%s", r.Output)
	}
	out := r.Output
	for _, want := range []string{"You now own this", "sub-agent " + agent, wt, "sub-a", "docs/a.md", "FORBIDDEN text in the changeset"} {
		if !strings.Contains(out, want) {
			t.Fatalf("the refusal lacks %q:\n%s", want, out)
		}
	}
	if i, j := strings.Index(out, "You now own this"), strings.Index(out, "FORBIDDEN text"); i < 0 || j < 0 || i > j {
		t.Fatalf("the ownership notice must come first:\n%s", out)
	}

	// The command it names works; the fix made there passes at the next Stop.
	fix := commandIn(t, out, "worktree add")
	runCommand(t, proj, fix)
	_, rest, _ := strings.Cut(fix, "worktree add ")
	newTree := strings.Trim(strings.Fields(rest)[0], "'")
	e.WriteFile(newTree, "docs/a.md", "clean words\n")
	e.CommitAll(newTree, "fix a")
	if r := e.StopNow(proj, sess, false); harness.Blocked(r) {
		t.Fatalf("the fixed inherited branch was still refused:\n%s", r.Output)
	}
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

// T003_72: rows recorded under a folder that is not a folder of the session at all (a manual
// data patch, a store from an older engine) are orphans too: the root judges them, in that
// repository, by that repository's rules.
func TestT003_72_RowsUnderAFolderThatIsNoSessionFolderAreInherited(t *testing.T) {
	e, proj, _ := project(t, docsRule)
	other := e.Project()
	e.GitInit(other)
	e.WriteFile(other, "docs/seed.md", "seed\n")
	e.CommitAll(other, "the other project")
	e.FileGuard(other, "docs", docsRule, map[string]string{"check.sh": recorder(filepath.Join(t.TempDir(), "other-ledger.jsonl"))})
	e.CommitAll(other, "the other rule")
	e.Git(other, "switch", "-q", "-c", "feat")
	e.WriteFile(other, "docs/x.md", "FORBIDDEN words\n")
	tip := e.CommitAll(other, "violate in the other repository")
	e.Git(other, "switch", "-q", "main")
	const sess = "s-003-72c"

	e.Run(proj, sess, "begin", Turns("done", Bash("b0", "true")))
	id := e.SessionFolders(proj, sess)[0].SessionID
	patch := e.CLIDirectEnv(proj, e.SessionEnv(sess), "sr-session", "refs", "add", "--session", id, "--workspace", proj,
		"--folder", other, "--ref", "feat", "--tip", tip)
	if patch.Code != 0 {
		t.Fatalf("premise: the manual patch failed: %s", patch.Output)
	}
	r := e.StopNow(proj, sess, false)
	if !harness.Blocked(r) || !strings.Contains(r.Output, "You now own this") || !strings.Contains(r.Output, "docs/x.md") {
		t.Fatalf("a row under a folder that is no session folder was never judged:\n%s", r.Output)
	}
}
