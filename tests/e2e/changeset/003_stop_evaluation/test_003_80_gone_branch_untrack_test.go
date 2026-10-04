package e2e

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/internal/sessionstate"
	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T003_80: a sub-agent's worktree folder still exists but its branch is gone. The Stop's advice,
// `sr-session refs untrack`, must work for that range: the head no longer resolves, so the range
// is found by the folder and head it was stored under.
func TestT003_80_AnUntrackNamesARangeWhoseBranchIsGoneInAFolderThatStands(t *testing.T) {
	e, proj, _ := project(t, docsRule)
	const sess = "s-003-80"
	sub := harness.SubagentScript(t, Turns("sub done",
		Bash("b1", "git switch -q -c sub-gone-branch"),
		harness.CommitFile("c1", "docs/a.md", "FORBIDDEN words", "sub adds a"),
	))
	e.Run(proj, sess, "delegate", Turns("root done", harness.Dispatch("d1", "write the docs", sub, "worktree")))
	wt, _ := subagentFolder(t, e, proj, sess)

	e.Git(wt, "switch", "-q", "--detach")
	e.Git(proj, "branch", "-D", "sub-gone-branch")

	r := e.CLIDirectEnv(proj, e.SessionEnv(sess), "sr-session", "refs", "untrack", "--folder", wt, "--head", "sub-gone-branch", "--reason", "the branch is gone")
	if r.Code != 0 {
		t.Fatalf("untrack of a range whose branch is gone (folder kept): exit %d:\n%s", r.Code, r.Output)
	}
	rows := func() []sessionstate.TrackedRange {
		r := e.CLIDirectEnv(proj, e.SessionEnv(sess), "sr-session", "refs", "list", "--json")
		if r.Code != 0 {
			t.Fatalf("refs list: exit %d:\n%s", r.Code, r.Output)
		}
		var rs []sessionstate.TrackedRange
		if err := json.Unmarshal([]byte(r.Output), &rs); err != nil {
			t.Fatalf("refs list --json is not JSON (%v):\n%s", err, r.Output)
		}
		return rs
	}
	found := false
	for _, x := range rows() {
		if x.Head == "sub-gone-branch" && x.Folder != "" {
			found = true
			if x.Tracked() || x.UntrackedReason != "the branch is gone" {
				t.Fatalf("the gone branch's range is not untracked with the reason given: %+v", x)
			}
			if x.AbandonedTip == "" || x.AbandonedTip != x.HeadSHA {
				t.Fatalf("the range was not untracked at the last tip it held: %+v", x)
			}
		}
	}
	if !found {
		t.Fatalf("the range stored for sub-gone-branch is gone from refs list")
	}
	// The detach step above leaves a "detached at" range of its own (the engine tracks it at the
	// Stop), and that one is still refused. The gone branch's range must not be: it is neither
	// refused as a range nor reported unreadable, only listed as untracked.
	r = e.StopNow(proj, sess, false)
	if !harness.Blocked(r) {
		t.Fatalf("premise: the detached range's FORBIDDEN text should still block the Stop:\n%s", r.Output)
	}
	for _, bad := range []string{"(sub-gone-branch, from", "cannot be read", `sub-gone-branch\" is gone`} {
		if strings.Contains(r.Output, bad) {
			t.Fatalf("the untracked range was still refused (%q):\n%s", bad, r.Output)
		}
	}
	if strings.Contains(r.Output, "sub-gone-branch (reason: the branch is gone)") {
		t.Fatalf("the Stop lists an untracked range one by one; it is noise:\n%s", r.Output)
	}
	if !strings.Contains(r.Output, "not verified because they are untracked (`sr-session refs list` shows them)") {
		t.Fatalf("the Stop does not carry the one-line count of untracked refs:\n%s", r.Output)
	}
	r = e.CLIDirectEnv(proj, e.SessionEnv(sess), "sr-session", "refs", "untrack", "--folder", wt, "--head", "never-tracked", "--reason", "x")
	if r.Code == 0 {
		t.Fatalf("an untrack of a head that was never stored succeeded:\n%s", r.Output)
	}
}
