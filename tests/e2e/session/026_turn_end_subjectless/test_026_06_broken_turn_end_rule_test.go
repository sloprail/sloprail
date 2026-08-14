package e2e

import (
	"strings"
	"testing"
)

// T026_06: a guardrail bound ONLY to TurnEnd that will not load is not silent.
//
// # The finding
//
// This is the same property 013 establishes for every other kind — "a broken
// declaration is not silent" — asked of the one kind that is dispatched at the
// end of a cycle rather than before a tool call. It was the one kind where it
// did not hold.
//
// Two independent pieces of the engine have to be wrong together for a rule to
// go silent, and both were:
//
//   - The pre-tool point refuses a broken declaration when an event of a kind it
//     bound to occurs (refuseForBroken). TurnEnd is never produced there — it is
//     the cycle's own event, appended by the Stop dispatch — so a rule bound only
//     to TurnEnd names a kind the pre-tool loop can never see, and is never
//     reached.
//   - The Stop dispatch, which IS where TurnEnd fires, discarded the invalid
//     list outright: `decls, _, err := guardrail.New(...).LoadWith(reg)`. Nothing
//     downstream of that line could know a declaration had failed to load.
//
// So the author writes `matcher: path endsWith ".md"` on TurnEnd — an ordinary
// mistake, since every other kind carries a path — the declaration is correctly
// rejected at load, and the project runs for the rest of the session with a rule
// it believes is enforcing completeness and which is enforcing nothing. No
// refusal, and no diagnostic on any channel that reaches the agent.
//
// That is the exact failure the whole 013 suite exists to prevent, surviving in
// the one place nobody had bound a test to. cyclemod's own package comment
// asserts the opposite outright — "A matcher naming `path` on this kind is
// refused when the guardrail loads" — which is true of the loader and was false
// of the engine.
//
// # What this test pins
//
// That the cycle is refused. Not the wording, and not which of the two sites is
// fixed: the claim is that a project keeping a guardrail it cannot load is told
// so through a channel that reaches the agent, which at the Stop hook means
// blocking the turn.
//
// The rule's hook PERMITS, so a refusal cannot come from the rule running. The
// declaration never loads, so the hook is never asked at all — the only thing
// that can block this turn is the engine's response to a rule it could not read.
func TestT026_06_ABrokenTurnEndRuleIsNotSilent(t *testing.T) {
	e, proj := project(t)
	e.Guardrail(proj, "narrowed-cycle", `---
hooks:
  TurnEnd:
    - matcher: path endsWith ".md"
      hooks:
        - type: command
          command: ./permit.sh
---

# Tries to narrow the end of a cycle to a path, which a cycle does not have
`, map[string]string{"permit.sh": "#!/bin/sh\ncat >/dev/null\nexit 0\n"})
	commitGuardrails(e, proj)

	got := e.Run(proj, "s-026-06", "write a note", Turns("done",
		Write("w1", "notes.md", "hello\n"),
	))

	// A blocked stop sends the agent round again, so the mock emits its final
	// result more than once. One result means the cycle ended with the project
	// believing a rule was enforcing something it was not.
	if strings.Count(got.Output, `"subtype":"success"`) < 2 {
		t.Fatalf("a guardrail bound only to TurnEnd failed to load and the cycle ended anyway — "+
			"the project keeps a rule it believes is guarding completeness and nothing is:\n%s",
			got.Output)
	}

	told := strings.Join(e.BlockingErrors(proj, "s-026-06"), "\n")
	if !strings.Contains(told, "narrowed-cycle") {
		t.Errorf("the refusal does not name the guardrail that would not load, so the author "+
			"cannot find the declaration to fix:\n%s", told)
	}
}

