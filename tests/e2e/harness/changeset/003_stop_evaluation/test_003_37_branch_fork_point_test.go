package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T003_37: a branch cut from an upstream that advanced since the session began is judged
// from where it was cut: the upstream commits (here one violating the rule) are not
// blamed on the agent, while its own violating commit on that branch is.
func TestT003_37_AnUpstreamCommitBeforeTheBranchWasCutIsNotBlamed(t *testing.T) {
	e, proj, _ := project(t, docsRule)
	main := e.Git(proj, "branch", "--show-current")
	// origin/main advances after the session start (the session begins at the current main).
	e.Git(proj, "switch", "-q", "-c", "tmp-upstream")
	e.WriteFile(proj, "docs/up.md", "FORBIDDEN upstream words")
	e.CommitAll(proj, "upstream violation")
	e.Git(proj, "update-ref", "refs/remotes/origin/main", "HEAD")
	e.Git(proj, "switch", "-q", main)
	e.Git(proj, "branch", "-D", "tmp-upstream")

	e.Run(proj, "s-003-37a", "cut from upstream", Turns("done",
		Bash("b1", "git switch -q -c feat-a refs/remotes/origin/main"),
		harness.CommitFile("c1", "docs/own.md", "clean words", "own clean work"),
		Bash("b2", "git switch -q "+main),
	))
	if got := stopRefusals(e, proj, "s-003-37a"); got != "" {
		t.Fatalf("upstream commits were blamed on the branch:\n%s", got)
	}
	blocks := stopBlocks(e, proj, "s-003-37a")

	e.Run(proj, "s-003-37a", "more", Turns("done",
		Bash("b3", "git switch -q feat-a"),
		harness.CommitFile("c2", "docs/own2.md", "FORBIDDEN own words", "own violation"),
		Bash("b4", "git switch -q "+main),
	))
	got := newBlocks(e, proj, "s-003-37a", blocks)
	if !strings.Contains(got, "feat-a") || !strings.Contains(got, "docs/own2.md") || strings.Contains(got, "docs/up.md") {
		t.Fatalf("want the agent's own violation on feat-a named and not the upstream one:\n%s", got)
	}
}
