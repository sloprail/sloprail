package e2e

import (
	"os"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// The plugin's sloprail/gate/no-destroying-owed-work refuses a git command that destroys
// commits the session owes a judgement (or a fix): deleting the branch, force-moving it,
// removing its worktree, pruning the objects. It ships on by default, so these tests install
// nothing: the plugin's own gate fires.
var (
	Turns = harness.Turns
	Bash  = harness.Bash
)

func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}

const ruleYAML = "match: \"docs/**\"\nchecks:\n  - script: ./check.sh\n"

const checkSh = `#!/bin/sh
payload="$(cat)"
if printf '%s' "$payload" | jq -e 'any(.changeset.files[]; .newContent | contains("FORBIDDEN"))' >/dev/null; then
  echo '{"reason":"FORBIDDEN text in the docs"}'
  exit 1
fi
exit 0
`

// project is a repository with the docs rule committed and nothing else.
func project(t *testing.T) (*harness.Env, string) {
	t.Helper()
	e := harness.New(t, harness.WithoutShippedFileGuards())
	e.SetStopBlockCap(1)
	proj := e.Project()
	e.GitInit(proj)
	e.WriteFile(proj, "docs/seed.md", "seed\n")
	e.CommitAll(proj, "the project")
	e.FileGuard(proj, "docs", ruleYAML, map[string]string{"check.sh": checkSh})
	e.CommitAll(proj, "the rule")
	return e, proj
}

// leaveBranch commits content on a new branch `name` and goes back to the default branch,
// all inside one turn, so no Stop has judged it when the next command runs.
func leaveBranch(name, content string) []harness.Turn {
	return []harness.Turn{
		Bash("lb-"+name, "git switch -q -c "+name),
		harness.CommitFile("lc-"+name, "docs/"+name+".md", content, "add "+name),
		Bash("lm-"+name, "git switch -q -"),
	}
}

func refusedByGate(res harness.Result) bool {
	return res.Refused() && res.Saw("no-destroying-owed-work")
}

// T035_01: deleting a branch whose commits no rule has judged is refused, with the way
// forward (end the turn so Stop judges it); once the Stop passed, the same delete is allowed.
func TestT035_01_DeletingAnUnjudgedBranchIsRefusedThenAllowedOnceJudged(t *testing.T) {
	const sess = "s-035-01"
	e, proj := project(t)

	turns := append(leaveBranch("feat", "clean words"), Bash("d1", "git branch -D feat"))
	res := e.Run(proj, sess, "do the work and drop it", Turns("done", turns...))
	if !refusedByGate(res) || !res.Saw("end your turn") || !res.Saw("trajectory cite") {
		t.Fatalf("deleting a branch no rule had judged was not refused with the way forward:\n%s", res.Output)
	}
	if e.Git(proj, "branch", "--list", "feat") == "" {
		t.Fatalf("the refused delete went ahead")
	}

	// The turn ended, the Stop judged the branch and it passed: it is nobody's debt now.
	res = e.Run(proj, sess, "now drop it", Turns("done", Bash("d2", "git branch -D feat")))
	if res.Saw("no-destroying-owed-work") {
		t.Fatalf("the delete was refused after the branch was judged and passed:\n%s", res.Output)
	}
	if e.Git(proj, "branch", "--list", "feat") != "" {
		t.Fatalf("the allowed delete did not happen")
	}
}

// T035_02: a branch a rule REFUSED stays owed after Stop: deleting it is refused until the
// user's words say so, which a citation in the same command carries.
func TestT035_02_DeletingARefusedBranchNeedsTheUsersWords(t *testing.T) {
	const sess = "s-035-02"
	e, proj := project(t)

	e.Run(proj, sess, "break it", Turns("done", leaveBranch("bad", "FORBIDDEN words")...))
	res := e.Run(proj, sess, "drop it", Turns("done", Bash("d1", "git branch -D bad")))
	if !refusedByGate(res) || !res.Saw("file-guard/docs") {
		t.Fatalf("a refused branch was deleted:\n%s", res.Output)
	}

	const said = "yes, delete the bad branch, I do not want it"
	res = e.Run(proj, sess, said, Turns("done",
		Bash("d2", "sr-session trajectory cite '"+said+"' && git branch -D bad")))
	if res.Saw("no-destroying-owed-work") {
		t.Fatalf("a delete citing the user's words was refused:\n%s", res.Output)
	}
}

