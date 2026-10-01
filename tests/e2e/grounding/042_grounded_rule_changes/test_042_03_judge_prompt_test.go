package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T042_09: the judge is handed what it rules on: the changed rule's diff, the quote the
// commit cites and the message it came from, and the rubric that says a rule's own
// refusal is no ground.
func TestT042_09_JudgeIsHandedTheChangeAndTheCitation(t *testing.T) {
	e := New(t)
	proj := project(t, e)
	e.InstallJudgeClaudeCapturing(proj, ".judge-prompt.txt", `{"pass": true, "reasoning": "grounded"}`)
	e.Run(proj, "s-042-09", "loosen the demo rule so my notes land", Turns("done",
		editScript(t, "w1", ".sloprail/file-guard/demo/check.sh", demoLoosened)...))
	e.Run(proj, "s-042-09", "commit it", Turns("done",
		harness.Commit("c1", "change the rule", harness.CitesUser("loosen the demo rule"))))
	if got, out := blocked(e, proj, "s-042-09"); got {
		t.Fatalf("a user-grounded change was refused:\n%s", out)
	}
	prompt := e.JudgePrompt(proj, ".judge-prompt.txt")
	for _, want := range []string{"--- .sloprail/file-guard/demo/check.sh (M)", "+# nothing to see", "loosen the demo rule so my notes land", "The rule's own refusal"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the judge prompt lacks %q:\n%s", want, prompt)
		}
	}
}
