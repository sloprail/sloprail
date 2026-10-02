package e2e

// The file-guard's DEFINING property on the commit-based model: a not-fine change is
// judged over the RANGE of commits since the rule last passed. So a not-fine
// endpoint keeps blocking the turn at Stop — even after an unrelated commit, which
// joins the range instead of moving its base — until a commit fixes it; from then
// on the passed range is behind and an unrelated commit is not held to it. The
// shipped marker-anchored example is a script plus a judge, so this installs a
// file-guard of the SAME shape (match: any(markers, .kind == "endpoint"), a
// deterministic script): the property is measured without any judge stub.

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// refireGuard is a marker-anchored file-guard whose script refuses a changeset
// whose content holds FORBIDDEN.
const refireGuard = `match: any(markers, .kind == "endpoint")
checks:
  - script: ./check.sh
`

const refireCheck = `#!/bin/sh
payload="$(cat)"
if printf '%s' "$payload" | grep -q FORBIDDEN; then
  echo '{"reason":"this endpoint uses a forbidden construct and is not fine"}'
  exit 1
fi
exit 0
`

// T048_06: a not-fine endpoint file keeps blocking until fixed, then stops.
//
//   - Cycle 1 commits a FORBIDDEN endpoint; the guard refuses.
//   - Cycle 2 commits UNRELATED work (a clean, different endpoint) and does NOT touch
//     the bad file — yet the Stop is refused again: the bad file is still in the range.
//   - Cycle 3 FIXES the bad file; nothing new is refused.
//   - Cycle 4 does more unrelated work; the passed range is behind, so nothing is
//     refused on its account.
//
// Several cycles in one session are several Run calls with the same session id.
func TestT048_06_NotFineEndpointBlocksUntilFixed(t *testing.T) {
	e := newEnv(t)
	proj := e.Project()
	e.GitInit(proj)
	e.FileGuard(proj, "endpoint-conforms", refireGuard, map[string]string{"check.sh": refireCheck})
	harness.CommitInstalled(t, proj)

	sess := "s-048-06"
	refusals := func() int { return len(e.AllBlockingErrorsFrom(proj, sess, "Stop")) }

	e.Run(proj, sess, "add a forbidden endpoint", Turns("done",
		Write("w1", "get-users.ts", "// sr:endpoint users.list\nFORBIDDEN construct here\n"),
	).ThenCommit("add the users endpoint"))
	afterFirst := refusals()
	if afterFirst == 0 {
		t.Fatalf("a not-fine endpoint did not block the first cycle's turn")
	}
	if joined := strings.Join(e.BlockingErrorsFrom(proj, sess, "Stop"), "\n"); !strings.Contains(joined, "forbidden construct and is not fine") {
		t.Fatalf("the guard's reason did not reach the agent:\n%s", joined)
	}

	e.Run(proj, sess, "add an unrelated clean endpoint", Turns("done",
		Write("w2", "get-orders.ts", "// sr:endpoint orders.list\nclean body\n"),
	).ThenCommit("add the orders endpoint"))
	afterSecond := refusals()
	if afterSecond <= afterFirst {
		t.Fatalf("an unfixed not-fine endpoint did not block the next cycle (%d refusals after cycle 1, %d after cycle 2)",
			afterFirst, afterSecond)
	}

	e.Run(proj, sess, "fix the forbidden endpoint", Turns("done",
		Write("w3", "get-users.ts", "// sr:endpoint users.list\nnow a clean body\n"),
	).ThenCommit("fix the users endpoint"))
	afterFix := refusals()
	if afterFix != afterSecond {
		t.Fatalf("the fixing commit was refused: %d refusals after cycle 2, %d after the fix", afterSecond, afterFix)
	}

	e.Run(proj, sess, "add one more clean endpoint", Turns("done",
		Write("w4", "get-carts.ts", "// sr:endpoint carts.list\nclean body\n"),
	).ThenCommit("add the carts endpoint"))
	if got := refusals(); got != afterFix {
		t.Fatalf("a FIXED endpoint still blocked: %d refusals after the fix, %d after one unrelated cycle", afterFix, got)
	}
}
