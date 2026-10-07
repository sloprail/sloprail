package e2e

import (
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

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
// file is present at the session's baseline — then DELETE it via Bash
// (`sr-file delete`, citing the user, since the delete drops the file's pin).
// filemod's observed-phase classify() only reports a PostFileDelete for a path
// that existed at baseline and is gone now (internal/filemod/observed.go's
// classify table), and "the baseline" is read from GIT, not merely the disk
// state before the session started (the same reason
// tests/e2e/harness/examples/049_no_unasked_deletion's T049_11 commits its seed before
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
	e.InstallJudgeClaude(`{"pass": true, "reasoning": "irrelevant — the script should refuse first"}`)

	// The pin is fine when the session starts: the first Stop passes it, which moves
	// the rule's base past this seed (see settleBaseline).
	fqn := proj + "@" + shaV1 + ":SPEC.md#L2-2"
	e.WriteFile(proj, "src/charge.go", invariantCode(fqn, "func charge(total int) { if total < 0 { panic(\"never negative\") } }\n"))
	e.CommitAll(proj, "seed src/charge.go")

	// The delete cites the user's words: removing the file removes its pin, which
	// pinned-spec-holds refuses without them (T046_16). Cited, the delete lands,
	// and what is tested here is what pinned-invariant makes of it.
	const ask = "delete the invariant-pinned file"
	sess := "s-046-10"
	settleBaseline(t, e, proj, sess, ask)

	// The spec is then reworded (a change of the test's own, past the passed range),
	// which is what makes the pin stale.
	commitSpec(t, e, proj, "SPEC.md",
		"an invariants spec\nan order total must never be negative OR ZERO\n(end)\n", "spec v2 reworded")
	e.Run(proj, sess, "go on", Turns("done",
		Bash("b1", "sr-file delete src/charge.go --cite:user '"+ask+"'"),
	).ThenCommit("write the files", harness.CitesUser(ask)))
	if e.Exists(proj, "src/charge.go") {
		t.Fatalf("the cited delete did not land, so there is no delete for pinned-invariant to judge")
	}

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
