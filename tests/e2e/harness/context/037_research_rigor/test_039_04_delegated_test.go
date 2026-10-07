package e2e

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// Research handed to a sub-agent is still the dispatching run's research: a
// dispatch whose prompt declares #research activates research-run, and the depth
// gate at the dispatching session's Stop counts the sub-agent's clones and reads
// too. Found in a real Haiku run, which put #research only in the sub-agent's
// prompt and so ran its research under no gate at all.

// noCloneReason is the depth gate's own wording when a research run shows no
// clone of its own — the words that must reach the agent.
const noCloneReason = "This #research run has not cloned a repository"

// whatToDo is what a depth refusal tells an agent that has cloned nothing.
const whatToDo = "To finish the research: git clone a real repository that implements what you are researching, then read at least 2 of its source files (not only the README or docs)"

// delegatedResearch runs a root turn that dispatches a #research sub-agent
// scripted with subTurns, followed by the root's own after turns, and returns
// the blocking errors at the root's Stop.
func delegatedResearch(t *testing.T, e *harness.Env, proj, sess string, subTurns []harness.Turn, after ...harness.Turn) []string {
	t.Helper()
	sub := filepath.Join(t.TempDir(), "sub.sh")
	if err := harness.Turns("sub done", subTurns...).Script(sub); err != nil {
		t.Fatalf("write sub-agent scenario: %v", err)
	}
	turns := append([]harness.Turn{
		harness.Dispatch("d1", "#research how real projects implement retry-with-backoff", sub, ""),
	}, after...)
	e.Run(proj, sess, "look into retry libraries", Turns("done", turns...))
	return e.BlockingErrorsFrom(proj, sess, "Stop")
}

// T039_09: a #research dispatch whose sub-agent clones nothing is refused at the
// dispatching session's Stop, naming what the run lacks.
func TestT039_09_ShallowDelegatedResearchRefused(t *testing.T) {
	e, proj := research(t)
	blocks := delegatedResearch(t, e, proj, "s-039-09",
		[]harness.Turn{Bash("sb1", "echo 'read the README only'")},
	)
	joined := strings.Join(blocks, "\n")
	if !strings.Contains(joined, noCloneReason) || !strings.Contains(joined, whatToDo) {
		t.Fatalf("delegated shallow research was not refused by the depth gate:\n%s", joined)
	}
}

// T039_10: the same dispatch, whose sub-agent clones and reads two source files,
// admits: the sub-agent's work counts as this run's research.
func TestT039_10_DeepDelegatedResearchAdmits(t *testing.T) {
	e, proj := research(t)
	src := sourceRepo(t, e, "retry-lib")
	dst := filepath.Join(scratch(t), "retry-lib")
	blocks := delegatedResearch(t, e, proj, "s-039-10", []harness.Turn{
		Bash("sb1", "git clone "+src+" "+dst),
		Read("sr1", filepath.Join(dst, "lib", "retry.js")),
		Bash("sb2", "head -n 20 "+filepath.Join(dst, "index.js")),
	})
	if !harness.HasCap(t, harness.CapSubagentParentLink) {
		requireUnlinkedRefusal(t, blocks)
		return
	}
	if len(blocks) != 0 {
		t.Fatalf("delegated deep research was refused:\n%s", strings.Join(blocks, "\n"))
	}
}

// T039_11: the sub-agent clones, the dispatcher reads what it cloned — one run's
// research split across two trajectories still admits, because the gate judges
// the clones and the reads of every trajectory together.
func TestT039_11_SubAgentCloneDispatcherReadsAdmits(t *testing.T) {
	e, proj := research(t)
	src := sourceRepo(t, e, "retry-lib")
	dst := filepath.Join(scratch(t), "retry-lib")
	blocks := delegatedResearch(t, e, proj, "s-039-11",
		[]harness.Turn{Bash("sb1", "git clone "+src+" "+dst)},
		Read("r1", filepath.Join(dst, "lib", "retry.js")),
		Read("r2", filepath.Join(dst, "lib", "backoff.js")),
	)
	if !harness.HasCap(t, harness.CapSubagentParentLink) {
		requireUnlinkedRefusal(t, blocks)
		return
	}
	if len(blocks) != 0 {
		t.Fatalf("a sub-agent's clone read by the dispatcher was refused:\n%s", strings.Join(blocks, "\n"))
	}
}

// requireUnlinkedRefusal: a harness whose sub-agent record names no parent cannot tie the
// sub-agent's clone to the run that dispatched it, so the dispatcher's research stands alone
// and is refused for the clone it does not hold.
func requireUnlinkedRefusal(t *testing.T, blocks []string) {
	t.Helper()
	if !strings.Contains(strings.Join(blocks, "\n"), "has not cloned a repository") {
		t.Fatalf("a sub-agent linked to no parent was counted in the run's research; refusals:\n%s", strings.Join(blocks, "\n"))
	}
}
