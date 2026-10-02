package e2e

import (
	"strings"
	"testing"
)

// T058_01: a commit of a citation-guarded file with no cite is refused, naming the file and the
// command to run; nothing is committed.
func TestT058_01_UncitedCommitOfAGuardedFileIsRefused(t *testing.T) {
	e, proj := project(t)
	res := e.Run(proj, "s-058-01", prompt, Turns("done",
		stage("a", "docs/a.md", "a"),
		Bash("c", "git commit -q -m 'add a'"),
	))
	if !res.Refused() {
		t.Fatalf("an uncited commit of a guarded file was not refused:\n%s", res.Output)
	}
	has(t, res.Output, "docs/a.md")
	has(t, res.Output, "sr-session trajectory cite")
	if strings.Contains(subjects(e, proj), "add a") {
		t.Fatalf("the refused commit was made:\n%s", subjects(e, proj))
	}
}

// T058_02: the same commit behind a cite that resolves, carrying the trailer, goes through, and the
// file-guard is satisfied at Stop.
func TestT058_02_CitedCommitPasses(t *testing.T) {
	e, proj := project(t)
	res := e.Run(proj, "s-058-02", prompt, Turns("done",
		stage("a", "docs/a.md", "a"),
		Bash("c", citedCommit("add a")),
	))
	if res.Refused() {
		t.Fatalf("a cited commit was refused:\n%s", res.Output)
	}
	has(t, subjects(e, proj), "add a")
	if got := strings.Join(e.StopContinuations(proj, "s-058-02"), "\n"); strings.Contains(got, "citation") {
		t.Fatalf("the file-guard still wanted a citation at Stop:\n%s", got)
	}
}

// T058_03: a commit touching only unguarded files needs nothing.
func TestT058_03_UnguardedCommitPasses(t *testing.T) {
	e, proj := project(t)
	res := e.Run(proj, "s-058-03", prompt, Turns("done",
		stage("a", "src/x.go", "package x"),
		Bash("c", "git commit -q -m 'add x'"),
	))
	if res.Refused() {
		t.Fatalf("a commit of unguarded files was refused:\n%s", res.Output)
	}
	has(t, subjects(e, proj), "add x")
}

// T058_04: `git commit --amend` that adds a guarded change is refused without a cite.
func TestT058_04_AmendWithoutACiteIsRefused(t *testing.T) {
	e, proj := project(t)
	res := e.Run(proj, "s-058-04", prompt, Turns("done",
		stage("a", "docs/seed.md", "changed"),
		Bash("c", "git commit -q --amend --no-edit"),
	))
	if !res.Refused() {
		t.Fatalf("an uncited amend touching a guarded file was not refused:\n%s", res.Output)
	}
	has(t, res.Output, "docs/seed.md")
	if got := e.Git(proj, "show", "HEAD:docs/seed.md"); strings.Contains(got, "changed") {
		t.Fatalf("the refused amend was made:\n%s", got)
	}
}

// T058_05: an amend with --no-edit of a HEAD that already carries its citation passes.
func TestT058_05_AmendOfACitedHeadPasses(t *testing.T) {
	e, proj := project(t)
	res := e.Run(proj, "s-058-05", prompt, Turns("done",
		stage("a", "docs/a.md", "a"),
		Bash("c", citedCommit("add a")),
		Bash("m", "git commit -q --amend --no-edit"),
	))
	if res.Refused() {
		t.Fatalf("an amend --no-edit of a cited HEAD was refused:\n%s", res.Output)
	}
}

// T058_06: `git add <file> && git commit` in one line is judged on what the add will stage.
func TestT058_06_AddAndCommitInOneLineIsJudged(t *testing.T) {
	e, proj := project(t)
	res := e.Run(proj, "s-058-06", prompt, Turns("done",
		Bash("w", "mkdir -p docs && printf '%s' a > docs/a.md"),
		Bash("c", "git add docs/a.md && git commit -q -m 'add a'"),
	))
	if !res.Refused() {
		t.Fatalf("an uncited add-and-commit of a guarded file was not refused:\n%s", res.Output)
	}
	has(t, res.Output, "docs/a.md")
	if strings.Contains(subjects(e, proj), "add a") {
		t.Fatal("the refused commit was made")
	}
}
