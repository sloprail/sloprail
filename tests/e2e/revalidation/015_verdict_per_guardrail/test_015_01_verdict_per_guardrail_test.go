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
//
// # RE-VEHICLED onto the NEW file-guard nature (was old GUARDRAIL.md hooks)
//
// The per-guardrail grain is a property of the SHARED revalidation store: each
// verdict is keyed by (path, GUARDRAIL, fingerprint), and the file-guard's
// after-check drives that same keying (nature_fileguard.go namespaces each
// guard's revalidation key by its own name, then calls the same rev.Skip /
// rev.Record). So installing each rule as its own file-guard and firing the
// after-check observes the SAME grain — two guards asked about one file, a pass by
// one not exempting another, one file satisfying one guard while violating a
// second. The exact transformation is in tests/e2e/REVEHICLE-PATTERN.md.
//
// Each rule's own ledger moves from `.sloprail/guardrails/<name>/log` to
// `.sloprail/file-guard/<name>/log` (read with e.FileGuardLedgerLines), a refusal
// moves from a Pre-tool stream deny to a Stop block (read with
// e.BlockingErrorsFrom(…, "Stop")), and each guard writes its own ledger via
// $SR_GUARDRAIL_DIR. `match: "**/*.md"` selects the written files at any depth and
// never a guard's own `log` (no `.md` suffix).

// watcherGuard is a file-guard that admits every file, recording that it was
// asked. Two of them differ only in which folder's ledger they write to (their own
// $SR_GUARDRAIL_DIR), which is the whole point: one stream of events serves every
// guard, and each keeps its own record.
const watcherGuard = `match: "**/*.md"
checks:
  - script: ./watch.sh
`

