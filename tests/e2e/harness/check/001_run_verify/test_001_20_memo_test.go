package e2e

import (
	"fmt"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// The checks' read paths keep two in-process memos while one command runs: the decoded results
// log (per ref tip) and the ancestry answers of the effective base (per commit pair, shared by
// every rule). Neither may change what `run` or `verify` say: a verdict stored by the previous
// command (or by another machine) is always the one seen, and a rule's effective base is the
// one its own passes reach. Each command below is a fresh process of the compiled binary.

// manyRules is a project with n script rules over docs/** (all refusing FORBIDDEN), committed
// after one seed commit. Returns the env, the project, the rules' names and the base (the commit
// that added them).
func manyRules(t *testing.T, n int) (*Env, string, []string, string) {
	t.Helper()
	e := harness.New(t, harness.WithoutShippedFileGuards(), harness.NoAutoCheck())
	proj := e.Project()
	e.GitInit(proj)
	e.WriteFile(proj, "docs/seed.md", "seed\n")
	e.CommitAll(proj, "the project")
	var names []string
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("docs%02d", i)
		e.FileGuard(proj, name, scriptRule, map[string]string{"check.sh": forbiddenCheck})
		names = append(names, name)
	}
	e.CommitAll(proj, "the rules")
	return e, proj, names, e.Git(proj, "rev-parse", "HEAD")
}

func mustCode(t *testing.T, r harness.Result, want int, what string) {
	t.Helper()
	if r.Code != want {
		t.Fatalf("%s: exit %d, want %d:\n%s", what, r.Code, want, r.Output)
	}
}

// T001_20: a fail, then the fix, then verify: verify sees the newest verdict, never the older
// one, in the process that follows the run and in every later one.
// sr:proves cache/finished-verdicts-reused
func TestT001_20_VerifySeesTheNewestVerdictAfterEachRun(t *testing.T) {
	e, proj, names, base := manyRules(t, 3)
	commitDoc(e, proj, "docs/a.md", "FORBIDDEN\n", "add a")

	mustCode(t, run(e, proj, base, "HEAD"), 1, "run of the forbidden file")
	v := verify(e, proj, base, "HEAD")
	mustCode(t, v, 1, "verify after the failing run")
	for _, n := range names {
		mustContain(t, v.Output, n)
	}

	commitDoc(e, proj, "docs/a.md", "fine now\n", "fix a")
	if v := verify(e, proj, base, "HEAD"); v.Code != 1 {
		t.Fatalf("verify of fixed content nobody judged: exit %d, want 1 (not judged):\n%s", v.Code, v.Output)
	}
	mustCode(t, run(e, proj, base, "HEAD"), 0, "run of the fixed file")
	for i := 0; i < 2; i++ {
		mustCode(t, verify(e, proj, base, "HEAD"), 0, fmt.Sprintf("verify #%d after the passing run", i+1))
	}

	// and back: a new failing change after the pass is seen as a fail, never the stale pass
	commitDoc(e, proj, "docs/b.md", "FORBIDDEN again\n", "add b")
	mustCode(t, run(e, proj, base, "HEAD"), 1, "run of the second forbidden file")
	v = verify(e, proj, base, "HEAD")
	mustCode(t, v, 1, "verify after the second failing run")
	mustContain(t, v.Output, "docs/b.md")
}

// T001_21: a run recorded by another clone and pulled: the reader's verdict goes from "not
// judged" to the pass the moment the writer's results reach it, and a later fail from the
// writer is seen as a fail.
func TestT001_21_AResultPulledFromAnotherCloneIsSeen(t *testing.T) {
	e, a, names, base := manyRules(t, 3)
	e.PushBranch(a, "main")
	b := e.CloneFresh(a)

	commitDoc(e, a, "docs/a.md", "fine\n", "add a")
	e.PushBranch(a, "main")
	e.Git(b, "pull", "-q", "origin", "main")

	mustCode(t, verify(e, b, base, "HEAD"), 1, "verify on the reader before anyone judged")
	mustCode(t, run(e, a, base, "HEAD"), 0, "run on the writer")
	mustCode(t, verify(e, b, base, "HEAD"), 0, "verify on the reader after the writer's results landed")

	commitDoc(e, a, "docs/b.md", "FORBIDDEN\n", "add b")
	e.PushBranch(a, "main")
	e.Git(b, "pull", "-q", "origin", "main")
	mustCode(t, run(e, a, base, "HEAD"), 1, "run on the writer of a forbidden file")
	v := verify(e, b, base, "HEAD")
	mustCode(t, v, 1, "verify on the reader after the writer's fail")
	for _, n := range names {
		mustContain(t, v.Output, n)
	}
}

