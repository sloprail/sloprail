package e2e

import (
	"path/filepath"
	"strings"
	"testing"
)

// before_refusable_only, the `after` half: refusing an after-the-fact (post)
// action cannot undo the work that already landed, but it must still stop the
// TURN — which is the mechanism by which an after-the-fact rule gets anything
// corrected.
//
// # Vehicle: a plain file-guard (was old GUARDRAIL.md hooks)
//
// The old copy of this half hand-wrote a PostToolUse hook into settings.json —
// wiring no user has — and was then deleted because `sr-session stop` dispatched
// nothing, so a Post binding loaded without complaint and never ran. Both reasons
// have expired for the new dispatch: a file-guard IS the product's own
// "after the fact" binding, and its after-check runs at Stop on the settled
// file. So this is the mirror of T006_01 — the SAME refusing check, on a
// file-guard instead of a gate — and where T006_01 asserts the
// file is absent, this asserts it is present. Between them they are the whole of
// before_refusable_only, and neither means much without the other.
//
// # WHAT "CANNOT PREVENT THE WORK" MEANS, PRECISELY
//
// A file-guard (after) refusal CANNOT undo the write: the file is on disk and
// the cycle is over (nature_fileguard.go's runFileGuardsPost — "a refusal here
// does not undo the write"). But it MUST still block the turn. Conflating those
// two was a real design error before it was corrected, so this test asserts BOTH,
// on different channels, because a test checking only the file could not tell a
// blocking engine from a silently-permitting one: the file survives either way.
//
// # WHERE THE ANSWERS ARE READ FROM
//
// Not res.Refused for the block — a Stop refusal never appears as a pre-tool deny
// marker on the stream. It arrives as a hook_blocking_error attachment inside the
// conversation record, read here with BlockingErrorsFrom(…, "Stop") (the same
// channel fileguard/034_01 reads). The ledger under the guard's own folder proves
// the check RAN, which is what separates "correctly permitted" from "never fired".

// refuseAfterTheWriteLanded is the file-guard counterpart of T006_01's gate: the
// same check, bound to the same files, runs one timing later — after the write
// settles, at Stop, where it cannot undo the file. That is what makes the pair a
// comparison rather than two unrelated tests.
const refuseAfterTheWriteLanded = `match: "**/*.md"
checks:
  - script: ./refuse.sh
`

// The check records that it ran into the guard's own folder (SR_GUARDRAIL_DIR,
// which has no .md suffix so the `**/*.md` match never re-selects it), then
// refuses. Recording before refusing makes a run visible whether or not the
// refusal is acted on.
const recordThenRefuseAfter = `#!/bin/sh
cat >/dev/null
echo ran >> "$SR_GUARDRAIL_DIR/ran"
echo '{"reason":"refused after it had already landed"}'
exit 1
`

// T006_02: a refusal AFTER the fact cannot prevent work that already landed — but
// it does stop the turn.
//
// The mirror of T006_01. Same write, same refusing check, one timing later.
func TestT006_02_AnAfterRefusalCannotPreventTheWork(t *testing.T) {
	e := New(t)
	proj := e.Project()

	// The tree needs a baseline to be compared against, or no Post events are
	// produced at all and every assertion below would read the silence of a cycle
	// that never dispatched.
	e.GitInit(proj)

	e.FileGuard(proj, "afterguard", refuseAfterTheWriteLanded, map[string]string{
		"refuse.sh": recordThenRefuseAfter,
	})

	// Committed before the session, so the guard's own files are part of the
	// baseline rather than part of what the cycle appears to have changed.
	e.CommitAll(proj, "the project before the session")

	got := e.Run(proj, "s-006-02", "write a note", Turns("done",
		Write("w1", "some/notes.md", "hello"),
	))

	// The check RAN. This is the assertion the whole file rests on, and the one
	// whose absence made the original deletion correct: a guard bound to a kind
	// nothing dispatches is silent, and a silent check is indistinguishable from
	// one that ran and permitted.
	if e.FileGuardLedger(proj, "afterguard", "ran") == 0 {
		t.Fatalf("the after-check never ran, so nothing below is evidence about after-the-fact refusals:\n%s", got.Output)
	}

	// The work LANDED and stayed landed. The `after` half of the invariant: a
	// refusal at an after-the-fact point demands a correction, it does not perform
	// one. If this file were gone the engine would have rolled back a write it had
	// already allowed.
	if !e.Exists(proj, filepath.Join("some", "notes.md")) {
		t.Errorf("a check refusing AFTER the write removed the file — an after-the-fact refusal must demand a correction, not perform one")
	}

	// The turn was BLOCKED, on a separate channel. The file survives whether the
	// engine blocks or silently permits, so without this the test would be green
	// against an engine that let the turn end with the violation unaddressed.
	blocking := e.BlockingErrorsFrom(proj, "s-006-02", "Stop")
	if len(blocking) == 0 {
		t.Fatalf("the turn was not blocked despite the after-check refusing — an after-the-fact refusal cannot undo the write, but it must stop the turn, which is the only way it gets anything corrected:\n%s", got.Output)
	}

	// And the agent was TOLD why, and told which rule. Read from the blocking
	// attachments, not the record as a whole: the guard's own folder path travels
	// on payloads, so searching the whole record for the rule's name would find it
	// whether or not the refusal ever named it — an assertion that cannot fail.
	told := strings.Join(blocking, "\n")
	if !strings.Contains(told, "refused after it had already landed") {
		t.Errorf("the check's own words did not reach the agent, so it cannot know what to fix:\n%s", told)
	}
	if !strings.Contains(told, "afterguard") {
		t.Errorf("the refusal did not name the file-guard that produced it:\n%s", told)
	}
}
