package e2e

import (
	"strings"
	"testing"
)

// order_within_binding: within one binding, the checks run in the order they are
// declared.
//
// A rule may sequence its own checks — one writing what the next reads — and the
// list it wrote them in is the only order it can express. The failure this guards
// against is a map iteration or a concurrent dispatch somewhere in the chain,
// which is invisible until a rule depends on the order and then fails
// intermittently. The NEW dispatch must uphold it too.
//
// # Vehicle: a PreFileWrite gate
//
// The old format expressed "hooks of one binding" as a list under one event kind.
// The new format's analogue is a gate's `checks:` list, which internal/dispatch's
// Runner walks IN ORDER, stopping at the first refusal (dispatch.go Run — "the
// checks, in order. The first that refuses ends it"). So the four ordered hooks
// become four ordered checks in one gate, and the ordering invariant is re-proven
// against the new dispatch.
//
// A gate runs its whole `checks:` list ONCE per pre file event, before the write
// lands, so the recorded order is exactly the declared order with no doubling. The
// order file is written under the gate's own folder, not the project tree.

// orderedChecks is a NEW-FORMAT gate with four checks in one binding, each
// appending its own name. Four rather than two: two checks in the wrong order are
// still in one of two orders, and a shuffle has an even chance of looking right.
// Four make an accidental pass unlikely and a stable-but-wrong order obvious.
//
// The last check refuses (see step vs stepRefuse in each test), so the write is
// DENIED at pre-tool and never lands; a denied pre-write is retried by the mock,
// so the ledger holds that one pass repeated, and every test reads the first.
const orderedChecks = `on:
  - event: PreFileWrite
    match: event.path endsWith ".md"
checks:
  - script: ./a.sh
  - script: ./b.sh
  - script: ./c.sh
  - script: ./d.sh
`

// step is a check that records its name and permits, so the next one runs.
func step(name string) string {
	return "#!/bin/sh\ncat >/dev/null\necho " + name + " >> \"$SR_GUARDRAIL_DIR/order\"\nexit 0\n"
}

// stepRefuse records its name and then REFUSES, ending the pass. Used as the last
// check so the write is denied at pre-tool and the sequence is one clean pass.
func stepRefuse(name string) string {
	return "#!/bin/sh\ncat >/dev/null\necho " + name + " >> \"$SR_GUARDRAIL_DIR/order\"\necho '{\"reason\":\"end of the sequence\"}'\nexit 1\n"
}

// firstPass returns the recorded sequence up to and including its first refusal —
// one dispatch's pass over the `checks:` list. A denied pre-write is retried by the
// mock, so the ledger holds that same pass repeated; the invariant is about a
// SINGLE pass, so the first is what every assertion reads. The refusing check is
// always the last name recorded in a pass, so the pass length is (index of the
// first `stop` name) + 1.
func firstPass(all []string, stop string) []string {
	for i, name := range all {
		if name == stop {
			return all[:i+1]
		}
	}
	return all
}

// T010_01: the checks of one binding run in the order the declaration lists them.
//
// All four record in order, and d refuses at the end so the write is denied (one
// pass, no landing, no re-fire). The full order a,b,c,d is what proves the sequence
// rather than a prefix of it.
func TestT010_01_ChecksRunInDeclaredOrder(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Gate(proj, "sequenced", orderedChecks, map[string]string{
		"a.sh": step("a"),
		"b.sh": step("b"),
		"c.sh": step("c"),
		"d.sh": stepRefuse("d"),
	})

	e.Run(proj, "s-010-01", "write a note", Turns("done",
		Write("w1", "some/notes.md", "hello"),
	))

	got := firstPass(e.GateLedgerLines(proj, "sequenced", "order"), "d")
	want := []string{"a", "b", "c", "d"}

	// Every check must have run before the order means anything. A binding that ran
	// one check and stopped would otherwise satisfy a prefix comparison.
	if len(got) != len(want) {
		t.Fatalf("want all %d checks to run in one pass, %d did: %v", len(want), len(got), got)
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("checks ran out of declared order:\n got: %v\nwant: %v", got, want)
	}
}

// T010_02: a check refusing stops the ones declared after it.
//
// The other half of what an order means. If the later checks ran anyway, the order
// would be a reporting detail rather than a sequence — a rule could not use an
// early check as a precondition for a later one, which is the whole reason the
// order is promised. The Runner returns at the first refusal, so c and d never run.
// Here b refuses (rather than d), so a single pass is exactly a,b.
func TestT010_02_RefusalStopsLaterChecks(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)

	e.Gate(proj, "sequenced", orderedChecks, map[string]string{
		"a.sh": step("a"),
		// b records then refuses. c and d must not run.
		"b.sh": stepRefuse("b"),
		"c.sh": step("c"),
		"d.sh": step("d"),
	})

	res := e.Run(proj, "s-010-02", "write a note", Turns("done",
		Write("w1", "some/notes.md", "hello"),
	))

	// The refusal reached the agent, denied at pre-tool by the refusing check.
	if !res.Refused() || !res.Saw("end of the sequence") {
		t.Fatalf("the refusal never reached the agent:\n%s", res.Output)
	}

	// Within one pass the sequence is a,b — c and d, declared after b, never ran.
	got := firstPass(e.GateLedgerLines(proj, "sequenced", "order"), "b")
	if strings.Join(got, ",") != "a,b" {
		t.Fatalf("a refusal did not stop the checks declared after it: ran %v (full ledger %v), want [a b]",
			got, e.GateLedgerLines(proj, "sequenced", "order"))
	}
	// And the later checks never ran at all, in any pass.
	for _, name := range e.GateLedgerLines(proj, "sequenced", "order") {
		if name == "c" || name == "d" {
			t.Fatalf("a check declared after the refusing one ran: %q appears in %v", name, e.GateLedgerLines(proj, "sequenced", "order"))
		}
	}
}
