package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/internal/checkstore"
	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T033_08: an ENGINE failure (a run with an error and no failing check, e.g. a snapshot
// that could not be made) is not the agent's refusal: the merge gate does not list it as a
// refusal to fix. The tip it left unjudged is still owed a judgement, so the merge waits for
// the next Stop, which judges it again.
func TestT033_08_AnEngineFailureIsNotARefusalOfTheWork(t *testing.T) {
	const sess = "s-033-08"
	e, proj := unjudgedProject(t)

	e.Run(proj, sess, "write the docs", Turns("done", harness.CommitFile("c1", "docs/a.md", "clean words", "add a")))
	if strings.Contains(e.ChecksStatus(proj, sess, "--failing"), "file-guard/docs") {
		t.Fatalf("premise: the clean commit was refused:\n%s", e.ChecksStatus(proj, sess))
	}
	tip := e.Git(proj, "rev-parse", "HEAD")

	// Another rule's run at that tip died in the engine.
	e.RecordCheckRun(proj, sess, checkstore.CheckRun{
		CheckID: "file-guard/other", BaseRef: "base000", HeadRef: tip,
		ExitCode: 1, Error: "gitrepo: snapshot of " + tip[:12] + ": gitrepo: git worktree add --detach --force /tmp/x/tree failed",
		Metadata: map[string]any{"ruleHash": "h1", "eventKind": "Changeset", "state": "complete"},
	})

	res := e.Run(proj, sess, "merge", Turns("done", Bash("m1", "gh pr merge --admin --squash")))
	if res.Saw("has refusals") || res.Saw("(engine)") || res.Saw("snapshot of") {
		t.Fatalf("an engine failure was listed as a refusal of the agent's work:\n%s", res.Output)
	}
}
