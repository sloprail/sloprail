package e2e

import (
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// This file covers a file-guard's AFTER-check (the only moment a file-guard acts): a
// not-fine file blocks the TURN at Stop, a fine file admits, and — the
// file-guard's defining property — a not-fine file RE-FIRES on the next cycle
// until it is fixed. All drive a real Write turn against a file-guard whose match
// selects the written path.

// forbidSecretGuard is a file-guard: a memories/ markdown file is
// not fine if it holds the word SECRET. The check reads the Changeset payload
// (the committed files' content) and refuses on a match, appending a line to its
// own ledger (a harness ledger) each time it is ASKED — so a test can count how often it was judged.
const forbidSecretGuard = `match: memories/**/*.md
checks:
  - script: ./check.sh
`

// checkForbidSecret refuses a file whose content holds SECRET, recording every time it
// runs — and the paths of the files it was handed — into led, a harness ledger OUTSIDE
// the project (inside the rule's folder it would change the rule's hash: T034_19).
func checkForbidSecret(led *harness.Ledger) string {
	return `#!/bin/sh
payload="$(cat)"
echo "asked $(printf '%s' "$payload" | jq -r '[.changeset.files[].path] | join(",")')" >> ` + led.Sh() + `
if printf '%s' "$payload" | grep -q SECRET; then
  echo '{"reason":"this file holds a SECRET and is not fine"}'
  exit 1
fi
exit 0
`
}

// T034_01: a not-fine committed file blocks the TURN at Stop.
//
// The write LANDS and is committed (a file-guard judges commits and cannot undo
// them), but the turn is blocked so the agent is sent round again, and the guard's
// own words reach it. This is the file-guard after-check, the authoritative one.
func TestT034_01_NotFineFileBlocksTurn(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	led := e.NewLedger("ledger")
	e.FileGuard(proj, "no-secrets", forbidSecretGuard, map[string]string{"check.sh": checkForbidSecret(led)})
	e.DisableShippedFileGuards(proj)
	e.CommitAll(proj, "the rule and its scripts")

	e.Run(proj, "s-034-01", "write a memory with a secret", Turns("done",
		Write("w1", "memories/note.md", "the password is SECRET"),
	).ThenCommit("add the memory"))

	blocks := e.BlockingErrorsFrom(proj, "s-034-01", "Stop")
	if len(blocks) == 0 {
		t.Fatalf("a file-guard whose after-check refused did not block the turn")
	}
	joined := ""
	for _, b := range blocks {
		joined += b + "\n"
	}
	if !containsStr(joined, "not fine") {
		t.Errorf("the file-guard's refusal reason did not reach the agent:\n%s", joined)
	}
	if !containsStr(joined, "no-secrets") {
		t.Errorf("the refusal did not name the file-guard:\n%s", joined)
	}
}

// T034_02: a FINE file admits — the turn ends, no block.
//
// The control for T034_01: without it a guard that blocked everything would pass
// T034_01 while being broken.
func TestT034_02_FineFileAdmits(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	led := e.NewLedger("ledger")
	e.FileGuard(proj, "no-secrets", forbidSecretGuard, map[string]string{"check.sh": checkForbidSecret(led)})
	e.DisableShippedFileGuards(proj)
	e.CommitAll(proj, "the rule and its scripts")

	e.Run(proj, "s-034-02", "write a clean memory", Turns("done",
		Write("w1", "memories/note.md", "a perfectly ordinary note"),
	).ThenCommit("add the memory"))

	blocks := e.BlockingErrorsFrom(proj, "s-034-02", "Stop")
	if len(blocks) != 0 {
		t.Errorf("a file-guard whose after-check passed blocked the turn anyway:\n%v", blocks)
	}
	// The guard DID run — it just passed. (Proves the pass is a real check, not a
	// guard that never fired.)
	if n := led.Count(); n == 0 {
		t.Errorf("the file-guard never ran on a matching file")
	}
}

// T034_03: a not-fine file keeps being refused until it is fixed, and a fix is
// judged together with what it fixes.
//
// Cycle 1 commits the bad file and the guard refuses. The refused range is never
// partly passed, so cycle 2 — unrelated work — is judged with the bad commit still
// in its range, and refused again. Cycle 3 FIXES the file: the squashed range now
// holds the fixed content, and passes. Cycle 4 is unrelated work, and only that
// new commit is judged.
//
// Cycles in one session are Run calls with the same session id.
func TestT034_03_NotFineFileKeepsRefusingUntilFixed(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	led := e.NewLedger("ledger")
	e.FileGuard(proj, "no-secrets", forbidSecretGuard, map[string]string{"check.sh": checkForbidSecret(led)})
	e.DisableShippedFileGuards(proj)
	e.CommitAll(proj, "the rule and its scripts")

	sess := "s-034-03"

	// Cycle 1: commit the bad file. The guard is asked and refuses.
	e.Run(proj, sess, "write a memory with a secret", Turns("done",
		Write("w1", "memories/note.md", "holds a SECRET"),
	).ThenCommit("add the memory"))
	afterFirst := led.Count()
	if afterFirst == 0 {
		t.Fatalf("the file-guard never ran in the first cycle")
	}

	// Cycle 2: unrelated work that does NOT touch the bad file. The refused range
	// did not move, so it is judged again with the bad commit in it.
	e.Run(proj, sess, "do something unrelated", Turns("done",
		Write("w2", "memories/other.md", "clean"),
	).ThenCommit("add another memory"))
	afterSecond := led.Count()
	if afterSecond <= afterFirst {
		t.Fatalf("an unfixed not-fine file was NOT judged again on the next cycle: "+
			"asked %d times after cycle 1, %d after cycle 2 — the range is still refused and must be judged with the new commit",
			afterFirst, afterSecond)
	}
	if blocks := e.BlockingErrorsFrom(proj, sess, "Stop"); len(blocks) == 0 {
		t.Errorf("the still-not-fine range did not block the second cycle's turn")
	}

	// Cycle 3: FIX the file. The squashed range passes, and is not reported again.
	blocked := len(e.AllBlockingErrorsFrom(proj, sess, "Stop"))
	e.Run(proj, sess, "fix the memory", Turns("done",
		Write("w3", "memories/note.md", "the secret is gone now"),
	).ThenCommit("fix the memory"))
	if n := len(e.AllBlockingErrorsFrom(proj, sess, "Stop")); n != blocked {
		t.Errorf("the fixed range was still refused: %d blocking errors, had %d", n, blocked)
	}
	afterFix := led.Count()

	// Cycle 4: unrelated work. Only the new commit is judged: the passed range
	// is behind the rule's watermark.
	e.Run(proj, sess, "more unrelated work", Turns("done",
		Write("w4", "memories/third.md", "clean"),
	).ThenCommit("add a third memory"))
	afterUnrelated := led.Count()
	if afterUnrelated != afterFix+1 {
		t.Errorf("after a pass the next cycle judged more than its own commit: asked %d times after the fix, "+
			"%d after one unrelated cycle (want exactly one more)", afterFix, afterUnrelated)
	}
	if n := len(e.AllBlockingErrorsFrom(proj, sess, "Stop")); n != blocked {
		t.Errorf("the unrelated clean commit was refused: %d blocking errors, had %d", n, blocked)
	}
}