// T026_07: a SOUND TurnEnd rule beside nothing broken still lets the cycle end.
//
// The negative control for T026_06, and it is not optional. Every assertion
// above is "the turn was blocked", which passes trivially against an engine that
// blocks every cycle — and a fix that refused whenever any guardrail existed
// would satisfy T026_06 completely while making the product unusable.
//
// Same shape as T026_06: one guardrail, bound to TurnEnd, whose hook permits.
// The only difference is that its matcher is one the kind can answer — which
// here means no matcher at all, since TurnEnd carries no fields to narrow on.
func TestT026_07_ASoundTurnEndRuleLetsTheCycleEnd(t *testing.T) {
	e, proj := project(t)
	e.Guardrail(proj, "sound-cycle", boundToTurnEnd, map[string]string{
		"record.sh": "#!/bin/sh\ncat >/dev/null\nexit 0\n",
	})
	commitGuardrails(e, proj)

	got := e.Run(proj, "s-026-07", "write a note", Turns("done",
		Write("w1", "notes.md", "hello\n"),
	))

	if strings.Count(got.Output, `"subtype":"success"`) >= 2 {
		t.Fatalf("a sound TurnEnd rule whose hook permitted still blocked the cycle:\n%s", got.Output)
	}
}

// T026_08: a broken rule bound to a kind this cycle never produced does not
// block the cycle.
//
// The scoping half, and the mirror of the bug rather than a fix for it. 013
// establishes at the pre-tool point that a broken declaration stops exactly the
// events it bound to and nothing else — a typo in a rule about commands must not
// block a write no rule was ever written about. The same must hold here, and for
// a sharper reason: fixing a broken declaration is itself work done inside a
// cycle, so a rule that blocked every cycle for any load fault would leave the
// author no way to clear it.
//
// Without this, the obvious fix to T026_06 — refuse the cycle whenever anything
// failed to load — passes, and the scoping the rest of the engine is careful
// about is quietly abandoned at the one hook point that ends turns.
//
// # Why PostFileDelete, and not PostFileCreate
//
// The kind has to be one this cycle genuinely does not produce, and
// PostFileCreate is not that kind however little the scenario does: the harness
// writes .scenario.sh into the project to drive the mock, and it lands after the
// guardrails are committed, so every cycle really does create an untracked file
// and really does dispatch a PostFileCreate for it. A first version of this test
// used PostFileCreate and failed — correctly, and against a correctly scoped
// engine, because the event it assumed was absent was present. That is the test
// being wrong rather than the engine, and it is recorded here because the same
// trap is waiting for the next test in this directory.
//
// Nothing in the scenario deletes anything, so PostFileDelete is produced by no
// cycle here. A block naming this rule could then only be the engine refusing on
// behalf of a rule about deletions.
func TestT026_08_ABrokenRuleDoesNotBlockACycleItNeverGuarded(t *testing.T) {
	e, proj := project(t)
	e.Guardrail(proj, "broken-delete-rule", `---
hooks:
  PostFileDelete:
    - matcher: paht endsWith ".md"
      hooks:
        - type: command
          command: ./permit.sh
---

# A misspelled field on a kind that does carry one
`, map[string]string{"permit.sh": "#!/bin/sh\ncat >/dev/null\nexit 0\n"})
	commitGuardrails(e, proj)

	e.Run(proj, "s-026-08", "write something, delete nothing", Turns("done",
		Write("w1", "notes.md", "hello\n"),
	))

	// Scoped, so the rule about deletions must not be what stops this cycle.
	// Asserted on the blocking text rather than on the turn merely ending: this
	// project has a broken rule, and a future change that legitimately blocks
	// cycles for some other reason should not silently satisfy this test.
	told := strings.Join(e.BlockingErrors(proj, "s-026-08"), "\n")
	if strings.Contains(told, "broken-delete-rule") {
		t.Fatalf("a declaration broken on PostFileDelete blocked a cycle in which nothing was "+
			"deleted — a broken rule must stop the events it bound to and nothing else, or the "+
			"author cannot fix it:\n%s", told)
	}
}
