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

	e.Run(proj, sess, "work", Turns("done", Bash("b1", "true")))
	// The harness publishes what the project committed before its first session to origin/main
	// when that session starts (a real project has it pushed), so the merge base is read now.
	// T056_09 keeps origin where it was, for the unpushed-commits case.
	initial := e.Git(proj, "rev-parse", "origin/main")

	rs := ranges(t, e, proj, sess)
	if len(rs) != 1 {
		t.Fatalf("want one tracked range, got %+v", rs)
	}
	r := rs[0]
	if r.Head != "main" || r.Base != initial || r.AddedBy != sessionstate.RangeAuto || !r.Tracked() {
		t.Fatalf("the branch is not tracked from the merge base with origin/main (%s): %+v", initial, r)
	}
}

// T056_09: what the branch holds that origin does not (commits made, never pushed, before the
// session began: a resumed session) is in the session's range: the base is origin's position, not
// the HEAD the session started at, and the Stop reports a failure over those commits.
func TestT056_09_UnpushedCommitsFromBeforeTheSessionAreInItsRange(t *testing.T) {
	e := harness.New(t, harness.WithoutShippedFileGuards(), harness.KeepOrigin())
	proj := e.Project()
	e.GitInit(proj)
	e.FileGuard(proj, "docs", judgeRule, map[string]string{"rubric.md.j2": rubric})
	e.CommitAll(proj, "the rule")
	e.InstallJudgeClaudeCapturing(proj, promptFile, `{"pass": false, "reasoning": "the date is not in the sources"}`)
	const sess = "s-056-09"
	origin := e.Git(proj, "rev-parse", "origin/main")
	e.WriteFile(proj, "docs/old.md", "written before the session")
	e.CommitAll(proj, "old: unpushed, before the session")
	start := e.Git(proj, "rev-parse", "HEAD")
	if start == origin {
		t.Fatal("premise: the pre-session commit is not ahead of origin/main")
	}

	e.Run(proj, sess, "continue", Turns("done", Bash("b1", "true")))
	if got := e.Git(proj, "rev-parse", "origin/main"); got != origin {
		t.Fatalf("the harness moved origin/main (%s) although KeepOrigin was set", got)
	}
	rs := ranges(t, e, proj, sess)
	if len(rs) != 1 || rs[0].Base != origin {
		t.Fatalf("the range does not start at origin/main (%s), so the unpushed commit is outside it: %+v", origin, rs)
	}
	if joined := strings.Join(e.BlockingErrorsFrom(proj, sess, "Stop"), "\n"); !strings.Contains(joined, failedText) {
		t.Fatalf("the Stop did not report the failing verdict on the unpushed pre-session commit:\n%s", joined)
	}
}

// T056_02: the Stop reports failures only. A range nobody has judged passes it, silently: it is
// CI's to refuse (and the push gate's, if enabled), and the Stop asked no model.
// sr:proves session/tracked-ranges-verified-from-recorded-results
func TestT056_02_AnUnjudgedRangePassesTheStopSilently(t *testing.T) {
	e, proj := project(t)
	const sess = "s-056-02"

	res := e.Run(proj, sess, "write the doc", Turns("done", harness.CommitFile("c1", "docs/a.md", "the release is Friday", "add a")))

	if got := e.AllBlockingErrorsFrom(proj, sess, "Stop"); len(got) != 0 {
		t.Fatalf("an unjudged tracked range was refused at Stop:\n%s", strings.Join(got, "\n"))
	}
	if strings.Contains(res.Output, "not judged yet") {
		t.Fatalf("an unjudged range was reported at Stop:\n%s", res.Output)
	}
	if n := e.JudgeCalls(proj, promptFile, ""); n != 0 {
		t.Fatalf("the Stop asked the judge (%d calls): it must only verify", n)
	}
}

// T056_02 (b): a stored FAIL is still refused at Stop, with its reason.
// sr:proves session/tracked-ranges-verified-from-recorded-results
func TestT056_02_AStoredFailStillRefusesTheStop(t *testing.T) {
	e, proj := failingProject(t)
	const sess = "s-056-02b"

	e.Run(proj, sess, "write the doc", Turns("done", harness.CommitFile("c1", "docs/a.md", "the release is Friday", "add a")))

	joined := strings.Join(e.BlockingErrorsFrom(proj, sess, "Stop"), "\n")
	if !strings.Contains(joined, failedText) {
		t.Fatalf("a range with a stored FAIL was not refused at Stop with its reason:\n%s", joined)
	}
	if strings.Contains(joined, "not judged yet") {
		t.Fatalf("the refusal reports an unjudged key:\n%s", joined)
	}
}

