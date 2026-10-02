package e2e

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

func spellingsSetup(t *testing.T) (*Env, string, string) {
	e, proj, _ := project(t, docsRule)
	bare := e.Origin(proj)
	e.Run(proj, "s-057-02", "commit", Turns("done", harness.CommitFile("c1", "docs/a.md", "FORBIDDEN words", "add a")))
	return e, proj, bare
}

func reached(bare, ref string) bool {
	return exec.Command("git", "-C", bare, "rev-parse", "--verify", "-q", ref).Run() == nil
}

// T057_02: every spelling of a push of unjudged commits is refused, naming the command that
// judges them; none of them reaches the remote.
func TestT057_02_EverySpellingOfAPushIsRefusedAndNamesTheRunCommand(t *testing.T) {
	e, proj, bare := spellingsSetup(t)
	for i, cmd := range []string{
		"git push origin HEAD:refs/heads/work",
		"git -C " + proj + " push origin HEAD:refs/heads/work",
		"cd /tmp && git -C " + proj + " push -q origin HEAD:refs/heads/work",
		"GIT_TERMINAL_PROMPT=0 git push origin HEAD:refs/heads/work",
		"bash -c 'git push origin HEAD:refs/heads/work'",
		"git push --all origin",
		"git push --force-with-lease origin HEAD:refs/heads/work",
	} {
		res := e.Run(proj, "s-057-02", "push it", Turns("done", Bash("p"+string(rune('a'+i)), cmd)))
		if !res.Refused() || !res.Saw("verify-before-push") || !res.Saw("sr-checks run --base") {
			t.Fatalf("%q was not refused with the run command:\n%s", cmd, res.Output)
		}
		if reached(bare, "refs/heads/work") {
			t.Fatalf("%q reached the remote", cmd)
		}
	}
}

// T057_02b: a push whose refs cannot be resolved, or that rides with a command that moves refs, is
// refused (fail closed); a push that sends nothing is not.
func TestT057_02_AnUnresolvablePushFailsClosed(t *testing.T) {
	e, proj, _ := spellingsSetup(t)
	for i, cmd := range []string{
		"git push origin nosuchbranch:refs/heads/x",
		"git commit --allow-empty -m more && git push origin HEAD:refs/heads/work",
	} {
		res := e.Run(proj, "s-057-02", "push it", Turns("done", Bash("f"+string(rune('a'+i)), cmd)))
		if !res.Refused() || !res.Saw("verify-before-push") {
			t.Fatalf("%q was not refused:\n%s", cmd, res.Output)
		}
	}
	res := e.Run(proj, "s-057-02", "not a push", Turns("done", Bash("ok", "git status && echo push")))
	if res.Saw("verify-before-push") {
		t.Fatalf("a command that pushes nothing was refused:\n%s", res.Output)
	}
}

// T057_03: only sr-checks writes the sloprail/checks results branch: an agent cannot push it, move
// it, delete it or write it by hand, and can still read it and run sr-checks.
func TestT057_03_OnlySrChecksWritesTheResultsBranch(t *testing.T) {
	e, proj, _ := spellingsSetup(t)
	e.Run(proj, "s-057-03", "judge", Turns("done",
		Bash("j", "sr-checks run --base "+e.Git(proj, "rev-list", "--max-parents=0", "HEAD")+" --head HEAD")))
	for i, cmd := range []string{
		"git push origin sloprail/checks",
		"git push origin HEAD:refs/heads/sloprail/checks",
		"git push --all origin",
		"git push --mirror origin",
		"git update-ref refs/heads/sloprail/checks HEAD",
		"git branch -f sloprail/checks HEAD",
		"git branch -D sloprail/checks",
		"git -C " + proj + " update-ref -d refs/heads/sloprail/checks",
		"echo deadbeef > .git/refs/heads/sloprail/checks",
		// the ref sr-checks really keeps, and the forms that reach it
		"git update-ref refs/sloprail/checks HEAD",
		"git update-ref -d refs/sloprail/checks",
		"git push origin HEAD:refs/sloprail/checks",
		"echo deadbeef > .git/refs/sloprail/checks",
		// forms that write the ref without naming it
		"printf 'update refs/sloprail/checks HEAD\\n' | git update-ref --stdin",
		"git fetch origin '+refs/*:refs/*'",
		"git fetch origin '+refs/sloprail/*:refs/sloprail/*'",
		"git push origin 'refs/*:refs/*'",
	} {
		res := e.Run(proj, "s-057-03", "write it", Turns("done", Bash("w"+string(rune('a'+i)), cmd)))
		if !res.Refused() || !(res.Saw("checks-ref-sr-only") || res.Saw("verify-before-push")) {
			t.Fatalf("%q was not refused:\n%s", cmd, res.Output)
		}
	}
	// a file write straight into the ref's storage is refused too
	for i, path := range []string{".git/refs/sloprail/checks", ".git/logs/refs/sloprail/checks", ".git/packed-refs"} {
		res := e.Run(proj, "s-057-03", "forge it", Turns("done", harness.Write("f"+string(rune('a'+i)), path, "deadbeef\n")))
		if !res.Refused() || !res.Saw("checks-ref-sr-only") {
			t.Fatalf("a write to %s was not refused by the results-branch gate:\n%s", path, res.Output)
		}
	}
	for i, cmd := range []string{"git log --oneline sloprail/checks", "git rev-parse refs/sloprail/checks", "sr-checks show --base HEAD --head HEAD"} {
		res := e.Run(proj, "s-057-03", "read it", Turns("done", Bash("r"+string(rune('a'+i)), cmd)))
		if strings.Contains(res.Output, "checks-ref-sr-only") {
			t.Fatalf("%q was refused by the results-branch gate:\n%s", cmd, res.Output)
		}
	}
}

// T057_04: results `sr-checks run` stored while the remote was unreachable are local only, and verify (and
// CI) read the remote's copy: the gated push is refused, naming the run command that retries the push; once
// the run has pushed them, the same push goes through.
func TestT057_04_APushWithUnpushedResultsIsRefused(t *testing.T) {
	e, proj, _ := project(t, docsRule)
	origin := e.Origin(proj)
	base := e.Git(proj, "rev-list", "--max-parents=0", "HEAD")
	run := "sr-checks run --base " + base + " --head HEAD"
	push := "git push -q origin HEAD:refs/heads/work"
	res := e.Run(proj, "s-057-04", "judge while offline, then push", Turns("done",
		harness.CommitFile("c1", "docs/a.md", "clean words", "add a"),
		Bash("off", "git remote set-url origin /nonexistent/remote.git"),
		Bash("j1", run),
		Bash("on", "git remote set-url origin "+origin),
		Bash("p1", push),
	))
	if !res.Refused() || !res.Saw("results not pushed") || !res.Saw("sr-checks run --base") {
		t.Fatalf("a push with unpushed results was not refused with the retry command:\n%s", res.Output)
	}
	if reached(origin, "refs/heads/work") {
		t.Fatal("the refused push reached the remote")
	}
	e.Run(proj, "s-057-04", "retry the push of the results, then push", Turns("done",
		Bash("j2", run),
		Bash("p2", push),
	))
	if !reached(origin, "refs/heads/work") {
		t.Fatal("the push was refused after the run had pushed the results")
	}
}
