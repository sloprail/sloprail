package e2e

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// pushSetup is a project with a bare `origin`, and a probe of whether `work` reached it.
func pushSetup(t *testing.T, config string) (*Env, string, func() bool) {
	e, proj, _ := project(t, docsRule)
	bare := e.Project()
	if out, err := exec.Command("git", "-C", bare, "init", "-q", "--bare").CombinedOutput(); err != nil {
		t.Fatalf("bare init: %v\n%s", err, out)
	}
	e.Git(proj, "remote", "add", "origin", bare)
	if config != "" {
		e.WriteFile(proj, ".sloprail/config.yaml", config)
		e.CommitAll(proj, "configure the push gate")
	}
	return e, proj, func() bool {
		return exec.Command("git", "-C", bare, "rev-parse", "--verify", "-q", "refs/heads/work").Run() == nil
	}
}

// T003_41: the push gate is OPT-IN (`enabled: [sloprail/gate/judge-before-push]` in .sloprail/config.yaml). Off, a
// push goes through (the Stop still judges the commits). On, commits a file-guard refuses
// do not leave the machine; once fixed, the same push goes through.
func TestT003_41_ThePushGateIsOffByDefault(t *testing.T) {
	e, proj, pushed := pushSetup(t, "")
	e.Run(proj, "s-003-41a", "commit and push", Turns("done",
		harness.CommitFile("c1", "docs/a.md", "FORBIDDEN words", "add a"),
		Bash("p1", "git push -q origin HEAD:refs/heads/work"),
	))
	if !pushed() {
		t.Fatal("with the push gate off, the push was blocked")
	}
	if got := stopRefusals(e, proj, "s-003-41a"); !strings.Contains(got, refusalText) {
		t.Fatalf("the Stop must still judge the pushed commit:\n%s", got)
	}
}

func TestT003_41_APushOfRefusedCommitsIsBlockedThenAllowedOnceFixedWhenEnabled(t *testing.T) {
	e, proj, pushed := pushSetup(t, "enabled:\n  - sloprail/gate/judge-before-push\n")
	e.Run(proj, "s-003-41b", "commit and push", Turns("done",
		harness.CommitFile("c1", "docs/a.md", "FORBIDDEN words", "add a"),
		Bash("p1", "git push -q origin HEAD:refs/heads/work"),
	))
	if pushed() {
		t.Fatal("commits a file-guard refuses were pushed with the push gate on")
	}
	if got := stopRefusals(e, proj, "s-003-41b"); !strings.Contains(got, refusalText) {
		t.Fatalf("premise: the refused commit should also be refused at Stop:\n%s", got)
	}

	e.Run(proj, "s-003-41b", "fix and push", Turns("done",
		harness.CommitFile("c2", "docs/a.md", "clean words", "fix a"),
		Bash("p2", "git push -q origin HEAD:refs/heads/work"),
	))
	if !pushed() {
		t.Fatal("the fixed commits were not pushed")
	}
}
