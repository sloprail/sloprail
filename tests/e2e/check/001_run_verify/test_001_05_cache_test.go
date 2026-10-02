package e2e

import "testing"

func judgeProject(t *testing.T, verdict string) (*Env, string, string) {
	return project(t, judgeRule, map[string]string{"rubric.md.j2": rubric}, verdict)
}

// T001_05: a judge's PASS is reused: the same range again is not judged again.
func TestT001_05_APassIsReusedWithoutAskingTheJudge(t *testing.T) {
	e, proj, base := judgeProject(t, verdictPass)
	commitDoc(e, proj, "docs/a.md", "the release is Friday\n", "add a")

	if r := run(e, proj, base, "HEAD"); r.Code != 0 {
		t.Fatalf("run: exit %d:\n%s", r.Code, r.Output)
	}
	if n := judged(e, proj); n != 1 {
		t.Fatalf("the judge was asked %d times, want 1", n)
	}
	if r := run(e, proj, base, "HEAD"); r.Code != 0 {
		t.Fatalf("second run: exit %d:\n%s", r.Code, r.Output)
	}
	if n := judged(e, proj); n != 1 {
		t.Fatalf("a second run over the same content asked the judge again (%d calls)", n)
	}
}

// T001_06: the same content after a rebase or an amend (new SHAs, same change) is a hit.
func TestT001_06_TheSameContentAfterARebaseIsAHit(t *testing.T) {
	e, proj, base := judgeProject(t, verdictPass)
	commitDoc(e, proj, "docs/a.md", "the release is Friday\n", "add a")
	run(e, proj, base, "HEAD")
	before := e.Git(proj, "rev-parse", "HEAD")

	e.Git(proj, "commit", "-q", "--amend", "-m", "add a, reworded")
	if after := e.Git(proj, "rev-parse", "HEAD"); after == before {
		t.Fatal("premise: the amend did not change the SHA")
	}
	if r := run(e, proj, base, "HEAD"); r.Code != 0 {
		t.Fatalf("run after the amend: exit %d:\n%s", r.Code, r.Output)
	}
	if n := judged(e, proj); n != 1 {
		t.Fatalf("an amend (same content, new SHA) was judged again (%d calls)", n)
	}
}

// T001_07: the same net content after a SQUASH (fewer commits, other messages) is a hit.
func TestT001_07_TheSameContentAfterASquashIsAHit(t *testing.T) {
	e, proj, base := judgeProject(t, verdictPass)
	commitDoc(e, proj, "docs/a.md", "the release is Friday\n", "add a")
	commitDoc(e, proj, "docs/b.md", "the release is Monday\n", "add b")
	run(e, proj, base, "HEAD")
	if n := judged(e, proj); n != 1 {
		t.Fatalf("premise: %d judge calls, want 1", n)
	}

	e.Git(proj, "reset", "-q", "--soft", base)
	e.Git(proj, "commit", "-q", "-m", "docs: both pages, squashed")
	if r := run(e, proj, base, "HEAD"); r.Code != 0 {
		t.Fatalf("run after the squash: exit %d:\n%s", r.Code, r.Output)
	}
	if n := judged(e, proj); n != 1 {
		t.Fatalf("a squash (same net content) was judged again (%d calls)", n)
	}
}

// T001_08: a change that is reverted hits the verdict of the content it returns to.
func TestT001_08_ARevertedChangeIsAHit(t *testing.T) {
	e, proj, base := judgeProject(t, verdictPass)
	commitDoc(e, proj, "docs/a.md", "the release is Friday\n", "add a")
	run(e, proj, base, "HEAD")

	commitDoc(e, proj, "docs/a.md", "the release is Monday\n", "change a")
	run(e, proj, base, "HEAD")
	if n := judged(e, proj); n != 2 {
		t.Fatalf("premise: the changed content was judged %d times in all, want 2", n)
	}

	commitDoc(e, proj, "docs/a.md", "the release is Friday\n", "revert a")
	if r := run(e, proj, base, "HEAD"); r.Code != 0 {
		t.Fatalf("run after the revert: exit %d:\n%s", r.Code, r.Output)
	}
	if n := judged(e, proj); n != 2 {
		t.Fatalf("the reverted content was judged again (%d calls, want 2)", n)
	}
}

