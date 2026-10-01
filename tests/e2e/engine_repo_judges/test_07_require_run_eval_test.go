package e2e

import (
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// require-run-eval-skill: an sr-eval command is refused until the run-eval skill
// was loaded this session; once it was, the same command runs. Both the direct
// binary (any path, matched by basename) and the `sr eval` proxy form are gated.
func TestRequireRunEvalSkill_RefusedThenAdmitted(t *testing.T) {
	for _, cmd := range []string{"bin/sr-eval --help", "sr eval --help"} {
		t.Run(cmd, func(t *testing.T) {
			e := New(t)
			proj := project(t, e, "require-run-eval-skill")

			res := e.Run(proj, "s-run-eval-refused", "run an eval", Turns("done",
				harness.Bash("b1", cmd),
			))
			if !res.Refused() {
				t.Fatalf("%q ran without the run-eval skill loaded:\n%s", cmd, res.Output)
			}
			if !res.Saw("run-eval") {
				t.Errorf("the refusal did not name the run-eval skill:\n%s", res.Output)
			}

			res = e.Run(proj, "s-run-eval-admitted", "load the skill, then run an eval", Turns("done",
				harness.Skill("s1", "run-eval"),
				harness.Bash("b1", cmd),
			))
			if res.Refused() {
				t.Errorf("%q was refused although run-eval was loaded:\n%s", cmd, res.Output)
			}
		})
	}
}

// Other commands are not gated, so the rule never blocks unrelated work.
func TestRequireRunEvalSkill_OtherCommandsUngated(t *testing.T) {
	e := New(t)
	proj := project(t, e, "require-run-eval-skill")

	res := e.Run(proj, "s-run-eval-other", "list files", Turns("done",
		harness.Bash("b1", "ls"),
	))
	if res.Refused() {
		t.Errorf("an unrelated command was refused by require-run-eval-skill:\n%s", res.Output)
	}
}
