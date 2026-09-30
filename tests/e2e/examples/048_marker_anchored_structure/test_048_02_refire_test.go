package e2e

// TODO(D3): N/A here — this file's guard is a synthetic, deterministic, script-ONLY
// file-guard (no judge), so there is no model verdict to swap from a stub to the
// a10n-claude-mock; the D3 mock migration does not touch this test.
//
// The file-guard's DEFINING property: a not-fine file blocks the TURN at Stop and
// RE-FIRES every cycle until its content satisfies the checks, then stops. The
// shipped marker-anchored example writes no ledger, so a re-fire count cannot be
// read off it directly; this test installs a file-guard of the SAME shape
// (match: any(markers, .kind == "endpoint"), a deterministic script) that appends
// a ledger line each time it is ASKED, which is how the count is observed — the
// same mechanism tests/e2e/fileguard/034_fileguard_dispatch uses for exactly this
// property. The script is deterministic (no judge), so the re-fire is measured
// without any dependence on the judge stub.

import "testing"

// refireGuard is a marker-anchored file-guard whose script refuses a file whose
// content holds FORBIDDEN, appending a line to its own ledger each time it runs.
const refireGuard = `match: any(markers, .kind == "endpoint")
checks:
  - script: ./check.sh
`

// refireCheck refuses a FORBIDDEN body and records every run into a ledger under
// the guard's own folder (SR_GUARDRAIL_DIR), so a test can count re-fires.
const refireCheck = `#!/bin/sh
payload="$(cat)"
echo asked >> "$SR_GUARDRAIL_DIR/ledger"
if printf '%s' "$payload" | grep -q FORBIDDEN; then
  echo '{"reason":"this endpoint uses a forbidden construct and is not fine"}'
  exit 1
fi
exit 0
`

// T048_06: a not-fine endpoint file RE-FIRES until fixed, then stops.
//
//   - Cycle 1 writes a FORBIDDEN endpoint; the guard is asked and refuses.
//   - Cycle 2 does UNRELATED work (a clean, different endpoint) and does NOT touch
//     the bad file — yet the guard is asked about it AGAIN (the re-fire) and the
//     ledger count climbs past cycle 1.
//   - Cycle 3 FIXES the bad file (removes FORBIDDEN); it passes.
//   - Cycle 4 does more unrelated work; the FIXED file is not re-reported on its
//     own account (a passed file at unchanged content is skipped), so the count
//     does not keep climbing beyond the one new file that cycle.
//
// Several cycles in one session are several Run calls with the same session id.
func TestT048_06_NotFineEndpointReFiresUntilFixed(t *testing.T) {
	e := newEnv(t)
	proj := e.Project()
	e.GitInit(proj)
	e.FileGuard(proj, "endpoint-conforms", refireGuard, map[string]string{"check.sh": refireCheck})

	sess := "s-048-06"

	// Cycle 1: a forbidden endpoint. Asked once, refuses.
	e.Run(proj, sess, "add a forbidden endpoint", Turns("done",
		Write("w1", "get-users.ts", "// sr:endpoint users.list\nFORBIDDEN construct here\n"),
	).ThenCommit("write the files"))
	afterFirst := fileGuardLedger(t, proj, "endpoint-conforms", "ledger")
	if afterFirst == 0 {
		t.Fatalf("the endpoint guard never ran in the first cycle")
	}
	if blocks := e.BlockingErrorsFrom(proj, sess, "Stop"); len(blocks) == 0 {
		t.Fatalf("a not-fine endpoint did not block the first cycle's turn")
	}

	// Cycle 2: unrelated clean endpoint; the still-not-fine file must re-fire.
	e.Run(proj, sess, "add an unrelated clean endpoint", Turns("done",
		Write("w2", "get-orders.ts", "// sr:endpoint orders.list\nclean body\n"),
	).ThenCommit("write the files"))
	afterSecond := fileGuardLedger(t, proj, "endpoint-conforms", "ledger")
	if afterSecond <= afterFirst {
		t.Fatalf("an unfixed not-fine endpoint was NOT re-checked on the next cycle: "+
			"asked %d times after cycle 1, %d after cycle 2 — it must re-fire until fixed",
			afterFirst, afterSecond)
	}
	if blocks := e.BlockingErrorsFrom(proj, sess, "Stop"); len(blocks) == 0 {
		t.Fatalf("the re-fired not-fine endpoint did not block the second cycle's turn")
	}

	// Cycle 3: FIX the bad file. It passes now.
	e.Run(proj, sess, "fix the forbidden endpoint", Turns("done",
		Write("w3", "get-users.ts", "// sr:endpoint users.list\nnow a clean body\n"),
	).ThenCommit("write the files"))
	afterFix := fileGuardLedger(t, proj, "endpoint-conforms", "ledger")

	// Cycle 4: more unrelated work. The FIXED file is not re-reported on its own
	// account. Allow the one new file this cycle to be checked, but the count must
	// not keep climbing on the fixed file's account.
	e.Run(proj, sess, "add one more clean endpoint", Turns("done",
		Write("w4", "get-carts.ts", "// sr:endpoint carts.list\nclean body\n"),
	).ThenCommit("write the files"))
	afterUnrelated := fileGuardLedger(t, proj, "endpoint-conforms", "ledger")
	if afterUnrelated > afterFix+2 {
		t.Fatalf("a FIXED endpoint was still re-reported after it passed: asked %d times after the fix, "+
			"%d after one unrelated cycle — a passed file at unchanged content should be skipped",
			afterFix, afterUnrelated)
	}
}
