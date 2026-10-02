package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T003_74: the session family keeps ONE check-results database, the root's, written by the root
// and every sub-agent (each run carries its agent_id). What a sub-agent passed or refused on a
// commit under a rule is the root's to see: the root never judges again a tip a sub-agent
// passed, and the root's merge gate sees a sub-agent's refusal.

// pullRequests makes `gh pr view N --json headRefName,headRefOid` answer for the given pull
// requests (number -> head branch), as a real gh would; every other gh call does nothing.
func pullRequests(e *Env, prs map[string]string) {
	cases := ""
	for n, b := range prs {
		cases += n + ") b=" + b + " ;;\n"
	}
	e.InstallPathShim("gh", "#!/bin/sh\n[ \"$1\" = pr ] && [ \"$2\" = view ] || exit 0\nb=\nfor a in \"$@\"; do case \"$a\" in\n"+cases+"esac; done\n"+
		"[ -n \"$b\" ] || exit 1\noid=\"$(git rev-parse \"refs/heads/$b\")\" || exit 1\n"+
		"printf '{\"headRefName\":\"%s\",\"headRefOid\":\"%s\"}\\n' \"$b\" \"$oid\"\n")
}

// A sub-agent's pass on its branch is reused: once its worktree is gone and the root inherits
// the branch, the rule is not run again, and the branch can be merged.
func TestT003_74_ASubagentsPassIsReusedByTheRootAndItsBranchMerges(t *testing.T) {
	e, proj, led := project(t, docsRule)
	const sess = "s-003-74"
	pullRequests(e, map[string]string{"7": "sub-ok"})
	sub := harness.SubagentScript(t, Turns("sub done",
		Bash("b1", "git switch -q -c sub-ok"),
		harness.CommitFile("c1", "docs/ok.md", "clean words", "sub adds ok"),
	))
	res := e.Run(proj, sess, "delegate", Turns("root done", harness.Dispatch("d1", "write the docs", sub, "worktree")))
	if res.AnySubagentStopBlocked() {
		t.Fatalf("premise: the clean sub-agent was refused:\n%s", res.Output)
	}
	if n := len(ledger(t, led)); n == 0 {
		t.Fatal("premise: the rule never ran for the sub-agent")
	}
	if rows := e.ChecksSQL(proj, sess, "select agent_id, check_id from check_runs where agent_id <> ''"); !strings.Contains(rows.Output, "file-guard/docs") {
		t.Fatalf("the sub-agent's run is not in the family's database, tagged with its agent:\n%s", rows.Output)
	}
	_, agent := subagentFolder(t, e, proj, sess)
	for _, db := range e.ChecksDBs() {
		if strings.Contains(db, "agent-"+agent) {
			t.Fatalf("the sub-agent kept check results of its own (%s) instead of writing the family's", db)
		}
	}

	// The root's merge of the sub-agent's branch is not held up by a pass it cannot see.
	if r := e.Run(proj, sess, "merge it", Turns("done", Bash("m1", "gh pr merge 7 --squash"))); r.Saw("no-merge-over-refusals") {
		t.Fatalf("the merge gate did not see the sub-agent's pass:\n%s\n%s", r.Output, e.ChecksStatus(proj, sess))
	}

	wt, _ := subagentFolder(t, e, proj, sess)
	e.Git(proj, "worktree", "remove", "--force", wt)
	before := len(ledger(t, led))
	r := e.StopNow(proj, sess, false)
	if harness.Blocked(r) {
		t.Fatalf("the root refused a branch a sub-agent had passed:\n%s", r.Output)
	}
	if after := len(ledger(t, led)); after != before {
		t.Fatalf("the root judged again a tip a sub-agent had already passed (%d runs, then %d)", before, after)
	}
}

// A sub-agent's refusal is visible to the root's merge gate: the open refusal itself, not just a
// tip nobody has passed.
func TestT003_74_ASubagentsRefusalIsVisibleToTheRootsMergeGate(t *testing.T) {
	e, proj, _ := project(t, docsRule)
	const sess = "s-003-74b"
	pullRequests(e, map[string]string{"8": "sub-a"})
	sub := harness.SubagentScript(t, Turns("sub done",
		Bash("b1", "git switch -q -c sub-a"),
		harness.CommitFile("c1", "docs/a.md", "FORBIDDEN words", "sub adds a"),
	))
	res := e.Run(proj, sess, "delegate", Turns("root done", harness.Dispatch("d1", "write the docs", sub, "worktree")))
	if !res.AnySubagentStopBlocked() {
		t.Fatalf("premise: the sub-agent's own Stop should refuse:\n%s", res.Output)
	}
	m := e.Run(proj, sess, "merge it", Turns("done", Bash("m1", "gh pr merge 8 --squash")))
	if !m.Refused() || !m.Saw("no-merge-over-refusals") {
		t.Fatalf("the merge of a sub-agent's refused branch was not refused:\n%s", m.Output)
	}
	if !m.Saw("nobody resolved") || !m.Saw("file-guard/docs") {
		t.Fatalf("the root's gate did not see the sub-agent's open refusal itself:\n%s", m.Output)
	}
}
