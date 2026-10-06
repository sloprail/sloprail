package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// writesOwnFolderGuard is a file-guard that passes everything and records each
// time it is asked into a ledger INSIDE its own rule folder ($SR_GUARDRAIL_DIR).
// Once the ledger is swept into a commit the rule's folder has changed, which a verdict's key
// does not see.
const writesOwnFolderGuard = `match: "notes/**/*.md"
checks:
  - script: ./check.sh
`

const writesOwnFolderCheck = `#!/bin/sh
cat > /dev/null
echo asked >> "$SR_GUARDRAIL_DIR/ledger"
exit 0
`

// T034_19: a ledger a file-guard writes into its own rule folder never changes what is judged.
//
// Every other test in this tree keeps its ledger OUTSIDE the project (harness.Ledger). Here
// the ledger is written into the rule's folder on purpose, and the agent's `git add -A` sweeps
// it into a commit. A verdict is keyed by its input (rule, subject, content), never by the
// rule's definition, so the commit changes the rule's recorded hash and nothing else: `verify`
// still reads the verdict `run` stored, and the next `run` does not ask the rule again.
func TestT034_19_ALedgerCommittedInsideTheGuardFolderDoesNotReJudge(t *testing.T) {
	e := harness.New(t, harness.WithoutShippedFileGuards(), harness.NoAutoCheck())
	proj := e.Project()
	e.GitInit(proj)
	e.FileGuard(proj, "own-ledger", writesOwnFolderGuard, map[string]string{"check.sh": writesOwnFolderCheck})
	harness.CommitInstalled(t, proj)

	const session = "s-034-19"
	base := e.Git(proj, "rev-parse", "HEAD")
	asked := func() int { return len(e.FileGuardLedgerLines(proj, "own-ledger", "ledger")) }

	e.Run(proj, session, "first note", Turns("done",
		Write("w1", "notes/a.md", "a\n"),
	).ThenCommit("first note"))
	if r := e.CheckRunRaw(proj, session, base, "HEAD"); r.Code != 0 {
		t.Fatalf("premise: the note should pass:\n%s", r.Output)
	}
	if got := asked(); got != 1 {
		t.Fatalf("premise: the rule should be asked once, asked %d times", got)
	}

	e.CommitAll(proj, "sweep everything, ledger included")
	if out := e.Git(proj, "show", "--stat", "--format=", "HEAD"); !strings.Contains(out, ".sloprail/file-guard/own-ledger/ledger") {
		t.Fatalf("premise: the commit should have swept the ledger into the rule's folder:\n%s", out)
	}
	if r := e.CheckVerify(proj, session, base, "HEAD"); r.Code != 0 {
		t.Fatalf("a ledger committed inside the guard folder must not void the stored verdict:\n%s", r.Output)
	}
	if r := e.CheckRunRaw(proj, session, base, "HEAD"); r.Code != 0 {
		t.Fatalf("run:\n%s", r.Output)
	}
	if got := asked(); got != 1 {
		t.Fatalf("the rule was asked again after a ledger commit (asked %d times, want 1)", got)
	}
}
