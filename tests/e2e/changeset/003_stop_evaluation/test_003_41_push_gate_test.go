package e2e

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T003_41: `git push` is judged before it runs. Commits a file-guard refuses do not leave
// the machine; once fixed, the same push goes through.
func TestT003_41_APushOfRefusedCommitsIsBlockedThenAllowedOnceFixed(t *testing.T) {
	e, proj, _ := project(t, docsRule)
	bare := e.Project()
	if out, err := exec.Command("git", "-C", bare, "init", "-q", "--bare").CombinedOutput(); err != nil {
		t.Fatalf("bare init: %v\n%s", err, out)
	}
	e.Git(proj, "remote", "add", "origin", bare)
	pushed := func() bool {
		return exec.Command("git", "-C", bare, "rev-parse", "--verify", "-q", "refs/heads/work").Run() == nil
	}

	e.Run(proj, "s-003-41", "commit and push", Turns("done",
		harness.CommitFile("c1", "docs/a.md", "FORBIDDEN words", "add a"),
		Bash("p1", "git push -q origin HEAD:refs/heads/work"),
	))
	if pushed() {
		t.Fatal("commits a file-guard refuses were pushed")
	}
	if got := stopRefusals(e, proj, "s-003-41"); !strings.Contains(got, refusalText) {
		t.Fatalf("premise: the refused commit should also be refused at Stop:\n%s", got)
	}

	e.Run(proj, "s-003-41", "fix and push", Turns("done",
		harness.CommitFile("c2", "docs/a.md", "clean words", "fix a"),
		Bash("p2", "git push -q origin HEAD:refs/heads/work"),
	))
	if !pushed() {
		t.Fatal("the fixed commits were not pushed")
	}
}
