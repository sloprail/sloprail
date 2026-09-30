package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A ledger is where a guardrail's check writes down what it was handed, so a test can
// say what the engine asked it and how often.
//
// It lives OUTSIDE the project. A rule's hash covers the whole rule folder, so a ledger
// a check grew INSIDE its folder (through $SR_GUARDRAIL_DIR) would change the hash on
// every run: a rule's watermark would be voided between cycles, and a "re-reported on
// the next cycle" assertion would pass for that reason alone, whatever the engine does
// with a refusal. A ledger under the test's own temp dir is neither in the tree the
// engine diffs nor in any commit. (tests/e2e/fileguard/034's T034_19 is the test that
// documents the trap itself; it writes into its folder on purpose.)

// Ledger is one such file. The check's script appends to Path (Sh gives it quoted for a
// script); the test reads it back with Lines or Count.
type Ledger struct {
	t    testing.TB
	path string
}

// NewLedger is a fresh, empty ledger named name, under the test's own temp dir.
func (e *Env) NewLedger(name string) *Ledger {
	e.t.Helper()
	return &Ledger{t: e.t, path: filepath.Join(e.t.TempDir(), name)}
}

// Path is the ledger's absolute path.
func (l *Ledger) Path() string { return l.path }

// Sh is the path quoted for a shell script: `echo ran >> ` + l.Sh().
func (l *Ledger) Sh() string { return shQuote(l.path) }

// Lines is what the check appended, one entry per non-blank line; a ledger never
// written is empty, which is a real answer: the check never ran (or never recorded).
func (l *Ledger) Lines() []string {
	l.t.Helper()
	return ReadLedgerLines(l.t, l.path)
}

// Count is how many lines the check appended.
func (l *Ledger) Count() int {
	l.t.Helper()
	return len(l.Lines())
}

// ReadLedgerLines is the non-blank lines of the file at path; a file not yet written is
// empty.
func ReadLedgerLines(t testing.TB, path string) []string {
	t.Helper()
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("harness: read ledger %s: %v", path, err)
	}
	var lines []string
	for _, l := range strings.Split(string(body), "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	return lines
}
