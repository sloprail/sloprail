package e2e

import "testing"

const citeRule = "match: \"docs/**\"\nrequire:\n  - citation:\n      source_types: [user]\nchecks:\n  - script: ./check.sh\n"

const passCheck = "#!/bin/sh\ncat >/dev/null\nexit 0\n"

// T001_14: a citation requirement is checkable from the repository alone: verify counts a
// Sloprail-Cites-User trailer on the commit that last changed the file (the author's `run`
// resolved the quote against the transcript), and refuses a change that carries none.
func TestT001_14_VerifyReadsCitationsFromCommitTrailers(t *testing.T) {
	e, proj, base := project(t, citeRule, map[string]string{"check.sh": passCheck}, "")
	commitDoc(e, proj, "docs/a.md", "uncited\n", "add a")

	v := verify(e, proj, base, "HEAD")
	if v.Code != 1 {
		t.Fatalf("verify of an uncited change: exit %d, want 1:\n%s", v.Code, v.Output)
	}
	mustContain(t, v.Output, "docs/a.md")

	e.WriteFile(proj, "docs/a.md", "now cited\n")
	e.CommitAll(proj, "cite a", "Sloprail-Cites-User: write the docs")
	if v := verify(e, proj, base, "HEAD"); v.Code != 0 {
		t.Fatalf("verify of a change whose commit carries the trailer: exit %d, want 0:\n%s", v.Code, v.Output)
	}
}
