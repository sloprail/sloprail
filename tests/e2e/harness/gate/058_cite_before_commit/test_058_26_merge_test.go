package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// A merge needs a citation only for what its resolution changed. The files it takes unchanged from a
// side were changed by that side's commits, which carry their own trailers.

// merging is a project on branch main with a cited side branch (docs/side.md, and docs/seed.md when
// conflict is set) and a diverged main (an unguarded change, and docs/seed.md when conflict is set).
func merging(t *testing.T, conflict bool) (*Env, string) {
	t.Helper()
	e, proj := project(t)
	e.Git(proj, "checkout", "-q", "-b", "side")
	e.WriteFile(proj, "docs/side.md", "side\n")
	if conflict {
		e.WriteFile(proj, "docs/seed.md", "from side\n")
	}
	e.CommitAll(proj, "side work", harness.CitesUser(quote))
	e.Git(proj, "checkout", "-q", "main")
	e.WriteFile(proj, "src/main.go", "package main\n")
	if conflict {
		e.WriteFile(proj, "docs/seed.md", "from main\n")
	}
	e.CommitAll(proj, "main work", harness.CitesUser(quote))
	return e, proj
}

func noCitationAtStop(t *testing.T, e *Env, proj, session string) {
	t.Helper()
	if got := strings.Join(e.StopContinuations(proj, session), "\n"); strings.Contains(got, "citation") {
		t.Fatalf("the file-guard wanted a citation at Stop:\n%s", got)
	}
}

// T058_26: `git commit --no-edit` concluding a clean merge of a cited branch needs no citation of its
// own, at the gate or at Stop.
func TestT058_26_CleanMergeCommitNeedsNoCitation(t *testing.T) {
	e, proj := merging(t, false)
	res := e.Run(proj, "s-058-26", prompt, Turns("done",
		Bash("m", "git merge -q --no-commit --no-ff side"),
		Bash("c", "git commit -q --no-edit"),
	))
	if res.Refused() {
		t.Fatalf("the commit concluding a clean merge was refused:\n%s", res.Output)
	}
	has(t, e.Git(proj, "log", "-1", "--format=%P"), " ")
	noCitationAtStop(t, e, proj, "s-058-26")
}

// T058_27: `git merge` making the merge commit itself is not refused either.
func TestT058_27_PlainMergeNeedsNoCitation(t *testing.T) {
	e, proj := merging(t, false)
	res := e.Run(proj, "s-058-27", prompt, Turns("done",
		Bash("m", "git merge -q --no-edit side"),
	))
	if res.Refused() {
		t.Fatalf("git merge was refused:\n%s", res.Output)
	}
	has(t, e.Git(proj, "log", "-1", "--format=%P"), " ")
	noCitationAtStop(t, e, proj, "s-058-27")
}

// T058_28: a merge whose resolution changed a guarded file is refused for that file alone, not for
// what came in from the side; the trailer on the merge commit lets it through, and Stop is satisfied.
func TestT058_28_ConflictResolutionIsTheOnlyCitedPart(t *testing.T) {
	e, proj := merging(t, true)
	res := e.Run(proj, "s-058-28", prompt, Turns("done",
		Bash("m", "git merge -q --no-commit side || true"),
		Bash("r", "printf 'resolved\\n' > docs/seed.md && git add docs/seed.md"),
		Bash("c", "git commit -q --no-edit"),
	))
	if !res.Refused() {
		t.Fatalf("a merge commit with an uncited resolution was not refused:\n%s", res.Output)
	}
	has(t, res.Output, "docs/seed.md")
	if strings.Contains(res.Output[strings.Index(res.Output, "hook error"):], "docs/side.md") {
		t.Fatalf("the refusal names a file that only came in from the side:\n%s", res.Output)
	}

	e2, proj2 := merging(t, true)
	res = e2.Run(proj2, "s-058-29", prompt, Turns("done",
		Bash("m", "git merge -q --no-commit side || true"),
		Bash("r", "printf 'resolved\\n' > docs/seed.md && git add docs/seed.md"),
		Bash("c", "git commit -q -m 'merge side' -m 'Sloprail-Cites-User: "+quote+"'"),
	))
	if res.Refused() {
		t.Fatalf("a merge commit whose trailer cites the resolution was refused:\n%s", res.Output)
	}
	noCitationAtStop(t, e2, proj2, "s-058-29")
}

// T058_30: a conflict resolved to one side's own version changed nothing the merge could cite.
func TestT058_30_ResolvingToOneSideNeedsNoCitation(t *testing.T) {
	e, proj := merging(t, true)
	res := e.Run(proj, "s-058-30", prompt, Turns("done",
		Bash("m", "git merge -q --no-commit side || true"),
		Bash("r", "git checkout --theirs docs/seed.md && git add docs/seed.md"),
		Bash("c", "git commit -q --no-edit"),
	))
	if res.Refused() {
		t.Fatalf("a merge taking a file unchanged from one side was refused:\n%s", res.Output)
	}
	noCitationAtStop(t, e, proj, "s-058-30")
}