// T056_02 (c): what the Stop lets through unjudged, the push gate (when enabled) refuses.
func TestT056_02_ThePrePushGateStillRefusesTheUnjudgedRange(t *testing.T) {
	e, proj := project(t, harness.WithEnabledShipped("sloprail/gate/verify-before-push"))
	const sess = "s-056-02c"

	e.Run(proj, sess, "write the doc and push it", Turns("done",
		harness.CommitFile("c1", "docs/a.md", "the release is Friday", "add a"),
		Bash("p1", "git push -q origin HEAD:refs/heads/work"),
	))

	if got := e.Git(proj, "ls-remote", "origin", "refs/heads/work"); strings.TrimSpace(got) != "" {
		t.Fatalf("an unjudged range was pushed past the push gate: %s", got)
	}
	if got := e.AllBlockingErrorsFrom(proj, sess, "Stop"); len(got) != 0 {
		t.Fatalf("the Stop refused the unjudged range the gate refused:\n%s", strings.Join(got, "\n"))
	}
}

// T056_03: a range the agent untracks, with a reason, is not verified; it stays listed with the
// reason, and automatic tracking does not bring it back while its tip does not move.
// sr:proves session/untracked-range-returns-when-tip-moves
func TestT056_03_AnUntrackedRangeIsNotVerifiedAndStaysListed(t *testing.T) {
	e, proj := failingProject(t)
	const sess = "s-056-03"
	e.Run(proj, sess, "write the doc", Turns("done", harness.CommitFile("c1", "docs/a.md", "the release is Friday", "add a")))
	if got := strings.Join(e.BlockingErrorsFrom(proj, sess, "Stop"), "\n"); !strings.Contains(got, failedText) {
		t.Fatalf("premise: the failing commit is not refused at Stop:\n%s", got)
	}

	if r := refs(e, proj, sess, "untrack"); r.Code == 0 {
		t.Fatalf("untrack without a reason succeeded:\n%s", r.Output)
	}
	if r := refs(e, proj, sess, "untrack", "--reason", "this branch is the user's own work, not mine"); r.Code != 0 {
		t.Fatalf("untrack: exit %d:\n%s", r.Code, r.Output)
	}

	before := len(e.BlockingErrorsFrom(proj, sess, "Stop"))
	e.Run(proj, sess, "anything else?", Turns("done", Bash("b2", "true")))
	if got := e.BlockingErrorsFrom(proj, sess, "Stop"); len(got) != before {
		t.Fatalf("an untracked range, its tip unmoved, was verified at Stop:\n%s", strings.Join(got[before:], "\n"))
	}
	rs := ranges(t, e, proj, sess)
	if len(rs) != 1 || rs[0].Tracked() || !strings.Contains(rs[0].UntrackedReason, "the user's own work") {
		t.Fatalf("the untracked range is not listed with its reason: %+v", rs)
	}
}

// T056_03 (b): an untrack is no escape: the range is tracked again, automatically, once the
// branch's tip moves.
// sr:proves session/untracked-range-returns-when-tip-moves
func TestT056_03_AnUntrackedRangeIsTrackedAgainWhenItsTipMoves(t *testing.T) {
	e, proj := project(t)
	const sess = "s-056-03b"
	e.Run(proj, sess, "start", Turns("done", Bash("b1", "true")))
	if r := refs(e, proj, sess, "untrack", "--reason", "the user's own work"); r.Code != 0 {
		t.Fatalf("untrack: exit %d:\n%s", r.Code, r.Output)
	}
	e.Run(proj, sess, "more", Turns("done", harness.CommitFile("c1", "docs/a.md", "the release is Friday", "add a")))
	rs := ranges(t, e, proj, sess)
	if len(rs) != 1 || !rs[0].Tracked() {
		t.Fatalf("a moved tip did not track the untracked range again: %+v", rs)
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
	// A base that is the head (or after it) would empty the range: refused, that is an untrack.
	e.Run(proj, sess, "work", Turns("done", harness.CommitFile("c1", "docs/a.md", "the release is Friday", "add a")))
	if r := refs(e, proj, sess, "track", "--base", e.Git(proj, "rev-parse", "HEAD")); r.Code == 0 {
		t.Fatalf("a base equal to the head emptied the range without an untrack reason:\n%s", r.Output)
	}
	if r := refs(e, proj, sess, "track", "--base", head); r.Code != 0 {
		t.Fatalf("track: exit %d:\n%s", r.Code, r.Output)
	}
	rs := ranges(t, e, proj, sess)
	if len(rs) != 1 || !rs[0].Tracked() || rs[0].Base != head || rs[0].AddedBy != sessionstate.RangeAgent {
		t.Fatalf("the agent's range did not replace the automatic one: %+v", rs)
	}
}
