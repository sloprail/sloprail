package e2e

import (
	"os/exec"
	"testing"
)

// T003_46: a commit that was only CHECKED OUT (another pull request's, fetched during the
// session) is never the session's work: nothing about it is judged.
func TestT003_46_ACommitOnlyCheckedOutDetachedIsNotJudged(t *testing.T) {
	e, proj, _ := project(t, docsRule)
	main := e.Git(proj, "branch", "--show-current")

	// Somebody else's violating commit, made in a clone; the session fetches it.
	other := e.Project()
	if out, err := exec.Command("git", "clone", "-q", proj, other+"/clone").CombinedOutput(); err != nil {
		t.Fatalf("clone: %v\n%s", err, out)
	}
	clone := other + "/clone"
	e.WriteFile(clone, "docs/other.md", "FORBIDDEN words from another PR")
	e.Git(clone, "add", "-A")
	e.Git(clone, "-c", "user.email=a@b.c", "-c", "user.name=other", "commit", "-q", "-m", "another PR's violation")

	e.Run(proj, "s-003-46", "review another PR", Turns("done",
		Bash("b1", "git fetch -q "+clone+" HEAD:refs/remotes/origin/other-pr"),
		Bash("b2", "git switch -q --detach origin/other-pr"),
		Bash("b3", "git switch -q "+main),
	))
	if got := stopRefusals(e, proj, "s-003-46"); got != "" {
		t.Fatalf("a commit the session only checked out was judged:\n%s", got)
	}
}
