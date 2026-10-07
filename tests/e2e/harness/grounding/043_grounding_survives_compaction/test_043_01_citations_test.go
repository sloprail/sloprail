package e2e

import (
	"strings"
	"testing"
)

// T043_01: the user's words cited in a commit before the compaction still resolve after it:
// the range judged afterwards is not refused for want of a citation, and a commit made after
// the compaction behind a cite of the same words goes through the commit gate.
func TestT043_01_AUserCitationMadeBeforeACompactionResolvesAfterIt(t *testing.T) {
	e, proj := project(t)
	res := e.Run(proj, "s-043-01", prompt, Turns("done", steps(
		stage("a", "docs/a.md", "a"),
		citeUser("c1", "add a", userQuote),
		Compact("k1"),
		stage("b", "docs/b.md", "b"),
		citeUser("c2", "add b", userQuote),
		Bash("v", "sr-session trajectory cite '"+userQuote+"'"),
	)...))
	if res.Refused() {
		t.Fatalf("a cited commit was refused:\n%s", res.Output)
	}
	subjects := e.Git(proj, "log", "--format=%s")
	has(t, subjects, "add a")
	has(t, subjects, "add b")
	if got := refusal(e, proj, "s-043-01"); strings.Contains(got, noCitation) {
		t.Errorf("the citation made before the compaction no longer grounds the range:\n%s", got)
	}
}

// T043_02: the control. Words nobody said do not resolve after a compaction either, so the
// check above is judging citations on this harness and not passing for want of anything to judge.
func TestT043_02_WordsNobodySaidStillDoNotResolveAfterACompaction(t *testing.T) {
	e, proj := project(t)
	res := e.Run(proj, "s-043-02", prompt, Turns("done", steps(
		stage("a", "docs/a.md", "a"),
		Compact("k1"),
		citeUser("c1", "add a", "words nobody said 5521"),
	)...))
	if !res.Refused() {
		t.Fatalf("a cite of words nobody said resolved after a compaction:\n%s", res.Output)
	}
	if strings.Contains(e.Git(proj, "log", "--format=%s"), "add a") {
		t.Errorf("the refused commit was made")
	}
}

// T043_03: a tool's output cited before the compaction still resolves after it.
func TestT043_03_AToolResultCitationMadeBeforeACompactionResolvesAfterIt(t *testing.T) {
	e, proj := project(t)
	res := e.Run(proj, "s-043-03", prompt, Turns("done", steps(
		Bash("t1", "echo '"+toolQuote+"'"),
		stage("a", "results/a.md", "a"),
		citeTool("c1", "record the build", toolQuote),
		Compact("k1"),
		Bash("v", "sr-session trajectory cite --source-types tool_result '"+toolQuote+"'"),
	)...))
	if res.Refused() {
		t.Fatalf("a cited commit was refused:\n%s", res.Output)
	}
	has(t, e.Git(proj, "log", "--format=%s"), "record the build")
	if got := refusal(e, proj, "s-043-03"); strings.Contains(got, noCitation) {
		t.Errorf("the citation made before the compaction no longer grounds the range:\n%s", got)
	}
}
