package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// verdict_per_guardrail: a check records the guardrail that produced it, and
// never stands in for a different guardrail's judgement of the same path.
//
// Verdicts belong to the rule that reached them. Pooled per file instead, they
// would have two consequences the spec names outright, and there is a test for
// each:
//
//   - adding a guardrail would silently exempt every file already judged by
//     the others (T015_02, T015_03);
//   - a file could not be recorded as satisfying one rule while violating
//     another (T015_04).
//
// T015_01 is the plain case that makes the rest mean something: two rules, one
// file, and both are asked.
//
// The grain is a check, not a file. A file governed by three guardrails has
// three rows.

// Two rules that both admit every write, differing only in which ledger they
// append to. Neither refuses, so nothing here turns on refusal ordering between
// guardrails — see the note in T013_04 on why cross-guardrail order is not
// something a test may rest on.
const watcherDecl = `---
hooks:
  PreFileCreate:
    - hooks:
        - type: command
          command: ./watch.sh
  PreFileUpdate:
    - hooks:
        - type: command
          command: ./watch.sh
---

# Records every event it is asked about, and permits it.
`

const watcherScript = `#!/bin/sh
cat >/dev/null
echo "asked" >> "$PWD/log"
exit 0
`

// T015_01: two guardrails bound to the same event are both asked about one file.
//
// The floor this whole file stands on. Without it every "the second rule was
// asked" assertion below could be satisfied by a build where nothing is ever
// skipped, and the per-guardrail grain would be untested rather than confirmed.
//
// Both rules see the same write, and each records its own invocation.
func TestT015_01_TwoGuardrailsAreBothAskedAboutOneFile(t *testing.T) {
	harness.RequireSessionStore(t)

	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "alpha", watcherDecl, map[string]string{"watch.sh": watcherScript})
	e.Guardrail(proj, "beta", watcherDecl, map[string]string{"watch.sh": watcherScript})

	e.Run(proj, "s-015-01", "write a file", Turns("done",
		Write("w1", "notes.md", "hello"),
	))

	for _, name := range []string{"alpha", "beta"} {
		if n := len(e.Ledger(proj, name, "log")); n != 1 {
			t.Fatalf("guardrail %q was asked %d time(s), want 1 — one stream of events serves "+
				"every rule bound to it", name, n)
		}
	}
}

// T015_02: one guardrail passing a file does not exempt another.
//
// Two rules and the same content offered twice. The first offer is judged by
// both and passes for both. The second offer is identical content, so each rule
// is legitimately exempt FROM ITS OWN earlier pass — and that is the point: the
// exemption each rule gets is its own, and neither inherits the other's.
//
// So this is stated where the two designs differ visibly: a THIRD rule, bound
// to the same events, which has never seen the file. If verdicts were pooled
// per file, the passes already recorded would exempt it and it would never run.
// It must run.
func TestT015_02_APassByOneGuardrailDoesNotExemptAnother(t *testing.T) {
	harness.RequireSessionStore(t)

	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "alpha", watcherDecl, map[string]string{"watch.sh": watcherScript})
	e.Guardrail(proj, "beta", watcherDecl, map[string]string{"watch.sh": watcherScript})
	e.Guardrail(proj, "gamma", watcherDecl, map[string]string{"watch.sh": watcherScript})

	e.Run(proj, "s-015-02", "write a file", Turns("done",
		Write("w1", "notes.md", "hello"),
	))

	// Every rule bound to the event is asked about it, whatever the others
	// concluded. A rule missing here is one that was exempted by a verdict it
	// did not reach.
	for _, name := range []string{"alpha", "beta", "gamma"} {
		if n := len(e.Ledger(proj, name, "log")); n != 1 {
			t.Fatalf("guardrail %q was asked %d time(s), want 1 — a verdict belongs to the "+
				"rule that reached it, so one rule's pass must never stand in for another's "+
				"judgement of the same file", name, n)
		}
	}
}