// T001_22: sequential passes B1..H1 then H1..H2 let verify over B1..H2 find every rule's passes
// (the effective base is H2 for each); a pass over a NARROW range does not (B1..B2 was never
// judged); a branch that does not contain the passes' heads gets no advance from them.
// sr:proves fileguard/passes-not-re-examined
func TestT001_22_EveryRulesEffectiveBaseChains(t *testing.T) {
	e, proj, _, base := manyRules(t, 8)
	h1 := commitDocSHA(e, proj, "docs/a.md", "a\n", "add a")
	h2 := commitDocSHA(e, proj, "docs/b.md", "b\n", "add b")

	// narrow: only c1..c2 is judged: base..c1 stays unjudged, so verify over base..c2 fails
	mustCode(t, run(e, proj, h1, h2), 0, "narrow run h1..h2")
	if v := verify(e, proj, base, h2); v.Code != 1 {
		t.Fatalf("verify over base..h2 after a pass over h1..h2 only: exit %d, want 1:\n%s", v.Code, v.Output)
	}

	mustCode(t, run(e, proj, base, h1), 0, "run base..h1")
	mustCode(t, verify(e, proj, base, h2), 0, "verify base..h2 chains base..h1 and h1..h2")

	// a branch off the base with its own commit: the passes above are not on it
	e.Git(proj, "checkout", "-q", "-b", "other", base)
	z := commitDocSHA(e, proj, "docs/z.md", "z\n", "add z")
	if v := verify(e, proj, base, z); v.Code != 1 {
		t.Fatalf("verify over base..z (an unrelated branch): exit %d, want 1:\n%s", v.Code, v.Output)
	}
	e.Git(proj, "checkout", "-q", "main")
	mustCode(t, verify(e, proj, base, h2), 0, "back on main, the chain still holds")
	mustCode(t, run(e, proj, base, z), 0, "run base..z")
	mustCode(t, verify(e, proj, base, z), 0, "verify base..z after its own run")
	mustCode(t, verify(e, proj, base, h2), 0, "and main's chain is untouched by the other branch's pass")
}

// T001_23: many rules at once (the pool runs them in parallel) give a verify the same words as
// one rule at a time: every rule that refuses is named with its reason, and fixed content reports none.
// sr:proves fileguard/refusals-independent
func TestT001_23_ManyRulesRefuseExactlyAsEachWouldAlone(t *testing.T) {
	e, proj, names, base := manyRules(t, 12)
	commitDoc(e, proj, "docs/a.md", "FORBIDDEN\n", "add a")
	mustCode(t, run(e, proj, base, "HEAD"), 1, "run")

	v := verify(e, proj, base, "HEAD")
	mustCode(t, v, 1, "verify")
	for _, n := range names {
		if c := strings.Count(v.Output, "file-guard/"+n); c == 0 {
			t.Errorf("verify does not name the refusing rule %s:\n%s", n, v.Output)
		}
	}
	mustContain(t, v.Output, "FORBIDDEN text in the changeset")
	// the same words on a second process: nothing depends on what the first one decoded
	if v2 := verify(e, proj, base, "HEAD"); noTiming(v2.Output) != noTiming(v.Output) || v2.Code != v.Code {
		t.Fatalf("two verifies of the same stored results differ:\n--- first\n%s\n--- second\n%s", v.Output, v2.Output)
	}

	// only some rules see the forbidden file: the others pass and are not named
	commitDoc(e, proj, "docs/a.md", "fixed\n", "fix a")
	mustCode(t, run(e, proj, base, "HEAD"), 0, "run of the fixed file")
	v = verify(e, proj, base, "HEAD")
	mustCode(t, v, 0, "verify of the fixed file")
	for _, l := range strings.Split(v.Output, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "fail") || strings.Contains(l, "refused") {
			t.Errorf("verify reports a refusal for fixed content: %q\n%s", l, v.Output)
		}
	}
}

func commitDocSHA(e *Env, proj, path, body, msg string) string {
	commitDoc(e, proj, path, body, msg)
	return e.Git(proj, "rev-parse", "HEAD")
}

// noTiming drops the line that reports how long the evaluation took.
func noTiming(out string) string {
	var keep []string
	for _, l := range strings.Split(out, "\n") {
		if !strings.Contains(l, "file-guards evaluated in") {
			keep = append(keep, l)
		}
	}
	return strings.Join(keep, "\n")
}
