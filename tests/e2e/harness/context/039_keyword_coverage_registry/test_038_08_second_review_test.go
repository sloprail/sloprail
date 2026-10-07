package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

// The second review of #84. Each test is a reviewer's probe, reproduced first on
// the branch it reviewed.

// T038_36: padding a scanner's folder past the byte budget of a recursive
// removal does not hide the removal. The whole folder used to predict nothing
// past 8 MiB; now every file is predicted, the padding and what sorts after it
// unread — and the guard, unable to read what the scanner held, asks for the
// user's words.
func TestT038_36_APaddedFolderStillPredictsTheDelete(t *testing.T) {
	e, proj := researchProjectWithScanner(t)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)
	pad := filepath.Join(proj, "scanners", "mine", "pad.bin")
	if err := os.WriteFile(pad, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(pad, 9<<20); err != nil {
		t.Fatal(err)
	}
	res := e.Run(proj, "s-038-36", "tidy up", Turns("done",
		Bash("b1", "rm -rf scanners/mine"),
	).ThenCommit("write the files"))
	if !res.Refused() {
		t.Errorf("rm -rf of a padded scanner folder was not refused:\n%s", res.Output)
	}
	if got := readScanner(t, proj); got != activeScanner {
		t.Errorf("the refused delete reached the file:\n%s", got)
	}
}
