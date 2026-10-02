package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// The refusal for work whose branch is gone has to be something the agent can act on without a
// hint: it says which branch, which files, and names commands that work.

// T003_73: work on a branch that is gone: the refusal says the branch is gone, that the range
// was verified at the commit it last pointed at, names the files, and names the commands that
// re-track or untrack it.
func TestT003_73_ADeletedBranchsRefusalSaysItIsGoneAndHowToTrackOrDropIt(t *testing.T) {
	e, proj, _ := project(t, docsRule)
	main := e.Git(proj, "branch", "--show-current")
	const sess = "s-003-73b"

	e.Run(proj, sess, "commit and delete", Turns("done",
		Bash("b1", "git switch -q -c side"),
		harness.CommitFile("c1", "docs/a.md", "FORBIDDEN words", "add a"),
		Bash("b2", "git switch -q "+main),
		Bash("b3", "git branch -D side"),
	))
	got := stopRefusals(e, proj, sess)
	for _, want := range []string{`The branch "side" is gone`, "verified at the commit it last pointed at", "sr-session refs track", "sr-session refs untrack", "docs/a.md"} {
		if !strings.Contains(got, want) {
			t.Fatalf("the refusal lacks %q:\n%s", want, got)
		}
	}
}

// T003_73: the commit the gone branch pointed at is also gone from the object store: the
// range cannot be read, and the refusal says so and names the way out (untrack, with a reason).
func TestT003_73_AnUnreadableTipOfADeletedBranchIsRefusedNamingUntrack(t *testing.T) {
	e, proj, _ := project(t, docsRule)
	main := e.Git(proj, "branch", "--show-current")
	const sess = "s-003-73c"

	e.Run(proj, sess, "commit, delete and gc", Turns("done",
		Bash("b1", "git switch -q -c side"),
		harness.CommitFile("c1", "docs/a.md", "FORBIDDEN words", "add a"),
		Bash("b2", "git switch -q "+main),
		Bash("b3", "git branch -D side"),
		Bash("b4", "git reflog expire --expire=now --all && git gc -q --prune=now"),
	))
	got := stopRefusals(e, proj, sess)
	if !strings.Contains(got, "cannot be read") || !strings.Contains(got, "sr-session refs untrack") {
		t.Fatalf("the refusal for an unreadable tip does not say so and name the way out:\n%s", got)
	}
}