// T035_03: the other ways to destroy a branch's commits are held to the same rule: a remote
// delete, a force-move, update-ref -d, removing its worktree, a reset off them.
func TestT035_03_OtherDestroyersAreRefusedLikewise(t *testing.T) {
	const sess = "s-035-03"
	e, proj := project(t)
	wt := t.TempDir() + "/wt"

	turns := leaveBranch("feat", "clean words")
	turns = append(turns, Bash("w1", "git worktree add -q "+wt+" feat"))
	e.Run(proj, sess, "leave a branch with a worktree", Turns("done", turns...))
	// Make the branch owed again after that Stop: a second commit on it, unjudged until Stop.
	for i, cmd := range []string{
		"git push origin --delete feat",
		"git push origin :feat",
		"git branch -f feat HEAD",
		"git update-ref -d refs/heads/feat",
		"git worktree remove --force " + wt,
		"git -C " + wt + " reset --hard HEAD~1",
	} {
		more := Turns("done",
			Bash("x"+string(rune('a'+i)), "git -C "+wt+" commit -q --allow-empty -m more"),
			Bash("y"+string(rune('a'+i)), cmd),
		)
		res := e.Run(proj, sess, "destroy it", more)
		if !refusedByGate(res) {
			t.Fatalf("%q destroyed a branch holding unjudged commits:\n%s", cmd, res.Output)
		}
	}
}

// T035_04: `git gc --prune=now`, `git prune` and `git reflog expire` endanger everything at
// once: refused while anything is owed, allowed when nothing is.
func TestT035_04_PruningIsRefusedWhileAnythingIsOwed(t *testing.T) {
	const sess = "s-035-04"
	e, proj := project(t)

	// Nothing owed yet: allowed.
	res := e.Run(proj, sess, "tidy", Turns("done", Bash("g0", "git gc --prune=now -q")))
	if res.Saw("no-destroying-owed-work") {
		t.Fatalf("pruning was refused with nothing owed:\n%s", res.Output)
	}

	turns := append(leaveBranch("feat", "clean words"),
		Bash("g1", "git gc --prune=now -q"),
		Bash("g2", "git prune"),
		Bash("g3", "git reflog expire --expire=now --all"),
	)
	res = e.Run(proj, sess, "work then tidy", Turns("done", turns...))
	n := strings.Count(res.Output, "no-destroying-owed-work")
	if n < 3 {
		t.Fatalf("pruning went ahead while a branch was unjudged (refused %d of 3):\n%s", n, res.Output)
	}

	// Judged and passed at the end of that turn: pruning is allowed again.
	res = e.Run(proj, sess, "tidy now", Turns("done", Bash("g4", "git gc --prune=now -q")))
	if res.Saw("no-destroying-owed-work") {
		t.Fatalf("pruning was refused after everything was judged:\n%s", res.Output)
	}
}

// T035_05: a target the gate cannot tell (a shell variable) is refused while anything is
// owed (fail closed), and passes when nothing is.
func TestT035_05_AnUnknownTargetIsRefusedWhileAnythingIsOwed(t *testing.T) {
	const sess = "s-035-05"
	e, proj := project(t)

	res := e.Run(proj, sess, "drop old", Turns("done", Bash("d0", "b=nothing; git branch -D $b")))
	if res.Saw("no-destroying-owed-work") {
		t.Fatalf("a delete by variable was refused with nothing owed:\n%s", res.Output)
	}

	turns := append(leaveBranch("feat", "clean words"), Bash("d1", "for b in feat; do git branch -D $b; done"))
	res = e.Run(proj, sess, "work then drop", Turns("done", turns...))
	if !refusedByGate(res) || !res.Saw("cannot tell") {
		t.Fatalf("a delete by variable went ahead while a branch was unjudged:\n%s", res.Output)
	}
}
