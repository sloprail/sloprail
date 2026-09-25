package e2e

import "testing"

// T046_10: DELETING an invariant-pinned file is still judged. The shipped guard
// sets `deletions: include` — the rule is about the file's state "whichever event
// last touched it" (README) — and on a delete its marker match reads the markers
// the deleted file carried (oldMarkers). So removing code whose pin has since gone
// stale does not slip past the pin check: the script's drift reason reaches the
// agent at Stop.
//
// The code file is COMMITTED before the session: a PostFileDelete is produced only
// for a file the session's baseline holds. The spec is reworded afterwards so the
// pin the deleted file carried no longer matches HEAD.
//
// Fails on the old engine (a marker match read only newMarkers, which a delete
// never carries, so the guard never selected a delete), and on a guard left on the
// default `deletions: skip`.
func TestT046_10_DeletingAStalePinnedFileStillBlocks(t *testing.T) {
	e := newEnv(t)
	proj := biProject(t, e)
	shaV1 := commitSpec(t, e, proj, "SPEC.md", specV1, "spec v1")

	fqn := proj + "@" + shaV1 + ":SPEC.md#L2-2"
	e.WriteFile(proj, "src/charge.go", invariantCode(fqn, "func charge(total int) { if total < 0 { panic(\"never negative\") } }\n"))
	commitSpec(t, e, proj, "SPEC.md",
		"an invariants spec\nan order total must never be negative OR ZERO\n(end)\n", "code pinned to v1; spec reworded")

	// Judge would pass — so a block can only come from the pin script.
	e.InstallJudgeClaude(`{"pass": true, "reasoning": "irrelevant — the script should refuse first"}`)

	sess := "s-046-10"
	e.Run(proj, sess, "delete the invariant-pinned code", Turns("done",
		Bash("b1", "rm src/charge.go"),
	))
	if e.Exists(proj, "src/charge.go") {
		t.Fatalf("the rm never ran, so there is no delete to observe")
	}

	joined := joinBlocks(e.BlockingErrorsFrom(proj, sess, "Stop"))
	if !containsAll(joined, "pinned to text that has since changed at HEAD", "pinned-invariant") {
		t.Fatalf("deleting a stale-pinned file was not checked — the guard never saw the delete:\n%s", joined)
	}
}