// T015_03: a guardrail added AFTER a file was settled still judges it.
//
// The consequence the spec names first, and the one that makes pooling
// dangerous rather than merely imprecise: were verdicts pooled per file, adding
// a rule would silently exempt everything the existing rules had already
// passed. The new rule would be installed, bound, enabled — and inert on every
// file that mattered.
//
// Two runs under ONE session, because the record being consulted is the
// session's. The first run settles the file under "alpha" alone. "beta" is
// written between the runs, so it does not exist when the verdict is recorded
// and cannot have contributed to it. The second run offers the same content
// again:
//
//   - alpha is legitimately exempt — same content, its own pass. Asserted, so
//     that beta running is not merely "nothing is ever skipped".
//   - beta has never judged this file and must be asked.
//
// Those two assertions together are what distinguish the per-guardrail grain
// from a build with no exemption at all.
func TestT015_03_AGuardrailAddedAfterAFileWasSettledStillJudgesIt(t *testing.T) {
	harness.RequireSessionStore(t)

	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "alpha", watcherDecl, map[string]string{"watch.sh": watcherScript})

	const sess = "s-015-03"

	// Both runs offer a CREATE of the same bytes, so the subject is the pending
	// content and is identical across them. The file is removed in between
	// because a create that landed would make the second offer an update, whose
	// pending content the engine cannot see (internal/filemod/module.go:70) —
	// the subject would then come from disk and the comparison would be a
	// different one than intended.
	e.Run(proj, sess, "settle the file", Turns("done",
		Write("w1", "notes.md", "settled content"),
		Bash("b1", "rm -f notes.md"),
	))

	if n := len(e.Ledger(proj, "alpha", "log")); n != 1 {
		t.Fatalf("alpha was asked %d time(s) in the first run, want 1 — the file has to be "+
			"settled under alpha alone for this test to mean anything", n)
	}

	// The new rule arrives mid-session, after the verdict was recorded.
	e.Guardrail(proj, "beta", watcherDecl, map[string]string{"watch.sh": watcherScript})

	e.Run(proj, sess, "offer the same content again", Turns("done",
		Write("w2", "notes.md", "settled content"),
	))

	if n := len(e.Ledger(proj, "beta", "log")); n != 1 {
		t.Fatalf("beta was asked %d time(s), want 1 — a guardrail added after a file was "+
			"settled must still judge it. Nothing beta did produced the verdict on record, so "+
			"an exemption here is beta inheriting alpha's judgement, and the new rule is inert "+
			"on every file the old ones had already passed", n)
	}

	// alpha stays exempt on its own pass. Without this the test would also pass
	// on a build where the exemption never fires at all, and beta running would
	// prove nothing about the grain.
	if n := len(e.Ledger(proj, "alpha", "log")); n != 1 {
		t.Fatalf("alpha was asked %d time(s) in total, want 1 — it had already passed this "+
			"exact content, so its own exemption should hold. If it ran again, nothing is "+
			"being skipped here and beta running says nothing about per-guardrail verdicts", n)
	}
}

// T015_04: one file can be recorded as satisfying one rule while violating
// another.
//
// The other consequence of pooling, and the one that shows up as a lost
// refusal rather than a lost check. One content, two rules: "permits" passes
// it, "refuses" does not. Both verdicts have to exist at once — a single row
// per file could hold only the last one written, and whichever arrived second
// would erase the first.
//
// Observed the only way it can be from outside: the same content is offered
// twice, and the refusing rule must refuse BOTH times. If the passing rule's
// verdict had overwritten it, the second offer would be exempt and the refusal
// would vanish.
func TestT015_04_AFileCanSatisfyOneRuleAndViolateAnother(t *testing.T) {
	harness.RequireSessionStore(t)

	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "permits", watcherDecl, map[string]string{"watch.sh": watcherScript})
	e.Guardrail(proj, "refuses", watcherDecl, map[string]string{"watch.sh": refusingScript})

	got := e.Run(proj, "s-015-04", "offer the same content twice", Turns("done",
		Write("w1", "notes.md", "one content, two opinions"),
		Write("w2", "notes.md", "one content, two opinions"),
	))

	// The refusing rule is asked both times: its own verdict is a refusal, which
	// never licenses a skip, and the other rule's pass is not its to inherit.
	if n := len(e.Ledger(proj, "refuses", "log")); n != 2 {
		t.Fatalf("the refusing guardrail was asked %d time(s), want 2 — its refusal must "+
			"survive alongside the other rule's pass on the same file. One verdict per file "+
			"instead of one per check would let the pass overwrite it, and the violation would "+
			"go quiet on the second offer.\n%s", n, got.Output)
	}
	if n := strings.Count(got.Output, "this rule says no"); n != 2 {
		t.Fatalf("the refusal reached the agent %d time(s), want 2:\n%s", n, got.Output)
	}
}

// refusingScript records that it was asked and then refuses, so a test can tell
// "asked and refused" from "never asked".
const refusingScript = `#!/bin/sh
cat >/dev/null
echo "asked" >> "$PWD/log"
echo "this rule says no" >&2
exit 2
`
