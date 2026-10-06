package e2e

import (
	"strings"
	"testing"
)

// A commit that restores a guarded file to exactly the content it has on the remote default
// branch (the merge base) changes nothing a citation could ground, so the gate does not ask
// for one; a partial restore still differs, and still needs one.

// T058_40: a cited change, then an uncited commit restoring origin/main's content: not refused,
// and nothing is wanted at Stop either.
func TestT058_40_RestoringTheBasesContentNeedsNoCitation(t *testing.T) {
	e, proj := project(t)
	e.PushBranch(proj, "main")
	res := e.Run(proj, "s-058-40", prompt, Turns("done",
		stage("a", "docs/seed.md", "changed"),
		Bash("c", citedCommit("change the seed")),
		Bash("r", "git checkout origin/main -- docs/seed.md"),
		Bash("m", "git commit -q -m 'revert the seed to main'"),
	))
	if res.Refused() {
		t.Fatalf("a commit restoring the base's content was refused:\n%s", res.Output)
	}
	has(t, subjects(e, proj), "revert the seed to main")
	noCitationAtStop(t, e, proj, "s-058-40")
}

// T058_41: a partial restore (content that is neither the base's nor a cited state) is refused.
func TestT058_41_APartialRestoreStillNeedsACitation(t *testing.T) {
	e, proj := project(t)
	e.PushBranch(proj, "main")
	res := e.Run(proj, "s-058-41", prompt, Turns("done",
		stage("a", "docs/seed.md", "changed"),
		Bash("c", citedCommit("change the seed")),
		stage("p", "docs/seed.md", "seed and a bit"),
		Bash("m", "git commit -q -m 'half revert'"),
	))
	if !res.Refused() {
		t.Fatalf("a partial restore passed without a citation:\n%s", res.Output)
	}
	if strings.Contains(subjects(e, proj), "half revert") {
		t.Fatalf("the refused commit was made:\n%s", subjects(e, proj))
	}
}
