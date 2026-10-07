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
	has(t, res.Output, "Sloprail-Cites-User")
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

// T058_07: the trailer in the commit's own message is the citation: `-m ... -m 'Sloprail-Cites-User:
// <quote>'` with no chained cite passes, and the file-guard is satisfied at Stop.
func TestT058_07_TrailerInTheMessageCitesTheCommit(t *testing.T) {
	e, proj := project(t)
	res := e.Run(proj, "s-058-07", prompt, Turns("done",
		stage("a", "docs/a.md", "a"),
		Bash("c", "git commit -q -m 'add a' -m 'Sloprail-Cites-User: "+quote+"'"),
	))
	if res.Refused() {
		t.Fatalf("a commit whose message carries a resolving trailer was refused:\n%s", res.Output)
	}
	has(t, subjects(e, proj), "add a")
	if got := strings.Join(e.StopContinuations(proj, "s-058-07"), "\n"); strings.Contains(got, "citation") {
		t.Fatalf("the file-guard still wanted a citation at Stop:\n%s", got)
	}
}

// T058_08: a trailer whose quote resolves nowhere grounds nothing: refused, naming the quote.
func TestT058_08_UnresolvedTrailerIsRefused(t *testing.T) {
	e, proj := project(t)
	res := e.Run(proj, "s-058-08", prompt, Turns("done",
		stage("a", "docs/a.md", "a"),
		Bash("c", "git commit -q -m 'add a' -m 'Sloprail-Cites-User: nobody ever said this'"),
	))
	if !res.Refused() {
		t.Fatalf("a commit whose trailer does not resolve was not refused:\n%s", res.Output)
	}
	has(t, res.Output, "nobody ever said this")
	if strings.Contains(subjects(e, proj), "add a") {
		t.Fatal("the refused commit was made")
	}
}

// T058_09: `-F msgfile` carrying the trailer passes.
func TestT058_09_TrailerInAMessageFilePasses(t *testing.T) {
	e, proj := project(t)
	res := e.Run(proj, "s-058-09", prompt, Turns("done",
		stage("a", "docs/a.md", "a"),
		Bash("m", "printf 'add a\\n\\nSloprail-Cites-User: "+quote+"\\n' > ../msg.txt"),
		Bash("c", "git commit -q -F ../msg.txt"),
	))
	if res.Refused() {
		t.Fatalf("a commit whose -F message file carries a resolving trailer was refused:\n%s", res.Output)
	}
	has(t, subjects(e, proj), "add a")
}

// T058_10: the refusal hands back the quotes the session already recorded with sr-file --cite.
func TestT058_10_RefusalNamesTheRecordedQuote(t *testing.T) {
	e, proj := project(t)
	res := e.Run(proj, "s-058-10", prompt, Turns("done",
		Bash("w", "sr-file write docs/a.md --content a --cite:user '"+quote+"'"),
		Bash("s", "git add docs/a.md"),
		Bash("c", "git commit -q -m 'add a'"),
	))
	if !res.Refused() {
		t.Fatalf("an uncited commit was not refused:\n%s", res.Output)
	}
	has(t, res.Output, "recorded")
	has(t, res.Output, quote)
}

// T058_11: a multi-line -m value (the trailers harness.Commit writes) is one argument, not several
// pathspecs.
func TestT058_11_MultiLineMessageIsOneArgument(t *testing.T) {
	e, proj := project(t)
	res := e.Run(proj, "s-058-11", prompt, Turns("done",
		stage("a", "docs/a.md", "a"),
		Bash("c", "git commit -q -m 'add a' -m 'Sloprail-Cites-User: "+quote+"\nCo-Authored-By: X <x@example.com>'"),
	))
	if res.Refused() {
		t.Fatalf("a commit with a multi-line message was refused:\n%s", res.Output)
	}
	has(t, subjects(e, proj), "add a")
}
