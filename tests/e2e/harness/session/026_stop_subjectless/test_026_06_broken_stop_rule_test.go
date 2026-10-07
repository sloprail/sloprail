package e2e

import (
	"strings"
	"testing"
)

// T026_06: a gate bound ONLY to Stop whose trigger match will not load does not
// block the cycle.
//
// # The finding
//
// This is the same property 013 establishes for every other kind — "a broken rule
// is not silent, and does not wedge the session" — asked of the one kind that is
// dispatched at the end of a cycle. A rule bound only to Stop whose matcher would not load was the sharpest case, because
// such a project could block EVERY cycle and never end a turn.
//
// A gate whose trigger `match` cannot compile against
// its event's scope is SKIPPED at the moment it would fire — firstMatchingEvent
// compiles the trigger match per event and, on a compile error, reports it and
// moves on without waking the gate. So a Stop gate carrying a match Stop cannot
// answer (`event.path` on a fieldless kind) neither runs nor blocks: the cycle
// ENDS. Empirically confirmed against this engine.
//
// The reasoning is the same the old finding settled on: a declaration that will not
// load is not the agent's to fix, and blocking the cycle for it would leave a
// project bound only to Stop unable to end a turn at all. What is given up is real
// and stated rather than hidden: such a project runs the whole session believing
// completeness is enforced when it is not.
//
// The gate's check PERMITS, so nothing here can block except the engine's own
// response to a rule it could not read — which is what makes the assertion sharp.
func TestT026_06_ABrokenStopRuleDoesNotBlockTheCycle(t *testing.T) {
	e, proj := project(t)
	e.Gate(proj, "narrowed-cycle", `on:
  - event: Stop
    match: event.path endsWith ".md"
checks:
  - script: ./permit.sh
`, map[string]string{"permit.sh": "#!/bin/sh\ncat >/dev/null\nexit 0\n"})
	e.CommitAll(proj, "the project before the session")

	e.Run(proj, "s-026-06", "write a note", Turns("done",
		Write("w1", "notes.md", "hello\n"),
	))

	// The cycle ended: a Stop gate that could not load blocked nothing.
	told := strings.Join(e.BlockingErrorsFrom(proj, "s-026-06", "Stop"), "\n")
	if strings.Contains(told, "narrowed-cycle") {
		t.Fatalf("a gate whose trigger match could not load blocked the cycle — an invalid rule "+
			"must block nothing, and a rule bound only to Stop would otherwise make every turn in "+
			"the session unendable:\n%s", told)
	}
}

// T026_07: a SOUND Stop gate beside nothing broken still lets the cycle end.
//
// The negative control for T026_06, and it is not optional. The assertion above is
// "the turn was not blocked", which passes trivially against an engine that blocks
// no cycle at all — and a fix that refused whenever any gate existed would satisfy
// T026_06 completely while making the product unusable.
//
// Same shape as T026_06: one gate, bound to Stop, whose check permits. The only
// difference is that its trigger has no match — which here means it loads and runs,
// since Stop carries no fields to narrow on. Its verdict is a pass, and the cycle
// ends.
func TestT026_07_ASoundStopRuleLetsTheCycleEnd(t *testing.T) {
	e, proj := project(t)
	e.Gate(proj, "sound-cycle", `on:
  - event: Stop
checks:
  - script: ./permit.sh
`, map[string]string{"permit.sh": "#!/bin/sh\ncat >/dev/null\nexit 0\n"})
	e.CommitAll(proj, "the project before the session")

	e.Run(proj, "s-026-07", "write a note", Turns("done",
		Write("w1", "notes.md", "hello\n"),
	))

	if blocks := e.BlockingErrorsFrom(proj, "s-026-07", "Stop"); len(blocks) != 0 {
		t.Fatalf("a sound Stop gate whose check permitted still blocked the cycle:\n%s",
			strings.Join(blocks, "\n"))
	}
	// And it really did run and pass — proving the clean cycle above is a gate that
	// fired, not one that never loaded.
	if status := e.GateState(proj, "s-026-07", "sound-cycle"); status != "pass" {
		t.Fatalf("the sound Stop gate recorded %q, want pass — the control must be a gate that "+
			"actually ran", status)
	}
}

// T026_08: a broken rule bound to a kind this cycle never produced does not block
// the cycle.
//
// The scoping half, and the mirror of the bug rather than a fix for it. 013
// establishes at the pre-tool point that a broken rule stops exactly the events it
// bound to and nothing else — a typo in a rule about deletions must not block a
// write no rule was ever written about. The same must hold here, and for a sharper
// reason: fixing a broken rule is itself work done inside a cycle, so a rule that
// blocked every cycle for any load fault would leave the author no way to clear it.
//
// Without this, the obvious over-fix — refuse the cycle whenever anything failed to
// load — passes, and the scoping the rest of the engine is careful about is quietly
// abandoned at the hook point that ends turns.
//
// # Why PreFileDelete, and not PreFileCreate
//
// The kind has to be one this cycle genuinely does not produce, and a write DOES
// produce a PreFileCreate however little the scenario does. A gate wakes on
// pre-action kinds, so the broken rule is bound to PreFileDelete — deleting nothing
// means no PreFileDelete is ever extracted, and the gate's trigger match is never
// even reached. A block naming this rule could then only be the engine refusing on
// behalf of a rule about deletions.
func TestT026_08_ABrokenRuleDoesNotBlockACycleItNeverGuarded(t *testing.T) {
	e, proj := project(t)
	e.Gate(proj, "broken-delete-rule", `on:
  - event: PreFileDelete
    match: event.paht endsWith ".md"
checks:
  - script: ./permit.sh
`, map[string]string{"permit.sh": "#!/bin/sh\ncat >/dev/null\nexit 0\n"})
	e.CommitAll(proj, "the project before the session")

	e.Run(proj, "s-026-08", "write something, delete nothing", Turns("done",
		Write("w1", "notes.md", "hello\n"),
	))

	// Scoped, so the rule about deletions must not be what stops this cycle.
	// Asserted on the blocking text across every hook point, rather than on the turn
	// merely ending: this project has a broken rule, and a future change that
	// legitimately blocks for some other reason should not silently satisfy this.
	told := strings.Join(e.BlockingErrors(proj, "s-026-08"), "\n")
	if strings.Contains(told, "broken-delete-rule") {
		t.Fatalf("a rule broken on PreFileDelete blocked a cycle in which nothing was deleted — "+
			"a broken rule must stop the events it bound to and nothing else, or the author "+
			"cannot fix it:\n%s", told)
	}
}
