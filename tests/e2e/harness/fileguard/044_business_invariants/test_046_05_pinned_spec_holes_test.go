package e2e

// pinned-spec-holds, the routes around it. Each test here is a way an agent could
// rewrite a pinned rule without the user's words that the first version of the
// rule let through (review of #74): a marker spelled in a form the engine reads
// but the predicate did not, a marker dropped or moved before the spec edit, the
// spec moved away with `git mv` so the rewrite reads as a create, and the code
// re-pinned to new wording appended beside the rule. Every one must be refused,
// and SPEC.md must keep the rule.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ruleChangeHeading is the first line of pinned-spec-holds' judge prompt, which
// JudgeCalls counts by.
const ruleChangeHeading = "Did the user ask for this rule to change?"

func readFile(t *testing.T, proj, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(proj, rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

const refundBody = "func Refund(charged, amount int) bool { return amount <= charged }\n"

// pinnedSpecProjectMarker commits billingSpec and a charge.go whose Refund carries
// the marker line markerLine(proj, sha) builds, and returns the project and the
// spec's sha.
func pinnedSpecProjectMarker(t *testing.T, e *env, markerLine func(proj, sha string) string) (string, string) {
	t.Helper()
	proj := biProject(t, e)
	sha := commitSpec(t, e, proj, "SPEC.md", billingSpec, "spec")
	e.WriteFile(proj, "src/charge.go", markerLine(proj, sha)+"\n"+refundBody)
	e.CommitAll(proj, "pinned refund")
	return proj, sha
}

// T046_19: moving a pin to the SAME wording at a new place (a line inserted above
// the rule shifts it down) changes nothing the code answers to, and needs nothing.
func TestT046_19_RepinToSameWordingNeedsNothing(t *testing.T) {
	e := newEnv(t)
	proj := pinnedSpecProject(t, e)
	e.InstallJudgeClaudeCapturing(proj, "judge-prompt.txt", `{"pass": true, "reasoning": ""}`)

	shifted := strings.Replace(billingSpec, "Billing invariants\n", "Billing invariants\n(see also PAYMENTS.md)\n", 1)
	e.WriteFile(proj, "SPEC.md", shifted)
	e.CommitAll(proj, "a line above the rules")
	newSha := e.Git(proj, "rev-parse", "HEAD")

	repinned := invariantCode(proj+"@"+newSha+":SPEC.md#L4-4", refundBody)
	res := e.Run(proj, "s-046-19", "re-pin Refund after the spec moved", Turns("done",
		Write("w1", "src/charge.go", repinned),
	).ThenCommit("write the files"))
	if res.Refused() {
		t.Fatalf("a re-pin to the same wording was refused:\n%s", res.Output)
	}
	if n := e.JudgeCalls(proj, "judge-prompt.txt", ruleChangeHeading); n != 0 {
		t.Errorf("a re-pin to the same wording was sent to the rule-change judge %d time(s)", n)
	}
	if !strings.Contains(readFile(t, proj, "src/charge.go"), "#L4-4") {
		t.Errorf("the same-wording re-pin did not land")
	}
}
