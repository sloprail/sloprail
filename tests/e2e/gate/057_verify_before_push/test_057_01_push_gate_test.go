package e2e

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// pushSetup is a project with a bare `origin`, and a probe of whether `work` reached it.
func pushSetup(t *testing.T, disable ...string) (*Env, string, func() bool) {
	e, proj, _ := project(t, docsRule)
	bare := e.Origin(proj)
	if len(disable) > 0 {
		// Merged into the harness's own disabled list, never overwriting it.
		e.DisablePluginGuardrail(proj, disable...)
		e.CommitAll(proj, "configure the push gate")
	}
	return e, proj, func() bool {
		return exec.Command("git", "-C", bare, "rev-parse", "--verify", "-q", "refs/heads/work").Run() == nil
	}
}

// T057_01: the push gate is ON by default and switched off with `disabled: [sloprail/gate/verify-before-push]`
// in .sloprail/config.yaml. Off, a push goes through (the Stop still judges the commits). On, commits a
// file-guard refuses (or nobody judged) do not leave the machine; once judged clean, the same push goes through.
func TestT057_01_ThePushGateCanBeDisabled(t *testing.T) {
	e, proj, pushed := pushSetup(t, "sloprail/gate/verify-before-push")
	e.Run(proj, "s-057-01a", "commit and push", Turns("done",
		harness.CommitFile("c1", "docs/a.md", "FORBIDDEN words", "add a"),
		Bash("p1", "git push -q origin HEAD:refs/heads/work"),
	))
	if !pushed() {
		t.Fatal("with the push gate disabled, the push was blocked")
	}
	if got := stopRefusals(e, proj, "s-057-01a"); !strings.Contains(got, refusalText) {
		t.Fatalf("the Stop must still judge the pushed commit:\n%s", got)
	}
}

func TestT057_01_APushOfRefusedCommitsIsBlockedThenAllowedOnceFixedByDefault(t *testing.T) {
	e, proj, pushed := pushSetup(t)
	e.Run(proj, "s-057-01b", "commit and push", Turns("done",
		harness.CommitFile("c1", "docs/a.md", "FORBIDDEN words", "add a"),
		Bash("p1", "git push -q origin HEAD:refs/heads/work"),
	))
	if pushed() {
		t.Fatal("commits a file-guard refuses were pushed with the push gate on")
	}
	if got := stopRefusals(e, proj, "s-057-01b"); !strings.Contains(got, refusalText) {
		t.Fatalf("premise: the refused commit should also be refused at Stop:\n%s", got)
	}

	e.Run(proj, "s-057-01b", "fix and push", Turns("done",
		harness.CommitFile("c2", "docs/a.md", "clean words", "fix a"),
		Bash("j2", "sr-checks run --base "+e.Git(proj, "rev-list", "--max-parents=0", "HEAD")+" --head HEAD"),
		Bash("p2", "git push -q origin HEAD:refs/heads/work"),
	))
	if !pushed() {
		t.Fatal("the fixed commits were not pushed")
	}
}

// A repository with no remote default branch (a first push to a new remote: no origin/main, no
// origin/HEAD) is not a dead end: the gate judges from the root commit, blocks what a file-guard
// refuses, and lets the same push through once the commits are judged clean.
func TestT057_01_ThePushGateWorksWithNoRemoteDefaultBranch(t *testing.T) {
	e, proj, pushed := pushSetup(t)
	e.Git(proj, "remote", "set-head", "origin", "-d")
	e.Git(proj, "update-ref", "-d", "refs/remotes/origin/main")
	e.Git(proj, "update-ref", "-d", "refs/remotes/origin/master")

	e.Run(proj, "s-057-01c", "commit and push", Turns("done",
		harness.CommitFile("c1", "docs/a.md", "FORBIDDEN words", "add a"),
		Bash("p1", "git push -q origin HEAD:refs/heads/work"),
	))
	if pushed() {
		t.Fatal("commits a file-guard refuses were pushed when the repo had no remote default branch")
	}

	e.Run(proj, "s-057-01c", "fix and push", Turns("done",
		harness.CommitFile("c2", "docs/a.md", "clean words", "fix a"),
		Bash("j2", "sr-checks run --base "+e.Git(proj, "rev-list", "--max-parents=0", "HEAD")+" --head HEAD"),
		Bash("p2", "git push -q origin HEAD:refs/heads/work"),
	))
	if !pushed() {
		t.Fatal("with no remote default branch the verified commits could not be pushed: the gate is a dead end")
	}
}

// With no remote default branch the push gate judges from the root commit: a base the agent
// recorded in the session's refs registry (`sr-session refs track --base`) does not narrow it.
func TestT057_01_ARegistryBaseDoesNotNarrowThePushGate(t *testing.T) {
	e, proj, pushed := pushSetup(t)
	e.Git(proj, "remote", "set-head", "origin", "-d")
	e.Git(proj, "update-ref", "-d", "refs/remotes/origin/main")
	e.Git(proj, "update-ref", "-d", "refs/remotes/origin/master")

	e.Run(proj, "s-057-01d", "commit, narrow, push", Turns("done",
		harness.CommitFile("c1", "docs/a.md", "FORBIDDEN words", "add a"),
		Bash("t1", "sr-session refs track --base HEAD"),
		Bash("p1", "git push -q origin HEAD:refs/heads/work"),
	))
	if pushed() {
		t.Fatal("an agent-recorded registry base narrowed the push gate: refused commits were pushed")
	}
}

// T057_08: a push from a folder that does not exist, or whose folder is a variable, is allowed (the
// file-guards at Stop and in CI are the guarantee); it is not a "could not check" refusal.
func TestT057_08_PushFromNonExistentOrUnknownFolderIsAllowed(t *testing.T) {
	e, proj, _ := pushSetup(t)
	res := e.Run(proj, "s-057-08", "probe", Turns("done",
		Bash("p1", "git -C "+proj+"/nope/repo push -q origin HEAD:refs/heads/x"),
		Bash("p2", "d=.; cd $d && git push -q origin HEAD:refs/heads/x"),
	))
	if strings.Contains(res.Output, "could not be checked") {
		t.Fatalf("a push from a missing/unknown folder was refused:\n%s", res.Output)
	}
}
