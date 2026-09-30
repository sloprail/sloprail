package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// writesOwnFolderGuard is a file-guard that passes everything and records each
// time it is asked into a ledger INSIDE its own rule folder ($SR_GUARDRAIL_DIR).
// That is the trap: the rule's hash covers the whole folder, so once the ledger is
// swept into a commit the rule's definition has changed.
const writesOwnFolderGuard = `match: "notes/**/*.md"
checks:
  - script: ./check.sh
`

const writesOwnFolderCheck = `#!/bin/sh
cat > /dev/null
echo asked >> "$SR_GUARDRAIL_DIR/ledger"
exit 0
`

// T034_19: a file-guard that writes into its own rule folder voids its watermark.
//
// Every other test in this tree keeps its ledger OUTSIDE the project (harness.Ledger),
// which is what a test wants and what hides this trap. Here the ledger is written into
// the rule's folder on purpose, so the agent's `git add -A` sweeps it into its commit and
// the rule folder changes: its hash changes, so the watermark the rule earned by passing is voided
// and the rule is judged again from the PARENT of the last commit that touched its
// folder. That parent is the commit before the sweep, so the work committed together
// with the ledger IS judged (the floor is the parent, not the commit itself), and so
// is whatever the rule has already passed: the rule is re-asked about it.
//
// Keep a rule's state in `sr-session state` or under .git/, never in its folder.
func TestT034_19_ARuleThatWritesItsOwnFolderLosesItsWatermark(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.FileGuard(proj, "own-ledger", writesOwnFolderGuard, map[string]string{"check.sh": writesOwnFolderCheck})
	harness.CommitInstalled(t, proj)

	const session = "s-034-19"
	asked := func() int { return len(e.FileGuardLedgerLines(proj, "own-ledger", "ledger")) }

	e.Run(proj, session, "first note", Turns("done",
		Write("w1", "notes/a.md", "a\n"),
	).ThenCommit("first note"))
	first := asked()
	if first != 1 {
		t.Fatalf("premise: the rule should be asked once about the first note, asked %d times", first)
	}

	e.Run(proj, session, "second note", Turns("done",
		Write("w2", "notes/b.md", "b\n"),
	).ThenCommit("second note, with the ledger"))
	if out := e.Git(proj, "show", "--stat", "--format=", "HEAD"); !strings.Contains(out, ".sloprail/file-guard/own-ledger/ledger") {
		t.Fatalf("premise: the second commit should have swept the ledger into the rule's folder:\n%s", out)
	}
	if got := asked(); got <= first {
		t.Fatalf("the commit that swept the rule's ledger into its folder was not judged (%d asks, was %d): "+
			"the floor is the parent of the last commit that touched the folder, so that commit is in the range", got, first)
	}
}