// T001_09: a changed subject is judged.
func TestT001_09_AChangedSubjectIsJudged(t *testing.T) {
	e, proj, base := judgeProject(t, verdictPass)
	commitDoc(e, proj, "docs/a.md", "the release is Friday\n", "add a")
	run(e, proj, base, "HEAD")

	commitDoc(e, proj, "docs/a.md", "the release is Tuesday\n", "change a")
	run(e, proj, base, "HEAD")
	if n := judged(e, proj); n != 2 {
		t.Fatalf("changed content was not judged (%d calls, want 2)", n)
	}
}

// T001_10: a fail is kept and shown, never a hit: run asks the judge again.
func TestT001_10_AFailIsShownByVerifyAndNotReused(t *testing.T) {
	e, proj, base := judgeProject(t, verdictFail)
	commitDoc(e, proj, "docs/a.md", "the release is Friday\n", "add a")

	r := run(e, proj, base, "HEAD")
	if r.Code != 1 {
		t.Fatalf("run: exit %d, want 1:\n%s", r.Code, r.Output)
	}
	mustContain(t, r.Output, "JUDGE-SAYS-NO")

	v := verify(e, proj, base, "HEAD")
	if v.Code != 1 {
		t.Fatalf("verify: exit %d, want 1:\n%s", v.Code, v.Output)
	}
	mustContain(t, v.Output, "JUDGE-SAYS-NO", "fail")
	if n := judged(e, proj); n != 1 {
		t.Fatalf("verify asked the judge (%d calls)", n)
	}

	run(e, proj, base, "HEAD")
	if n := judged(e, proj); n != 2 {
		t.Fatalf("a stored fail was reused instead of judged again (%d calls)", n)
	}
}

// T001_11: verify never asks a model and never writes: with nothing stored a judge is red
// ("missing"), asked of no one, and after run it is green. Another clone of the repository
// shares the results: they are about the content, not the checkout.
func TestT001_11_VerifyReadsWhatRunStoredAndAsksNoOne(t *testing.T) {
	e, proj, base := judgeProject(t, verdictPass)
	commitDoc(e, proj, "docs/a.md", "the release is Friday\n", "add a")

	v := verify(e, proj, base, "HEAD")
	if v.Code != 1 {
		t.Fatalf("verify with nothing stored: exit %d, want 1:\n%s", v.Code, v.Output)
	}
	mustContain(t, v.Output, "missing")
	if n := judged(e, proj); n != 0 {
		t.Fatalf("verify asked the judge (%d calls)", n)
	}
	// ... and wrote nothing: a second verify is red the same way, and run still judges once.
	if v := verify(e, proj, base, "HEAD"); v.Code != 1 {
		t.Fatalf("verify wrote a result: exit %d:\n%s", v.Code, v.Output)
	}

	run(e, proj, base, "HEAD")
	if n := judged(e, proj); n != 1 {
		t.Fatalf("run judged %d times, want 1", n)
	}
	if v := verify(e, proj, base, "HEAD"); v.Code != 0 {
		t.Fatalf("verify after run: exit %d:\n%s", v.Code, v.Output)
	}

	clone := e.Project()
	e.Git(clone, "clone", "-q", proj, clone+"/c")
	if v := verify(e, clone+"/c", base, "HEAD"); v.Code != 0 {
		t.Fatalf("verify in another clone of the repository: exit %d:\n%s", v.Code, v.Output)
	}
	if n := judged(e, proj); n != 1 {
		t.Fatalf("the clone's verify asked the judge (%d calls)", n)
	}
}
