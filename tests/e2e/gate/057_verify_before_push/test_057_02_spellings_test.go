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
		"git update-ref refs/heads/sloprail/checks HEAD",
		"git branch -f sloprail/checks HEAD",
		"git branch -D sloprail/checks",
		"git -C " + proj + " update-ref -d refs/heads/sloprail/checks",
		"echo deadbeef > .git/refs/heads/sloprail/checks",
	} {
		res := e.Run(proj, "s-057-03", "write it", Turns("done", Bash("w"+string(rune('a'+i)), cmd)))
		if !res.Refused() || !(res.Saw("checks-ref-sr-only") || res.Saw("verify-before-push")) {
			t.Fatalf("%q was not refused:\n%s", cmd, res.Output)
		}
	}
	for i, cmd := range []string{"git log --oneline sloprail/checks", "git rev-parse sloprail/checks", "sr-checks status"} {
		res := e.Run(proj, "s-057-03", "read it", Turns("done", Bash("r"+string(rune('a'+i)), cmd)))
		if strings.Contains(res.Output, "checks-ref-sr-only") {
			t.Fatalf("%q was refused by the results-branch gate:\n%s", cmd, res.Output)
		}
	}
}
