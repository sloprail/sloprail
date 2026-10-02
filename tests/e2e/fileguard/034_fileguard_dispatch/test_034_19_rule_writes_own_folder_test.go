package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// writesOwnFolderGuard is a file-guard that passes everything and records each
// time it is asked into a ledger INSIDE its own rule folder ($SR_GUARDRAIL_DIR).
// That is the trap: the rule's hash covers the tracked files of its .sloprail root, so once
// the ledger is swept into a commit the rule's definition has changed.
const writesOwnFolderGuard = `match: "notes/**/*.md"
checks:
  - script: ./check.sh
`

const writesOwnFolderCheck = `#!/bin/sh
cat > /dev/null
echo asked >> "$SR_GUARDRAIL_DIR/ledger"
exit 0
`

// T034_19: a file-guard that writes into its own rule folder changes its own key.
//
// Every other test in this tree keeps its ledger OUTSIDE the project (harness.Ledger),
// which is what a test wants and what hides this trap. Here the ledger is written into
// the rule's folder on purpose. While it is untracked the hash ignores it, so `run`
// stores its verdict under the key it computed; the agent's `git add -A` then sweeps the
// ledger into a commit, the rule's hash changes, and the verdict `run` stored no longer
// applies: `verify` (Stop, CI) reads the new key and says "not judged yet", and the next
// `run` asks again.
//
// Keep a rule's state in `sr-session state` or under .git/, never in its folder.
func TestT034_19_ALedgerCommittedInsideTheGuardFolderChangesTheKey(t *testing.T) {
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
	if r := e.CheckVerify(proj, session, base, "HEAD"); r.Code != 0 {
		t.Fatalf("premise: the verdict run stored should be read by verify (the ledger is still untracked):\n%s", r.Output)
	}

	e.CommitAll(proj, "sweep everything, ledger included")
	if out := e.Git(proj, "show", "--stat", "--format=", "HEAD"); !strings.Contains(out, ".sloprail/file-guard/own-ledger/ledger") {
		t.Fatalf("premise: the commit should have swept the ledger into the rule's folder:\n%s", out)
	}
	if r := e.CheckVerify(proj, session, base, "HEAD"); r.Code == 0 || !strings.Contains(r.Output, "not judged yet") {
		t.Fatalf("a ledger committed inside the guard folder changes the rule's key, so the stored verdict must no longer apply:\n%s", r.Output)
	}
	if r := e.CheckRunRaw(proj, session, base, "HEAD"); r.Code != 0 {
		t.Fatalf("the rule judges again under its new key:\n%s", r.Output)
	}
	if got := asked(); got != 2 {
		t.Fatalf("the rule must be asked again under the new key (asked %d times, want 2)", got)
	}
}
