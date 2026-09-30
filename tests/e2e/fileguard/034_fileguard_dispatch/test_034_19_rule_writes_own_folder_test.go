package e2e

import (
	"os"
	"path/filepath"
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

// T034_19: a file-guard that writes into its own rule folder loses its place.
//
// Every other test in this tree keeps such a ledger out of commits (GitInit
// excludes untracked non-code files at the top of a rule folder), which is what a
// test wants and what hides this trap. Here the exclusion is removed, so the
// agent's `git add -A` sweeps the ledger into its commit and the rule folder changes:
// its hash changes, so the watermark the rule earned by passing is voided and the
// rule's range restarts at the last commit that touched its own folder, which is
// that very commit. The work committed together with the ledger is therefore never
// judged: the rule is asked about cycle one, silently NOT about cycle two, and is
// asked again about cycle three (whose range starts after cycle two's commit),
// and every commit that sweeps the ledger up again repeats the gap.
//
// Keep a rule's state in `sr-session state` or under .git/, never in its folder.
func TestT034_19_ARuleThatWritesItsOwnFolderLosesItsWatermark(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	// No exclusion: the point of this test.
	if err := os.WriteFile(filepath.Join(proj, ".git", "info", "exclude"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	e.FileGuard(proj, "own-ledger", writesOwnFolderGuard, map[string]string{"check.sh": writesOwnFolderCheck})
	harness.CommitInstalled(t, proj)

	const session = "s-034-09"
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
	if got := asked(); got != first {
		t.Fatalf("the rule was asked about the commit that swept its ledger into its own folder (%d asks, was %d): "+
			"the trap this test documents (the watermark voided, the floor moved to that commit) did not happen", got, first)
	}
	if out := e.Git(proj, "show", "--stat", "--format=", "HEAD"); !contains(out, ".sloprail/file-guard/own-ledger/ledger") {
		t.Fatalf("premise: the second commit should have swept the ledger into the rule's folder:\n%s", out)
	}

	e.Run(proj, session, "third note", Turns("done",
		Write("w3", "notes/c.md", "c\n"),
	).ThenCommit("third note"))
	if got := asked(); got != first+1 {
		t.Fatalf("the rule was not asked about the third note (%d asks): its range should start after the commit that touched its folder", got)
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }
