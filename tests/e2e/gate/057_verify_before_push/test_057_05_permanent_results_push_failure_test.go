package e2e

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T057_05: a results push that fails PERMANENTLY (the remote rejects refs/heads/sloprail/checks) while
// the code push would work must not trap the agent. The pending-results refusal names the actual push
// error and says to retry; after one failed retry it refuses once more; then the push is permitted (the gate warns on
// stderr that CI verify will say "not judged" until the results are pushed; `sr-checks run` says so too).
func TestT057_05_APermanentlyFailingResultsPushIsRefusedRetriedThenPermittedWithAWarning(t *testing.T) {
	e, proj, _ := project(t, docsRule)
	origin := e.Origin(proj)
	hook := "#!/bin/sh\nwhile read old new ref; do\n  case \"$ref\" in refs/heads/sloprail/checks) echo \"results branch locked by policy\" >&2; exit 1 ;; esac\ndone\nexit 0\n"
	if err := os.WriteFile(filepath.Join(origin, "hooks", "pre-receive"), []byte(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	base := e.Git(proj, "rev-list", "--max-parents=0", "HEAD")
	run := "sr-checks run --base " + base + " --head HEAD"
	push := "git push -q origin HEAD:refs/heads/work"

	res := e.Run(proj, "s-057-05", "judge, then push", Turns("done",
		harness.CommitFile("c1", "docs/a.md", "clean words", "add a"),
		Bash("j1", run),
		Bash("p1", push),
	))
	if !res.Refused() || !res.Saw("results not pushed") || !res.Saw("results branch locked by policy") {
		t.Fatalf("first push: not refused with the actual push error:\n%s", res.Output)
	}
	if reached(origin, "refs/heads/work") {
		t.Fatal("the refused push reached the remote")
	}

	res = e.Run(proj, "s-057-05", "retry once", Turns("done", Bash("j2", run), Bash("p2", push)))
	if !res.Refused() || !res.Saw("results not pushed") {
		t.Fatalf("second push (after a failed retry) must be refused once more:\n%s", res.Output)
	}
	if reached(origin, "refs/heads/work") {
		t.Fatal("the second refused push reached the remote")
	}

	res = e.Run(proj, "s-057-05", "retry again", Turns("done", Bash("j3", run), Bash("p3", push)))
	if !reached(origin, "refs/heads/work") {
		t.Fatalf("the loop breaker did not permit the push:\n%s", res.Output)
	}
}
