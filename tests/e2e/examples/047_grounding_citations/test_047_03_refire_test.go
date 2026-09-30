package e2e

// TODO(D3): N/A here — this file's guard is a synthetic, deterministic, script-ONLY
// file-guard (no judge), so there is no model verdict to swap from a stub to the
// a10n-claude-mock; the D3 mock migration does not touch this test.
//
// The file-guard's DEFINING property, for a markdown guard: a file-guard on
// `**/*.md` blocks the TURN at Stop on a not-fine doc and RE-FIRES every cycle
// until its content satisfies the checks, then stops. The shipped example's
// file-guard judges with a model and writes no ledger, so this installs a synthetic
// file-guard (match: "**/*.md", a deterministic script) that appends a ledger
// line each time it is ASKED — the same
// mechanism tests/e2e/fileguard/034_fileguard_dispatch and T048_06 use. The script
// is deterministic (no judge), so the re-fire is measured with no dependence on any
// stub.

import "testing"

// citeRefireGuard is a plain file-guard on `**/*.md` whose script refuses
// a doc holding UNRESOLVED (standing in for an unresolved citation), appending a
// line to its own ledger each time it runs.
const citeRefireGuard = `match: "**/*.md"
checks:
  - script: ./check.sh
`

// citeRefireCheck refuses an UNRESOLVED doc and records every run into a ledger
// under the guard's own folder (SR_GUARDRAIL_DIR).
const citeRefireCheck = `#!/bin/sh
payload="$(cat)"
echo asked >> "$SR_GUARDRAIL_DIR/ledger"
if printf '%s' "$payload" | grep -q UNRESOLVED; then
  echo '{"reason":"a citation in this doc does not resolve and is not fine"}'
  exit 1
fi
exit 0
`

// T047_09: a not-fine cited doc RE-FIRES until fixed, then stops.
//
//   - Cycle 1 writes a doc with an UNRESOLVED citation; the guard is asked and refuses.
//   - Cycle 2 does UNRELATED work (a clean, different doc) and does NOT touch the bad
//     doc — yet the guard is asked about it AGAIN (the re-fire) and the ledger climbs.
//   - Cycle 3 FIXES the bad doc (removes UNRESOLVED); it passes.
//   - Cycle 4 does more unrelated work; the FIXED doc is not re-reported on its own
//     account, so the count does not keep climbing beyond the one new doc that cycle.
//
// Several cycles in one session are several Run calls with the same session id.
func TestT047_09_NotFineDocReFiresUntilFixed(t *testing.T) {
	e := newEnv(t)
	proj := e.Project()
	e.GitInit(proj)
	e.FileGuard(proj, "citations-resolve", citeRefireGuard, map[string]string{"check.sh": citeRefireCheck})

	sess := "s-047-09"

	// Cycle 1: a doc with an unresolved citation. Asked once, refuses.
	e.Run(proj, sess, "write a doc with a bad citation", Turns("done",
		Write("w1", "report.md", "# Report\n\nsee [x](UNRESOLVED).\n"),
	))
	afterFirst := fileGuardLedger(t, proj, "citations-resolve", "ledger")
	if afterFirst == 0 {
		t.Fatalf("the citation guard never ran in the first cycle")
	}
	if blocks := e.BlockingErrorsFrom(proj, sess, "Stop"); len(blocks) == 0 {
		t.Fatalf("a not-fine cited doc did not block the first cycle's turn")
	}

	// Cycle 2: unrelated clean doc; the still-not-fine doc must re-fire.
	e.Run(proj, sess, "write an unrelated clean doc", Turns("done",
		Write("w2", "other.md", "# Other\n\nclean, nothing cited.\n"),
	))
	afterSecond := fileGuardLedger(t, proj, "citations-resolve", "ledger")
	if afterSecond <= afterFirst {
		t.Fatalf("an unfixed not-fine doc was NOT re-checked on the next cycle: "+
			"asked %d times after cycle 1, %d after cycle 2 — it must re-fire until fixed",
			afterFirst, afterSecond)
	}
	if blocks := e.BlockingErrorsFrom(proj, sess, "Stop"); len(blocks) == 0 {
		t.Fatalf("the re-fired not-fine doc did not block the second cycle's turn")
	}

	// Cycle 3: FIX the bad doc. It passes now.
	e.Run(proj, sess, "fix the bad citation", Turns("done",
		Write("w3", "report.md", "# Report\n\nthe citation is resolved now.\n"),
	))
	afterFix := fileGuardLedger(t, proj, "citations-resolve", "ledger")

	// Cycle 4: more unrelated work. The FIXED doc is not re-reported on its own
	// account. Allow the one new doc this cycle to be checked, but the count must
	// not keep climbing on the fixed doc's account.
	e.Run(proj, sess, "write one more clean doc", Turns("done",
		Write("w4", "third.md", "# Third\n\nclean.\n"),
	))
	afterUnrelated := fileGuardLedger(t, proj, "citations-resolve", "ledger")
	if afterUnrelated > afterFix+2 {
		t.Fatalf("a FIXED doc was still re-reported after it passed: asked %d times after the fix, "+
			"%d after one unrelated cycle — a passed file at unchanged content should be skipped",
			afterFix, afterUnrelated)
	}
}
