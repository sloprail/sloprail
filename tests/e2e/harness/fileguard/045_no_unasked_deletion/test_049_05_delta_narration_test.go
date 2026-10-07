package e2e

import (
	"github.com/sloprail/sloprail/tests/e2e/harness"
	"strings"
	"testing"
)

// T049_17: the judge's rubric rule #2 ("absolute, not a delta" —
// change-is-clean-and-absolute.md.j2) is judged against the ACTUAL diff of a
// delta-narrating replacement, not merely a stub verdict.
//
// TestT049_03 (test_049_01_verbatim_test.go) proves a FAILING stub verdict
// blocks — real plumbing, but the scenario there is a pure addition with a
// canned reasoning string; it says nothing about whether a genuinely
// delta-narrating `+` line (one that reads "changed from X to Y" instead of
// stating final content) actually reaches the judge as evidence to reason
// about. Since the real judge model is not driven in e2e (see the package's
// TODO(D3)), the strongest available proof is T049_11/12's own pattern:
// capture the rendered prompt and assert the SPECIFIC delta-narrating text
// the rubric's rule #2 exists to catch is present in the diff the judge is
// actually handed — closing the gap between "a stub blocks" and "the judge
// sees the evidence its own rubric names".
func TestT049_17_DeltaNarratingReplacementReachesJudgePrompt(t *testing.T) {
	e := newEnv(t)
	proj := nudProject(t, e)

	const original = "the deadline is March 1st"
	// A delta-narrating replacement — exactly rule #2's "Fail" shape: it keeps
	// the old value as an aside and describes the edit rather than just
	// stating the corrected content.
	const deltaNarrating = "the deadline was changed from March 1st to April 1st"
	seedCommittedMemory(t, e, proj, "memories/topic.md",
		"keep this line\n"+original+"\n")

	e.InstallJudgeClaudeCapturing(proj, "judge-prompt.txt",
		`{"pass": false, "reasoning": "the added line narrates the change instead of stating final content"}`)

	prompt := "please correct the deadline"
	sess := "s-049-16"
	res := e.Run(proj, sess, prompt, Turns("done",
		srWrite("w1", "memories/topic.md", "keep this line\n"+deltaNarrating+"\n", "please correct the deadline"),
	).ThenCommit("write the files", harness.CitesUser("please correct the deadline")))

	if blocks := e.BlockingErrorsFrom(proj, sess, "Stop"); len(blocks) == 0 {
		t.Fatalf("the delta-narrating replacement did not block the turn at Stop:\n%s", res.Output)
	}

	captured := e.JudgePrompt(proj, "judge-prompt.txt")
	if captured == "" {
		t.Fatalf("the judge never ran — no prompt captured")
	}
	// The rubric judges a unified diff. The delta-narrating text must reach it
	// as an ADDED (`+`) line — that is the evidence rule #2 is written against
	// ("A `+` line is written as a delta"). Without this, a rubric that
	// perfectly describes the violation could still be judging a diff that
	// never actually shows it.
	if !strings.Contains(captured, "+"+deltaNarrating) {
		t.Fatalf("the delta-narrating replacement did not reach the judge prompt as an added diff line — rule #2 has nothing to judge against:\n%s", captured)
	}
	// The old (removed) value must ALSO be visible as a `-` line — rule #2's
	// "keeps the old value as an aside" failure mode is only judgable if the
	// diff shows what was replaced, not just what was added.
	if !strings.Contains(captured, "-"+original) {
		t.Fatalf("the original value did not reach the judge prompt as a removed diff line:\n%s", captured)
	}
}
