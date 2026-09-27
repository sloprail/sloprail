package e2e

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// Research handed to a sub-agent is still the dispatching run's research: a
// dispatch whose prompt declares #research activates research-run, and the depth
// gate at the dispatching session's Stop counts the sub-agent's commands too.
// Found in a real Haiku run, which put #research only in the sub-agent's prompt
// and so ran its research under no gate at all.

// delegatedResearch runs a root turn that dispatches a #research sub-agent
// scripted with subTurns, and returns the blocking errors at the root's Stop.
func delegatedResearch(t *testing.T, sess string, subTurns ...harness.Turn) []string {
	t.Helper()
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj, exampleName)
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "install")

	sub := filepath.Join(t.TempDir(), "sub.sh")
	if err := harness.Turns("sub done", subTurns...).Script(sub); err != nil {
		t.Fatalf("write sub-agent scenario: %v", err)
	}
	e.Run(proj, sess, "look into retry libraries", Turns("done",
		harness.Dispatch("d1", "#research how real projects implement retry-with-backoff", sub, ""),
	))
	return e.BlockingErrorsFrom(proj, sess, "Stop")
}

// T039_09: a #research dispatch whose sub-agent only reads a README is refused at
// the dispatching session's Stop, naming everything the run lacks.
func TestT039_09_ShallowDelegatedResearchRefused(t *testing.T) {
	blocks := delegatedResearch(t, "s-039-09",
		Bash("sb1", "echo 'read the README only'"),
	)
	joined := strings.Join(blocks, "\n")
	if !strings.Contains(joined, noCloneReason) || !strings.Contains(joined, "No gh CLI calls found") {
		t.Fatalf("delegated shallow research was not refused by the depth gate:\n%s", joined)
	}
}

// T039_10: the same dispatch, whose sub-agent clones and paginates a gh search,
// admits: the sub-agent's commands count as this run's research.
func TestT039_10_DeepDelegatedResearchAdmits(t *testing.T) {
	blocks := delegatedResearch(t, "s-039-10",
		Bash("sb1", "git clone https://github.com/owner/repo /tmp/study-039-10 || true"),
		Bash("sb2", "gh search repos retry backoff --paginate || true"),
	)
	if len(blocks) != 0 {
		t.Fatalf("delegated deep research was refused:\n%s", strings.Join(blocks, "\n"))
	}
}
