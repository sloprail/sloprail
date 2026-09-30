package e2e

import (
	"os"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// require_citation: `require: [{citation}]` on a gate (prevention) and on a file-guard (Stop),
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

// recordScript appends what the check was handed — kind, path, citation count and
// the first quote — so a test sees the citations the event carried.
const recordScript = `#!/usr/bin/env bash
set -uo pipefail
payload="$(cat)"
printf '%s' "$payload" | jq -c '{kind: .event.kind, path: (.event.path // ""), resultKnown: (.event.resultKnown // null), n: (.event.citations | length), quote: (.event.citations[0].quote // ""), line: (.event.citations[0].line // 0)}' >> "$SR_GUARDRAIL_DIR/ledger"
exit 0
`

// failClosedRecordScript is recordScript for a GATE bound to a pre-write event: it
// notes what it was handed, then refuses a create or update whose result the
// engine could not compute (`resultKnown` not true). A gate does not fail closed
// on an unknown result by itself: a write whose bytes nobody saw has not been
// checked, so the gate that exists to prevent refuses it.
const failClosedRecordScript = `#!/usr/bin/env bash
set -uo pipefail
payload="$(cat)"
printf '%s' "$payload" | jq -c '{kind: .event.kind, path: (.event.path // ""), resultKnown: (.event.resultKnown // null), n: (.event.citations | length), quote: (.event.citations[0].quote // ""), line: (.event.citations[0].line // 0)}' >> "$SR_GUARDRAIL_DIR/ledger"
case "$(printf '%s' "$payload" | jq -r '.event.kind')" in
  PreFileCreate|PreFileUpdate)
    if [ "$(printf '%s' "$payload" | jq -r '.event.resultKnown')" != "true" ]; then
      echo '{"reason":"the result of this write could not be computed, so it cannot be checked before it lands"}'
      exit 1
    fi ;;
esac
exit 0
`
