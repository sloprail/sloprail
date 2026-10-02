package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T003_36: a file-guard judges every line of history the session committed on, not only
// HEAD. A session that cuts branches, commits on each, and then checks out another one
// used to be judged on the last only; the commits left behind merged unchecked.

const refusalText = "FORBIDDEN text in the changeset"

func stopRefusals(e *Env, proj, sess string) string {
	return strings.Join(e.StopContinuations(proj, sess), "\n")
}

// (a) a violation on branch A, a checkout of B with clean work: A is judged and named;
// fixing it on A passes.
func TestT003_36_AViolationOnABranchTheAgentLeftIsJudgedAndNamed(t *testing.T) {
	e, proj, _ := project(t, docsRule)
	main := e.Git(proj, "branch", "--show-current")

	e.Run(proj, "s-003-36a", "two branches", Turns("done",
		Bash("b1", "git switch -q -c feat-a"),
		harness.CommitFile("c1", "docs/a.md", "FORBIDDEN words", "add a"),
		Bash("b2", "git switch -q -c feat-b "+main),
		harness.CommitFile("c2", "docs/b.md", "clean words", "add b"),
	))
	got := stopRefusals(e, proj, "s-003-36a")
	if !strings.Contains(got, refusalText) || !strings.Contains(got, "feat-a") {
		t.Fatalf("the branch the agent left was not judged and named; refusals:\n%s", got)
	}
	if strings.Contains(got, "feat-b") {
		t.Fatalf("the clean branch HEAD is on was named in a refusal:\n%s", got)
	}
	blocks := stopBlocks(e, proj, "s-003-36a")

	e.Run(proj, "s-003-36a", "fix a", Turns("fixed",
		Bash("b3", "git switch -q feat-a"),
		harness.CommitFile("c3", "docs/a.md", "clean words", "fix a"),
		Bash("b4", "git switch -q feat-b"),
	))
	if n := stopBlocks(e, proj, "s-003-36a"); n != blocks {
		t.Fatalf("the fixed branch was still refused (%d refusals, had %d):\n%s", n, blocks, newBlocks(e, proj, "s-003-36a", blocks))
	}
}

// (b) the same in a sub-agent's own worktree.
func TestT003_36_TheSameInASubagentWorktree(t *testing.T) {
	e, proj, _ := project(t, docsRule)
	sub := harness.SubagentScript(t, Turns("sub done",
		Bash("b1", "git switch -q -c sub-a"),
		harness.CommitFile("c1", "docs/a.md", "FORBIDDEN words", "sub adds a"),
		Bash("b2", "git switch -q -c sub-b HEAD~1"),
		harness.CommitFile("c2", "docs/b.md", "clean words", "sub adds b"),
	))
	res := e.Run(proj, "s-003-36b", "delegate", Turns("root done",
		harness.Dispatch("d1", "write the docs", sub, "worktree"),
	))
	if !res.AnySubagentStopBlocked() && strings.Contains(res.Output, "(sub-a, from ") {
		t.Fatalf("a sub-agent's left branch was not judged at its Stop:\n%s", res.Output)
	}
	if !strings.Contains(res.Output, "sub-a") {
		t.Fatalf("the refusal does not name the branch sub-a:\n%s", res.Output)
	}
}

