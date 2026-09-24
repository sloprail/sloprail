package e2e

import "testing"

// T046_10: DELETING a file that carries an sr:invariant marker still ARMS the
// guard.
//
// The engine's file-guard dispatch reads a file event's markers off
// `newMarkers` — a create/update declares it, but filemod's delete kinds
// (KindPreDelete/KindPostDelete) declare no `newMarkers` field at all, only
// `oldMarkers` (the markers the file carried before the delete). Before the
// nature_fileguard.go fix (fileMarkers falling back to oldMarkers when
// newMarkers is absent), `any(markers, .kind == "invariant")` always evaluated
// FALSE on a delete — the guard's match never selected the deleted file, so
// neither pin-still-matches-head.sh nor the judge ever ran, regardless of what
// the deleted file held. This example's own README says the rule is about "the
// file's state... it does not matter which event last touched the file" — a
// silent escape via delete directly contradicts that.
//
// This proves the fix: seed a file with a STALE pin (HEAD has moved past what
// the marker pins to — the same drift T046_02 proves the script catches on a
// create) directly on disk AND commit it, BEFORE the session starts — so the
// file is present at the session's baseline — then DELETE it via Bash.
// filemod's observed-phase classify() only reports a PostFileDelete for a path
// that existed at baseline and is gone now (internal/filemod/observed.go's
// classify table), and "the baseline" is read from GIT, not merely the disk
// state before the session started (the same reason
// tests/e2e/examples/049_no_unasked_deletion's T049_11 commits its seed before
// removing it) — a file only written, not committed, or one created and removed
// within the same cycle, leaves no PostFileDelete event at all. If the guard's
// match still selects the delete (fileMarkers falls back to oldMarkers), the
// stale-pin script re-fires and the delete's turn still blocks at Stop —
// proving the marker was carried through the deletion rather than silently
// dropped.
func TestT046_10_DeletingStalePinnedFileStillBlocksAtStop(t *testing.T) {
	e := newEnv(t)
	proj := biProject(t, e)
	shaV1 := commitSpec(t, e, proj, "SPEC.md", specV1, "spec v1")
	commitSpec(t, e, proj, "SPEC.md",
		"an invariants spec\nan order total must never be negative OR ZERO\n(end)\n", "spec v2 reworded")
	e.InstallJudgeClaude(`{"pass": true, "reasoning": "irrelevant — the script should refuse first"}`)

	fqn := proj + "@" + shaV1 + ":SPEC.md#L2-2" // pinned to the OLD, since-reworded wording
	code := invariantCode(fqn, "func charge(total int) { if total < 0 { panic(\"never negative\") } }\n")
	e.WriteFile(proj, "src/charge.go", code)
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "seed src/charge.go")

	sess := "s-046-10"
	e.Run(proj, sess, "delete the invariant-pinned file", Turns("done",
		Bash("b1", "rm src/charge.go"),
	))

	blocks := e.BlockingErrorsFrom(proj, sess, "Stop")
	if len(blocks) == 0 {
		t.Fatalf("deleting a file with a STALE invariant pin was never refused — the guard's match " +
			"silently stopped selecting the file once it was deleted (fileMarkers not falling back to " +
			"oldMarkers), so the stale-pin drift went unreported")
	}
	joined := joinBlocks(blocks)
	if !containsAll(joined, "pinned to text that has since changed at HEAD", "pinned-invariant") {
		t.Fatalf("the stale-pin (script) reason did not reach the agent for the deleted file:\n%s", joined)
	}
}
