package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// The refusal for work whose branch is gone has to be something the agent can act on without a
// hint: it says whose work, where it is now, which files, and names a command that works.

func worktreeOf(command string) string {
	_, rest, _ := strings.Cut(command, "worktree add ")
	return strings.Trim(strings.Fields(rest)[0], "'")
}

// T003_73: work that already LANDED on the default branch, its branch deleted. The branch is no
// place to fix it: the refusal says it is on origin/main (naming the squash commit that carries
// it), what to do (a branch from origin/main, the real fix, a commit, stop again), and, for a
// finding that is only a missing citation, to ask the user NOW. The command it names works; once
// main no longer holds the refused content the work is settled as superseded.
func TestT003_73_ALandedAndDeletedTipSaysItIsOnMainAndHowToFixItThere(t *testing.T) {
	e, proj, _ := project(t, docsRule)
	main := e.Git(proj, "branch", "--show-current")
	e.Git(proj, "update-ref", "refs/remotes/origin/main", "HEAD")
	const sess = "s-003-73"

	e.Run(proj, sess, "merge it and delete the branch", Turns("done", squashedAndDeleted("feat", main)...))
	got := stopRefusals(e, proj, sess)
	for _, want := range []string{
		"already on origin/main", "carried by", "squash feat", "docs/a.md", "docs/b.md",
		"create a branch from origin/main", "-b fix/feat origin/main", "supersedes the landed content",
		"AskUserQuestion", `"keep it"`, `"revert it"`, "do not park it",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("the refusal lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "worktree add <path>") || strings.Contains(got, "'feat'`") {
		t.Fatalf("the refusal tells the agent to check out a branch that is gone:\n%s", got)
	}

	fix := commandIn(t, got, "worktree add", "-b fix/feat")
	runCommand(t, proj, fix)
	fixTree := worktreeOf(fix)
	e.WriteFile(fixTree, "docs/a.md", "clean words\n")
	e.WriteFile(fixTree, "docs/b.md", "clean words\n")
	fixed := e.CommitAll(fixTree, "the real fix")
	blocks := stopBlocks(e, proj, sess)

	// The fix lands on main: nothing of the refused content stands, so the old work is settled.
	e.Git(proj, "update-ref", "refs/remotes/origin/main", fixed)
	e.Run(proj, sess, "stop", Turns("done", Bash("n1", "true")))
	if n := stopBlocks(e, proj, sess); n > blocks+1 {
		t.Fatalf("the landed work was still refused after main held the fix:\n%s", newBlocks(e, proj, sess, blocks))
	}
	if status := e.ChecksStatus(proj, sess); !strings.Contains(status, "superseded") {
		t.Fatalf("the settled landed work does not say it was superseded:\n%s", status)
	}
}

// T003_73: work on a branch that is gone and did NOT land: the refusal says it is on no branch, that
// its commits are pinned, and names the command that restores it from the pinned tip.
func TestT003_73_AnUnlandedDeletedTipSaysHowToRestoreItFromThePin(t *testing.T) {
	e, proj, _ := project(t, docsRule)
	main := e.Git(proj, "branch", "--show-current")
	const sess = "s-003-73b"

	e.Run(proj, sess, "commit and delete", Turns("done",
		Bash("b1", "git switch -q -c side"),
		harness.CommitFile("c1", "docs/a.md", "FORBIDDEN words", "add a"),
		Bash("b2", "git switch -q "+main),
		Bash("b3", "git branch -D side"),
		Bash("b4", "git reflog expire --expire=now --all && git gc -q --prune=now"),
	))
	got := stopRefusals(e, proj, sess)
	for _, want := range []string{"on no branch any more", "pinned", "restore the branch from the pinned tip", "-b side ", "AskUserQuestion", "docs/a.md"} {
		if !strings.Contains(got, want) {
			t.Fatalf("the refusal lacks %q:\n%s", want, got)
		}
	}
	restore := commandIn(t, got, "worktree add", "-b side")
	runCommand(t, proj, restore)
	tree := worktreeOf(restore)
	blocks := stopBlocks(e, proj, sess)
	e.WriteFile(tree, "docs/a.md", "clean words\n")
	e.CommitAll(tree, "fix a")
	if r := e.StopNow(proj, sess, false); harness.Blocked(r) {
		t.Fatalf("the restored and fixed work was still refused (after %d blocks):\n%s", blocks, r.Output)
	}
}