// (d) commits on a detached HEAD, then a checkout of the branch: still judged.
func TestT003_36_ADetachedHeadCommitIsStillJudged(t *testing.T) {
	e, proj, _ := project(t, docsRule)
	main := e.Git(proj, "branch", "--show-current")

	e.Run(proj, "s-003-36d", "detached", Turns("done",
		Bash("b1", "git switch -q --detach"),
		harness.CommitFile("c1", "docs/a.md", "FORBIDDEN words", "detached add a"),
		Bash("b2", "git switch -q "+main),
	))
	got := stopRefusals(e, proj, "s-003-36d")
	if !strings.Contains(got, refusalText) || !strings.Contains(got, "detached") {
		t.Fatalf("a detached-HEAD commit left behind was not judged; refusals:\n%s", got)
	}
	blocks := stopBlocks(e, proj, "s-003-36d")

	// The refusal's own advice: give the commits a branch, fix there.
	var tip string
	for _, line := range strings.Split(e.Git(proj, "reflog", "show", "HEAD", "--format=%H %gs"), "\n") {
		if strings.Contains(line, " commit: detached add a") {
			tip = strings.Fields(line)[0]
		}
	}
	if tip == "" {
		t.Fatal("premise: the detached commit is not in the reflog")
	}
	e.Run(proj, "s-003-36d", "fix it", Turns("fixed",
		Bash("b3", "git switch -q -c rescue "+tip),
		harness.CommitFile("c2", "docs/a.md", "clean words", "fix a"),
		Bash("b4", "git switch -q "+main),
	))
	if n := stopBlocks(e, proj, "s-003-36d"); n != blocks {
		t.Fatalf("the rescued commits were still refused:\n%s", newBlocks(e, proj, "s-003-36d", blocks))
	}
}

// (e) no regression: one branch, refused then fixed; and a branch that existed before
// the session, with commits the session never made, is not judged.
func TestT003_36_ASingleBranchSessionIsJudgedAsBefore(t *testing.T) {
	e, proj, _ := project(t, docsRule)
	main := e.Git(proj, "branch", "--show-current")
	// Someone else's branch, made before the session: it must not be judged.
	e.Git(proj, "switch", "-q", "-c", "elsewhere")
	e.WriteFile(proj, "docs/old.md", "FORBIDDEN words")
	e.CommitAll(proj, "an old violation on another branch")
	e.Git(proj, "switch", "-q", main)

	e.Run(proj, "s-003-36e", "one branch", Turns("done",
		harness.CommitFile("c1", "docs/a.md", "FORBIDDEN words", "add a"),
	))
	got := stopRefusals(e, proj, "s-003-36e")
	if !strings.Contains(got, refusalText) || strings.Contains(got, "elsewhere") || strings.Contains(got, "docs/old.md") {
		t.Fatalf("refusals should be about HEAD's own commit only:\n%s", got)
	}
	blocks := stopBlocks(e, proj, "s-003-36e")
	e.Run(proj, "s-003-36e", "fix", Turns("fixed", harness.CommitFile("c2", "docs/a.md", "clean words", "fix a")))
	if n := stopBlocks(e, proj, "s-003-36e"); n != blocks {
		t.Fatalf("a fixed single-branch session was still refused:\n%s", newBlocks(e, proj, "s-003-36e", blocks))
	}
}

// A row patched into the session's store for a branch the session never checked out is
// judged at the next Stop (`sr-session refs track`).
func TestT003_36_APatchedRowIsJudgedAtTheNextStop(t *testing.T) {
	e, proj, _ := project(t, docsRule)
	main := e.Git(proj, "branch", "--show-current")
	e.Run(proj, "s-003-36g", "clean", Turns("done", harness.CommitFile("c1", "docs/a.md", "clean words", "add a")))
	blocks := stopBlocks(e, proj, "s-003-36g")

	// A branch cut and committed on outside the session's view (no reflog entry in it).
	e.Git(proj, "switch", "-q", "-c", "patched", main)
	e.WriteFile(proj, "docs/p.md", "FORBIDDEN words")
	e.CommitAll(proj, "violation on a branch the engine never saw")
	base := e.Git(proj, "rev-parse", "patched~1")
	e.Git(proj, "switch", "-q", main)

	out := e.CLIDirectEnv(proj, e.SessionEnv("s-003-36g"), "sr-session", "refs", "track", "--head", "patched", "--base", base)
	if out.Code != 0 {
		t.Fatalf("refs track failed:\n%s", out.Output)
	}
	e.Run(proj, "s-003-36g", "anything else?", Turns("no", Bash("b1", "true")))
	if got := newBlocks(e, proj, "s-003-36g", blocks); !strings.Contains(got, refusalText) || !strings.Contains(got, "patched") {
		t.Fatalf("a patched row was not judged at Stop:\n%s", got)
	}
}
