package e2e

import (
	"os/exec"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T057_08b: the push gate reads the commits and the stored check results, never the session's
// tracked refs, so it refuses an unverified range with SR_AUTO_WATCH_GIT_REFS unset (nothing
// watched), and lets the same push through once the range is judged clean.
func TestT057_08_ThePushGateRefusesAnUnverifiedRangeWithNothingWatched(t *testing.T) {
	e := harness.New(t, harness.WithoutShippedFileGuards(), harness.WithoutAutoWatch(), harness.WithEnabledShipped(pushGate))
	proj := e.Project()
	e.GitInit(proj)
	e.FileGuard(proj, "docs", docsRule, map[string]string{"check.sh": recorder("")})
	e.CommitAll(proj, "the rule")
	bare := e.Origin(proj)
	pushed := func() bool {
		return exec.Command("git", "-C", bare, "rev-parse", "--verify", "-q", "refs/heads/work").Run() == nil
	}

	e.Run(proj, "s-057-08b", "commit and push", Turns("done",
		harness.CommitFile("c1", "docs/a.md", "clean words", "add a"),
		Bash("p1", "git push -q origin HEAD:refs/heads/work"),
	))
	if pushed() {
		t.Fatal("an unverified range was pushed with nothing watched: the gate must not depend on the session's tracked refs")
	}

	e.Run(proj, "s-057-08b", "judge and push", Turns("done",
		Bash("j2", "sr-checks run --base "+e.Git(proj, "rev-list", "--max-parents=0", "HEAD")+" --head HEAD"),
		Bash("p2", "git push -q origin HEAD:refs/heads/work"),
	))
	if !pushed() {
		t.Fatal("the verified range was not pushed")
	}
}
