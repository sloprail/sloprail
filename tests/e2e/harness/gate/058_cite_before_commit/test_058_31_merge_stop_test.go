package e2e

import (
	"strings"
	"testing"
)

// T058_31: a merge concluded past the gate (`git merge --continue`) with an uncited resolution is
// caught at Stop, for the resolved file only: the side's file is grounded by the side's own commit.
func TestT058_31_UncitedResolutionIsCaughtAtStopForThatFileOnly(t *testing.T) {
	e, proj := merging(t, true)
	e.Run(proj, "s-058-31", prompt, Turns("done",
		Bash("m", "git merge -q --no-commit side || true"),
		Bash("r", "printf 'resolved\\n' > docs/seed.md && git add docs/seed.md"),
		Bash("c", "GIT_EDITOR=true git merge --continue"),
	))
	got := strings.Join(e.StopContinuations(proj, "s-058-31"), "\n")
	has(t, got, "docs/seed.md")
	has(t, got, "citation")
	if strings.Contains(got, "docs/side.md") {
		t.Fatalf("Stop asked for a citation of a file that only came in from the side:\n%s", got)
	}
}
