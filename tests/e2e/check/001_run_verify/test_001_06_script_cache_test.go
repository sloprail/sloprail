package e2e

import (
	"path/filepath"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// A SCRIPT verdict is cached by content like a judge's (every check kind is): the same net
// content is not executed again, whatever its SHAs, and `verify` executes nothing. The script
// appends one line to a ledger outside the project each time it runs.

func scriptProject(t *testing.T) (*Env, string, string, func() int) {
	t.Helper()
	ledger := filepath.Join(t.TempDir(), "script-ledger")
	e, proj, base := project(t, scriptRule, map[string]string{"check.sh": harness.RecordScript(ledger)}, "")
	return e, proj, base, func() int { return len(harness.ReadLedgerLines(t, ledger)) }
}

// T001_05s: a script's pass is reused: the same range again does not run the script.
func TestT001_05s_AScriptPassIsReusedWithoutRunningIt(t *testing.T) {
	e, proj, base, ran := scriptProject(t)
	commitDoc(e, proj, "docs/a.md", "the release is Friday\n", "add a")
	if r := run(e, proj, base, "HEAD"); r.Code != 0 {
		t.Fatalf("run: exit %d:\n%s", r.Code, r.Output)
	}
	if n := ran(); n != 1 {
		t.Fatalf("the script ran %d times, want 1", n)
	}
	if r := run(e, proj, base, "HEAD"); r.Code != 0 {
		t.Fatalf("second run: exit %d:\n%s", r.Code, r.Output)
	}
	if n := ran(); n != 1 {
		t.Fatalf("a second run over the same content ran the script again (%d runs)", n)
	}
}

// T001_06s: the same content after an amend, and after a squash (new SHAs and messages), is a hit.
func TestT001_06s_TheSameScriptContentAfterARewriteIsAHit(t *testing.T) {
	e, proj, base, ran := scriptProject(t)
	commitDoc(e, proj, "docs/a.md", "the release is Friday\n", "add a")
	commitDoc(e, proj, "docs/b.md", "the release is Monday\n", "add b")
	run(e, proj, base, "HEAD")
	if n := ran(); n != 1 {
		t.Fatalf("premise: the script ran %d times, want 1", n)
	}

	e.Git(proj, "commit", "-q", "--amend", "-m", "add b, reworded")
	e.Git(proj, "reset", "-q", "--soft", base)
	e.Git(proj, "commit", "-q", "-m", "docs: both pages, squashed")
	if r := run(e, proj, base, "HEAD"); r.Code != 0 {
		t.Fatalf("run after the rewrite: exit %d:\n%s", r.Code, r.Output)
	}
	if n := ran(); n != 1 {
		t.Fatalf("the same net content after a rewrite ran the script again (%d runs)", n)
	}

	// Changed content does run it.
	commitDoc(e, proj, "docs/a.md", "the release is Tuesday\n", "change a")
	run(e, proj, base, "HEAD")
	if n := ran(); n != 2 {
		t.Fatalf("changed content ran the script %d times in all, want 2", n)
	}
}

// T001_11s: verify never executes a script: with nothing stored it is red ("missing") and the
// script has not run; after `run` it is green and still ran once.
func TestT001_11s_VerifyExecutesNoScript(t *testing.T) {
	e, proj, base, ran := scriptProject(t)
	commitDoc(e, proj, "docs/a.md", "the release is Friday\n", "add a")

	v := verify(e, proj, base, "HEAD")
	if v.Code != 1 {
		t.Fatalf("verify with nothing stored: exit %d, want 1:\n%s", v.Code, v.Output)
	}
	mustContain(t, v.Output, "missing")
	if n := ran(); n != 0 {
		t.Fatalf("verify executed the script (%d runs)", n)
	}

	run(e, proj, base, "HEAD")
	if v := verify(e, proj, base, "HEAD"); v.Code != 0 {
		t.Fatalf("verify after run: exit %d:\n%s", v.Code, v.Output)
	}
	if n := ran(); n != 1 {
		t.Fatalf("the script ran %d times, want 1 (run executed it, verify did not)", n)
	}
}
