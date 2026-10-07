package e2e

import "testing"

// goalVerifyRelative is a verify.sh written the way the skill tells agents to write
// one: against a REPO-RELATIVE path, with no $SR_WORKSPACE. It only works when the
// gate runs it from the workspace root; run from the guard's own folder (a check's
// default cwd) the path never resolves and the gate refuses a met target.
const goalVerifyRelative = `#!/bin/sh
if [ -f "evals/target-met" ]; then
  exit 0
fi
echo "accuracy below target" >&2
exit 1
`

// T050_03: goal-verify runs verify.sh from the workspace root. Unmet: refused. Met
// (the file a repo-relative path names exists): admitted.
func TestT050_03_VerifyRunsFromWorkspaceRoot(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj)
	e.WriteExecutable(proj, "goal/"+goalName+"/verify.sh", goalVerifyRelative)

	sess := "s-050-03"
	e.Run(proj, sess, "commit to the accuracy target", Turns("done",
		Write("g1", "goal/"+goalName+"/goal.yaml", goalYAML),
	))
	if blocks := e.BlockingErrorsFrom(proj, sess, "Stop"); !containsAny(blocks, keepIterating) {
		t.Fatalf("the unmet target did not block the Stop:\n%v", blocks)
	}

	e.Run(proj, sess, "hit the target", Turns("done",
		Write("m1", "evals/target-met", "accuracy=0.96"),
	))
	if status := e.GateState(proj, sess, "goal-verify"); status != "pass" {
		t.Fatalf("goal-verify recorded %q after the repo-relative target file existed, want pass — verify.sh must run with the workspace as its cwd", status)
	}
	if active, _ := e.ContextState(proj, sess, "goal-tracking"); active {
		t.Fatalf("the goal-tracking context stayed active after the goal was met")
	}
}
