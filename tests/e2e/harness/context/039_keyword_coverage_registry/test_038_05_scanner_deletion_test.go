package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// Deleting a declared scanner drops every keyword it declared. Found on a real
// security-scan run: refused at SubagentStop by verify-scanner-coverage, a
// sub-agent ran `rm -rf .../scanners/token-leakage-logs` — and the gate went
// quiet, because the context it matches on had closed at the refused Stop and no
// Post event re-entered it for a file that was gone.
//
// So: scanner-keywords-hold (deletions: include) refuses the delete itself —
// `rm -rf` of the scanner's directory included — without the user's words; and
// whatever still gets the file deleted does not clear the obligation: the
// context stays open while coverage is refused, and the gate refuses from its
// registry whether or not the file survives.

// coverageRefusals counts the coverage gate's refusals recorded for the session — every
// attempt, not deduplicated, so a later turn's refusal is told apart from an earlier
// turn's identical one. Read through the harness's own reader of its record, which is
// where this harness keeps a refusal.
func coverageRefusals(e *harness.Env, proj, sess string) int {
	n := 0
	for _, b := range e.AllBlockingErrorsFrom(proj, sess, "") {
		if strings.Contains(b, coverageRefusal) {
			n++
		}
	}
	return n
}

// T038_24: a declared scanner stays owed its search after its file disappears
// by a route the engine cannot see — the refusal keeps standing in the NEXT
// turn. Before, the context closed at the refused Stop, nothing re-entered it
// for a file that was gone, and the gate never ran again.
func TestT038_24_ObligationSurvivesAnUnseenDelete(t *testing.T) {
	e, proj := researchProject(t)
	const sess = "s-038-24"

	e.Run(proj, sess, "research guardrails", Turns("done",
		Write("w1", "scanners/mine/scanner.yaml", activeScanner),
		Bash("b1", stubbed(`gh search repos guardrail`)),
	).ThenCommit("write the files"))
	after1 := coverageRefusals(e, proj, sess)
	if after1 == 0 {
		t.Fatalf("precondition: turn 1 should be refused for the uncovered scanner")
	}
	if active, _ := e.ContextState(proj, sess, "scanner-declared"); !active {
		t.Errorf("the context closed although coverage was refused")
	}

	// `find -delete` is not a command the file module reads, so no PreFileDelete
	// reaches scanner-keywords-hold: the file really goes.
	e.Run(proj, sess, "clean up", Turns("done",
		Bash("b2", "find scanners -name scanner.yaml -delete"),
	).ThenCommit("write the files"))
	if _, err := os.Stat(filepath.Join(proj, "scanners", "mine", "scanner.yaml")); !os.IsNotExist(err) {
		t.Fatalf("precondition: the unseen delete should have removed the file: %v", err)
	}
	if after2 := coverageRefusals(e, proj, sess); after2 <= after1 {
		t.Fatalf("the coverage refusal stopped once the scanner's file was gone (refusals %d -> %d)", after1, after2)
	}
}
