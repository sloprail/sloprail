package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// Grounding is by content, not position (#276): a commit that restores a file to a state a
// cited commit already made needs no citation of its own. An uncited change to content no
// cited commit made (a partial revert included) is still refused.

// T041_72: cited change, uncited tweak, uncited revert of the tweak: the file is back to the
// cited state, so it is grounded by that commit's citation.
func TestT041_72_RevertingToACitedStateIsGrounded(t *testing.T) {
	e, proj := guardedUncited(t, afterCitationGuard)
	e.Run(proj, "s-041-72", prompt, Turns("done",
		Write("w1", "memories/a.md", "# log\n"),
		harness.Commit("c1", "write the log", harness.CitesUser("adopt a decision log")),
		Write("w2", "memories/a.md", "# log\nand a line nobody asked for\n"),
		harness.Commit("c2", "tidy"),
		Write("w3", "memories/a.md", "# log\n"),
		harness.Commit("c3", "revert the tidy"),
	))
	if blocks := stopRefusal(e, proj, "s-041-72"); blocks != "" {
		t.Errorf("restoring a cited state was refused at Stop:\n%s", blocks)
	}
}

// T041_73: the same history, but the last commit only half-reverts: its content is no cited
// state, so the file is refused and named.
func TestT041_73_APartialRevertStillNeedsACitation(t *testing.T) {
	e, proj := guardedUncited(t, afterCitationGuard)
	e.Run(proj, "s-041-73", prompt, Turns("done",
		Write("w1", "memories/a.md", "# log\n"),
		harness.Commit("c1", "write the log", harness.CitesUser("adopt a decision log")),
		Write("w2", "memories/a.md", "# log\nand a line nobody asked for\n"),
		harness.Commit("c2", "tidy"),
		Write("w3", "memories/a.md", "# log\nand a line\n"),
		harness.Commit("c3", "half revert"),
	))
	blocks := stopRefusal(e, proj, "s-041-73")
	if !strings.Contains(blocks, noCitation) || !strings.Contains(blocks, "memories/a.md") {
		t.Fatalf("a partial revert passed at Stop, or the refusal does not name the file:\n%s", blocks)
	}
}
