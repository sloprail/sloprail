package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/internal/sessionstate"
	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T056_01: the session's own branch is tracked automatically, from the merge base with origin's
// default branch, as soon as the folder is discovered.
func TestT056_01_TheCurrentBranchIsTrackedFromTheMergeBase(t *testing.T) {
	e, proj := project(t)
	const sess = "s-056-01"
	initial := e.Git(proj, "rev-parse", "origin/main")

	e.Run(proj, sess, "work", Turns("done", Bash("b1", "true")))

	rs := ranges(t, e, proj, sess)
	if len(rs) != 1 {
		t.Fatalf("want one tracked range, got %+v", rs)
	}
	r := rs[0]
	if r.Head != "main" || r.Base != initial || r.AddedBy != sessionstate.RangeAuto || !r.Tracked() {
		t.Fatalf("the branch is not tracked from the merge base with origin/main (%s): %+v", initial, r)
	}
}

// T056_02: a range nobody has judged is refused at Stop with the command that judges it; once
// `sr-checks run` has judged it, the same Stop passes — and the Stop asked no model.
func TestT056_02_StopVerifiesATrackedRangeAndNamesTheRunThatJudgesIt(t *testing.T) {
	e, proj := project(t)
	const sess = "s-056-02"

	e.Run(proj, sess, "write the doc", Turns("done", harness.CommitFile("c1", "docs/a.md", "the release is Friday", "add a")))

	joined := strings.Join(e.BlockingErrorsFrom(proj, sess, "Stop"), "\n")
	if !strings.Contains(joined, "not judged yet") || !strings.Contains(joined, "sr-checks run --base") {
		t.Fatalf("an unjudged tracked range was not refused with the run that judges it:\n%s", joined)
	}
	if n := e.JudgeCalls(proj, promptFile, ""); n != 0 {
		t.Fatalf("the Stop asked the judge (%d calls): it must only verify", n)
	}

	// The agent judges what it committed, as the refusal says.
	base := e.Git(proj, "rev-parse", "origin/main")
	if r := e.CheckRunRaw(proj, sess, base, "HEAD"); r.Code != 0 {
		t.Fatalf("sr-checks run: exit %d:\n%s", r.Code, r.Output)
	}
	before := len(e.AllBlockingErrorsFrom(proj, sess, "Stop"))
	e.Run(proj, sess, "done?", Turns("yes", Bash("b2", "true")))
	if after := len(e.AllBlockingErrorsFrom(proj, sess, "Stop")); after != before {
		t.Fatalf("a judged range was still refused at Stop:\n%s", strings.Join(e.AllBlockingErrorsFrom(proj, sess, "Stop")[before:], "\n"))
	}
}

// T056_03: a range the agent untracks, with a reason, is not verified; it stays listed with the
// reason, and automatic tracking does not bring it back.
func TestT056_03_AnUntrackedRangeIsNotVerifiedAndStaysListed(t *testing.T) {
	e, proj := project(t)
	const sess = "s-056-03"
	e.Run(proj, sess, "start", Turns("done", Bash("b1", "true")))

	if r := refs(e, proj, sess, "untrack"); r.Code == 0 {
		t.Fatalf("untrack without a reason succeeded:\n%s", r.Output)
	}
	if r := refs(e, proj, sess, "untrack", "--reason", "this branch is the user's own work, not mine"); r.Code != 0 {
		t.Fatalf("untrack: exit %d:\n%s", r.Code, r.Output)
	}

	e.Run(proj, sess, "write the doc", Turns("done", harness.CommitFile("c1", "docs/a.md", "the release is Friday", "add a")))
	if got := strings.Join(e.BlockingErrorsFrom(proj, sess, "Stop"), "\n"); strings.Contains(got, "not judged yet") {
		t.Fatalf("an untracked range was verified at Stop:\n%s", got)
	}
	rs := ranges(t, e, proj, sess)
	if len(rs) != 1 || rs[0].Tracked() || !strings.Contains(rs[0].UntrackedReason, "the user's own work") {
		t.Fatalf("the untracked range is not listed with its reason: %+v", rs)
	}
}

// T056_04: the agent can track a range of its own: another base for the same branch replaces
// the automatic one, and `refs track` is how it takes back what it untracked.
func TestT056_04_TheAgentCanTrackAnotherBaseAndTakeBackWhatItDropped(t *testing.T) {
	e, proj := project(t)
	const sess = "s-056-04"
	e.Run(proj, sess, "start", Turns("done", Bash("b1", "true")))
	head := e.Git(proj, "rev-parse", "HEAD")

	if r := refs(e, proj, sess, "untrack", "--reason", "dropping it for now"); r.Code != 0 {
		t.Fatalf("untrack: exit %d:\n%s", r.Code, r.Output)
	}
	if r := refs(e, proj, sess, "track", "--base", head); r.Code != 0 {
		t.Fatalf("track: exit %d:\n%s", r.Code, r.Output)
	}
	rs := ranges(t, e, proj, sess)
	if len(rs) != 1 || !rs[0].Tracked() || rs[0].Base != head || rs[0].AddedBy != sessionstate.RangeAgent {
		t.Fatalf("the agent's range did not replace the automatic one: %+v", rs)
	}
}
