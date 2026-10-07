package e2e

import (
	"strings"
	"testing"
)

// onceGate refuses a second write under once/ in a session: the first is let through and
// remembered, in the session's own state. It logs the session id the engine gave it each time.
const onceGate = `on:
  - event: PreFileWrite
    match: event.path startsWith "once/"
checks:
  - script: ./once.sh
`

const onceScript = `#!/usr/bin/env bash
set -uo pipefail
cat >/dev/null
echo "id=$SR_SESSION_ID before=[$(sr-session state get wrote 2>&1)]" >> "$SR_GUARDRAIL_DIR/ledger"
if [ -n "$(sr-session state get wrote 2>/dev/null)" ]; then
  echo '{"reason":"ONCE-PER-SESSION: this session already wrote under once/"}'
  exit 1
fi
sr-session state set wrote yes >/dev/null 2>&1
exit 0
`

// T043_04: the session is the same session after a compaction: the engine's id for it does not
// change, what a rule stored before is read back after, and a gate keyed on the session (once per
// session) does not reset — the write after the compaction is refused as the second.
func TestT043_04_ASessionKeepsItsIdentityAndStateAcrossACompaction(t *testing.T) {
	e, proj := project(t)
	e.Gate(proj, "once-per-session", onceGate, map[string]string{"once.sh": onceScript})
	e.CommitAll(proj, "the gate")

	res := e.Run(proj, "s-043-04", prompt, Turns("done",
		Write("w1", "once/a.md", "first"),
		Compact("k1"),
		Write("w2", "once/b.md", "second"),
	))
	if !res.Refused() {
		t.Fatalf("the second write of the session was let through after a compaction: the gate's state was lost:\n%s", res.Output)
	}
	if !strings.Contains(strings.Join(res.Refusals(), "\n"), "ONCE-PER-SESSION") {
		t.Errorf("the refusal is not the gate's:\n%v", res.Refusals())
	}
	if !e.Exists(proj, "once/a.md") || e.Exists(proj, "once/b.md") {
		t.Errorf("the first write should have landed and the second not (a: %v, b: %v)", e.Exists(proj, "once/a.md"), e.Exists(proj, "once/b.md"))
	}

	lines := e.GateLedgerLines(proj, "once-per-session", "ledger")
	if len(lines) != 2 {
		t.Fatalf("want the gate asked twice (before and after the compaction), got %d: %q", len(lines), lines)
	}
	idBefore, idAfter := strings.Fields(lines[0])[0], strings.Fields(lines[1])[0]
	if idBefore == "id=" || idBefore != idAfter {
		t.Errorf("the session's id changed across the compaction: %q then %q (a new identity is minted)", idBefore, idAfter)
	}
	if !strings.HasSuffix(lines[0], "before=[]") || !strings.HasSuffix(lines[1], "before=[yes]") {
		t.Errorf("what the gate stored before the compaction was not read back after it: %q", lines)
	}
}
