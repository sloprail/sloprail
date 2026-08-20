package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

// This file covers a file-guard's AFTER-check (the default, non-preventive): a
// not-fine file blocks the TURN at Stop, a fine file admits, and — the
// file-guard's defining property — a not-fine file RE-FIRES on the next cycle
// until it is fixed. All drive a real Write turn against a file-guard whose match
// selects the written path.

// forbidSecretGuard is a non-preventive file-guard: a memories/ markdown file is
// not fine if it holds the word SECRET. The check reads the CheckPayload's
// event.newContent (the settled file's content) and refuses on a match, appending
// a line to its own ledger each time it is ASKED — so a test can count re-fires.
const forbidSecretGuard = `match: memories/**/*.md
checks:
  - script: ./check.sh
`

// checkForbidSecret refuses a file whose newContent holds SECRET, recording every
// time it runs into a ledger under the guard's own folder (SR_GUARDRAIL_DIR).
const checkForbidSecret = `#!/bin/sh
payload="$(cat)"
echo asked >> "$SR_GUARDRAIL_DIR/ledger"
if printf '%s' "$payload" | grep -q SECRET; then
  echo '{"reason":"this file holds a SECRET and is not fine"}'
  exit 1
fi
exit 0
`

// fileGuardLedger reads the ledger a file-guard's check appended to, counting how
// many times the check was asked. Absent means it never ran.
func fileGuardLedger(t *testing.T, projDir, guardName string) int {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(projDir, ".sloprail", "file-guard", guardName, "ledger"))
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatalf("read file-guard ledger: %v", err)
	}
	n := 0
	for _, line := range splitNonEmpty(string(body)) {
		_ = line
		n++
	}
	return n
}

func splitNonEmpty(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == '\n' {
			if cur != "" {
				out = append(out, cur)
			}
			cur = ""
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

// T034_01: a not-fine file blocks the TURN at Stop.
//
// The write LANDS (a Post check cannot undo it — the file is on disk), but the
// turn is blocked so the agent is sent round again, and the guard's own words
// reach it. This is the file-guard after-check, the authoritative one.
func TestT034_01_NotFineFileBlocksTurn(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.FileGuard(proj, "no-secrets", forbidSecretGuard, map[string]string{"check.sh": checkForbidSecret})

	e.Run(proj, "s-034-01", "write a memory with a secret", Turns("done",
		Write("w1", "memories/note.md", "the password is SECRET"),
	))

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
	e.FileGuard(proj, "no-secrets", forbidSecretGuard, map[string]string{"check.sh": checkForbidSecret})

	e.Run(proj, "s-034-02", "write a clean memory", Turns("done",
		Write("w1", "memories/note.md", "a perfectly ordinary note"),
	))

	blocks := e.BlockingErrorsFrom(proj, "s-034-02", "Stop")
	if len(blocks) != 0 {
		t.Errorf("a file-guard whose after-check passed blocked the turn anyway:\n%v", blocks)
	}
	// The guard DID run — it just passed. (Proves the pass is a real check, not a
	// guard that never fired.)
	if n := fileGuardLedger(t, proj, "no-secrets"); n == 0 {
		t.Errorf("the file-guard never ran on a matching file")
	}
}

// T034_03: a not-fine file RE-FIRES on the next cycle until it is fixed.
//
// Cycle 1 writes the bad file and the guard refuses (recorded in the same
// revalidation store the old format re-fires from). Cycle 2 does something
// unrelated and does NOT touch the bad file — yet the guard is asked about it
// AGAIN (readdOutstanding re-adds the still-refused path to the difference, so a
// Post event is produced for it), and refuses again. Cycle 3 FIXES the file, and
// from then on it is not re-reported.
//
// Two cycles in one session are two Run calls with the same session id, which is
// what makes the second a later cycle of the same conversation.
func TestT034_03_NotFineFileReFiresUntilFixed(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.FileGuard(proj, "no-secrets", forbidSecretGuard, map[string]string{"check.sh": checkForbidSecret})

	sess := "s-034-03"

	// Cycle 1: write the bad file. The guard is asked once and refuses.
	e.Run(proj, sess, "write a memory with a secret", Turns("done",
		Write("w1", "memories/note.md", "holds a SECRET"),
	))
	afterFirst := fileGuardLedger(t, proj, "no-secrets")
	if afterFirst == 0 {
		t.Fatalf("the file-guard never ran in the first cycle")
	}

	// Cycle 2: unrelated work that does NOT touch the bad file. The still-not-fine
	// file must be re-checked anyway — the re-fire.
	e.Run(proj, sess, "do something unrelated", Turns("done",
		Write("w2", "memories/other.md", "clean"),
	))
	afterSecond := fileGuardLedger(t, proj, "no-secrets")
	if afterSecond <= afterFirst {
		t.Fatalf("an unfixed not-fine file was NOT re-checked on the next cycle: "+
			"asked %d times after cycle 1, %d after cycle 2 — the file is still not fine and must re-fire",
			afterFirst, afterSecond)
	}
	// The re-fire still blocks the turn.
	blocks := e.BlockingErrorsFrom(proj, sess, "Stop")
	if len(blocks) == 0 {
		t.Errorf("the re-fired not-fine file did not block the second cycle's turn")
	}

	// Cycle 3: FIX the file. It passes now, and stops being reported.
	e.Run(proj, sess, "fix the memory", Turns("done",
		Write("w3", "memories/note.md", "the secret is gone now"),
	))
	afterFix := fileGuardLedger(t, proj, "no-secrets")

	// Cycle 4: unrelated work. The FIXED file is not re-reported (it passed at its
	// current content, so revalidation lets it be skipped).
	e.Run(proj, sess, "more unrelated work", Turns("done",
		Write("w4", "memories/third.md", "clean"),
	))
	afterUnrelated := fileGuardLedger(t, proj, "no-secrets")
	// The fixed note.md should not drive further asks of its own. Allow the
	// unrelated third.md to be checked (it matches the glob), but the count must
	// not keep climbing on note.md's account across many cycles.
	if afterUnrelated > afterFix+1 {
		t.Errorf("a FIXED file was still re-reported after it passed: asked %d times after the fix, "+
			"%d after one unrelated cycle — a passed file at unchanged content should be skipped",
			afterFix, afterUnrelated)
	}
}
