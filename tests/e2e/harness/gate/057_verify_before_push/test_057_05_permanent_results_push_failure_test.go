package e2e

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T057_05: a results push that fails PERMANENTLY (the remote rejects refs/heads/sloprail/checks) while
// the code push would work keeps the gate refusing, every time: the gate never permits a push whose
// results are unpushed (no counter, nothing the agent can write to get past it). It quotes the actual
// push error and says this is an environment problem the USER must fix (push themselves, or disable
// the gate in .sloprail/config.yaml).
func TestT057_05_APermanentlyFailingResultsPushKeepsRefusingAndNamesTheUserFix(t *testing.T) {
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
	if !res.Saw("environment problem") || !res.Saw("USER") || !res.Saw(".sloprail/config.yaml") {
		t.Fatalf("the refusal does not say the user must fix the environment or disable the gate:\n%s", res.Output)
	}

	for i, name := range []string{"retry", "retry again", "and again"} {
		res = e.Run(proj, "s-057-05", name, Turns("done", Bash("j"+string(rune('2'+i)), run), Bash("p"+string(rune('2'+i)), push)))
		if !res.Refused() || !res.Saw("results not pushed") {
			t.Fatalf("push %d (after a failed retry) must still be refused:\n%s", i+2, res.Output)
		}
		if reached(origin, "refs/heads/work") {
			t.Fatalf("a refused push reached the remote on try %d", i+2)
		}
	}
}
