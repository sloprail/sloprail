package e2e

import (
	"strings"
	"testing"
)

// gateNamingNewContentKnown reads the pending write's newContent and mentions
// newContentKnown (and `.changeset`) in strings, but never consults resultKnown, and
// names no non-comment Pre kind. Under a content-based exemption that would read as a
// Post-only / Changeset script and slip through; it is a GATE script, so it must not.
const gateNamingNewContentKnown = `#!/bin/sh
payload="$(cat)"
body="$(printf '%s' "$payload" | jq -r '.event.newContent')"
known="$(printf '%s' "$payload" | jq -r '.event.newContentKnown // .changeset.files[0].path')"
if printf '%s' "$body" | grep -q TODO; then
  echo '{"reason":"remove the TODO"}'
  exit 1
fi
exit 0
`

// T026_08: the resultKnown rule's exemption depends on where the script lives, not on
// what it names. The same text is refused as a gate script.
func TestT026_08_GateScriptCannotBorrowTheChangesetExemption(t *testing.T) {
	e := New(t)
	proj := e.Project()
	got := e.Run(proj, "s-026-08", "write a gate script", Turns("done",
		Write("w1", ".sloprail/gate/mine/check.sh", gateNamingNewContentKnown),
	))
	if !got.Refused() || !strings.Contains(got.Output, "without .resultKnown") {
		t.Errorf("a gate script reading newContent without resultKnown was not flagged:\n%s", got.Output)
	}
	if e.Exists(proj, ".sloprail/gate/mine/check.sh") {
		t.Errorf("the gate script landed despite the refusal")
	}
}
