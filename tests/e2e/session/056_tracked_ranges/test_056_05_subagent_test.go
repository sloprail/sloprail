package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// subagentProject is the project whose judge refuses (a stored FAIL is what Stop reports); enableSubagentStopCheck says whether the project
// opts in to a sub-agent's own Stop verifying the tracked ranges (the harness does not opt in
// by default). The config the harness wrote (its `disabled:` list of the shipped guards) stays:
// only the rule under test applies.
func subagentProject(t *testing.T, enableSubagentStopCheck bool) (*Env, string) {
	t.Helper()
	e, proj := failingProject(t)
	if enableSubagentStopCheck {
		path := filepath.Join(proj, ".sloprail", "config.yaml")
		body, _ := os.ReadFile(path)
		e.WriteFile(proj, ".sloprail/config.yaml", "enable_subagent_stop_check: true\n"+string(body))
		e.CommitAll(proj, "the config")
	}
	return e, proj
}

func runSubagentCommit(t *testing.T, e *Env, proj, sess string) harness.Result {
	// The sub-agent judges its range (the judge refuses it) before it stops.
	sub := harness.SubagentScript(t, harness.Turns("sub done",
		Bash("sb1", "mkdir -p docs && echo 'the release is Friday' > docs/a.md"),
	).ThenCommit("the sub-agent's doc"))
	return e.Run(proj, sess, "delegate the doc", Turns("root done", harness.Dispatch("d1", "write the doc", sub, "worktree")))
}

// T056_05: by default a sub-agent's folders are tracked, but only the ROOT's Stop verifies
// them: the sub-agent's own Stop does not (it does not see the whole picture), and the root's
// refuses the range with a stored FAIL, naming the sub-agent's worktree.
// sr:proves subagents/ranges-verified-at-the-parents-turn-end
func TestT056_05_ByDefaultOnlyTheRootsStopVerifiesASubagentsRange(t *testing.T) {
	e, proj := subagentProject(t, false)
	const sess = "s-056-05"

	res := runSubagentCommit(t, e, proj, sess)

	if res.AnySubagentStopBlocked() {
		t.Fatalf("the sub-agent's own Stop verified a tracked range:\n%s", res.Output)
	}
	got := strings.Join(e.BlockingErrorsFrom(proj, sess, "Stop"), "\n")
	if !strings.Contains(got, failedText) {
		t.Fatalf("the root's Stop did not refuse the sub-agent's failing range:\n%s", got)
	}
}

// T056_06: with `enable_subagent_stop_check: true` the sub-agent's own Stop verifies its ranges too.
// sr:proves subagents/ranges-verified-at-the-parents-turn-end
func TestT056_06_OptingInMakesTheSubagentsOwnStopVerify(t *testing.T) {
	e, proj := subagentProject(t, true)
	const sess = "s-056-06"

	res := runSubagentCommit(t, e, proj, sess)

	if !res.AnySubagentStopBlocked() || !strings.Contains(res.Output, failedText) {
		t.Fatalf("the sub-agent's Stop did not refuse its failing range:\n%s", res.Output)
	}
}
