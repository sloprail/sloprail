package e2e

import "testing"

// T001_01: --base and --head are required; the caller states the range.
func TestT001_01_BaseAndHeadAreRequired(t *testing.T) {
	e, proj, _ := project(t, scriptRule, map[string]string{"check.sh": forbiddenCheck}, "")
	for _, verb := range []string{"run", "verify"} {
		for _, args := range [][]string{{}, {"--base", "HEAD"}, {"--head", "HEAD"}} {
			r := e.CLIDirectEnv(proj, e.SessionEnv(session), "sr", append([]string{"check", verb}, args...)...)
			if r.Code == 0 {
				t.Fatalf("sr check %s %v succeeded without both flags:\n%s", verb, args, r.Output)
			}
			mustContain(t, r.Output, "required flag")
		}
	}
}

// T001_02: a revision that does not resolve is an error naming the flag, never an empty range.
func TestT001_02_AnUnresolvableRevisionIsAnError(t *testing.T) {
	e, proj, base := project(t, scriptRule, map[string]string{"check.sh": forbiddenCheck}, "")

	r := run(e, proj, "no-such-branch", "HEAD")
	if r.Code == 0 {
		t.Fatalf("an unresolvable --base passed:\n%s", r.Output)
	}
	mustContain(t, r.Output, "--base", "no-such-branch")

	r = run(e, proj, base, "no-such-branch")
	if r.Code == 0 {
		t.Fatalf("an unresolvable --head passed:\n%s", r.Output)
	}
	mustContain(t, r.Output, "--head")
}

// T001_03: the range is merge-base(base, head)..head, so a base that has moved on (a branch
// the work was cut from) widens nothing and narrows nothing, and branch names work.
func TestT001_03_TheRangeIsTheMergeBaseUpToHead(t *testing.T) {
	e, proj, base := project(t, scriptRule, map[string]string{"check.sh": forbiddenCheck}, "")
	e.Git(proj, "branch", "trunk") // trunk stays at the rule's commit
	commitDoc(e, proj, "docs/a.md", "FORBIDDEN here\n", "add a")

	// Another line moves trunk on; the work is judged against the fork point, not trunk's tip.
	e.Git(proj, "checkout", "-q", "trunk")
	commitDoc(e, proj, "elsewhere.txt", "unrelated\n", "trunk moves on")
	e.Git(proj, "checkout", "-q", "-")

	r := run(e, proj, "trunk", "HEAD")
	if r.Code == 0 {
		t.Fatalf("the work's FORBIDDEN file was not judged against the fork point:\n%s", r.Output)
	}
	mustContain(t, r.Output, "FORBIDDEN text in the changeset", "docs/a.md")

	// The same range stated by sha.
	if r := run(e, proj, base, "HEAD"); r.Code == 0 {
		t.Fatalf("the same range by sha passed:\n%s", r.Output)
	}
}

// T001_04: a refusal exits 1, a fix passes, and verify says the same without a model.
func TestT001_04_RunAndVerifyRefuseThenPass(t *testing.T) {
	e, proj, base := project(t, scriptRule, map[string]string{"check.sh": forbiddenCheck}, "")
	commitDoc(e, proj, "docs/a.md", "FORBIDDEN here\n", "add a")

	if r := run(e, proj, base, "HEAD"); r.Code != 1 {
		t.Fatalf("run: exit %d, want 1:\n%s", r.Code, r.Output)
	}
	if r := verify(e, proj, base, "HEAD"); r.Code != 1 {
		t.Fatalf("verify: exit %d, want 1:\n%s", r.Code, r.Output)
	}

	commitDoc(e, proj, "docs/a.md", "fine\n", "fix a")
	if r := run(e, proj, base, "HEAD"); r.Code != 0 {
		t.Fatalf("run after the fix: exit %d:\n%s", r.Code, r.Output)
	}
	r := verify(e, proj, base, "HEAD")
	if r.Code != 0 {
		t.Fatalf("verify after the fix: exit %d:\n%s", r.Code, r.Output)
	}
	mustContain(t, r.Output, "file-guard/docs")
}
