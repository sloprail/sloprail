package e2e

import "testing"

const skillRule = "match: \"docs/**\"\nrequire:\n  - skill: authoring-guardrails\nchecks:\n  - script: ./check.sh\n"

const citeRule = "match: \"docs/**\"\nrequire:\n  - citation:\n      source_types: [user]\nchecks:\n  - script: ./check.sh\n"

const passCheck = "#!/bin/sh\ncat >/dev/null\nexit 0\n"

// T001_14: verify never consults the session. A requirement about what the AGENT did (a skill
// it had to load) lives in the transcript, so it is checked where the session ran (`run`) and
// is not re-checked in CI, where there is no transcript.
func TestT001_14_VerifySkipsWhatOnlyTheSessionCanSay(t *testing.T) {
	e, proj, base := project(t, skillRule, map[string]string{"check.sh": passCheck}, "")
	commitDoc(e, proj, "docs/a.md", "text\n", "add a")

	// With no session behind it, run cannot see the skill loaded: refused.
	if r := run(e, proj, base, "HEAD"); r.Code != 1 {
		t.Fatalf("run without the skill loaded: exit %d, want 1:\n%s", r.Code, r.Output)
	}
	// verify does not ask the transcript: the requirement is skipped, and says so.
	v := verify(e, proj, base, "HEAD")
	if v.Code != 0 {
		t.Fatalf("verify: exit %d, want 0:\n%s", v.Code, v.Output)
	}
	mustContain(t, v.Output, "skipped", "needs the session")
}

// T001_15: a citation requirement is checkable from the repository alone: verify counts a
// Sloprail-Cites-User trailer on the commit that last changed the file (the author's `run`
// resolved the quote against the transcript), and refuses a change that carries none.
func TestT001_15_VerifyReadsCitationsFromCommitTrailers(t *testing.T) {
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
