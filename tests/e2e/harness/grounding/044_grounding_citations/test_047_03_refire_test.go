package e2e

// The file-guard's DEFINING property, for a markdown guard on the commit-based
// model: a not-fine change is judged over the RANGE of commits since the rule last
// passed. So an unfixed not-fine doc keeps blocking the turn at Stop — even after an
// unrelated commit, which joins the range rather than moving its base — until a
// commit fixes it; from then on the passed range is left behind and an unrelated
// commit is not held to the old failure. The shipped example judges with a model
// and writes no ledger, so this installs a synthetic file-guard (match:
// "**/*.md", a deterministic script), so the property is measured with no
// dependence on any stub.

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// citeRefireGuard is a plain file-guard on `**/*.md` whose script refuses
// a changeset holding UNRESOLVED (standing in for an unresolved citation).
const citeRefireGuard = `match: "**/*.md"
checks:
  - script: ./check.sh
`

const citeRefireCheck = `#!/bin/sh
payload="$(cat)"
if printf '%s' "$payload" | grep -q UNRESOLVED; then
  echo '{"reason":"a citation in this doc does not resolve and is not fine"}'
  exit 1
fi
exit 0
`

// T047_09: a not-fine cited doc keeps blocking until fixed, then stops.
//
//   - Cycle 1 commits a doc with an UNRESOLVED citation; the guard refuses.
//   - Cycle 2 commits UNRELATED work (a clean, different doc) and does NOT touch the
//     bad doc — yet the Stop is refused again: the bad doc is still in the range.
//   - Cycle 3 FIXES the bad doc; nothing new is refused.
//   - Cycle 4 does more unrelated work; the fixed range is behind, so nothing is
//     refused on its account.
//
// Several cycles in one session are several Run calls with the same session id.
func TestT047_09_NotFineDocBlocksUntilFixed(t *testing.T) {
	e := newEnv(t)
	proj := e.Project()
	e.GitInit(proj)
	e.FileGuard(proj, "citations-resolve", citeRefireGuard, map[string]string{"check.sh": citeRefireCheck})
	harness.CommitInstalled(t, proj)

	sess := "s-047-09"
	refusals := func() int { return len(e.AllBlockingErrorsFrom(proj, sess, "Stop")) }

	e.Run(proj, sess, "write a doc with a bad citation", Turns("done",
		Write("w1", "report.md", "# Report\n\nsee [x](UNRESOLVED).\n"),
	).ThenCommit("add the report"))
	afterFirst := refusals()
	if afterFirst == 0 {
		t.Fatalf("a not-fine doc did not block the first cycle's turn")
	}
	if joined := strings.Join(e.BlockingErrorsFrom(proj, sess, "Stop"), "\n"); !strings.Contains(joined, "does not resolve and is not fine") {
		t.Fatalf("the guard's reason did not reach the agent:\n%s", joined)
	}

	e.Run(proj, sess, "write an unrelated clean doc", Turns("done",
		Write("w2", "other.md", "# Other\n\nclean, nothing cited.\n"),
	).ThenCommit("add another doc"))
	afterSecond := refusals()
	if afterSecond <= afterFirst {
		t.Fatalf("an unfixed not-fine doc did not block the next cycle (%d refusals after cycle 1, %d after cycle 2)",
			afterFirst, afterSecond)
	}

	e.Run(proj, sess, "fix the bad citation", Turns("done",
		Write("w3", "report.md", "# Report\n\nthe citation is resolved now.\n"),
	).ThenCommit("fix the report"))
	afterFix := refusals()
	if afterFix != afterSecond {
		t.Fatalf("the fixing commit was refused: %d refusals after cycle 2, %d after the fix", afterSecond, afterFix)
	}

	e.Run(proj, sess, "write one more clean doc", Turns("done",
		Write("w4", "third.md", "# Third\n\nclean.\n"),
	).ThenCommit("add a third doc"))
	if got := refusals(); got != afterFix {
		t.Fatalf("a FIXED doc still blocked: %d refusals after the fix, %d after one unrelated cycle", afterFix, got)
	}
}
