package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// subagentProject is the judged-docs project; enableSubagentStopCheck says whether the project
// opted in to a sub-agent's own Stop verifying the tracked ranges (the harness opts in by
// default, so the default is restored here by rewriting the config).
func subagentProject(t *testing.T, enableSubagentStopCheck bool) (*Env, string) {
	t.Helper()
	e, proj := project(t)
	cfg := ""
	if enableSubagentStopCheck {
		cfg = "enable_subagent_stop_check: true\n"
	}
	e.WriteFile(proj, ".sloprail/config.yaml", cfg)
	e.CommitAll(proj, "the config")
	return e, proj
}

func runSubagentCommit(t *testing.T, e *Env, proj, sess string) harness.Result {
	// The sub-agent stops without judging its range: that is what both tests are about.
	sub := harness.SubagentScriptUnjudged(t, harness.Turns("sub done",
		Bash("sb1", "mkdir -p docs && echo 'the release is Friday' > docs/a.md"),
	).ThenCommit("the sub-agent's doc"))
	return e.Run(proj, sess, "delegate the doc", Turns("root done", harness.Dispatch("d1", "write the doc", sub, "worktree")))
}

// T056_05: by default a sub-agent's folders are tracked, but only the ROOT's Stop verifies
// them: the sub-agent's own Stop does not (it does not see the whole picture), and the root's
// refuses the range nobody judged, naming the sub-agent's worktree.
func TestT056_05_ByDefaultOnlyTheRootsStopVerifiesASubagentsRange(t *testing.T) {
	e, proj := subagentProject(t, false)
	const sess = "s-056-05"

	res := runSubagentCommit(t, e, proj, sess)

	if res.AnySubagentStopBlocked() {
		t.Fatalf("the sub-agent's own Stop verified a tracked range:\n%s", res.Output)
	}
	got := strings.Join(e.BlockingErrorsFrom(proj, sess, "Stop"), "\n")
	if !strings.Contains(got, "not judged yet") || !strings.Contains(got, "sr-checks run --base") {
		t.Fatalf("the root's Stop did not refuse the sub-agent's unjudged range:\n%s", got)
	}
}

// T056_06: with `enable_subagent_stop_check: true` the sub-agent's own Stop verifies its ranges too.
func TestT056_06_OptingInMakesTheSubagentsOwnStopVerify(t *testing.T) {
	e, proj := subagentProject(t, true)
	const sess = "s-056-06"

	res := runSubagentCommit(t, e, proj, sess)

	if !res.AnySubagentStopBlocked() || !strings.Contains(res.Output, "not judged yet") {
		t.Fatalf("the sub-agent's Stop did not refuse its unjudged range:\n%s", res.Output)
	}
}
