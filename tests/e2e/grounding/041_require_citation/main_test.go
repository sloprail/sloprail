package e2e

import (
	"os"
	"os/exec"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// require_citation: `require: [{citation}]` on a file-guard and on a gate,
// driven through the mock as a real session.
//
// A citation rides on the ACTION: `sr-file write|edit|delete ... --cite:user
// '<quote>'` grounds the file it changes, and `sr-session trajectory cite
// '<quote>' && <cmd>` grounds a command. The session resolves each quote against
// its own record before any rule sees the event, and a rule requiring a citation
// refuses an action carrying none — so a guarded file can only be changed the
// grounded way, and a guarded command only run behind a resolving cite.
var New = harness.New

func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}

var (
	Turns = harness.Turns
	Write = harness.Write
	Bash  = harness.Bash
)

// prompt is the user's own message every scenario cites.
const prompt = "record the decision to adopt a decision log"

// commitAll commits the project's current tree so installed rules (and any
// seeded file) are the baseline, not the first cycle's difference.
func commitAll(t *testing.T, proj string) {
	t.Helper()
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-m", "baseline"}} {
		if out, err := exec.Command("git", append([]string{"-C", proj}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

// recordScript appends what the check was handed — kind, path, citation count and
// the first quote — so a test sees the citations the event carried.
const recordScript = `#!/usr/bin/env bash
set -uo pipefail
payload="$(cat)"
printf '%s' "$payload" | jq -c '{kind: .event.kind, path: (.event.path // ""), resultKnown: (.event.resultKnown // null), n: (.event.citations | length), quote: (.event.citations[0].quote // ""), line: (.event.citations[0].line // 0)}' >> "$SR_GUARDRAIL_DIR/ledger"
exit 0
`
