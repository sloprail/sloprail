package e2e

import (
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

const citeRule = "match: \"docs/**\"\nrequire:\n  - citation:\n      source_types: [user]\nchecks:\n  - script: ./check.sh\n"

const passCheck = "#!/bin/sh\ncat >/dev/null\nexit 0\n"

// T001_14: a citation requirement is checkable from the repository alone: verify counts a
// Sloprail-Cites-User trailer on the commit that last changed the file (the author's `run`
// resolved the quote against the transcript), and refuses a change that carries none.
func TestT001_14_VerifyReadsCitationsFromCommitTrailers(t *testing.T) {
	e, proj, base := project(t, citeRule, map[string]string{"check.sh": passCheck}, "")
	// The session the author's run resolves the quote against: the user's own words.
	e.Run(proj, session, "write the docs", harness.Turns("done"))
	commitDoc(e, proj, "docs/a.md", "uncited\n", "add a")

	// run judges the range (and keeps its verdict, citations included); verify only reads.
	if r := run(e, proj, base, "HEAD"); r.Code != 1 {
		t.Fatalf("run of an uncited change: exit %d, want 1:\n%s", r.Code, r.Output)
	}
	v := verify(e, proj, base, "HEAD")
	if v.Code != 1 {
		t.Fatalf("verify of an uncited change: exit %d, want 1:\n%s", v.Code, v.Output)
	}
	mustContain(t, v.Output, "docs/a.md")

	e.WriteFile(proj, "docs/a.md", "now cited\n")
	e.CommitAll(proj, "cite a", "Sloprail-Cites-User: write the docs")
	if v := verify(e, proj, base, "HEAD"); v.Code != 1 {
		t.Fatalf("verify of a changed commit message with no run (the citations are in the key): exit %d, want 1 (not judged):\n%s", v.Code, v.Output)
	}
	if r := run(e, proj, base, "HEAD"); r.Code != 0 {
		t.Fatalf("run of a change whose commit carries the trailer: exit %d, want 0:\n%s", r.Code, r.Output)
	}
	if v := verify(e, proj, base, "HEAD"); v.Code != 0 {
		t.Fatalf("verify of a change whose commit carries the trailer: exit %d, want 0:\n%s", v.Code, v.Output)
	}
}
