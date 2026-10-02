package e2e

import (
	"github.com/sloprail/sloprail/tests/e2e/harness"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const citingRule = "match: \"docs/**\"\nrequire:\n  - citation: {source_types: [user]}\nchecks:\n  - script: ./check.sh\n"

// T003_03: `require: citation` reads the range's commits. A commit with no
// trailer is refused with the remedy (a trailer, not a command); a commit citing
// words the user really said passes and the citation is on the changeset; words
// nobody said are not a citation.
func TestT003_03_CitationTrailersGroundTheRange(t *testing.T) {
	const prompt = "please document the release process in docs"
	e, proj, led := project(t, citingRule)

	// Refusal: no citation at all.
	e.Run(proj, "s-003-03", prompt, Turns("done", harness.CommitFile("c1", "docs/release.md", "steps", "document the release")))
	joined := strings.Join(e.BlockingErrorsFrom(proj, "s-003-03", "Stop"), "\n")
	for _, want := range []string{"Sloprail-Cites-User", "must cite"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("the refusal should say %q:\n%s", want, joined)
		}
	}
	if n := len(ledger(t, led)); n != 0 {
		t.Fatalf("the checks ran (%d times) though the prerequisite was unmet", n)
	}

	// Words nobody said: still not a citation, and the refusal says which trailer failed.
	e.Run(proj, "s-003-03", "amend it", Turns("done", harness.CommitFile("c2", "docs/release.md", "steps v2", "document it", "Sloprail-Cites-User: delete the release notes")))
	joined = strings.Join(e.BlockingErrorsFrom(proj, "s-003-03", "Stop"), "\n")
	if !strings.Contains(joined, "delete the release notes") || !strings.Contains(joined, "did not resolve") {
		t.Fatalf("an unresolvable trailer should be named:\n%s", joined)
	}

	// Pass: the user's own words, in a trailer of a later commit in the range.
	e.Run(proj, "s-003-03", "cite it", Turns("done", harness.CommitFile("c3", "docs/release.md", "steps v3", "cite the ask", "Sloprail-Cites-User: document the release process")))
	runs := ledger(t, led)
	if len(runs) == 0 {
		t.Fatal("with the citation in place the checks never ran")
	}
	last := runs[len(runs)-1]
	if len(last.Citations) != 1 || last.Citations[0] != "document the release process" || len(last.Commits) != 3 {
		t.Fatalf("the check was handed %+v; want the resolved citation and the three commits of the range (the agent's own; the rule predates the session)", last)
	}
}

// T003_04: a git error fails closed and is recorded as an engine failure. A
// blob in the range is lost from the object database, so the changeset cannot be
// read: the Stop is refused — never read as an empty range — and `sr-checks
// status` shows an error carrying the cause. Restoring the object lets the same
// Stop pass, and the failed run moved nothing.
func TestT003_04_AGitErrorFailsClosed(t *testing.T) {
	e, proj, led := project(t, docsRule)
	e.Run(proj, "s-003-04", "hello", Turns("done", Bash("b1", "true")))

	e.WriteFile(proj, "docs/a.md", "content that will be lost\n")
	e.CommitAll(proj, "add a")
	blob := e.Git(proj, "rev-parse", "HEAD:docs/a.md")
	object := filepath.Join(proj, ".git", "objects", blob[:2], blob[2:])
	if err := os.Remove(object); err != nil {
		t.Fatalf("premise: the blob is not a loose object: %v", err)
	}

	r := e.StopNow(proj, "s-003-04", false)
	if !harness.Blocked(r) || !strings.Contains(r.Output, "could not be evaluated") {
		t.Fatalf("an unreadable range did not fail closed:\n%s", r.Output)
	}
	if n := len(ledger(t, led)); n != 0 {
		t.Fatalf("the check ran %d times against a range that could not be read", n)
	}
	e.CheckRunRaw(proj, "s-003-04", e.RunBase("s-003-04"), "HEAD")
	var recorded bool
	for _, run := range e.CacheRecords(proj) {
		recorded = recorded || (run.Error != "" && strings.Contains(run.Rule, "docs"))
	}
	if !recorded {
		t.Fatalf("the engine failure was not recorded as an error run: %+v", e.CacheRecords(proj))
	}

	// The object comes back; the failed run moved nothing, so the range is the
	// same one and is now judged.
	e.Git(proj, "hash-object", "-w", "docs/a.md")
	if r := e.StopNow(proj, "s-003-04", false); harness.Blocked(r) {
		t.Fatalf("a readable range was still refused:\n%s", r.Output)
	}
	runs := ledger(t, led)
	if len(runs) != 1 || len(runs[0].Files) != 1 || runs[0].Files[0].Path != "docs/a.md" {
		t.Fatalf("ledger = %+v; want the one docs/a.md range judged once the error cleared", runs)
	}
}
