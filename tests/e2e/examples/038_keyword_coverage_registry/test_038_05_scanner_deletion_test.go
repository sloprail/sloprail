package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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

const deleteHint = "Deleting this scanner drops every keyword it declared"

// T038_21: `rm -rf` of a declared scanner's DIRECTORY is refused before it runs.
// Both a committed scanner and one declared earlier this turn — the real run's
// shape.
func TestT038_21_RemovingTheScannerDirectoryRefused(t *testing.T) {
	t.Run("committed scanner", func(t *testing.T) {
		e, proj := keywordsProject(t)
		e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)
		res := e.Run(proj, "s-038-21a", "research guardrails", Turns("done",
			Bash("b1", "rm -rf scanners/mine"),
		))
		if !res.Refused() || !res.Saw(deleteHint) || !res.Saw("guardrail,llm") {
			t.Fatalf("rm -rf of the scanner's directory was not refused with the hint:\n%s", res.Output)
		}
		if got := readScanner(t, proj); got != activeScanner {
			t.Errorf("the refused delete reached the file:\n%s", got)
		}
	})

	t.Run("declared this turn, then removed after a narrow search", func(t *testing.T) {
		e, proj := researchProject(t)
		e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)
		res := e.Run(proj, "s-038-21b", "research guardrails", Turns("done",
			Write("w1", "scanners/mine/scanner.yaml", activeScanner),
			Bash("b1", stubbed(`gh search repos guardrail`)),
			Bash("b2", "cd scanners && rm -rf mine"),
		))
		if !res.Refused() || !res.Saw(deleteHint) {
			t.Fatalf("removing a just-declared scanner's directory was not refused:\n%s", res.Output)
		}
		if _, err := os.Stat(filepath.Join(proj, "scanners", "mine", "scanner.yaml")); err != nil {
			t.Errorf("the refused delete reached the file: %v", err)
		}
	})
}

// T038_22: a Bash `rm` of the scanner FILE is refused the same way.
func TestT038_22_RemovingTheScannerFileRefused(t *testing.T) {
	e, proj := keywordsProject(t)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)
	res := e.Run(proj, "s-038-22", "research guardrails", Turns("done",
		Bash("b1", "rm scanners/mine/scanner.yaml"),
	))
	if !res.Refused() || !res.Saw(deleteHint) {
		t.Fatalf("rm of the scanner file was not refused with the hint:\n%s", res.Output)
	}
	if got := readScanner(t, proj); got != activeScanner {
		t.Errorf("the refused delete reached the file:\n%s", got)
	}
}

// T038_23: a delete the user asked for, citing their words, is admitted — the
// requirement is the user's words, not a ban.
func TestT038_23_CitedScannerDeleteAdmits(t *testing.T) {
	e, proj := keywordsProject(t)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": "the user asked to remove the scanner"}`)
	const ask = "remove the mine scanner, we no longer track it"
	res := e.Run(proj, "s-038-23", ask, Turns("done",
		Bash("b1", "sr-session trajectory cite '"+ask+"' --source-types user && rm -rf scanners/mine"),
	))
	if res.Refused() {
		t.Fatalf("a scanner delete the user asked for was refused:\n%s", res.Output)
	}
	if _, err := os.Stat(filepath.Join(proj, "scanners", "mine", "scanner.yaml")); !os.IsNotExist(err) {
		t.Errorf("the cited delete did not land: %v", err)
	}
}

// coverageRefusals counts the coverage gate's Stop refusals in the record — every
// attempt, not deduplicated, so a later turn's refusal is told apart from an
// earlier turn's identical one.
func coverageRefusals(t *testing.T, path string) int {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the record: %v", err)
	}
	n := 0
	for _, line := range strings.Split(string(b), "\n") {
		if strings.Contains(line, `"hook_blocking_error"`) && strings.Contains(line, coverageRefusal) {
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
	))
	after1 := coverageRefusals(t, e.TranscriptPath(proj, sess))
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
	))
	if _, err := os.Stat(filepath.Join(proj, "scanners", "mine", "scanner.yaml")); !os.IsNotExist(err) {
		t.Fatalf("precondition: the unseen delete should have removed the file: %v", err)
	}
	if after2 := coverageRefusals(t, e.TranscriptPath(proj, sess)); after2 <= after1 {
		t.Fatalf("the coverage refusal stopped once the scanner's file was gone (refusals %d -> %d)", after1, after2)
	}
}