const watcherScript = `#!/bin/sh
cat >/dev/null
echo "asked" >> "$SR_GUARDRAIL_DIR/log"
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
	e.GitInit(proj)
	e.FileGuard(proj, "alpha", watcherGuard, map[string]string{"watch.sh": watcherScript})
	e.FileGuard(proj, "beta", watcherGuard, map[string]string{"watch.sh": watcherScript})

	e.Run(proj, "s-015-01", "write a file", Turns("done",
		Write("w1", "notes.md", "hello"),
	))

	for _, name := range []string{"alpha", "beta"} {
		if n := len(e.FileGuardLedgerLines(proj, name, "log")); n != 1 {
			t.Fatalf("guardrail %q was asked %d time(s), want 1 — one stream of events serves "+
				"every rule bound to it", name, n)
		}
	}
}

// T015_02: one guardrail passing a file does not exempt another.
//
// Three rules and one write. Every rule bound to the event judges the file, whatever
// the others concluded. If verdicts were pooled per file, the first rule's pass
// would exempt the others and they would never run.
func TestT015_02_APassByOneGuardrailDoesNotExemptAnother(t *testing.T) {
	harness.RequireSessionStore(t)

	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.FileGuard(proj, "alpha", watcherGuard, map[string]string{"watch.sh": watcherScript})
	e.FileGuard(proj, "beta", watcherGuard, map[string]string{"watch.sh": watcherScript})
	e.FileGuard(proj, "gamma", watcherGuard, map[string]string{"watch.sh": watcherScript})

	e.Run(proj, "s-015-02", "write a file", Turns("done",
		Write("w1", "notes.md", "hello"),
	))

	// Every rule bound to the event is asked about it, whatever the others
	// concluded. A rule missing here is one that was exempted by a verdict it
	// did not reach.
	for _, name := range []string{"alpha", "beta", "gamma"} {
		if n := len(e.FileGuardLedgerLines(proj, name, "log")); n != 1 {
			t.Fatalf("guardrail %q was asked %d time(s), want 1 — a verdict belongs to the "+
				"rule that reached it, so one rule's pass must never stand in for another's "+
				"judgement of the same file", name, n)
		}
	}
}

// T015_03: a guardrail added AFTER a file was settled still judges it.
//
// The consequence the spec names first, and the one that makes pooling dangerous
// rather than merely imprecise: were verdicts pooled per file, adding a rule would
// silently exempt everything the existing rules had already passed. The new rule
// would be installed, bound, enabled — and inert on every file that mattered.
//
// Two cycles under ONE session, because the record being consulted is the
// session's. The first cycle settles the file under "alpha" alone. "beta" is
// installed between the cycles, so it does not exist when the verdict is recorded
// and cannot have contributed to it. The second cycle rewrites the file with the
// SAME bytes:
//
//   - alpha is legitimately exempt — same settled content, its own pass. Asserted,
//     so that beta running is not merely "nothing is ever skipped".
//   - beta has never judged this file and must be asked.
//
// On the after-check the subject is fingerprinted from the settled file on disk, so
// re-writing the same bytes yields the same fingerprint and alpha's own pass
// exempts it — while beta, holding no verdict, is asked.
func TestT015_03_AGuardrailAddedAfterAFileWasSettledStillJudgesIt(t *testing.T) {
	harness.RequireSessionStore(t)

	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.FileGuard(proj, "alpha", watcherGuard, map[string]string{"watch.sh": watcherScript})

	const sess = "s-015-03"

	e.Run(proj, sess, "settle the file", Turns("done",
		Write("w1", "notes.md", "settled content"),
	))
	if n := len(e.FileGuardLedgerLines(proj, "alpha", "log")); n != 1 {
		t.Fatalf("alpha was asked %d time(s) in the first cycle, want 1 — the file has to be "+
			"settled under alpha alone for this test to mean anything", n)
	}

	// The new rule arrives mid-session, after the verdict was recorded.
	e.FileGuard(proj, "beta", watcherGuard, map[string]string{"watch.sh": watcherScript})

	e.Run(proj, sess, "rewrite the same content", Turns("done",
		Write("w2", "notes.md", "settled content"),
	))

	if n := len(e.FileGuardLedgerLines(proj, "beta", "log")); n != 1 {
		t.Fatalf("beta was asked %d time(s), want 1 — a guardrail added after a file was "+
			"settled must still judge it. Nothing beta did produced the verdict on record, so "+
			"an exemption here is beta inheriting alpha's judgement, and the new rule is inert "+
			"on every file the old ones had already passed", n)
	}

	// alpha stays exempt on its own pass. Without this the test would also pass
	// on a build where the exemption never fires at all, and beta running would
	// prove nothing about the grain.
	if n := len(e.FileGuardLedgerLines(proj, "alpha", "log")); n != 1 {
		t.Fatalf("alpha was asked %d time(s) in total, want 1 — it had already passed this "+
			"exact content, so its own exemption should hold. If it ran again, nothing is "+
			"being skipped here and beta running says nothing about per-guardrail verdicts", n)
	}
}

// T015_04: one file can be recorded as satisfying one rule while violating
// another.
//
// The other consequence of pooling, and the one that shows up as a lost refusal
// rather than a lost check. One content, two rules: "permits" passes it, "refuses"
// does not. Both verdicts have to exist at once — a single row per file could hold
// only the last one written, and whichever arrived second would erase the first.
//
// Observed as re-fire across cycles: the file is written not-fine in cycle 1
// (permits passes and records a pass; refuses refuses and records a fail), and a
// later cycle does unrelated work. If the passing rule's verdict had overwritten
// the refusing rule's, the refused file would be exempt next cycle and its refusal
// would vanish — so the refusing rule must be asked about it AGAIN, while the
// permitting rule, holding its own pass on the settled bytes, is not.
func TestT015_04_AFileCanSatisfyOneRuleAndViolateAnother(t *testing.T) {
	harness.RequireSessionStore(t)

	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.FileGuard(proj, "permits", watcherGuard, map[string]string{"watch.sh": watcherScript})
	e.FileGuard(proj, "refuses", watcherGuard, map[string]string{"watch.sh": refusingScript})

	const sess = "s-015-04"

	seen := map[string]int{}
	e.Run(proj, sess, "write not-fine content", Turns("done",
		Write("w1", "notes.md", "one content, two opinions"),
	))
	for _, name := range []string{"permits", "refuses"} {
		n := len(e.FileGuardLedgerLines(proj, name, "log"))
		if n == 0 {
			t.Fatalf("%q did not judge the file in the first cycle", name)
		}
		seen[name] = n
	}
	if !refused(t, e, proj, sess) {
		t.Fatalf("the refusing rule did not refuse in the first cycle, so there is no refusal to " +
			"survive the other's pass")
	}

	// A later cycle of unrelated work. The refusing rule's refusal is retained and
	// re-fires on the still-not-fine file; the permitting rule's pass on the same
	// settled bytes exempts it.
	e.Run(proj, sess, "unrelated work", Turns("done",
		Write("u2", "unrelated.md", "fine"),
	))

	if n := len(e.FileGuardLedgerLines(proj, "refuses", "log")); n <= seen["refuses"] {
		t.Fatalf("the refusing guardrail was asked %d time(s) in total, was %d after the first "+
			"cycle — its refusal must survive alongside the other rule's pass on the same file and "+
			"re-fire. One verdict per file instead of one per check would let the pass overwrite "+
			"it, and the violation would go quiet", n, seen["refuses"])
	}
	// The refusal reached the agent, with the refusing rule's own words.
	if !refused(t, e, proj, sess) {
		t.Fatalf("the refusal never reached the agent, so being asked is not shown to be refusing")
	}
}

// refused reports whether the refusing rule's Stop-blocking refusal reached the
// conversation.
func refused(t *testing.T, e *harness.Env, proj, sess string) bool {
	t.Helper()
	for _, b := range e.BlockingErrorsFrom(proj, sess, "Stop") {
		if strings.Contains(b, "this rule says no") {
			return true
		}
	}
	return false
}

// refusingScript records that it was asked and then refuses, so a test can tell
// "asked and refused" from "never asked". New-format refusal contract: exit
// non-zero with the reason as `{"reason":"…"}` on stdout.
const refusingScript = `#!/bin/sh
cat >/dev/null
echo "asked" >> "$SR_GUARDRAIL_DIR/log"
echo '{"reason":"this rule says no"}'
exit 1
`
