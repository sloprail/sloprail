package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T033_04: a merge whose target the gate cannot determine is refused (fail closed), with
// the way forward: name the pull request literally. Seen in a real coordinator session: the
// number in a shell variable inside a loop, and the gate passed it.
func TestT033_04_AMergeWhoseTargetCannotBeToldIsRefused(t *testing.T) {
	const sess = "s-033-04"
	e, proj := unjudgedProject(t)
	setPR(t, e, "5", e.Git(proj, "branch", "--show-current"))

	for i, cmd := range []string{
		`for n in 5; do gh pr merge $n --squash --admin; done`,
		`n=5; gh pr merge "$n" --squash`,
		`gh pr merge $(echo 5) --squash`,
		`gh pr merge --squash --made-up-flag 5`,
		`gh pr merge 99 --squash`, // gh cannot look it up
		`gh pr merge 5 --squash && gh pr merge $N --squash`,
	} {
		res := e.Run(proj, sess, "merge", Turns("done", Bash("m"+string(rune('a'+i)), cmd)))
		if !res.Refused() || !res.Saw("no-merge-over-refusals") || !res.Saw("literally") {
			t.Fatalf("%q was not refused with the advice to name the PR literally:\n%s", cmd, res.Output)
		}
	}

	// Named literally, with a PR gh can look up and nothing owed, it goes through.
	res := e.Run(proj, sess, "merge 5", Turns("done", Bash("ok", "gh pr merge 5 --squash --admin")))
	if res.Saw("no-merge-over-refusals") {
		t.Fatalf("a literal merge of a clean PR was refused:\n%s", res.Output)
	}

	// The user's own words still let a command the gate cannot follow through.
	const said = "merge whatever is in n, I know"
	res = e.Run(proj, sess, said, Turns("done",
		Bash("c", "n=5; sr-session trajectory cite '"+said+"' && gh pr merge $n --squash"),
	))
	if res.Saw("no-merge-over-refusals") {
		t.Fatalf("a merge citing the user's words was refused:\n%s", res.Output)
	}
}

// T033_05: `gh api .../merge`, `gh pr merge -R`, and a push to the default branch are held
// to the same rule as `gh pr merge`.
func TestT033_05_OtherWaysToLandAreRefusedLikewise(t *testing.T) {
	const sess = "s-033-05"
	e, proj := refusedProject(t, sess)
	cur := e.Git(proj, "branch", "--show-current")
	setPR(t, e, "7", cur)

	for i, cmd := range []string{
		"gh api -X PUT repos/o/r/pulls/7/merge -f merge_method=squash",
		"gh pr merge 7 --repo o/r --squash --admin",
		"git push origin HEAD:main",
		"git push origin HEAD:master",
	} {
		res := e.Run(proj, sess, "land it", Turns("done", Bash("l"+string(rune('a'+i)), cmd)))
		if !res.Refused() || !res.Saw("file-guard/docs") || !res.Saw("trajectory cite") {
			t.Fatalf("%q landed work a rule refused:\n%s", cmd, res.Output)
		}
	}
	// A push of another branch is not this gate's business.
	res := e.Run(proj, sess, "push", Turns("done", Bash("p", "git push origin HEAD:some-feature")))
	if res.Saw("no-merge-over-refusals") {
		t.Fatalf("a push to a feature branch was refused by the merge gate:\n%s", res.Output)
	}
}

// subRefusedProject has a sub-agent, in a worktree of its own, commit FORBIDDEN text on
// branch sub-a and end; its SubagentStop records the refusal in the sub-agent's own store,
// which the coordinator's session does not own.
func subRefusedProject(t *testing.T, sess string) (*harness.Env, string) {
	t.Helper()
	e, proj := unjudgedProject(t)
	e.SetStopBlockCap(1)
	setPR(t, e, "7", "sub-a")
	sub := harness.SubagentScript(t, Turns("sub done",
		Bash("b1", "git switch -q -c sub-a"),
		harness.CommitFile("c1", "docs/a.md", "FORBIDDEN words", "sub adds a"),
	))
	res := e.Run(proj, sess, "delegate", Turns("root done", harness.Dispatch("d1", "write the docs", sub, "worktree")))
	if !res.Saw("SubagentStop blocked (") {
		t.Fatalf("premise: the sub-agent's Stop did not refuse its commit:\n%s", res.Output)
	}
	if strings.Contains(e.ChecksStatus(proj, sess, "--failing"), "file-guard/docs") {
		t.Fatalf("premise: the refusal is in the root's own store, so this proves nothing:\n%s", e.ChecksStatus(proj, sess))
	}
	return e, proj
}

// T033_06: the open refusal a SUB-AGENT's Stop recorded (in its own store) blocks the
// coordinator's merge; the user's words override it; fixed on top and judged, the merge
// goes through.
func TestT033_06_ASubagentsOpenRefusalBlocksTheCoordinatorsMerge(t *testing.T) {
	const sess = "s-033-06"
	e, proj := subRefusedProject(t, sess)

	res := e.Run(proj, sess, "merge 7", Turns("done", Bash("m1", "gh pr merge 7 --squash --admin")))
	if !res.Refused() || !res.Saw("file-guard/docs") || !res.Saw("trajectory cite") {
		t.Fatalf("the coordinator merged over a refusal its sub-agent recorded:\n%s", res.Output)
	}

	const said = "merge it anyway, I accept the refusal"
	res = e.Run(proj, sess, said, Turns("done",
		Bash("m2", "sr-session trajectory cite '"+said+"' && gh pr merge 7 --squash --admin"),
	))
	if res.Saw("no-merge-over-refusals") {
		t.Fatalf("a merge citing the user's words was refused:\n%s", res.Output)
	}

	// The fix lands on top of the refused tip and is judged: the PR is now that branch.
	setPR(t, e, "8", "fixer")
	e.Run(proj, sess, "fix it", Turns("fixed",
		Bash("f1", "git switch -q -c fixer sub-a"),
		harness.CommitFile("c2", "docs/a.md", "clean words", "fix a"),
	))
	res = e.Run(proj, sess, "now merge", Turns("done", Bash("m3", "gh pr merge 8 --squash --admin")))
	if res.Saw("no-merge-over-refusals") {
		t.Fatalf("the merge was refused after the fix was judged and passed:\n%s", res.Output)
	}
}

