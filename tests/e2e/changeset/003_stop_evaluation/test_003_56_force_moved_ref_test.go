package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T003_56: a force-move of a ref is not a commit. The session committed on branch X (clean,
// judged), and later reused the name: `git branch -f X foreign`, a branch somebody else made
// before the session began. X now points at commits the session never made, so Stop must not
// judge them as "this session committed on X" - only commit-type reflog entries of the
// folder claim a commit, never a ref that was moved onto it.
func TestT003_56_AForceMovedRefDoesNotClaimWhatItNowPointsAt(t *testing.T) {
	e, proj, _ := project(t, docsRule)
	main := e.Git(proj, "branch", "--show-current")

	// Somebody else's branch, with a commit the rule refuses, made before the session.
	e.Git(proj, "switch", "-q", "-c", "foreign")
	e.WriteFile(proj, "docs/foreign.md", "FORBIDDEN words\n")
	e.CommitAll(proj, "somebody else adds foreign")
	e.Git(proj, "switch", "-q", main)

	e.Run(proj, "s-003-56", "do the work, then reuse the branch name", Turns("done",
		Bash("b1", "git switch -q -c x"),
		harness.CommitFile("c1", "docs/x.md", "clean words", "add x"),
		Bash("b2", "git switch -q "+main),
		Bash("b3", "git branch -f x foreign"),
	))
	if got := stopRefusals(e, proj, "s-003-56"); strings.Contains(got, refusalText) || strings.Contains(got, "committed on it") {
		t.Fatalf("a ref force-moved onto commits the session never made was judged as the session's work:\n%s", got)
	}
}

// T003_56: the same ref, moved by a plain commit on top of what the session recorded, is
// still the session's (the claim follows the commits, not the name).
func TestT003_56_ACommitOnTopOfARecordedRefIsStillJudged(t *testing.T) {
	e, proj, _ := project(t, docsRule)
	main := e.Git(proj, "branch", "--show-current")

	e.Run(proj, "s-003-56b", "commit twice on x, leave it", Turns("done",
		Bash("b1", "git switch -q -c x"),
		harness.CommitFile("c1", "docs/x.md", "clean words", "add x"),
		harness.CommitFile("c2", "docs/x.md", "FORBIDDEN words", "break x"),
		Bash("b2", "git switch -q "+main),
	))
	if got := stopRefusals(e, proj, "s-003-56b"); !strings.Contains(got, refusalText) {
		t.Fatalf("a branch the session committed on was not judged:\n%s", got)
	}
}
